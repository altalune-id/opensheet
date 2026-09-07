package scheduler

import "context"

// Provider is the zero-arg port a domain's Scheduler adapter implements to contribute Jobs.
type Provider interface {
	SchedulerJobs() []Job
}

// Tenants fans a ScopeTenant Job out over every tenant, binding each to ctx.
type Tenants interface {
	Each(ctx context.Context, fn func(ctx context.Context, tenantID string) error) error
}

// Locker serializes a Singleton Job across processes. NOTE: release must not depend on ctx still being live.
type Locker interface {
	TryLock(ctx context.Context, name string) (release func(), acquired bool, err error)
}

// ErrorReporter escalates job failures.
type ErrorReporter interface {
	Report(ctx context.Context, message string, cause error, attrs ...any)
}
