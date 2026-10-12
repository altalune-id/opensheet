package queue

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"altalune.id/opensheet/internal/platform/tenant"
)

func newBroadcastEnv(t *testing.T, o Options) consumerEnv {
	t.Helper()
	rec := &recorder{}
	o.Unexpected = rec.unexpected
	c := connectTest(t, o)
	require.NoError(t, c.Declare(nil, []Broadcast{testBroadcast()}))
	return consumerEnv{client: c, rec: rec}
}

func startSeq(t *testing.T, c *Client) uint64 {
	t.Helper()
	seq, err := c.BroadcastStartSeq(t.Context())
	require.NoError(t, err)
	return seq
}

func testListener(fn func(ctx context.Context, m Message) error) Listener {
	return Listener{Broadcast: testBroadcast(), Handle: fn}
}

func listen(t *testing.T, c *Client, seq uint64, ls ...Listener) <-chan error {
	t.Helper()
	l, err := NewListener(c, ls, seq)
	require.NoError(t, err)
	_, done := runWorker(t, l)
	return done
}

func titles(ch chan<- string) Listener {
	return testListener(func(_ context.Context, m Message) error {
		p, err := Decode[testPayload](m)
		if err != nil {
			return err
		}
		ch <- p.Title
		return nil
	})
}

func TestListener_EveryInstanceHearsTheBroadcastIncludingTheEmitter(t *testing.T) {
	url := startNATS(t)
	a := newBroadcastEnv(t, testOptions(url))
	b := newBroadcastEnv(t, testOptions(url))
	seq := startSeq(t, a.client)

	var flagA, flagB atomic.Bool
	flagA.Store(true)
	flagB.Store(true)
	lower := func(f *atomic.Bool) Listener {
		return testListener(func(context.Context, Message) error {
			f.Store(false)
			return nil
		})
	}
	listen(t, a.client, seq, lower(&flagA))
	listen(t, b.client, seq, lower(&flagB))

	require.NoError(t, a.client.Emit(t.Context(), testBroadcast(), testPayload{}))
	require.Eventually(t, func() bool { return !flagA.Load() && !flagB.Load() }, waitFor, tick)
	assert.Empty(t, a.rec.messages())
	assert.Empty(t, b.rec.messages())
}

func TestListener_StartSeqReadBeforeEmitStillDelivers(t *testing.T) {
	e := newBroadcastEnv(t, testOptions(startNATS(t)))
	seq := startSeq(t, e.client)
	require.NoError(t, e.client.Emit(t.Context(), testBroadcast(), testPayload{Title: "gap"}))

	got := make(chan string, 4)
	listen(t, e.client, seq, titles(got))
	assert.Equal(t, "gap", receive(t, got))
}

func TestListener_StartSeqAfterEmitSkipsIt(t *testing.T) {
	e := newBroadcastEnv(t, testOptions(startNATS(t)))
	require.NoError(t, e.client.Emit(t.Context(), testBroadcast(), testPayload{Title: "old"}))
	seq := startSeq(t, e.client)

	got := make(chan string, 4)
	listen(t, e.client, seq, titles(got))
	require.NoError(t, e.client.Emit(t.Context(), testBroadcast(), testPayload{Title: "new"}))
	assert.Equal(t, "new", receive(t, got))
}

func TestListener_FailureIsReportedAndListeningContinues(t *testing.T) {
	tests := []struct {
		name string
		fail func() error
	}{
		{"error", func() error { return errors.New("refresh failed") }},
		{"panic", func() error { panic("boom") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := testOptions(startNATS(t))
			reader := testMeter(t, &o)
			e := newBroadcastEnv(t, o)
			got := make(chan string, 4)
			var calls atomic.Int32
			listen(t, e.client, startSeq(t, e.client), testListener(func(_ context.Context, m Message) error {
				if calls.Add(1) == 1 {
					return tt.fail()
				}
				p, err := Decode[testPayload](m)
				if err != nil {
					return err
				}
				got <- p.Title
				return nil
			}))

			require.NoError(t, e.client.Emit(t.Context(), testBroadcast(), testPayload{Title: "first"}))
			require.NoError(t, e.client.Emit(t.Context(), testBroadcast(), testPayload{Title: "second"}))
			assert.Equal(t, "second", receive(t, got))
			assert.Equal(t, []string{"queue: broadcast listener failed"}, e.rec.messages())
			assert.Equal(t, int64(1), collectMetrics(t, reader).counters["queue.broadcast_failed"])
		})
	}
}

func TestListener_HandlerTimeout(t *testing.T) {
	e := newBroadcastEnv(t, testOptions(startNATS(t)))
	deadline := make(chan time.Duration, 1)
	listen(t, e.client, startSeq(t, e.client), testListener(func(ctx context.Context, _ Message) error {
		dl, ok := ctx.Deadline()
		if !ok {
			return errors.New("no deadline")
		}
		deadline <- time.Until(dl)
		return nil
	}))
	require.NoError(t, e.client.Emit(t.Context(), testBroadcast(), testPayload{}))
	got := receive(t, deadline)
	assert.LessOrEqual(t, got, listenerTimeout)
	assert.Greater(t, got, listenerTimeout-time.Second)
	assert.Equal(t, 5*time.Second, listenerTimeout)
}

func TestListener_CarriesTenantAndTrace(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	o := testOptions(startNATS(t))
	o.Tracer = tp.Tracer("test")
	e := newBroadcastEnv(t, o)
	seq := startSeq(t, e.client)
	tc := tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New(), UserID: uuid.New()}
	require.NoError(t, e.client.Emit(tenant.Into(t.Context(), tc), testBroadcast(), testPayload{}))
	sent := lastMsg(t, e.client, streamBroadcast, "broadcast.test.ping.v1")

	got := make(chan Message, 1)
	gotTenant := make(chan tenant.Context, 1)
	listen(t, e.client, seq, testListener(func(ctx context.Context, m Message) error {
		scope, _ := tenant.From(ctx)
		gotTenant <- scope
		got <- m
		return nil
	}))

	m := receive(t, got)
	assert.Equal(t, tc, receive(t, gotTenant))
	assert.Equal(t, sent.Header.Get("Nats-Msg-Id"), m.ID.String())
	assert.Equal(t, "test.ping", m.Name)
	assert.Equal(t, 1, m.Version)
	assert.WithinDuration(t, time.Now(), m.CreatedAt, time.Minute)

	var emit, heard sdktrace.ReadOnlySpan
	require.Eventually(t, func() bool {
		for _, s := range sr.Ended() {
			switch s.Name() {
			case "queue.emit":
				emit = s
			case "queue.listen":
				heard = s
			}
		}
		return emit != nil && heard != nil
	}, waitFor, tick)
	assert.Equal(t, emit.SpanContext().SpanID(), heard.Parent().SpanID())
	assert.Equal(t, emit.SpanContext().TraceID(), heard.SpanContext().TraceID())
}

func TestListener_UnscopedHasNoTenant(t *testing.T) {
	e := newBroadcastEnv(t, testOptions(startNATS(t)))
	scoped := make(chan bool, 1)
	listen(t, e.client, startSeq(t, e.client), testListener(func(ctx context.Context, _ Message) error {
		_, err := tenant.From(ctx)
		scoped <- err == nil
		return nil
	}))
	require.NoError(t, e.client.Emit(t.Context(), testBroadcast(), testPayload{}))
	assert.False(t, receive(t, scoped))
}

func TestNewListener_Rejects(t *testing.T) {
	e := newBroadcastEnv(t, testOptions(startNATS(t)))
	noop := func(context.Context, Message) error { return nil }

	tests := []struct {
		name  string
		ls    []Listener
		check func(error) bool
	}{
		{"duplicate broadcast", []Listener{testListener(noop), testListener(noop)}, IsHandlerWiringError},
		{"invalid broadcast", []Listener{{Broadcast: Broadcast{Name: "bad", Version: 1}, Handle: noop}}, IsInvalidBroadcastError},
		{"nil Handle", []Listener{{Broadcast: testBroadcast()}}, IsNilHandlerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewListener(e.client, tt.ls, 1)
			require.Error(t, err)
			assert.True(t, tt.check(err), "got %T: %v", err, err)
		})
	}
}

func TestNewListener_NilHandleNamesTheBroadcastSubject(t *testing.T) {
	_, err := NewListener(Disabled(nil), []Listener{{Broadcast: testBroadcast()}}, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "broadcast.test.ping.v1")
}

func TestNewListener_DisabledClient(t *testing.T) {
	_, err := NewListener(Disabled(nil), []Listener{testListener(func(context.Context, Message) error { return nil })}, 1)
	require.Error(t, err)
	assert.True(t, IsDisabledError(err), "got %T: %v", err, err)
}

func TestListener_Name(t *testing.T) {
	e := newBroadcastEnv(t, testOptions(startNATS(t)))
	l, err := NewListener(e.client, nil, 1)
	require.NoError(t, err)
	assert.Equal(t, "queue.listener", l.Name())
	assert.Equal(t, ListenerName, l.Name())
}

func TestListener_EmptyListReturnsAtOnceWithoutNATS(t *testing.T) {
	e := newBroadcastEnv(t, testOptions(startNATS(t)))
	l, err := NewListener(e.client, nil, 1)
	require.NoError(t, err)
	require.NoError(t, e.client.Close())

	done := make(chan error, 1)
	go func() { done <- l.Run(t.Context()) }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Run with no listeners did not return at once")
	}
}

func TestListener_RunReturnsNilOnCancel(t *testing.T) {
	e := newBroadcastEnv(t, testOptions(startNATS(t)))
	l, err := NewListener(e.client, []Listener{testListener(func(context.Context, Message) error { return nil })}, 1)
	require.NoError(t, err)
	cancel, done := runWorker(t, l)
	require.Eventually(t, func() bool { return len(consumerNames(t, e.client)) == 1 }, waitFor, tick)

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(8 * time.Second):
		t.Fatal("Run did not return within 8s")
	}
}

func TestListener_RunFailsWhenTheOrderedConsumerCannotBeCreated(t *testing.T) {
	e := newBroadcastEnv(t, testOptions(startNATS(t)))
	l, err := NewListener(e.client, []Listener{testListener(func(context.Context, Message) error { return nil })}, 1)
	require.NoError(t, err)
	require.NoError(t, e.client.js.DeleteStream(t.Context(), streamBroadcast))

	err = l.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "queue: listener")
}

func TestListener_RunReturnsClosedWhenTheConnectionCloses(t *testing.T) {
	e := newBroadcastEnv(t, testOptions(startNATS(t)))
	l, err := NewListener(e.client, []Listener{testListener(func(context.Context, Message) error { return nil })}, 1)
	require.NoError(t, err)
	_, done := runWorker(t, l)
	require.Eventually(t, func() bool { return len(consumerNames(t, e.client)) == 1 }, waitFor, tick)

	e.client.nc.Close()
	err = receive(t, done)
	require.Error(t, err)
	assert.True(t, IsConsumerClosedError(err), "got %T: %v", err, err)
	assert.Contains(t, err.Error(), "broadcast")
}

func TestListener_ConsumerDeletedServerSideIsRecreated(t *testing.T) {
	e := newBroadcastEnv(t, testOptions(startNATS(t)))
	got := make(chan string, 4)
	done := listen(t, e.client, startSeq(t, e.client), titles(got))
	require.NoError(t, e.client.Emit(t.Context(), testBroadcast(), testPayload{Title: "before"}))
	assert.Equal(t, "before", receive(t, got))

	names := consumerNames(t, e.client)
	require.Len(t, names, 1)
	require.NoError(t, e.client.js.DeleteConsumer(t.Context(), streamBroadcast, names[0]))
	require.NoError(t, e.client.Emit(t.Context(), testBroadcast(), testPayload{Title: "after"}))

	select {
	case title := <-got:
		assert.Equal(t, "after", title)
	case err := <-done:
		assert.True(t, IsConsumerClosedError(err), "got %T: %v", err, err)
	case <-time.After(waitFor):
		t.Fatal("silent: the broadcast after the delete was never handled and Run did not return")
	}
}

func consumerNames(t *testing.T, c *Client) []string {
	t.Helper()
	s, err := c.js.Stream(t.Context(), streamBroadcast)
	require.NoError(t, err)
	lister := s.ConsumerNames(t.Context())
	var names []string
	for n := range lister.Name() {
		names = append(names, n)
	}
	require.NoError(t, lister.Err())
	return names
}
