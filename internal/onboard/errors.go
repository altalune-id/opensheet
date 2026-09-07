package onboard

import (
	"errors"

	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/template/gen/go/apperror/v1"
	"altalune.id/template/internal/apperror"
)

// NotOnboardedError signals no bootstrap row is present.
type NotOnboardedError struct{}

func (e *NotOnboardedError) Error() string { return "onboard: not onboarded" }

// ToAppError maps NotOnboardedError to the canonical FailedPrecondition envelope.
func (e *NotOnboardedError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeOnboardingRequired,
		"Onboarding required",
		codes.FailedPrecondition,
		&apperrorv1.ErrorDetail{Code: apperror.CodeOnboardingRequired},
	)
}

// IsNotOnboardedError reports whether err's tree contains a *NotOnboardedError.
func IsNotOnboardedError(err error) bool {
	_, ok := errors.AsType[*NotOnboardedError](err)
	return ok
}

// AlreadyOnboardedError signals the singleton row was already inserted.
type AlreadyOnboardedError struct{}

func (e *AlreadyOnboardedError) Error() string { return "onboard: already onboarded" }

// ToAppError maps AlreadyOnboardedError to the canonical AlreadyExists envelope.
func (e *AlreadyOnboardedError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeOnboardingAlreadyDone,
		"Onboarding already completed",
		codes.AlreadyExists,
		&apperrorv1.ErrorDetail{Code: apperror.CodeOnboardingAlreadyDone},
	)
}

// IsAlreadyOnboardedError reports whether err's tree contains an *AlreadyOnboardedError.
func IsAlreadyOnboardedError(err error) bool {
	_, ok := errors.AsType[*AlreadyOnboardedError](err)
	return ok
}

// InvalidMethodError signals a rejected method or invariant on New.
type InvalidMethodError struct {
	Method string
	Reason string
}

func (e *InvalidMethodError) Error() string {
	if e.Reason != "" {
		return "onboard: invalid method: " + e.Reason
	}
	return "onboard: invalid method: " + e.Method
}

// ToAppError maps InvalidMethodError to the canonical InvalidArgument envelope.
func (e *InvalidMethodError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeValidation,
		"Invalid onboarding method",
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{Code: apperror.CodeValidation},
	)
}

// IsInvalidMethodError reports whether err's tree contains an *InvalidMethodError.
func IsInvalidMethodError(err error) bool {
	_, ok := errors.AsType[*InvalidMethodError](err)
	return ok
}
