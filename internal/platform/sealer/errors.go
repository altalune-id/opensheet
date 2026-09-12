package sealer

import (
	"errors"
	"fmt"
	"strconv"

	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/internal/apperror"
)

// UnavailableError reports that no encryption key is configured.
type UnavailableError struct{}

func (*UnavailableError) Error() string {
	return "sealer: encryption unavailable: no key configured"
}

// ToAppError converts the typed error into the wire envelope.
func (*UnavailableError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeEncryptionUnavailable,
		"Encryption is not configured on this deployment",
		codes.FailedPrecondition,
		&apperrorv1.ErrorDetail{Code: apperror.CodeEncryptionUnavailable},
	)
}

// IsUnavailableError reports whether err's chain contains an *UnavailableError.
func IsUnavailableError(err error) bool {
	_, ok := errors.AsType[*UnavailableError](err)
	return ok
}

// InvalidKeyError reports that the configured key is not 32 bytes.
type InvalidKeyError struct{ Len int }

func (e *InvalidKeyError) Error() string {
	return fmt.Sprintf("sealer: key: got %d bytes, want %d", e.Len, KeyLen)
}

// ToAppError converts the typed error into the wire envelope.
func (e *InvalidKeyError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeEncryptionUnavailable,
		"Encryption key is misconfigured on this deployment",
		codes.FailedPrecondition,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeEncryptionUnavailable,
			Meta: map[string]string{
				"key_len":  strconv.Itoa(e.Len),
				"want_len": strconv.Itoa(KeyLen),
			},
		},
	)
}

// IsInvalidKeyError reports whether err's chain contains an *InvalidKeyError.
func IsInvalidKeyError(err error) bool {
	_, ok := errors.AsType[*InvalidKeyError](err)
	return ok
}

// OpenFailedError reports that a ciphertext could not be authenticated or decrypted.
type OpenFailedError struct{ Cause error }

// SECURITY: the message names no key material and no plaintext.
func (e *OpenFailedError) Error() string {
	if e.Cause == nil {
		return "sealer: open: ciphertext failed authentication"
	}
	return "sealer: open: ciphertext failed authentication: " + e.Cause.Error()
}

func (e *OpenFailedError) Unwrap() error { return e.Cause }

// ToAppError converts the typed error into the wire envelope.
func (*OpenFailedError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeEncryptionOpenFailed,
		"Stored secret could not be decrypted; the encryption key may have changed",
		codes.FailedPrecondition,
		&apperrorv1.ErrorDetail{Code: apperror.CodeEncryptionOpenFailed},
	)
}

// IsOpenFailedError reports whether err's chain contains an *OpenFailedError.
func IsOpenFailedError(err error) bool {
	_, ok := errors.AsType[*OpenFailedError](err)
	return ok
}
