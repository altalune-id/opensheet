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

// SessionRequiredError signals a key or token principal calling a procedure only a signed-in person may call.
type SessionRequiredError struct{}

func (*SessionRequiredError) Error() string { return "authn: a signed-in session is required" }

// IsSessionRequiredError reports whether err's tree contains a *SessionRequiredError.
func IsSessionRequiredError(err error) bool {
	_, ok := errors.AsType[*SessionRequiredError](err)
	return ok
}

// UnavailableMessage is the caller-facing text of an *UnavailableError on every surface.
const UnavailableMessage = "credential verification unavailable"

// UnavailableError signals that a credential could not be checked because a dependency failed, not that it was refused.
type UnavailableError struct{ Cause error }

func (e *UnavailableError) Error() string {
	if e.Cause == nil {
		return "authn: credential check unavailable"
	}
	return "authn: credential check unavailable: " + e.Cause.Error()
}

func (e *UnavailableError) Unwrap() error { return e.Cause }

// IsUnavailableError reports whether err's tree contains an *UnavailableError.
func IsUnavailableError(err error) bool {
	_, ok := errors.AsType[*UnavailableError](err)
	return ok
}
