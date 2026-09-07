package scheduler

import (
	"errors"
	"fmt"
)

// UnknownJobError is returned by RunOnce when no Job matches the requested name.
type UnknownJobError struct{ Name string }

func (e *UnknownJobError) Error() string { return fmt.Sprintf("scheduler: unknown job %q", e.Name) }

// IsUnknownJobError reports whether err's tree contains an *UnknownJobError.
func IsUnknownJobError(err error) bool {
	_, ok := errors.AsType[*UnknownJobError](err)
	return ok
}

// BusyError is returned by RunOnce when another run of the same Job is in flight.
type BusyError struct{ Name string }

func (e *BusyError) Error() string {
	return fmt.Sprintf("scheduler: job %q is already running", e.Name)
}

// IsBusyError reports whether err's tree contains a *BusyError.
func IsBusyError(err error) bool {
	_, ok := errors.AsType[*BusyError](err)
	return ok
}

// DuplicateJobError is returned by Register when Name is already taken.
type DuplicateJobError struct{ Name string }

func (e *DuplicateJobError) Error() string {
	return fmt.Sprintf("scheduler: duplicate job name %q", e.Name)
}

// IsDuplicateJobError reports whether err's tree contains a *DuplicateJobError.
func IsDuplicateJobError(err error) bool {
	_, ok := errors.AsType[*DuplicateJobError](err)
	return ok
}

// DrainingError is returned by RunOnce after Shutdown has begun.
type DrainingError struct{}

func (e *DrainingError) Error() string { return "scheduler: runner is draining" }

// IsDrainingError reports whether err's tree contains a *DrainingError.
func IsDrainingError(err error) bool {
	_, ok := errors.AsType[*DrainingError](err)
	return ok
}

// NotLeaderError is returned by RunOnce when another process holds the Job's lock.
type NotLeaderError struct{ Name string }

func (e *NotLeaderError) Error() string {
	return fmt.Sprintf("scheduler: job %q is held by another process", e.Name)
}

// IsNotLeaderError reports whether err's tree contains a *NotLeaderError.
func IsNotLeaderError(err error) bool {
	_, ok := errors.AsType[*NotLeaderError](err)
	return ok
}

// PanicError reports a panic recovered from a Job's Run, carrying the stack.
type PanicError struct {
	Job   string
	Value any
	Stack string
}

func (e *PanicError) Error() string {
	return fmt.Sprintf("scheduler: job %q panicked: %v\n%s", e.Job, e.Value, e.Stack)
}

// IsPanicError reports whether err's tree contains a *PanicError.
func IsPanicError(err error) bool {
	_, ok := errors.AsType[*PanicError](err)
	return ok
}
