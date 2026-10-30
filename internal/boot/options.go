package boot

import (
	"log/slog"
	"net/http"

	"altalune.id/opensheet/internal/platform/outbox"
)

// Option tunes what BootServer wires and starts.
type Option func(*options)

type options struct {
	scheduler     bool
	schedulerOnly bool
	consumer      bool
	consumerOnly  bool
	logger        *slog.Logger
	deliverer     outbox.Deliverer
	dispatch      outbox.WorkerOpts
	oidcHTTP      *http.Client
}

func newOptions() *options { return &options{scheduler: true, consumer: true} }

// WithScheduler enables or disables the periodic-job runner, overriding scheduler.enabled.
func WithScheduler(on bool) Option { return func(o *options) { o.scheduler = on } }

// WithSchedulerOnly runs the scheduler plus a health-only listener, with no web or API handler.
func WithSchedulerOnly(on bool) Option { return func(o *options) { o.schedulerOnly = on } }

// WithConsumer enables or disables the queue job consumer on this instance.
func WithConsumer(on bool) Option { return func(o *options) { o.consumer = on } }

// WithConsumerOnly runs the queue consumer plus a health-only listener, with no scheduler, web or API handler.
func WithConsumerOnly(on bool) Option { return func(o *options) { o.consumerOnly = on } }

// WithLogger replaces the logger built from log config, so an embedder or test can observe boot and request output.
func WithLogger(l *slog.Logger) Option { return func(o *options) { o.logger = l } }

// WithDispatch supplies the outbound Deliverer surface S5 drains the outbox through, plus the dispatch worker's tuning.
func WithDispatch(d outbox.Deliverer, opts outbox.WorkerOpts) Option {
	return func(o *options) {
		o.deliverer = d
		o.dispatch = opts
	}
}

// WithOIDCHTTPClient replaces the client that reaches the OIDC issuer, for a test issuer with its own certificate.
func WithOIDCHTTPClient(c *http.Client) Option { return func(o *options) { o.oidcHTTP = c } }
