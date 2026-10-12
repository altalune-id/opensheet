package queue

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/worker"
)

const (
	testDLQSubject = "dlq.jobs.test.run.v1"
	testDurable    = "test_run_v1"
	waitFor        = 15 * time.Second
	tick           = 20 * time.Millisecond
)

type report struct {
	message string
	cause   error
}

type recorder struct {
	mu      sync.Mutex
	reports []report
}

func (r *recorder) unexpected(_ context.Context, message string, cause error, _ ...any) *apperror.AppError {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reports = append(r.reports, report{message: message, cause: cause})
	return nil
}

func (r *recorder) messages() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.reports))
	for _, rp := range r.reports {
		out = append(out, rp.message)
	}
	return out
}

type consumerEnv struct {
	client *Client
	rec    *recorder
}

func newConsumerEnv(t *testing.T, o Options) consumerEnv {
	t.Helper()
	rec := &recorder{}
	o.Unexpected = rec.unexpected
	c := connectTest(t, o)
	require.NoError(t, c.Declare([]Job{testJob()}, nil))
	return consumerEnv{client: c, rec: rec}
}

func (e consumerEnv) consumer(t *testing.T, hs ...Handler) *Consumer {
	t.Helper()
	cons, err := NewConsumer(t.Context(), e.client, hs)
	require.NoError(t, err)
	cons.delays = func(int) time.Duration { return 10 * time.Millisecond }
	cons.dlqRetry = 100 * time.Millisecond
	return cons
}

func runWorker(t *testing.T, w worker.Worker) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		done <- w.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(10 * time.Second):
			t.Errorf("%s did not stop", w.Name())
		}
	})
	return cancel, done
}

func streamMsgs(t *testing.T, c *Client, stream string) uint64 {
	t.Helper()
	return streamInfo(t, c, stream).State.Msgs
}

func waitDLQ(t *testing.T, c *Client) *jetstream.RawStreamMsg {
	t.Helper()
	require.Eventually(t, func() bool { return streamMsgs(t, c, streamDLQ) == 1 }, waitFor, tick)
	return lastMsg(t, c, streamDLQ, testDLQSubject)
}

func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(waitFor):
		t.Fatal("timed out waiting on channel")
		var zero T
		return zero
	}
}

func testMeter(t *testing.T, o *Options) *sdkmetric.ManualReader {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })
	o.Meter = mp.Meter("test")
	return reader
}

type consumerMetrics struct {
	outcomes map[string]int64
	counters map[string]int64
	attempts uint64
}

func collectMetrics(t *testing.T, reader *sdkmetric.ManualReader) consumerMetrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &rm))
	got := consumerMetrics{outcomes: map[string]int64{}, counters: map[string]int64{}}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch data := m.Data.(type) {
			case metricdata.Sum[int64]:
				for _, dp := range data.DataPoints {
					got.counters[m.Name] += dp.Value
					if m.Name == "queue.consumed" {
						v, _ := dp.Attributes.Value("outcome")
						got.outcomes[v.AsString()] += dp.Value
					}
				}
			case metricdata.Histogram[int64]:
				if m.Name == "queue.delivery_attempts" {
					for _, dp := range data.DataPoints {
						got.attempts += dp.Count
					}
				}
			}
		}
	}
	return got
}

func testHandler(fn func(ctx context.Context, m Message) error) Handler {
	return Handler{Job: testJob(), Handle: fn}
}

func TestNewConsumer_Rejects(t *testing.T) {
	e := newConsumerEnv(t, testOptions(startNATS(t)))
	noop := func(context.Context, Message) error { return nil }

	tests := []struct {
		name  string
		hs    []Handler
		check func(error) bool
	}{
		{"duplicate job", []Handler{testHandler(noop), testHandler(noop)}, IsHandlerWiringError},
		{"invalid job", []Handler{{Job: Job{Name: "bad", Version: 1}, Handle: noop}}, IsInvalidJobError},
		{"nil Handle", []Handler{{Job: testJob()}}, IsNilHandlerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewConsumer(t.Context(), e.client, tt.hs)
			require.Error(t, err)
			assert.True(t, tt.check(err), "got %T: %v", err, err)
		})
	}
}

func TestNewConsumer_DisabledClient(t *testing.T) {
	_, err := NewConsumer(t.Context(), Disabled(nil), []Handler{testHandler(func(context.Context, Message) error { return nil })})
	require.Error(t, err)
	assert.True(t, IsDisabledError(err), "got %T: %v", err, err)
}

func TestNewConsumer_CreatesDurable(t *testing.T) {
	e := newConsumerEnv(t, testOptions(startNATS(t)))
	cons := e.consumer(t, testHandler(func(context.Context, Message) error { return nil }))
	assert.Equal(t, "queue.consumer", cons.Name())
	assert.Equal(t, WorkerName, cons.Name())

	jc, err := e.client.js.Consumer(t.Context(), streamWork, testDurable)
	require.NoError(t, err)
	info, err := jc.Info(t.Context())
	require.NoError(t, err)
	assert.Equal(t, testDurable, info.Config.Durable)
	assert.Equal(t, "jobs.test.run.v1", info.Config.FilterSubject)
	assert.Equal(t, -1, info.Config.MaxDeliver)
	assert.Equal(t, 30*time.Second, info.Config.AckWait)
	assert.Equal(t, 16, info.Config.MaxAckPending)
	assert.Equal(t, jetstream.AckExplicitPolicy, info.Config.AckPolicy)
}

func TestConsumer_Success(t *testing.T) {
	e := newConsumerEnv(t, testOptions(startNATS(t)))
	tc := tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New(), UserID: uuid.New()}
	require.NoError(t, e.client.Submit(tenant.Into(t.Context(), tc), testJob(), testPayload{Title: "hi"}))
	sent := lastMsg(t, e.client, streamWork, "jobs.test.run.v1")

	got := make(chan Message, 1)
	gotTenant := make(chan tenant.Context, 1)
	cons := e.consumer(t, testHandler(func(ctx context.Context, m Message) error {
		scope, _ := tenant.From(ctx)
		gotTenant <- scope
		got <- m
		return nil
	}))
	runWorker(t, cons)

	m := receive(t, got)
	assert.Equal(t, tc, receive(t, gotTenant))
	assert.Equal(t, sent.Header.Get("Nats-Msg-Id"), m.ID.String())
	assert.Equal(t, "test.run", m.Name)
	assert.Equal(t, 1, m.Version)
	assert.Equal(t, 1, m.NumDelivered)
	assert.WithinDuration(t, time.Now(), m.CreatedAt, time.Minute)
	p, err := Decode[testPayload](m)
	require.NoError(t, err)
	assert.Equal(t, "hi", p.Title)
	require.Eventually(t, func() bool { return streamMsgs(t, e.client, streamWork) == 0 }, waitFor, tick)
	assert.Empty(t, e.rec.messages())
}

func TestConsumer_UnscopedHasNoTenant(t *testing.T) {
	e := newConsumerEnv(t, testOptions(startNATS(t)))
	require.NoError(t, e.client.Submit(t.Context(), testJob(), testPayload{}))

	scoped := make(chan bool, 1)
	cons := e.consumer(t, testHandler(func(ctx context.Context, _ Message) error {
		_, err := tenant.From(ctx)
		scoped <- err == nil
		return nil
	}))
	runWorker(t, cons)

	assert.False(t, receive(t, scoped))
}

func TestConsumer_RetriesThenSucceeds(t *testing.T) {
	e := newConsumerEnv(t, testOptions(startNATS(t)))
	require.NoError(t, e.client.Submit(t.Context(), testJob(), testPayload{}))

	var mu sync.Mutex
	var seen []int
	cons := e.consumer(t, testHandler(func(_ context.Context, m Message) error {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, m.NumDelivered)
		if len(seen) < 3 {
			return errors.New("not yet")
		}
		return nil
	}))
	runWorker(t, cons)

	require.Eventually(t, func() bool { return streamMsgs(t, e.client, streamWork) == 0 }, waitFor, tick)
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []int{1, 2, 3}, seen)
	assert.Equal(t, uint64(0), streamMsgs(t, e.client, streamDLQ))
}

func TestConsumer_ExhaustedGoesToDLQ(t *testing.T) {
	e := newConsumerEnv(t, testOptions(startNATS(t)))
	require.NoError(t, e.client.Submit(t.Context(), testJob(), testPayload{}))
	sent := lastMsg(t, e.client, streamWork, "jobs.test.run.v1")

	var calls atomic.Int32
	cons := e.consumer(t, testHandler(func(context.Context, Message) error {
		calls.Add(1)
		return errors.New("boom")
	}))
	runWorker(t, cons)

	d := waitDLQ(t, e.client)
	assert.Equal(t, int32(5), calls.Load())
	assert.Equal(t, "exhausted", d.Header.Get("Opensheet-Dlq-Reason"))
	assert.Equal(t, "boom", d.Header.Get("Opensheet-Dlq-Error"))
	assert.Equal(t, "5", d.Header.Get("Opensheet-Dlq-Attempts"))
	assert.Equal(t, "1", d.Header.Get("Opensheet-Dlq-Stream-Seq"))
	assert.Equal(t, sent.Header.Get("Nats-Msg-Id"), d.Header.Get("Nats-Msg-Id"))
	assert.Equal(t, "test.run", d.Header.Get("Opensheet-Job"))
	assert.Equal(t, sent.Data, d.Data)
	require.Eventually(t, func() bool { return streamMsgs(t, e.client, streamWork) == 0 }, waitFor, tick)
	require.Eventually(t, func() bool { return len(e.rec.messages()) == 1 }, waitFor, tick)
	assert.Equal(t, []string{"queue: dead-lettered"}, e.rec.messages())
}

func TestConsumer_PermanentGoesToDLQAtOnce(t *testing.T) {
	e := newConsumerEnv(t, testOptions(startNATS(t)))
	require.NoError(t, e.client.Submit(t.Context(), testJob(), testPayload{}))

	var calls atomic.Int32
	cons := e.consumer(t, testHandler(func(context.Context, Message) error {
		calls.Add(1)
		return Permanent(errors.New("bad payload"))
	}))
	runWorker(t, cons)

	d := waitDLQ(t, e.client)
	assert.Equal(t, int32(1), calls.Load())
	assert.Equal(t, "permanent", d.Header.Get("Opensheet-Dlq-Reason"))
	assert.Contains(t, d.Header.Get("Opensheet-Dlq-Error"), "bad payload")
	assert.Equal(t, "1", d.Header.Get("Opensheet-Dlq-Attempts"))
}

func TestConsumer_PanicIsRetried(t *testing.T) {
	e := newConsumerEnv(t, testOptions(startNATS(t)))
	require.NoError(t, e.client.Submit(t.Context(), testJob(), testPayload{}))

	var calls atomic.Int32
	cons := e.consumer(t, testHandler(func(context.Context, Message) error {
		if calls.Add(1) == 1 {
			panic("kaboom")
		}
		return nil
	}))
	runWorker(t, cons)

	require.Eventually(t, func() bool { return calls.Load() == 2 }, waitFor, tick)
	require.Eventually(t, func() bool { return streamMsgs(t, e.client, streamWork) == 0 }, waitFor, tick)
	assert.Equal(t, uint64(0), streamMsgs(t, e.client, streamDLQ))
}

func TestConsumer_SuppressReportCallsOnDeadLetter(t *testing.T) {
	e := newConsumerEnv(t, testOptions(startNATS(t)))
	require.NoError(t, e.client.Submit(t.Context(), testJob(), testPayload{Title: "x"}))
	sent := lastMsg(t, e.client, streamWork, "jobs.test.run.v1")

	dead := make(chan DeadLetter, 1)
	cons := e.consumer(t, Handler{
		Job:            testJob(),
		Handle:         func(context.Context, Message) error { return Permanent(errors.New("nope")) },
		OnDeadLetter:   func(_ context.Context, d DeadLetter) error { dead <- d; return nil },
		SuppressReport: true,
	})
	runWorker(t, cons)

	d := receive(t, dead)
	assert.Equal(t, "permanent", d.Reason)
	assert.Contains(t, d.Cause, "nope")
	assert.Equal(t, sent.Header.Get("Nats-Msg-Id"), d.ID.String())
	assert.Equal(t, sent.Data, d.Data)
	waitDLQ(t, e.client)
	assert.Empty(t, e.rec.messages())
}

func TestConsumer_MalformedOrgHeaderIsPermanent(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	o := testOptions(startNATS(t))
	o.Tracer = tp.Tracer("test")
	e := newConsumerEnv(t, o)
	msg := nats.NewMsg("jobs.test.run.v1")
	msg.Data = []byte(`{}`)
	msg.Header.Set("Opensheet-Job", "test.run")
	msg.Header.Set("Opensheet-Job-Version", "1")
	msg.Header.Set("Opensheet-Created-At", time.Now().UTC().Format(time.RFC3339Nano))
	msg.Header.Set("Opensheet-Org-Id", "not-a-uuid")
	_, err := e.client.js.PublishMsg(t.Context(), msg, jetstream.WithMsgID(uuid.NewString()))
	require.NoError(t, err)

	var calls atomic.Int32
	cons := e.consumer(t, testHandler(func(context.Context, Message) error {
		calls.Add(1)
		return nil
	}))
	runWorker(t, cons)

	d := waitDLQ(t, e.client)
	assert.Equal(t, "permanent", d.Header.Get("Opensheet-Dlq-Reason"))
	assert.Contains(t, d.Header.Get("Opensheet-Dlq-Error"), "Opensheet-Org-Id")
	assert.Equal(t, int32(0), calls.Load())
	require.Eventually(t, func() bool {
		for _, s := range sr.Ended() {
			if s.Name() == "queue.consume" {
				return s.Status().Code == codes.Error
			}
		}
		return false
	}, waitFor, tick)
}

func TestConsumer_DLQPublishFailureRetriesWithoutHandler(t *testing.T) {
	o := testOptions(startNATS(t))
	reader := testMeter(t, &o)
	e := newConsumerEnv(t, o)
	require.NoError(t, e.client.js.DeleteStream(t.Context(), streamDLQ))
	require.NoError(t, e.client.Submit(t.Context(), testJob(), testPayload{}))

	var calls atomic.Int32
	cons := e.consumer(t, testHandler(func(context.Context, Message) error {
		calls.Add(1)
		return errors.New("boom")
	}))
	cons.dlqRetry = 2 * time.Second
	runWorker(t, cons)

	require.Eventually(t, func() bool {
		return len(e.rec.messages()) == 1
	}, waitFor, tick)
	assert.Equal(t, []string{"queue: dlq publish failed"}, e.rec.messages())
	assert.Equal(t, int32(5), calls.Load())
	assert.Equal(t, uint64(1), streamMsgs(t, e.client, streamWork))

	require.NoError(t, ensureStreams(t.Context(), e.client.js))
	d := waitDLQ(t, e.client)
	assert.Equal(t, int32(5), calls.Load())
	assert.Equal(t, "exhausted", d.Header.Get("Opensheet-Dlq-Reason"))
	assert.Equal(t, "6", d.Header.Get("Opensheet-Dlq-Attempts"))
	require.Eventually(t, func() bool { return streamMsgs(t, e.client, streamWork) == 0 }, waitFor, tick)

	got := collectMetrics(t, reader)
	assert.Equal(t, int64(1), got.counters["queue.dlq_publish_failed"])
	assert.Equal(t, map[string]int64{"retry": 4, "dead_letter": 1}, got.outcomes)
	assert.Equal(t, uint64(6), got.attempts)
}

func TestConsumer_DLQPublishFailureReportsDespiteSuppress(t *testing.T) {
	e := newConsumerEnv(t, testOptions(startNATS(t)))
	require.NoError(t, e.client.js.DeleteStream(t.Context(), streamDLQ))
	require.NoError(t, e.client.Submit(t.Context(), testJob(), testPayload{}))

	cons := e.consumer(t, Handler{
		Job:            testJob(),
		Handle:         func(context.Context, Message) error { return Permanent(errors.New("nope")) },
		SuppressReport: true,
	})
	cons.dlqRetry = time.Minute
	runWorker(t, cons)

	require.Eventually(t, func() bool { return len(e.rec.messages()) == 1 }, waitFor, tick)
	assert.Equal(t, []string{"queue: dlq publish failed"}, e.rec.messages())
}

func TestConsumer_PermanentRedeliveredAfterDLQFailureRunsHandlerAgain(t *testing.T) {
	e := newConsumerEnv(t, testOptions(startNATS(t)))
	require.NoError(t, e.client.js.DeleteStream(t.Context(), streamDLQ))
	require.NoError(t, e.client.Submit(t.Context(), testJob(), testPayload{}))

	var calls atomic.Int32
	cons := e.consumer(t, testHandler(func(context.Context, Message) error {
		n := calls.Add(1)
		return Permanent(fmt.Errorf("permanent cause %d", n))
	}))
	cons.dlqRetry = 2 * time.Second
	runWorker(t, cons)

	require.Eventually(t, func() bool { return len(e.rec.messages()) == 1 }, waitFor, tick)
	assert.Equal(t, []string{"queue: dlq publish failed"}, e.rec.messages())
	assert.Equal(t, int32(1), calls.Load())

	require.NoError(t, ensureStreams(t.Context(), e.client.js))

	d := waitDLQ(t, e.client)
	assert.Equal(t, int32(2), calls.Load(), "the handler must run again on redelivery, not skip straight to the DLQ")
	assert.Equal(t, "permanent", d.Header.Get("Opensheet-Dlq-Reason"))
	assert.Contains(t, d.Header.Get("Opensheet-Dlq-Error"), "permanent cause 2")
	assert.NotContains(t, d.Header.Get("Opensheet-Dlq-Error"), "cause 1")
	assert.Equal(t, "2", d.Header.Get("Opensheet-Dlq-Attempts"))
}

func TestConsumer_PastCapOnArrivalSkipsHandler(t *testing.T) {
	e := newConsumerEnv(t, testOptions(startNATS(t)))
	require.NoError(t, e.client.Submit(t.Context(), testJob(), testPayload{}))

	var calls atomic.Int32
	cons := e.consumer(t, testHandler(func(context.Context, Message) error {
		calls.Add(1)
		return nil
	}))

	jc, err := e.client.js.Consumer(t.Context(), streamWork, testDurable)
	require.NoError(t, err)
	for range MaxAttempts {
		batch, err := jc.Fetch(1, jetstream.FetchMaxWait(5*time.Second))
		require.NoError(t, err)
		n := 0
		for m := range batch.Messages() {
			require.NoError(t, m.Nak())
			n++
		}
		require.NoError(t, batch.Error())
		require.Equal(t, 1, n)
	}

	runWorker(t, cons)

	d := waitDLQ(t, e.client)
	assert.Equal(t, int32(0), calls.Load())
	assert.Equal(t, "exhausted", d.Header.Get("Opensheet-Dlq-Reason"))
	assert.Equal(t, "6", d.Header.Get("Opensheet-Dlq-Attempts"))
	assert.Equal(t, "outcome unknown: redelivered past the attempt cap", d.Header.Get("Opensheet-Dlq-Error"))
}

func TestConsumer_ConsumeSpanIsChildOfPublish(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	o := testOptions(startNATS(t))
	o.Tracer = tp.Tracer("test")
	e := newConsumerEnv(t, o)
	require.NoError(t, e.client.Submit(t.Context(), testJob(), testPayload{}))

	cons := e.consumer(t, testHandler(func(context.Context, Message) error { return nil }))
	runWorker(t, cons)

	var publish, consume sdktrace.ReadOnlySpan
	require.Eventually(t, func() bool {
		for _, s := range sr.Ended() {
			switch s.Name() {
			case "queue.publish":
				publish = s
			case "queue.consume":
				consume = s
			}
		}
		return publish != nil && consume != nil
	}, waitFor, tick)
	assert.Equal(t, publish.SpanContext().SpanID(), consume.Parent().SpanID())
	assert.Equal(t, publish.SpanContext().TraceID(), consume.SpanContext().TraceID())
}

func TestConsumer_Metrics(t *testing.T) {
	o := testOptions(startNATS(t))
	reader := testMeter(t, &o)
	e := newConsumerEnv(t, o)
	require.NoError(t, e.client.Submit(t.Context(), testJob(), testPayload{}))

	var calls atomic.Int32
	cons := e.consumer(t, testHandler(func(context.Context, Message) error {
		if calls.Add(1) == 1 {
			return errors.New("once")
		}
		return nil
	}))
	runWorker(t, cons)
	require.Eventually(t, func() bool { return streamMsgs(t, e.client, streamWork) == 0 }, waitFor, tick)

	got := collectMetrics(t, reader)
	assert.Equal(t, map[string]int64{"ok": 1, "retry": 1}, got.outcomes)
	assert.Equal(t, uint64(2), got.attempts)
}

func TestConsumer_HandlerContextOutlivesRunCancel(t *testing.T) {
	e := newConsumerEnv(t, testOptions(startNATS(t)))
	require.NoError(t, e.client.Submit(t.Context(), testJob(), testPayload{}))

	type seen struct {
		err      error
		deadline time.Duration
		ok       bool
	}
	started := make(chan struct{})
	release := make(chan struct{})
	result := make(chan seen, 1)
	cons := e.consumer(t, testHandler(func(ctx context.Context, _ Message) error {
		close(started)
		<-release
		dl, ok := ctx.Deadline()
		result <- seen{err: ctx.Err(), deadline: time.Until(dl), ok: ok}
		return nil
	}))
	cancel, done := runWorker(t, cons)

	receive(t, started)
	cancel()
	close(release)
	got := receive(t, result)
	require.NoError(t, got.err)
	require.True(t, got.ok)
	assert.Greater(t, got.deadline, 20*time.Second)
	assert.LessOrEqual(t, got.deadline, HandlerTimeout)
	require.NoError(t, receive(t, done))
}

func TestConsumer_RunReturnsClosedWhenDurableDeletedBetweenPulls(t *testing.T) {
	e := newConsumerEnv(t, testOptions(startNATS(t)))
	require.NoError(t, e.client.Submit(t.Context(), testJob(), testPayload{}))

	started := make(chan struct{})
	release := make(chan struct{})
	cons := e.consumer(t, testHandler(func(context.Context, Message) error {
		close(started)
		<-release
		return nil
	}))
	cons.heartbeat = time.Second
	_, done := runWorker(t, cons)

	receive(t, started)
	require.NoError(t, e.client.js.DeleteConsumer(t.Context(), streamWork, testDurable))
	close(release)

	err := receive(t, done)
	require.Error(t, err)
	assert.True(t, IsConsumerClosedError(err), "got %T: %v", err, err)
}

func TestConsumer_RunReturnsNilOnCancel(t *testing.T) {
	e := newConsumerEnv(t, testOptions(startNATS(t)))
	cons := e.consumer(t, testHandler(func(context.Context, Message) error { return nil }))
	cancel, done := runWorker(t, cons)

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(8 * time.Second):
		t.Fatal("Run did not return within 8s")
	}
}

func TestConsumer_RunReturnsClosedWhenDurableDeleted(t *testing.T) {
	e := newConsumerEnv(t, testOptions(startNATS(t)))
	cons := e.consumer(t, testHandler(func(context.Context, Message) error { return nil }))
	_, done := runWorker(t, cons)
	jc, err := e.client.js.Consumer(t.Context(), streamWork, testDurable)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		info, err := jc.Info(t.Context())
		return err == nil && info.NumWaiting > 0
	}, waitFor, tick)

	require.NoError(t, e.client.js.DeleteConsumer(t.Context(), streamWork, testDurable))
	select {
	case err := <-done:
		require.Error(t, err)
		assert.True(t, IsConsumerClosedError(err), "got %T: %v", err, err)
		assert.Contains(t, err.Error(), "jobs.test.run.v1")
	case <-time.After(waitFor):
		t.Fatal("Run did not return after the durable was deleted")
	}
}

func TestDecode_Error(t *testing.T) {
	_, err := Decode[testPayload](Message{Data: []byte("{")})
	require.Error(t, err)
}

func TestPermanent(t *testing.T) {
	cause := errors.New("x")
	err := Permanent(cause)
	assert.True(t, IsPermanentError(err))
	assert.ErrorIs(t, err, cause)
	assert.False(t, IsPermanentError(cause))
	assert.NoError(t, Permanent(nil))
	assert.True(t, IsConsumerClosedError(&ConsumerClosedError{Subject: "jobs.a.b.v1"}))
	assert.False(t, IsConsumerClosedError(cause))
}

func TestNakDelay(t *testing.T) {
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 10 * time.Second},
		{2, time.Minute},
		{3, 5 * time.Minute},
		{4, 15 * time.Minute},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, nakDelay(tt.attempt), "attempt %d", tt.attempt)
	}
}
