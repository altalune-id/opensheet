package boot

import (
	"context"

	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/queue"
	"altalune.id/opensheet/scheduler"
)

// SECURITY: the one place background work acts as the platform; a request path never reaches it.
func systemHandler(h queue.Handler) queue.Handler {
	if handle := h.Handle; handle != nil {
		h.Handle = func(ctx context.Context, m queue.Message) error { return handle(authz.AsSystem(ctx), m) }
	}
	if dead := h.OnDeadLetter; dead != nil {
		h.OnDeadLetter = func(ctx context.Context, d queue.DeadLetter) error { return dead(authz.AsSystem(ctx), d) }
	}
	return h
}

func systemListener(l queue.Listener) queue.Listener {
	if handle := l.Handle; handle != nil {
		l.Handle = func(ctx context.Context, m queue.Message) error { return handle(authz.AsSystem(ctx), m) }
	}
	return l
}

func systemJob(j scheduler.Job) scheduler.Job {
	if run := j.Run; run != nil {
		j.Run = func(ctx context.Context) error { return run(authz.AsSystem(ctx)) }
	}
	return j
}
