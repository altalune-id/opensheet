package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/worker"
)

// WorkerName is the name the consumer worker registers under.
const WorkerName = "queue.consumer"

// Delivery limits for the consumer.
const (
	MaxAttempts       = 5
	HandlerTimeout    = 25 * time.Second
	DLQPublishTimeout = 4 * time.Second
	AckWait           = 30 * time.Second
)

const (
	consumerMaxDeliver    = -1
	consumerMaxAckPending = 16
	pullMaxMessages       = 1
	pullHeartbeat         = 15 * time.Second
	drainWait             = 8 * time.Second
	dlqRetryDelay         = time.Minute
	unreadableRetryDelay  = time.Minute
	probeTimeout          = 2 * time.Second
	dlqErrorMaxLen        = 1 << 10

	reasonExhausted = "exhausted"
	reasonPermanent = "permanent"
	outcomeOK       = "ok"
	outcomeRetry    = "retry"
	outcomeDead     = "dead_letter"
	pastCapCause    = "outcome unknown: redelivered past the attempt cap"
	reportDead      = "queue: dead-lettered"
	reportDLQFailed = "queue: dlq publish failed"
)

// Handler binds one job to the domain code that runs it.
type Handler struct {
	Job            Job
	Handle         func(ctx context.Context, m Message) error
	OnDeadLetter   func(ctx context.Context, d DeadLetter) error
	SuppressReport bool
}

// Message is one delivery as a handler or listener sees it.
type Message struct {
	ID           uuid.UUID
	Name         string
	Version      int
	Data         []byte
	NumDelivered int
	CreatedAt    time.Time
}

// DeadLetter is a message the consumer moved to the DLQ, with why.
type DeadLetter struct {
	Message
	Reason string
	Cause  string
}

// Provider is a domain that contributes queue handlers.
type Provider interface{ ConsumerHandlers() []Handler }

// Decode unmarshals m.Data into T.
func Decode[T any](m Message) (T, error) {
	var v T
	if err := json.Unmarshal(m.Data, &v); err != nil {
		var zero T
		return zero, fmt.Errorf("queue: decode %s v%d: %w", m.Name, m.Version, err)
	}
	return v, nil
}

// Permanent marks err as not worth retrying.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &PermanentError{Cause: err}
}

// Consumer runs every handler's consume loop as one worker.Worker.
type Consumer struct {
	client    *Client
	bindings  []binding
	delays    func(attempt int) time.Duration
	dlqRetry  time.Duration
	heartbeat time.Duration
}

type binding struct {
	h    Handler
	cons jetstream.Consumer
}

var _ worker.Worker = (*Consumer)(nil)

// NewConsumer creates each handler's durable consumer, refusing an invalid or duplicated job.
func NewConsumer(ctx context.Context, c *Client, hs []Handler) (*Consumer, error) {
	jobs := make([]Job, 0, len(hs))
	for _, h := range hs {
		jobs = append(jobs, h.Job)
	}
	if err := checkWiring(jobs, func(i int) bool { return hs[i].Handle != nil }); err != nil {
		return nil, err
	}
	if !c.Enabled() {
		return nil, &DisabledError{}
	}
	bindings := make([]binding, 0, len(hs))
	for _, h := range hs {
		cons, err := c.js.CreateOrUpdateConsumer(ctx, streamWork, consumerConfig(h.Job))
		if err != nil {
			return nil, fmt.Errorf("queue: consumer %s: %w", h.Job.Subject(), err)
		}
		bindings = append(bindings, binding{h: h, cons: cons})
	}
	return &Consumer{client: c, bindings: bindings, delays: nakDelay, dlqRetry: dlqRetryDelay, heartbeat: pullHeartbeat}, nil
}

// Name implements worker.Worker.
func (c *Consumer) Name() string { return WorkerName }

// Run consumes until ctx is cancelled, returning nil then, or *ConsumerClosedError if a loop stops first.
func (c *Consumer) Run(ctx context.Context) error {
	loops := make([]jetstream.ConsumeContext, 0, len(c.bindings))
	for _, b := range c.bindings {
		cc, err := b.cons.Consume(c.handle(ctx, b.h),
			jetstream.PullMaxMessages(pullMaxMessages),
			jetstream.PullHeartbeat(c.heartbeat),
			c.consumeErrHandler(ctx, b))
		if err != nil {
			stopAll(loops)
			return fmt.Errorf("queue: consume %s: %w", b.h.Job.Subject(), err)
		}
		loops = append(loops, cc)
	}

	quit := make(chan struct{})
	defer close(quit)
	closed := make(chan string, len(loops))
	for i, cc := range loops {
		go func() {
			select {
			case <-cc.Closed():
				closed <- c.bindings[i].h.Job.Subject()
			case <-quit:
			}
		}()
	}

	select {
	case <-ctx.Done():
		c.drain(loops)
		return nil
	case subject := <-closed:
		stopAll(loops)
		if ctx.Err() != nil {
			return nil
		}
		return &ConsumerClosedError{Subject: subject}
	}
}

func (c *Consumer) drain(loops []jetstream.ConsumeContext) {
	for _, cc := range loops {
		cc.Drain()
	}
	deadline := time.After(drainWait)
	for i, cc := range loops {
		select {
		case <-cc.Closed():
		case <-deadline:
			c.client.log.Warn("queue: drain timed out", slog.String("subject", c.bindings[i].h.Job.Subject()))
			return
		}
	}
}

func stopAll(loops []jetstream.ConsumeContext) {
	for _, cc := range loops {
		cc.Stop()
	}
}

func (c *Consumer) consumeErrHandler(runCtx context.Context, b binding) jetstream.ConsumeErrHandler {
	subject := b.h.Job.Subject()
	return func(cc jetstream.ConsumeContext, err error) {
		c.client.log.Warn("queue: consume", slog.String("subject", subject), slog.String("err", err.Error()))
		if !errors.Is(err, jetstream.ErrNoHeartbeat) && !errors.Is(err, nats.ErrNoResponders) {
			return
		}
		go c.probe(runCtx, cc, b.cons, subject)
	}
}

func (c *Consumer) probe(runCtx context.Context, cc jetstream.ConsumeContext, cons jetstream.Consumer, subject string) {
	ctx, cancel := context.WithTimeout(runCtx, probeTimeout)
	defer cancel()
	_, err := cons.Info(ctx)
	if !errors.Is(err, jetstream.ErrConsumerNotFound) && !errors.Is(err, jetstream.ErrStreamNotFound) {
		return
	}
	c.client.log.Error("queue: durable consumer is gone", slog.String("subject", subject), slog.String("err", err.Error()))
	cc.Stop()
}

func (c *Consumer) handle(runCtx context.Context, h Handler) jetstream.MessageHandler {
	subject := h.Job.Subject()
	pub := c.client.jobPublication(h.Job)
	return func(msg jetstream.Msg) {
		meta, err := msg.Metadata()
		if err != nil {
			c.client.log.Error("queue: message metadata", slog.String("subject", subject), slog.String("err", err.Error()))
			c.count(context.Background(), outcomeRetry)
			c.settle(msg.NakWithDelay(unreadableRetryDelay), "nak", subject)
			return
		}
		n := MaxAttempts + 1
		if meta.NumDelivered <= MaxAttempts+1 {
			n = int(meta.NumDelivered)
		}
		ctx := extractTrace(context.WithoutCancel(runCtx), msg.Headers())
		ctx, span := c.client.tracer.Start(ctx, "queue.consume", trace.WithSpanKind(trace.SpanKindConsumer),
			trace.WithAttributes(
				attribute.String("messaging.system", "nats"),
				attribute.String("job", h.Job.Name),
				attribute.String("subject", subject),
				attribute.String("stream", streamWork),
				attribute.String("consumer", durableName(subject)),
				attribute.Int("num_delivered", n),
			))
		defer span.End()
		c.client.metrics.deliveryAttempts.Record(ctx, int64(n))

		m, tc, scoped, perr := decodeMessage(pub, msg, n)
		if perr != nil {
			c.deadLetter(ctx, h, msg, m, meta, reasonPermanent, perr)
			return
		}
		if scoped {
			ctx = tenant.Into(ctx, tc)
			span.SetAttributes(tenantAttributes(tc)...)
		}
		if n > MaxAttempts {
			c.deadLetter(ctx, h, msg, m, meta, reasonExhausted, errors.New(pastCapCause))
			return
		}
		herr := c.client.safely(ctx, subject, HandlerTimeout, func(ctx context.Context) error { return h.Handle(ctx, m) })
		if herr != nil {
			span.RecordError(herr)
			span.SetStatus(codes.Error, herr.Error())
		}
		switch {
		case herr == nil:
			c.count(ctx, outcomeOK)
			c.settle(msg.Ack(), "ack", subject)
		case IsPermanentError(herr):
			c.deadLetter(ctx, h, msg, m, meta, reasonPermanent, herr)
		case n < MaxAttempts:
			c.client.log.WarnContext(ctx, "queue: handler failed, retrying",
				slog.String("subject", subject), slog.Int("attempt", n), slog.String("err", herr.Error()))
			c.count(ctx, outcomeRetry)
			c.settle(msg.NakWithDelay(c.delays(n)), "nak", subject)
		default:
			c.deadLetter(ctx, h, msg, m, meta, reasonExhausted, herr)
		}
	}
}

func (c *Client) safely(ctx context.Context, subject string, timeout time.Duration, fn func(context.Context) error) (err error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			c.log.ErrorContext(ctx, "queue: handler panicked", slog.String("subject", subject), slog.Any("panic", r), slog.String("stack", string(debug.Stack())))
			err = fmt.Errorf("queue: handler panicked: %v", r)
		}
	}()
	return fn(ctx)
}

func decodeMessage(p publication, msg jetstream.Msg, n int) (Message, tenant.Context, bool, error) {
	h := msg.Headers()
	m := Message{Name: h.Get(p.nameHeader), Data: msg.Data(), NumDelivered: n}
	var errs []error
	id, err := uuid.Parse(h.Get(headerMsgID))
	if err != nil {
		errs = append(errs, fmt.Errorf("queue: header %s: %w", headerMsgID, err))
	}
	m.ID = id
	version, err := strconv.Atoi(h.Get(p.versionHeader))
	if err != nil {
		errs = append(errs, fmt.Errorf("queue: header %s: %w", p.versionHeader, err))
	}
	m.Version = version
	created, err := time.Parse(time.RFC3339Nano, h.Get(headerCreatedAt))
	if err != nil {
		errs = append(errs, fmt.Errorf("queue: header %s: %w", headerCreatedAt, err))
	}
	m.CreatedAt = created
	if len(errs) == 0 && (m.Name != p.name || m.Version != p.version) {
		errs = append(errs, fmt.Errorf("queue: headers name %s v%d, subject is %s", m.Name, m.Version, p.subject))
	}
	tc, scoped, err := tenantFrom(h)
	if err != nil {
		errs = append(errs, err)
	}
	return m, tc, scoped, errors.Join(errs...)
}

func (c *Consumer) deadLetter(ctx context.Context, h Handler, msg jetstream.Msg, m Message, meta *jetstream.MsgMetadata, reason string, cause error) {
	subject := h.Job.Subject()
	attempts := strconv.FormatUint(meta.NumDelivered, 10)
	causeText := apperror.TruncateCause(cause.Error(), dlqErrorMaxLen)
	span := trace.SpanFromContext(ctx)
	span.SetStatus(codes.Error, reason+": "+causeText)

	dlq := nats.NewMsg(dlqSubject(subject))
	dlq.Data = msg.Data()
	dlq.Header = maps.Clone(msg.Headers())
	if dlq.Header == nil {
		dlq.Header = nats.Header{}
	}
	dlq.Header.Set(headerDlqReason, reason)
	dlq.Header.Set(headerDlqError, causeText)
	dlq.Header.Set(headerDlqAttempts, attempts)
	dlq.Header.Set(headerDlqStreamSeq, strconv.FormatUint(meta.Sequence.Stream, 10))

	attrs := []any{"subject", subject, "reason", reason, "attempts", attempts, "msg_id", m.ID.String()}
	if err := c.client.deliver(ctx, c.dlqPublication(h.Job), dlq, msg.Headers().Get(headerMsgID)); err != nil {
		span.RecordError(err)
		c.settle(msg.NakWithDelay(c.dlqRetry), "nak", subject)
		_ = c.client.unexpected(ctx, reportDLQFailed, err, attrs...)
		return
	}
	c.settle(msg.Term(), "term", subject)
	if h.OnDeadLetter != nil {
		d := DeadLetter{Message: m, Reason: reason, Cause: causeText}
		if err := c.client.safely(ctx, subject, HandlerTimeout, func(ctx context.Context) error { return h.OnDeadLetter(ctx, d) }); err != nil {
			c.client.log.ErrorContext(ctx, "queue: OnDeadLetter", slog.String("subject", subject), slog.String("err", err.Error()))
		}
	}
	if h.SuppressReport {
		c.client.log.WarnContext(ctx, reportDead, attrs...)
		return
	}
	_ = c.client.unexpected(ctx, reportDead, cause, attrs...)
}

func (c *Consumer) dlqPublication(j Job) publication {
	return publication{
		span:    "queue.publish",
		stream:  streamDLQ,
		subject: dlqSubject(j.Subject()),
		kind:    "job",
		name:    j.Name,
		version: j.Version,
		budget:  DLQPublishTimeout,
		ok:      c.client.metrics.consumed,
		okOpts:  []metric.AddOption{metric.WithAttributes(attribute.String("outcome", outcomeDead))},
		failed:  c.client.metrics.dlqPublishFailed,
	}
}

func (c *Consumer) count(ctx context.Context, outcome string) {
	c.client.metrics.consumed.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", outcome)))
}

func (c *Consumer) settle(err error, op, subject string) {
	if err != nil {
		c.client.log.Warn("queue: "+op, slog.String("subject", subject), slog.String("err", err.Error()))
	}
}

func nakDelay(attempt int) time.Duration {
	switch attempt {
	case 1:
		return 10 * time.Second
	case 2:
		return time.Minute
	case 3:
		return 5 * time.Minute
	default:
		return 15 * time.Minute
	}
}
