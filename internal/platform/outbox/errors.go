package outbox

import "errors"

// NotFoundError reports an entry that does not exist inside the caller's org.
type NotFoundError struct{ ID string }

func (e *NotFoundError) Error() string { return "outbox: entry " + e.ID + " not found" }

// IsNotFoundError reports whether err's tree contains a *NotFoundError.
func IsNotFoundError(err error) bool {
	_, ok := errors.AsType[*NotFoundError](err)
	return ok
}

// TerminalStateError reports a transition refused because the entry already settled.
type TerminalStateError struct {
	ID     string
	Status Status
}

func (e *TerminalStateError) Error() string {
	return "outbox: entry " + e.ID + " is already " + string(e.Status)
}

// IsTerminalStateError reports whether err's tree contains a *TerminalStateError.
func IsTerminalStateError(err error) bool {
	_, ok := errors.AsType[*TerminalStateError](err)
	return ok
}

// StaleClaimError reports a settle refused because the entry was re-claimed under a later attempt.
type StaleClaimError struct {
	ID      string
	Claimed int
	Current int
}

func (e *StaleClaimError) Error() string {
	return "outbox: entry " + e.ID + " was re-claimed since the settling attempt"
}

// IsStaleClaimError reports whether err's tree contains a *StaleClaimError.
func IsStaleClaimError(err error) bool {
	_, ok := errors.AsType[*StaleClaimError](err)
	return ok
}

// NotFailedError reports a requeue refused because the entry is not in StatusFailed.
type NotFailedError struct {
	ID     string
	Status Status
}

func (e *NotFailedError) Error() string {
	return "outbox: entry " + e.ID + " is " + string(e.Status) + ", not failed"
}

// IsNotFailedError reports whether err's tree contains a *NotFailedError.
func IsNotFailedError(err error) bool {
	_, ok := errors.AsType[*NotFailedError](err)
	return ok
}

// InvalidEntryError reports an entry field that cannot be persisted.
type InvalidEntryError struct {
	Field  string
	Reason string
}

func (e *InvalidEntryError) Error() string {
	return "outbox: invalid entry: " + e.Field + " " + e.Reason
}

// IsInvalidEntryError reports whether err's tree contains an *InvalidEntryError.
func IsInvalidEntryError(err error) bool {
	_, ok := errors.AsType[*InvalidEntryError](err)
	return ok
}
