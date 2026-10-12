package authn

import "errors"

// UnauthorizedError is the single opaque authentication failure.
type UnauthorizedError struct{ causes error }

func (*UnauthorizedError) Error() string { return "authn: unauthorized" }

// Causes returns why each authenticator refused, for server-side logging only. SECURITY: never reachable through Error or Unwrap, so a cause cannot reach a caller by being formatted or matched.
func (e *UnauthorizedError) Causes() error {
	if e == nil {
		return nil
	}
	return e.causes
}

// IsUnauthorizedError reports whether err is an *UnauthorizedError.
func IsUnauthorizedError(err error) bool {
	var target *UnauthorizedError
	return errors.As(err, &target)
}

// InsufficientScopeError names the scope the credential was missing.
type InsufficientScopeError struct{ Scope string }

func (e *InsufficientScopeError) Error() string {
	return "authn: credential lacks scope " + e.Scope
}

// IsInsufficientScopeError reports whether err is an *InsufficientScopeError.
func IsInsufficientScopeError(err error) bool {
	var target *InsufficientScopeError
	return errors.As(err, &target)
}
