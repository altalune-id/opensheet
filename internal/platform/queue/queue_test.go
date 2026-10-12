package queue

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/tenant"
)

func testJob() Job { return Job{Name: "test.run", Version: 1} }

func testBroadcast() Broadcast { return Broadcast{Name: "test.ping", Version: 1} }

type testPayload struct {
	Title string `json:"title"`
}

func discardUnexpected(context.Context, string, error, ...any) *apperror.AppError { return nil }

func testOptions(url string) Options {
	return Options{
		URL:            url,
		ConnectTimeout: 5 * time.Second,
		Log:            slog.New(slog.DiscardHandler),
		Unexpected:     discardUnexpected,
	}
}

func connectTest(t *testing.T, o Options) *Client {
	t.Helper()
	c, err := Connect(t.Context(), o)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func streamInfo(t *testing.T, c *Client, name string) *jetstream.StreamInfo {
	t.Helper()
	s, err := c.js.Stream(t.Context(), name)
	require.NoError(t, err)
	info, err := s.Info(t.Context())
	require.NoError(t, err)
	return info
}

func lastMsg(t *testing.T, c *Client, stream, subject string) *jetstream.RawStreamMsg {
	t.Helper()
	s, err := c.js.Stream(t.Context(), stream)
	require.NoError(t, err)
	m, err := s.GetLastMsgForSubject(t.Context(), subject)
	require.NoError(t, err)
	return m
}

func TestConnect_CreatesStreams(t *testing.T) {
	c := connectTest(t, testOptions(startNATS(t)))

	tests := []struct {
		stream    string
		subjects  []string
		retention jetstream.RetentionPolicy
		discard   jetstream.DiscardPolicy
		maxAge    time.Duration
		maxBytes  int64
	}{
		{streamWork, []string{"jobs.>"}, jetstream.WorkQueuePolicy, jetstream.DiscardNew, 7 * 24 * time.Hour, 256 << 20},
		{streamDLQ, []string{"dlq.jobs.>"}, jetstream.LimitsPolicy, jetstream.DiscardOld, 30 * 24 * time.Hour, 256 << 20},
		{streamBroadcast, []string{"broadcast.>"}, jetstream.LimitsPolicy, jetstream.DiscardOld, time.Hour, 64 << 20},
	}
	for _, tt := range tests {
		t.Run(tt.stream, func(t *testing.T) {
			cfg := streamInfo(t, c, tt.stream).Config
			assert.Equal(t, tt.subjects, cfg.Subjects)
			assert.Equal(t, tt.retention, cfg.Retention)
			assert.Equal(t, tt.discard, cfg.Discard)
			assert.Equal(t, tt.maxAge, cfg.MaxAge)
			assert.Equal(t, tt.maxBytes, cfg.MaxBytes)
			assert.Equal(t, jetstream.FileStorage, cfg.Storage)
			assert.Equal(t, 1, cfg.Replicas)
			assert.Equal(t, 2*time.Minute, cfg.Duplicates)
		})
	}
}

func TestConnect_Idempotent(t *testing.T) {
	url := startNATS(t)
	connectTest(t, testOptions(url))
	c := connectTest(t, testOptions(url))

	assert.Equal(t, []string{"jobs.>"}, streamInfo(t, c, streamWork).Config.Subjects)
	assert.Equal(t, []string{"dlq.jobs.>"}, streamInfo(t, c, streamDLQ).Config.Subjects)
}

func TestConnect_RequiresUnexpected(t *testing.T) {
	o := testOptions(startNATS(t))
	o.Unexpected = nil
	_, err := Connect(t.Context(), o)
	require.Error(t, err)
}

func TestConnect_DeadPortFailsAfterConnectTimeout(t *testing.T) {
	ns := startNATSServer(t)
	url := ns.ClientURL()
	ns.Shutdown()

	o := testOptions(url)
	o.ConnectTimeout = time.Second
	start := time.Now()
	_, err := Connect(t.Context(), o)
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.GreaterOrEqual(t, elapsed, time.Second)
	assert.Less(t, elapsed, 4*time.Second)
}

func TestConnect_CancelledContextStopsRetrying(t *testing.T) {
	ns := startNATSServer(t)
	url := ns.ClientURL()
	ns.Shutdown()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	o := testOptions(url)
	o.ConnectTimeout = 10 * time.Second
	start := time.Now()
	_, err := Connect(ctx, o)

	require.ErrorIs(t, err, context.Canceled)
	assert.Less(t, time.Since(start), 2*time.Second)
}

func TestBroadcastStartSeq(t *testing.T) {
	c := connectTest(t, testOptions(startNATS(t)))
	require.NoError(t, c.Declare(nil, []Broadcast{testBroadcast()}))

	seq, err := c.BroadcastStartSeq(t.Context())
	require.NoError(t, err)
	assert.Equal(t, uint64(1), seq)

	require.NoError(t, c.Emit(t.Context(), testBroadcast(), testPayload{Title: "a"}))
	require.NoError(t, c.Emit(t.Context(), testBroadcast(), testPayload{Title: "b"}))

	seq, err = c.BroadcastStartSeq(t.Context())
	require.NoError(t, err)
	assert.Equal(t, streamInfo(t, c, streamBroadcast).State.LastSeq+1, seq)
	assert.Equal(t, uint64(3), seq)
}

func TestSubmit_PublishesWithHeaders(t *testing.T) {
	c := connectTest(t, testOptions(startNATS(t)))
	require.NoError(t, c.Declare([]Job{testJob()}, nil))

	tc := tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New(), UserID: uuid.New()}
	ctx := tenant.Into(t.Context(), tc)
	require.NoError(t, c.Submit(ctx, testJob(), testPayload{Title: "hello"}))

	assert.Equal(t, uint64(1), streamInfo(t, c, streamWork).State.Msgs)
	m := lastMsg(t, c, streamWork, "jobs.test.run.v1")

	var got testPayload
	require.NoError(t, json.Unmarshal(m.Data, &got))
	assert.Equal(t, "hello", got.Title)

	_, err := uuid.Parse(m.Header.Get("Nats-Msg-Id"))
	require.NoError(t, err)
	assert.Equal(t, "test.run", m.Header.Get("Opensheet-Job"))
	assert.Equal(t, "1", m.Header.Get("Opensheet-Job-Version"))
	_, err = time.Parse(time.RFC3339, m.Header.Get("Opensheet-Created-At"))
	require.NoError(t, err)
	assert.Equal(t, tc.OrgID.String(), m.Header.Get("Opensheet-Org-Id"))
	assert.Equal(t, tc.ProjectID.String(), m.Header.Get("Opensheet-Project-Id"))
	assert.Equal(t, tc.UserID.String(), m.Header.Get("Opensheet-User-Id"))
}

func TestSubmit_Unscoped(t *testing.T) {
	c := connectTest(t, testOptions(startNATS(t)))
	require.NoError(t, c.Declare([]Job{testJob()}, nil))

	require.NoError(t, c.Submit(t.Context(), testJob(), testPayload{}))

	m := lastMsg(t, c, streamWork, "jobs.test.run.v1")
	assert.Empty(t, m.Header.Values("Opensheet-Org-Id"))
}

func TestSubmit_UndeclaredJob(t *testing.T) {
	c := connectTest(t, testOptions(startNATS(t)))
	require.NoError(t, c.Declare([]Job{testJob()}, nil))

	err := c.Submit(t.Context(), Job{Name: "test.other", Version: 1}, testPayload{})
	require.Error(t, err)
	assert.True(t, IsUndeclaredJobError(err))
	assert.Equal(t, uint64(0), streamInfo(t, c, streamWork).State.Msgs)
}

func TestSubmit_BeforeDeclare(t *testing.T) {
	c := connectTest(t, testOptions(startNATS(t)))
	assert.True(t, IsUndeclaredJobError(c.Submit(t.Context(), testJob(), testPayload{})))
}

func TestDeclare_Rejects(t *testing.T) {
	c := Disabled(slog.New(slog.DiscardHandler))

	tests := []struct {
		name       string
		jobs       []Job
		broadcasts []Broadcast
		check      func(error) bool
	}{
		{"invalid job", []Job{{Name: "bad", Version: 1}}, nil, IsInvalidJobError},
		{"invalid broadcast", nil, []Broadcast{{Name: "test.ping", Version: 0}}, IsInvalidBroadcastError},
		{"duplicate job", []Job{testJob(), testJob()}, nil, IsHandlerWiringError},
		{"duplicate broadcast", nil, []Broadcast{testBroadcast(), testBroadcast()}, IsHandlerWiringError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := c.Declare(tt.jobs, tt.broadcasts)
			require.Error(t, err)
			assert.True(t, tt.check(err), "got %T: %v", err, err)
		})
	}
}

func TestDeclare_DuplicateNamesEverySubject(t *testing.T) {
	c := Disabled(slog.New(slog.DiscardHandler))
	err := c.Declare([]Job{testJob(), testJob()}, []Broadcast{testBroadcast(), testBroadcast()})

	we, ok := errors.AsType[*HandlerWiringError](err)
	require.True(t, ok)
	assert.Equal(t, []string{"broadcast.test.ping.v1", "jobs.test.run.v1"}, we.Duplicate)
}

func TestDeclare_SameNameAsJobAndBroadcastIsNotDuplicate(t *testing.T) {
	c := Disabled(slog.New(slog.DiscardHandler))
	require.NoError(t, c.Declare([]Job{{Name: "test.x", Version: 1}}, []Broadcast{{Name: "test.x", Version: 1}}))
}

func TestDeclare_FailureKeepsPreviousSet(t *testing.T) {
	c := connectTest(t, testOptions(startNATS(t)))
	require.NoError(t, c.Declare([]Job{testJob()}, nil))
	require.Error(t, c.Declare([]Job{{Name: "bad", Version: 1}}, nil))

	require.NoError(t, c.Submit(t.Context(), testJob(), testPayload{}))
}

func TestEmit(t *testing.T) {
	c := connectTest(t, testOptions(startNATS(t)))
	require.NoError(t, c.Declare(nil, []Broadcast{testBroadcast()}))

	require.NoError(t, c.Emit(t.Context(), testBroadcast(), testPayload{Title: "ping"}))

	assert.Equal(t, uint64(1), streamInfo(t, c, streamBroadcast).State.Msgs)
	m := lastMsg(t, c, streamBroadcast, "broadcast.test.ping.v1")
	assert.Equal(t, "test.ping", m.Header.Get("Opensheet-Broadcast"))
	assert.Equal(t, "1", m.Header.Get("Opensheet-Broadcast-Version"))
	_, err := uuid.Parse(m.Header.Get("Nats-Msg-Id"))
	require.NoError(t, err)
	assert.Empty(t, m.Header.Values("Opensheet-Job"))
}

func TestEmit_UndeclaredBroadcast(t *testing.T) {
	c := connectTest(t, testOptions(startNATS(t)))
	require.NoError(t, c.Declare(nil, []Broadcast{testBroadcast()}))

	err := c.Emit(t.Context(), Broadcast{Name: "test.other", Version: 1}, testPayload{})
	require.Error(t, err)
	assert.True(t, IsUndeclaredBroadcastError(err))
	assert.Equal(t, uint64(0), streamInfo(t, c, streamBroadcast).State.Msgs)
}

func TestPublish_SameMsgIDStoresOnce(t *testing.T) {
	c := connectTest(t, testOptions(startNATS(t)))
	require.NoError(t, c.Declare([]Job{testJob()}, nil))

	id := uuid.New()
	p := c.jobPublication(testJob())
	require.NoError(t, c.publish(t.Context(), p, testPayload{Title: "a"}, id))
	require.NoError(t, c.publish(t.Context(), p, testPayload{Title: "a"}, id))

	assert.Equal(t, uint64(1), streamInfo(t, c, streamWork).State.Msgs)
}

func TestSubmit_CancelledContextStillPublishes(t *testing.T) {
	c := connectTest(t, testOptions(startNATS(t)))
	require.NoError(t, c.Declare([]Job{testJob()}, nil))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.NoError(t, c.Submit(ctx, testJob(), testPayload{}))

	assert.Equal(t, uint64(1), streamInfo(t, c, streamWork).State.Msgs)
}

func TestSubmit_ServerDownIsPublishErrorAfterBudget(t *testing.T) {
	ns := startNATSServer(t)
	c := connectTest(t, testOptions(ns.ClientURL()))
	require.NoError(t, c.Declare([]Job{testJob()}, nil))
	c.budget = 1500 * time.Millisecond
	ns.Shutdown()

	start := time.Now()
	err := c.Submit(t.Context(), testJob(), testPayload{})
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.True(t, IsPublishError(err))
	assert.GreaterOrEqual(t, elapsed, 1400*time.Millisecond)
	assert.Less(t, elapsed, 3*time.Second)
}

func TestSubmit_NonRetryableErrorReturnsAtOnce(t *testing.T) {
	c := connectTest(t, testOptions(startNATS(t)))
	require.NoError(t, c.Declare([]Job{testJob()}, nil))

	var attempts atomic.Int64
	c.onAttempt = func() { attempts.Add(1) }

	oversized := strings.Repeat("a", int(c.nc.MaxPayload())+4096)
	err := c.Submit(t.Context(), testJob(), testPayload{Title: oversized})

	require.Error(t, err)
	assert.True(t, IsPublishError(err))
	assert.ErrorIs(t, err, nats.ErrMaxPayload)
	assert.Equal(t, int64(1), attempts.Load())
}

func TestSubmit_UnmarshalablePayload(t *testing.T) {
	c := connectTest(t, testOptions(startNATS(t)))
	require.NoError(t, c.Declare([]Job{testJob()}, nil))

	err := c.Submit(t.Context(), testJob(), make(chan int))
	assert.True(t, IsPublishError(err))
}

func TestMetrics_CountPublishes(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })

	o := testOptions(startNATS(t))
	o.Meter = mp.Meter("test")
	c := connectTest(t, o)
	require.NoError(t, c.Declare([]Job{testJob()}, []Broadcast{testBroadcast()}))

	require.NoError(t, c.Submit(t.Context(), testJob(), testPayload{}))
	require.NoError(t, c.Emit(t.Context(), testBroadcast(), testPayload{}))
	require.Error(t, c.Submit(t.Context(), testJob(), make(chan int)))

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &rm))
	got := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if sum, ok := m.Data.(metricdata.Sum[int64]); ok {
				for _, dp := range sum.DataPoints {
					got[m.Name] += dp.Value
				}
			}
		}
	}
	assert.Equal(t, int64(1), got["queue.published"])
	assert.Equal(t, int64(1), got["queue.emitted"])
	assert.Equal(t, int64(1), got["queue.publish_failed"])
}

func TestDisabled(t *testing.T) {
	c := Disabled(slog.New(slog.DiscardHandler))

	assert.False(t, c.Enabled())
	require.NoError(t, c.Declare([]Job{testJob()}, []Broadcast{testBroadcast()}))
	require.NoError(t, c.Submit(t.Context(), testJob(), testPayload{}))
	require.NoError(t, c.Submit(t.Context(), Job{Name: "test.undeclared", Version: 1}, testPayload{}))
	require.NoError(t, c.Emit(t.Context(), testBroadcast(), testPayload{}))

	seq, err := c.BroadcastStartSeq(t.Context())
	require.NoError(t, err)
	assert.Equal(t, uint64(0), seq)
	require.NoError(t, c.Close())
}

func TestDisabled_NilLogger(t *testing.T) {
	c := Disabled(nil)
	require.NoError(t, c.Submit(t.Context(), testJob(), testPayload{}))
}

func TestDisabled_DeclareStillValidates(t *testing.T) {
	c := Disabled(slog.New(slog.DiscardHandler))
	assert.True(t, IsInvalidJobError(c.Declare([]Job{{Name: "bad", Version: 1}}, nil)))
}

func TestClose_Twice(t *testing.T) {
	c, err := Connect(t.Context(), testOptions(startNATS(t)))
	require.NoError(t, err)
	assert.True(t, c.Enabled())
	require.NoError(t, c.Close())
	require.NoError(t, c.Close())
}

func TestErrors_Messages(t *testing.T) {
	tests := []struct {
		name  string
		err   error
		check func(error) bool
	}{
		{"publish", &PublishError{Subject: "jobs.a.b.v1", Cause: context.DeadlineExceeded}, IsPublishError},
		{"invalid", &InvalidJobError{Job: testJob(), Reason: "x"}, IsInvalidJobError},
		{"invalid broadcast", &InvalidBroadcastError{Broadcast: testBroadcast(), Reason: "x"}, IsInvalidBroadcastError},
		{"undeclared job", &UndeclaredJobError{Job: testJob()}, IsUndeclaredJobError},
		{"undeclared broadcast", &UndeclaredBroadcastError{Broadcast: testBroadcast()}, IsUndeclaredBroadcastError},
		{"wiring", &HandlerWiringError{Duplicate: []string{"jobs.a.b.v1"}}, IsHandlerWiringError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Contains(t, tt.err.Error(), "queue:")
			assert.True(t, tt.check(tt.err))
			assert.False(t, tt.check(context.Canceled))
		})
	}
	assert.ErrorIs(t, &PublishError{Cause: context.DeadlineExceeded}, context.DeadlineExceeded)
}
