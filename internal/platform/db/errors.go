package db

import (
	"errors"
	"fmt"
)

// InvalidRoleError is returned when a configured role name cannot be used as a Postgres identifier.
type InvalidRoleError struct {
	Role   string
	Reason string
}

func (e *InvalidRoleError) Error() string {
	return fmt.Sprintf("db: invalid role %q: %s", e.Role, e.Reason)
}

// IsInvalidRoleError reports whether err wraps an *InvalidRoleError.
func IsInvalidRoleError(err error) bool {
	_, ok := errors.AsType[*InvalidRoleError](err)
	return ok
}
