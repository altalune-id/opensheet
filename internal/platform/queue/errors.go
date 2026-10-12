package queue

import (
	"errors"
	"strconv"
	"strings"
)

// PublishError reports a Submit or Emit that the stream did not acknowledge.
type PublishError struct {
	Subject string
	Cause   error
}

func (e *PublishError) Error() string {
	return "queue: publish " + e.Subject + ": " + e.Cause.Error()
}

func (e *PublishError) Unwrap() error { return e.Cause }

// IsPublishError reports whether err's tree contains a *PublishError.
func IsPublishError(err error) bool {
	_, ok := errors.AsType[*PublishError](err)
	return ok
}

// InvalidJobError reports a job whose name or version breaks the naming rule.
type InvalidJobError struct {
	Job    Job
	Reason string
}

func (e *InvalidJobError) Error() string {
	return "queue: invalid job " + e.Job.Name + " v" + strconv.Itoa(e.Job.Version) + ": " + e.Reason
}

// IsInvalidJobError reports whether err's tree contains an *InvalidJobError.
func IsInvalidJobError(err error) bool {
	_, ok := errors.AsType[*InvalidJobError](err)
	return ok
}

// InvalidBroadcastError reports a broadcast whose name or version breaks the naming rule.
type InvalidBroadcastError struct {
	Broadcast Broadcast
	Reason    string
}

func (e *InvalidBroadcastError) Error() string {
	return "queue: invalid broadcast " + e.Broadcast.Name + " v" + strconv.Itoa(e.Broadcast.Version) + ": " + e.Reason
}

// IsInvalidBroadcastError reports whether err's tree contains an *InvalidBroadcastError.
func IsInvalidBroadcastError(err error) bool {
	_, ok := errors.AsType[*InvalidBroadcastError](err)
	return ok
}

// UndeclaredJobError reports a Submit of a job no handler was declared for.
type UndeclaredJobError struct{ Job Job }

func (e *UndeclaredJobError) Error() string {
	return "queue: job " + e.Job.Subject() + " is not declared"
}

// IsUndeclaredJobError reports whether err's tree contains an *UndeclaredJobError.
func IsUndeclaredJobError(err error) bool {
	_, ok := errors.AsType[*UndeclaredJobError](err)
	return ok
}

// UndeclaredBroadcastError reports an Emit of a broadcast that was not declared.
type UndeclaredBroadcastError struct{ Broadcast Broadcast }

func (e *UndeclaredBroadcastError) Error() string {
	return "queue: broadcast " + e.Broadcast.Subject() + " is not declared"
}

// IsUndeclaredBroadcastError reports whether err's tree contains an *UndeclaredBroadcastError.
func IsUndeclaredBroadcastError(err error) bool {
	_, ok := errors.AsType[*UndeclaredBroadcastError](err)
	return ok
}

// HandlerWiringError reports subjects that two declarations or handlers both claim.
type HandlerWiringError struct{ Duplicate []string }

func (e *HandlerWiringError) Error() string {
	return "queue: duplicate declaration for " + strings.Join(e.Duplicate, ", ")
}

// IsHandlerWiringError reports whether err's tree contains a *HandlerWiringError.
func IsHandlerWiringError(err error) bool {
	_, ok := errors.AsType[*HandlerWiringError](err)
	return ok
}

// PermanentError marks a handler failure that retrying cannot fix.
type PermanentError struct{ Cause error }

func (e *PermanentError) Error() string { return "queue: permanent: " + e.Cause.Error() }

func (e *PermanentError) Unwrap() error { return e.Cause }

// IsPermanentError reports whether err's tree contains a *PermanentError.
func IsPermanentError(err error) bool {
	_, ok := errors.AsType[*PermanentError](err)
	return ok
}

// ConsumerClosedError reports a consume loop that stopped while the consumer was still running.
type ConsumerClosedError struct{ Subject string }

func (e *ConsumerClosedError) Error() string {
	return "queue: consume loop for " + e.Subject + " closed"
}

// IsConsumerClosedError reports whether err's tree contains a *ConsumerClosedError.
func IsConsumerClosedError(err error) bool {
	_, ok := errors.AsType[*ConsumerClosedError](err)
	return ok
}

// DisabledError reports a call that needs a connected queue on a disabled Client.
type DisabledError struct{}

func (e *DisabledError) Error() string { return "queue: disabled, no NATS connection" }

// IsDisabledError reports whether err's tree contains a *DisabledError.
func IsDisabledError(err error) bool {
	_, ok := errors.AsType[*DisabledError](err)
	return ok
}

// NilHandlerError reports a Handler or Listener registered without a Handle func.
type NilHandlerError struct{ Subject string }

func (e *NilHandlerError) Error() string {
	return "queue: handler for " + e.Subject + " has no Handle func"
}

// IsNilHandlerError reports whether err's tree contains a *NilHandlerError.
func IsNilHandlerError(err error) bool {
	_, ok := errors.AsType[*NilHandlerError](err)
	return ok
}
