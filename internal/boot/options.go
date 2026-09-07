package boot

import "log/slog"

// Option tunes what BootServer wires and starts.
type Option func(*options)

type options struct {
	scheduler     bool
	schedulerOnly bool
	logger        *slog.Logger
}

func newOptions() *options { return &options{scheduler: true} }

// WithScheduler enables or disables the periodic-job runner, overriding scheduler.enabled.
func WithScheduler(on bool) Option { return func(o *options) { o.scheduler = on } }

// WithSchedulerOnly runs the scheduler plus a health-only listener, with no web or API handler.
func WithSchedulerOnly(on bool) Option { return func(o *options) { o.schedulerOnly = on } }

// WithLogger replaces the logger built from log config, so an embedder or test can observe boot and request output.
func WithLogger(l *slog.Logger) Option { return func(o *options) { o.logger = l } }
