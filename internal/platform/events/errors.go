package events

import (
	"errors"
	"fmt"
)

// UnknownTypeError reports a Type absent from the catalog.
type UnknownTypeError struct{ Type Type }

func (e *UnknownTypeError) Error() string {
	return fmt.Sprintf("events: %q: unknown type", e.Type)
}

// IsUnknownTypeError reports whether err's tree contains an *UnknownTypeError.
func IsUnknownTypeError(err error) bool {
	_, ok := errors.AsType[*UnknownTypeError](err)
	return ok
}

// PayloadMismatchError reports data that is not Type's payload type.
type PayloadMismatchError struct {
	Type Type
	Data any
}

func (e *PayloadMismatchError) Error() string {
	return fmt.Sprintf("events: %q: payload mismatch: got %T", e.Type, e.Data)
}

// IsPayloadMismatchError reports whether err's tree contains a *PayloadMismatchError.
func IsPayloadMismatchError(err error) bool {
	_, ok := errors.AsType[*PayloadMismatchError](err)
	return ok
}
