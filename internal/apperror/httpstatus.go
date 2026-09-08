package apperror

import (
	"net/http"

	"google.golang.org/grpc/codes"
)

//nolint:gochecknoglobals // immutable override table.
var httpStatusOverrides = map[string]int{
	CodeSheetPublicDisabled:      http.StatusForbidden,
	CodeSheetPayloadTooLarge:     http.StatusRequestEntityTooLarge,
	CodeCredentialReauthNeeded:   http.StatusFailedDependency,
	CodeCredentialInUse:          http.StatusConflict,
	CodeSheetIdempotencyMismatch: http.StatusUnprocessableEntity,
}

// HTTPStatus maps the error to an HTTP status, preferring a per-code override over its gRPC code.
func (e *AppError) HTTPStatus() int {
	if e == nil {
		return http.StatusInternalServerError
	}
	if s, ok := httpStatusOverrides[e.Code()]; ok {
		return s
	}
	switch e.GRPCCode() {
	case codes.InvalidArgument, codes.FailedPrecondition, codes.OutOfRange:
		return http.StatusBadRequest
	case codes.Unauthenticated:
		return http.StatusUnauthorized
	case codes.PermissionDenied:
		return http.StatusForbidden
	case codes.NotFound:
		return http.StatusNotFound
	case codes.AlreadyExists, codes.Aborted:
		return http.StatusConflict
	case codes.ResourceExhausted:
		return http.StatusTooManyRequests
	case codes.Unavailable:
		return http.StatusBadGateway
	case codes.DeadlineExceeded:
		return http.StatusGatewayTimeout
	case codes.Unimplemented:
		return http.StatusNotImplemented
	}
	return http.StatusInternalServerError
}
