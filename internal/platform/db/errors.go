package db

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
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

// IsRowSecurityViolation reports an upsert's conflict update refused on another org's row; a WITH CHECK refusal on the insert is not one.
func IsRowSecurityViolation(err error) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || pgErr.Code != "42501" {
		return false
	}
	// NOTE: the WITH CHECK refusal shares the code and routine, so only the English "(USING expression)" text tells them apart; a translated server fails closed with the raw error.
	return (pgErr.Routine == "" || pgErr.Routine == "ExecWithCheckOptions") &&
		strings.Contains(pgErr.Message, "row-level security policy (USING expression)")
}
