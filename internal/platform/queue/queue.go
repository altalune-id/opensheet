// Package queue submits jobs and broadcasts over NATS JetStream.
package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/tenant"
)

const (
	connectionName        = "opensheet"
	defaultConnectTimeout = 10 * time.Second
	connectBackoffBase    = 250 * time.Millisecond
	connectBackoffMax     = 2 * time.Second
	dialTimeout           = 2 * time.Second
	drainTimeout          = 5 * time.Second
	publishBudget         = 5 * time.Second
	publishAttempt        = time.Second
	publishRetryGap       = 200 * time.Millisecond
	workMaxAge            = 7 * 24 * time.Hour
	dlqMaxAge             = 30 * 24 * time.Hour
	broadcastMaxAge       = time.Hour
	streamMaxBytes        = 256 << 20
	broadcastMaxBytes     = 64 << 20
	duplicateWindow       = 2 * time.Minute
	streamReplicas        = 1
)

// Job names one kind of queued work and the version of its payload.
type Job struct {
	Name    string
	Version int
}

// Subject returns the NATS subject the job is published on.
func (j Job) Subject() string { return subjectFor(jobSubjectPrefix, j.Name, j.Version) }

// Validate reports an *InvalidJobError when the name or version breaks the naming rule.
func (j Job) Validate() error {
	if reason := nameProblem(j.Name, j.Version); reason != "" {
		return &InvalidJobError{Job: j, Reason: reason}
	}
	return nil
}

// Broadcast names one kind of notice every running instance reacts to.
type Broadcast struct {
	Name    string
	Version int
}

// Subject returns the NATS subject the broadcast is published on.
func (b Broadcast) Subject() string { return subjectFor(broadcastSubjectPrefix, b.Name, b.Version) }

// Validate reports an *InvalidBroadcastError when the name or version breaks the naming rule.
func (b Broadcast) Validate() error {
	if reason := nameProblem(b.Name, b.Version); reason != "" {
		return &InvalidBroadcastError{Broadcast: b, Reason: reason}
	}
	return nil
}

// Options configures Connect; the tracer, meter and reporter are injected, never global.
type Options struct {
	URL, Token     string
	User, Password string
	ConnectTimeout time.Duration
	Log            *slog.Logger
	Tracer         trace.Tracer
	Meter          metric.Meter
	Unexpected     apperror.UnexpectedFunc
}

// Client publishes jobs and broadcasts; a disabled Client accepts every call and publishes nothing.
type Client struct {
	nc         *nats.Conn
	js         jetstream.JetStream
	closed     chan struct{}
	log        *slog.Logger
	tracer     trace.Tracer
	unexpected apperror.UnexpectedFunc
	metrics    metrics
	declared   atomic.Pointer[declaredSet]
	budget     time.Duration
	onAttempt  func()
}

type metrics struct {
	published        metric.Int64Counter
	publishFailed    metric.Int64Counter
	emitted          metric.Int64Counter
	emitFailed       metric.Int64Counter
	consumed         metric.Int64Counter
	dlqPublishFailed metric.Int64Counter
	broadcastFailed  metric.Int64Counter
	deliveryAttempts metric.Int64Histogram
}

type declaredSet struct {
	jobs       map[Job]struct{}
	broadcasts map[Broadcast]struct{}
}

type publication struct {
	span          string
	stream        string
	subject       string
	kind          string
	name          string
	version       int
	nameHeader    string
	versionHeader string
	budget        time.Duration
	ok            metric.Int64Counter
	okOpts        []metric.AddOption
	failed        metric.Int64Counter
}

// Connect dials NATS, ensures the WORK, DLQ and BROADCAST streams exist, and returns a Client.
func Connect(ctx context.Context, o Options) (*Client, error) {
	if o.Unexpected == nil {
		return nil, errors.New("queue: Options.Unexpected is required")
	}
	c, err := newClient(o.Log, o.Tracer, o.Meter)
	if err != nil {
		return nil, err
	}
	c.unexpected = o.Unexpected
	c.closed = make(chan struct{})
	nc, err := dial(ctx, o, c.natsOptions(o))
	if err != nil {
		return nil, err
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("queue: jetstream: %w", err)
	}
	if err := ensureStreams(ctx, js); err != nil {
		nc.Close()
		return nil, err
	}
	c.nc, c.js = nc, js
	return c, nil
}

// Disabled returns a Client with no connection, whose Submit and Emit do nothing.
func Disabled(log *slog.Logger) *Client {
	c, _ := newClient(log, nil, nil)
	c.log.Info("queue: disabled — Submit is a no-op")
	return c
}

func newClient(log *slog.Logger, tracer trace.Tracer, meter metric.Meter) (*Client, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if tracer == nil {
		tracer = tracenoop.NewTracerProvider().Tracer("")
	}
	if meter == nil {
		meter = metricnoop.NewMeterProvider().Meter("")
	}
	m, err := newMetrics(meter)
	if err != nil {
		return nil, err
	}
	c := &Client{log: log, tracer: tracer, metrics: m, budget: publishBudget}
	c.declared.Store(&declaredSet{})
	return c, nil
}

func newMetrics(meter metric.Meter) (metrics, error) {
	var m metrics
	counters := []struct {
		dst  *metric.Int64Counter
		name string
		desc string
	}{
		{&m.published, "queue.published", "Jobs the WORK stream acknowledged."},
		{&m.publishFailed, "queue.publish_failed", "Jobs Submit could not publish."},
		{&m.emitted, "queue.emitted", "Broadcasts the BROADCAST stream acknowledged."},
		{&m.emitFailed, "queue.emit_failed", "Broadcasts Emit could not publish."},
		{&m.consumed, "queue.consumed", "Job deliveries the consumer settled, by outcome."},
		{&m.dlqPublishFailed, "queue.dlq_publish_failed", "Dead letters the DLQ stream did not acknowledge."},
		{&m.broadcastFailed, "queue.broadcast_failed", "Broadcasts a listener failed to handle."},
	}
	for _, ct := range counters {
		counter, err := meter.Int64Counter(ct.name, metric.WithDescription(ct.desc))
		if err != nil {
			return metrics{}, fmt.Errorf("queue: counter %s: %w", ct.name, err)
		}
		*ct.dst = counter
	}
	h, err := meter.Int64Histogram("queue.delivery_attempts", metric.WithDescription("Delivery attempt number of each consumed job."))
	if err != nil {
		return metrics{}, fmt.Errorf("queue: histogram queue.delivery_attempts: %w", err)
	}
	m.deliveryAttempts = h
	return m, nil
}

func (c *Client) natsOptions(o Options) []nats.Option {
	opts := []nats.Option{
		nats.Name(connectionName),
		nats.MaxReconnects(-1),
		nats.Timeout(dialTimeout),
		nats.DrainTimeout(drainTimeout),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			c.log.Warn("queue: disconnected", "err", err)
		}),
		nats.ReconnectHandler(func(*nats.Conn) { c.log.Info("queue: reconnected") }),
		nats.ClosedHandler(func(*nats.Conn) { close(c.closed) }),
	}
	if o.User != "" {
		return append(opts, nats.UserInfo(o.User, o.Password))
	}
	return append(opts, nats.Token(o.Token))
}

func dial(ctx context.Context, o Options, opts []nats.Option) (*nats.Conn, error) {
	timeout := o.ConnectTimeout
	if timeout <= 0 {
		timeout = defaultConnectTimeout
	}
	deadline := time.Now().Add(timeout)
	backoff := connectBackoffBase
	for {
		nc, err := nats.Connect(o.URL, opts...)
		if err == nil {
			return nc, nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, fmt.Errorf("queue: connect within %s: %w", timeout, err)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("queue: connect: %w", errors.Join(ctx.Err(), err))
		case <-time.After(min(backoff, remaining)):
		}
		backoff = min(backoff*2, connectBackoffMax)
	}
}

// Enabled reports whether c holds a NATS connection.
func (c *Client) Enabled() bool { return c.nc != nil }

// Declare records the jobs Submit accepts and the broadcasts Emit accepts, refusing an invalid or duplicated one.
func (c *Client) Declare(jobs []Job, broadcasts []Broadcast) error {
	jobSet, jobDup, err := collect(jobs)
	if err != nil {
		return err
	}
	broadcastSet, broadcastDup, err := collect(broadcasts)
	if err != nil {
		return err
	}
	if err := wiringError(append(jobDup, broadcastDup...)); err != nil {
		return err
	}
	c.declared.Store(&declaredSet{jobs: jobSet, broadcasts: broadcastSet})
	return nil
}

type declarable interface {
	comparable
	Validate() error
	Subject() string
}

func collect[T declarable](items []T) (set map[T]struct{}, dup []string, err error) {
	set = make(map[T]struct{}, len(items))
	for _, it := range items {
		if err := it.Validate(); err != nil {
			return nil, nil, err
		}
		if _, seen := set[it]; seen {
			dup = append(dup, it.Subject())
		}
		set[it] = struct{}{}
	}
	return set, dup, nil
}

func checkWiring[T declarable](items []T, hasHandle func(i int) bool) error {
	for i, it := range items {
		if !hasHandle(i) {
			return &NilHandlerError{Subject: it.Subject()}
		}
	}
	_, dup, err := collect(items)
	if err != nil {
		return err
	}
	return wiringError(dup)
}

func wiringError(dup []string) error {
	if len(dup) == 0 {
		return nil
	}
	slices.Sort(dup)
	return &HandlerWiringError{Duplicate: slices.Compact(dup)}
}

// Submit publishes data as one run of job j, waiting for the stream's ack.
func (c *Client) Submit(ctx context.Context, j Job, data any) error {
	if !c.Enabled() {
		return nil
	}
	if _, ok := c.declared.Load().jobs[j]; !ok {
		return &UndeclaredJobError{Job: j}
	}
	return c.send(ctx, c.jobPublication(j), data)
}

// Emit publishes data to every running instance, waiting for the stream's ack.
func (c *Client) Emit(ctx context.Context, b Broadcast, data any) error {
	if !c.Enabled() {
		return nil
	}
	if _, ok := c.declared.Load().broadcasts[b]; !ok {
		return &UndeclaredBroadcastError{Broadcast: b}
	}
	return c.send(ctx, c.broadcastPublication(b), data)
}

// BroadcastStartSeq returns the BROADCAST stream's next sequence, the point a listener starts from.
func (c *Client) BroadcastStartSeq(ctx context.Context) (uint64, error) {
	if !c.Enabled() {
		return 0, nil
	}
	s, err := c.js.Stream(ctx, streamBroadcast)
	if err != nil {
		return 0, fmt.Errorf("queue: stream %s: %w", streamBroadcast, err)
	}
	info, err := s.Info(ctx)
	if err != nil {
		return 0, fmt.Errorf("queue: stream %s info: %w", streamBroadcast, err)
	}
	return info.State.LastSeq + 1, nil
}

// Close drains the connection and waits for it to close.
func (c *Client) Close() error {
	if !c.Enabled() || c.nc.IsClosed() {
		return nil
	}
	if err := c.nc.Drain(); err != nil && !errors.Is(err, nats.ErrConnectionReconnecting) {
		return fmt.Errorf("queue: drain: %w", err)
	}
	select {
	case <-c.closed:
	case <-time.After(drainTimeout + time.Second):
		c.nc.Close()
	}
	return nil
}

func (c *Client) jobPublication(j Job) publication {
	return publication{
		span:          "queue.publish",
		stream:        streamWork,
		subject:       j.Subject(),
		kind:          "job",
		name:          j.Name,
		version:       j.Version,
		nameHeader:    headerJob,
		versionHeader: headerJobVersion,
		budget:        c.budget,
		ok:            c.metrics.published,
		failed:        c.metrics.publishFailed,
	}
}

func (c *Client) broadcastPublication(b Broadcast) publication {
	return publication{
		span:          "queue.emit",
		stream:        streamBroadcast,
		subject:       b.Subject(),
		kind:          "broadcast",
		name:          b.Name,
		version:       b.Version,
		nameHeader:    headerBroadcast,
		versionHeader: headerBroadcastVersion,
		budget:        c.budget,
		ok:            c.metrics.emitted,
		failed:        c.metrics.emitFailed,
	}
}

func (c *Client) send(ctx context.Context, p publication, data any) error {
	id, err := uuid.NewV7()
	if err != nil {
		return &PublishError{Subject: p.subject, Cause: err}
	}
	return c.publish(ctx, p, data, id)
}

func (c *Client) publish(ctx context.Context, p publication, data any, id uuid.UUID) error {
	body, err := json.Marshal(data)
	if err != nil {
		p.failed.Add(ctx, 1)
		return &PublishError{Subject: p.subject, Cause: err}
	}
	msg := nats.NewMsg(p.subject)
	msg.Data = body
	msg.Header.Set(p.nameHeader, p.name)
	msg.Header.Set(p.versionHeader, strconv.Itoa(p.version))
	msg.Header.Set(headerCreatedAt, time.Now().UTC().Format(time.RFC3339Nano))
	if tc, err := tenant.From(ctx); err == nil {
		setTenant(msg.Header, tc)
	}
	return c.deliver(ctx, p, msg, id.String())
}

func (c *Client) deliver(ctx context.Context, p publication, msg *nats.Msg, id string) error {
	ctx, span := c.tracer.Start(ctx, p.span, trace.WithSpanKind(trace.SpanKindProducer), trace.WithAttributes(spanAttributes(ctx, p)...))
	defer span.End()
	injectTrace(ctx, msg.Header)

	pubCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), p.budget)
	defer cancel()
	if err := c.publishWithRetry(pubCtx, msg, id); err != nil {
		span.RecordError(err)
		p.failed.Add(ctx, 1)
		return &PublishError{Subject: p.subject, Cause: err}
	}
	p.ok.Add(ctx, 1, p.okOpts...)
	return nil
}

func spanAttributes(ctx context.Context, p publication) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0, 6)
	attrs = append(attrs,
		attribute.String("messaging.system", "nats"),
		attribute.String(p.kind, p.name),
		attribute.String("subject", p.subject),
		attribute.String("stream", p.stream),
	)
	tc, err := tenant.From(ctx)
	if err != nil {
		return attrs
	}
	return append(attrs, tenantAttributes(tc)...)
}

func tenantAttributes(tc tenant.Context) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0, 2)
	attrs = append(attrs, attribute.String("org_id", tc.OrgID.String()))
	if tc.ProjectID != uuid.Nil {
		attrs = append(attrs, attribute.String("project_id", tc.ProjectID.String()))
	}
	return attrs
}

func (c *Client) publishWithRetry(ctx context.Context, msg *nats.Msg, id string) error {
	for {
		err := c.publishOnce(ctx, msg, id)
		if err == nil || !retryable(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(publishRetryGap):
		}
	}
}

func (c *Client) publishOnce(ctx context.Context, msg *nats.Msg, id string) error {
	if c.onAttempt != nil {
		c.onAttempt()
	}
	attemptCtx, cancel := context.WithTimeout(ctx, publishAttempt)
	defer cancel()
	_, err := c.js.PublishMsg(attemptCtx, msg, jetstream.WithMsgID(id), jetstream.WithRetryAttempts(0))
	return err
}

func retryable(err error) bool {
	for _, target := range []error{
		context.DeadlineExceeded,
		nats.ErrTimeout,
		jetstream.ErrNoStreamResponse,
		nats.ErrNoResponders,
		nats.ErrConnectionClosed,
		nats.ErrDisconnected,
	} {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}
