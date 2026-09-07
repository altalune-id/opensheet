package worker

import "context"

// Func adapts a plain function into a Worker with the given name.
func Func(name string, fn func(ctx context.Context) error) Worker {
	return funcWorker{name: name, fn: fn}
}

type funcWorker struct {
	name string
	fn   func(context.Context) error
}

func (f funcWorker) Name() string                  { return f.name }
func (f funcWorker) Run(ctx context.Context) error { return f.fn(ctx) }
