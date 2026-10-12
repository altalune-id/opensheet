package ingest

import "errors"

// UnknownProviderError reports a delivery addressed to a provider this surface does not serve.
type UnknownProviderError struct{}

func (*UnknownProviderError) Error() string { return "ingest: unknown provider" }

// IsUnknownProviderError reports whether err is an *UnknownProviderError.
func IsUnknownProviderError(err error) bool {
	var target *UnknownProviderError
	return errors.As(err, &target)
}

// UnverifiedError reports a delivery whose provider signature did not verify.
type UnverifiedError struct{}

func (*UnverifiedError) Error() string { return "ingest: unverified" }

// IsUnverifiedError reports whether err is an *UnverifiedError.
func IsUnverifiedError(err error) bool {
	var target *UnverifiedError
	return errors.As(err, &target)
}

// PayloadTooLargeError reports a delivery body over the surface's byte limit.
type PayloadTooLargeError struct{}

func (*PayloadTooLargeError) Error() string { return "ingest: payload too large" }

// IsPayloadTooLargeError reports whether err is a *PayloadTooLargeError.
func IsPayloadTooLargeError(err error) bool {
	var target *PayloadTooLargeError
	return errors.As(err, &target)
}

// BadRequestError reports a delivery body that could not be read.
type BadRequestError struct{}

func (*BadRequestError) Error() string { return "ingest: bad request" }

// IsBadRequestError reports whether err is a *BadRequestError.
func IsBadRequestError(err error) bool {
	var target *BadRequestError
	return errors.As(err, &target)
}

// NoHandlerError reports a registered provider whose verified deliveries have nowhere to go.
type NoHandlerError struct{}

func (*NoHandlerError) Error() string { return "ingest: provider has no handler" }

// IsNoHandlerError reports whether err is a *NoHandlerError.
func IsNoHandlerError(err error) bool {
	var target *NoHandlerError
	return errors.As(err, &target)
}
