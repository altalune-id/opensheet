package apperror

import (
	"net/http"
	"testing"

	"google.golang.org/grpc/codes"
)

func TestHTTPStatus_MapsFromGRPCCode(t *testing.T) {
	tests := []struct {
		name string
		code string
		grpc codes.Code
		want int
	}{
		{"not found", CodeSheetNotFound, codes.NotFound, http.StatusNotFound},
		{"unauthenticated", CodeAPIKeyUnauthorized, codes.Unauthenticated, http.StatusUnauthorized},
		{"permission denied", CodeAPIKeyInsufficientScope, codes.PermissionDenied, http.StatusForbidden},
		{"validation", CodeSheetInvalidSlug, codes.InvalidArgument, http.StatusBadRequest},
		{"already exists", CodeSheetAlreadyExists, codes.AlreadyExists, http.StatusConflict},
		{"quota", CodeGoogleQuotaExceeded, codes.ResourceExhausted, http.StatusTooManyRequests},
		{"unavailable", CodeGoogleUnavailable, codes.Unavailable, http.StatusBadGateway},
		{"internal", CodeUnexpectedError, codes.Internal, http.StatusInternalServerError},
		{"payload too large", CodeSheetPayloadTooLarge, codes.InvalidArgument, http.StatusRequestEntityTooLarge},
		{"public disabled", CodeSheetPublicDisabled, codes.FailedPrecondition, http.StatusForbidden},
		{"reauth needed", CodeCredentialReauthNeeded, codes.FailedPrecondition, http.StatusFailedDependency},
		{"credential in use", CodeCredentialInUse, codes.FailedPrecondition, http.StatusConflict},
		{"idempotency mismatch", CodeSheetIdempotencyMismatch, codes.FailedPrecondition, http.StatusUnprocessableEntity},
		{"encryption unavailable", CodeEncryptionUnavailable, codes.FailedPrecondition, http.StatusBadRequest},
		{"deadline exceeded", CodeUnexpectedError, codes.DeadlineExceeded, http.StatusGatewayTimeout},
		{"unimplemented", CodeUnexpectedError, codes.Unimplemented, http.StatusNotImplemented},
		{"aborted", CodeUnexpectedError, codes.Aborted, http.StatusConflict},
		{"out of range", CodeUnexpectedError, codes.OutOfRange, http.StatusBadRequest},
		{"unknown grpc code", CodeUnexpectedError, codes.DataLoss, http.StatusInternalServerError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := New(tc.code, "msg", tc.grpc, nil)
			if got := e.HTTPStatus(); got != tc.want {
				t.Fatalf("HTTPStatus() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestHTTPStatus_NilReceiverIsInternal(t *testing.T) {
	var e *AppError
	if got := e.HTTPStatus(); got != http.StatusInternalServerError {
		t.Fatalf("HTTPStatus() = %d, want 500", got)
	}
}
