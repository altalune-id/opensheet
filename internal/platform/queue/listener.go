package queue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/worker"
)

// ListenerName is the name the broadcast listener worker registers under.
const ListenerName = "queue.listener"

const listenerTimeout = 5 * time.Second

const (
	listenerSubject      = "broadcast"
	reportListenerFailed = "queue: broadcast listener failed"
)

// Listener binds one broadcast to the code that refreshes this instance's state.
type Listener struct {
	Broadcast Broadcast
	Handle    func(ctx context.Context, m Message) error
}

// ListenerProvider is implemented by anything that owns per-instance state a broadcast refreshes.
type ListenerProvider interface{ Listeners() []Listener }

// Listen delivers every broadcast to its listener through one ordered consumer per instance.
type Listen struct {
	client    *Client
	listeners map[string]Listener
	subjects  []string
	startSeq  uint64
}

var _ worker.Worker = (*Listen)(nil)

// NewListener builds the worker that delivers every broadcast from stream sequence startSeq on to ls.
func NewListener(c *Client, ls []Listener, startSeq uint64) (*Listen, error) {
	broadcasts := make([]Broadcast, 0, len(ls))
	for _, l := range ls {
		broadcasts = append(broadcasts, l.Broadcast)
	}
	if err := checkWiring(broadcasts, func(i int) bool { return ls[i].Handle != nil }); err != nil {
		return nil, err
	}
	if !c.Enabled() {
		return nil, &DisabledError{}
	}
	listeners := make(map[string]Listener, len(ls))
	for _, l := range ls {
		listeners[l.Broadcast.Subject()] = l
	}
	return &Listen{client: c, listeners: listeners, subjects: slices.Sorted(maps.Keys(listeners)), startSeq: startSeq}, nil
}

// Name implements worker.Worker.
func (l *Listen) Name() string { return ListenerName }

// Run listens until ctx is cancelled, returning nil then, or *ConsumerClosedError if the loop stops first.
func (l *Listen) Run(ctx context.Context) error {
	if len(l.subjects) == 0 {
		return nil
	}
	oc, err := l.client.js.OrderedConsumer(ctx, streamBroadcast, jetstream.OrderedConsumerConfig{
		FilterSubjects: l.subjects,
		DeliverPolicy:  jetstream.DeliverByStartSequencePolicy,
		OptStartSeq:    l.startSeq,
	})
	if err != nil {
		return startFailed(ctx, "ordered consumer", err)
	}
	cc, err := oc.Consume(l.handle(ctx), l.consumeErrHandler())
	if err != nil {
		return startFailed(ctx, "consume", err)
	}
	select {
	case <-cc.Closed():
		if !cancelled(ctx) {
			return &ConsumerClosedError{Subject: listenerSubject}
		}
		return nil
	case <-ctx.Done():
	}
	cc.Stop()
	select {
	case <-cc.Closed():
	case <-time.After(drainWait):
		l.client.log.Warn("queue: listener stop timed out", slog.String("subject", listenerSubject))
	}
	return nil
}

func startFailed(ctx context.Context, op string, err error) error {
	switch {
	case cancelled(ctx):
		return nil
	case errors.Is(err, nats.ErrConnectionClosed):
		return &ConsumerClosedError{Subject: listenerSubject}
	}
	return fmt.Errorf("queue: listener: %s: %w", op, err)
}

func cancelled(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

func (l *Listen) consumeErrHandler() jetstream.ConsumeErrHandler {
	return func(_ jetstream.ConsumeContext, err error) {
		l.client.log.Warn("queue: listen", slog.String("err", err.Error()))
	}
}

func (l *Listen) handle(runCtx context.Context) jetstream.MessageHandler {
	return func(msg jetstream.Msg) {
		subject := msg.Subject()
		ctx := extractTrace(context.WithoutCancel(runCtx), msg.Headers())
		ctx, span := l.client.tracer.Start(ctx, "queue.listen", trace.WithSpanKind(trace.SpanKindConsumer),
			trace.WithAttributes(
				attribute.String("messaging.system", "nats"),
				attribute.String("subject", subject),
				attribute.String("stream", streamBroadcast),
			))
		defer span.End()
		if err := l.dispatch(ctx, span, msg); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			l.client.log.ErrorContext(ctx, reportListenerFailed, slog.String("subject", subject), slog.String("err", err.Error()))
			l.client.metrics.broadcastFailed.Add(ctx, 1, metric.WithAttributes(attribute.String("subject", subject)))
			_ = l.client.unexpected(ctx, reportListenerFailed, err, "subject", subject, "msg_id", msg.Headers().Get(headerMsgID))
		}
	}
}

func (l *Listen) dispatch(ctx context.Context, span trace.Span, msg jetstream.Msg) error {
	subject := msg.Subject()
	ls, ok := l.listeners[subject]
	if !ok {
		return errors.New("queue: no listener for " + subject)
	}
	span.SetAttributes(attribute.String("broadcast", ls.Broadcast.Name))
	m, tc, scoped, err := decodeMessage(l.client.broadcastPublication(ls.Broadcast), msg, 1)
	if err != nil {
		return err
	}
	if scoped {
		ctx = tenant.Into(ctx, tc)
		span.SetAttributes(tenantAttributes(tc)...)
	}
	return l.client.safely(ctx, subject, listenerTimeout, func(ctx context.Context) error { return ls.Handle(ctx, m) })
}
