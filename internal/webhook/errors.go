package webhook

import (
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/internal/apperror"
)

// NotFoundError reports that no endpoint matched the lookup.
type NotFoundError struct {
	ID string
}

func (e *NotFoundError) Error() string {
	if e.ID == "" {
		return "webhook: endpoint not found"
	}
	return fmt.Sprintf("webhook: endpoint not found: id=%s", e.ID)
}

// ToAppError converts the typed error into the wire envelope.
func (e *NotFoundError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeWebhookEndpointNotFound,
		"Webhook endpoint not found",
		codes.NotFound,
		&apperrorv1.ErrorDetail{Code: apperror.CodeWebhookEndpointNotFound},
	)
}

// IsNotFoundError reports whether err's tree contains a *NotFoundError.
func IsNotFoundError(err error) bool {
	_, ok := errors.AsType[*NotFoundError](err)
	return ok
}

// InvalidURLError reports that the endpoint URL breaks its invariants.
type InvalidURLError struct {
	Reason string
}

func (e *InvalidURLError) Error() string {
	return fmt.Sprintf("webhook: invalid endpoint url: %s", e.Reason)
}

// ToAppError converts the typed error into the wire envelope.
func (e *InvalidURLError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeWebhookInvalidURL,
		"Webhook URL is invalid: it "+e.Reason,
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{Code: apperror.CodeWebhookInvalidURL},
	)
}

// IsInvalidURLError reports whether err's tree contains an *InvalidURLError.
func IsInvalidURLError(err error) bool {
	_, ok := errors.AsType[*InvalidURLError](err)
	return ok
}

// InvalidEventTypesError reports that the subscribed event types break their invariants.
type InvalidEventTypesError struct {
	Reason string
}

func (e *InvalidEventTypesError) Error() string {
	return fmt.Sprintf("webhook: invalid event types: %s", e.Reason)
}

// ToAppError converts the typed error into the wire envelope.
func (e *InvalidEventTypesError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeWebhookInvalidEventTypes,
		"Webhook event types are invalid: "+e.Reason,
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{Code: apperror.CodeWebhookInvalidEventTypes},
	)
}

// IsInvalidEventTypesError reports whether err's tree contains an *InvalidEventTypesError.
func IsInvalidEventTypesError(err error) bool {
	_, ok := errors.AsType[*InvalidEventTypesError](err)
	return ok
}

// InvalidDescriptionError reports that the endpoint description breaks its invariants.
type InvalidDescriptionError struct {
	Reason string
}

func (e *InvalidDescriptionError) Error() string {
	return fmt.Sprintf("webhook: invalid endpoint description: %s", e.Reason)
}

// ToAppError converts the typed error into the wire envelope.
func (e *InvalidDescriptionError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeValidation,
		"Webhook description is invalid: it "+e.Reason,
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{Code: apperror.CodeValidation},
	)
}

// IsInvalidDescriptionError reports whether err's tree contains an *InvalidDescriptionError.
func IsInvalidDescriptionError(err error) bool {
	_, ok := errors.AsType[*InvalidDescriptionError](err)
	return ok
}

// EndpointLimitError reports that the project already has the maximum number of endpoints.
type EndpointLimitError struct {
	Limit int
}

func (e *EndpointLimitError) Error() string {
	return fmt.Sprintf("webhook: endpoint limit reached: limit=%d", e.Limit)
}

// ToAppError converts the typed error into the wire envelope.
func (e *EndpointLimitError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeWebhookEndpointLimit,
		fmt.Sprintf("A project can have at most %d webhook endpoints", e.Limit),
		codes.FailedPrecondition,
		&apperrorv1.ErrorDetail{Code: apperror.CodeWebhookEndpointLimit},
	)
}

// IsEndpointLimitError reports whether err's tree contains an *EndpointLimitError.
func IsEndpointLimitError(err error) bool {
	_, ok := errors.AsType[*EndpointLimitError](err)
	return ok
}

// DeliveryNotRetryableError reports a retry refused because the delivery has not failed.
type DeliveryNotRetryableError struct {
	ID string
}

func (e *DeliveryNotRetryableError) Error() string {
	return fmt.Sprintf("webhook: delivery not retryable: id=%s", e.ID)
}

// ToAppError converts the typed error into the wire envelope.
func (e *DeliveryNotRetryableError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeWebhookDeliveryNotRetryable,
		"Only a failed delivery can be retried",
		codes.FailedPrecondition,
		&apperrorv1.ErrorDetail{Code: apperror.CodeWebhookDeliveryNotRetryable},
	)
}

// IsDeliveryNotRetryableError reports whether err's tree contains a *DeliveryNotRetryableError.
func IsDeliveryNotRetryableError(err error) bool {
	_, ok := errors.AsType[*DeliveryNotRetryableError](err)
	return ok
}

// DeliveryNotFoundError reports that no delivery of the endpoint matched the lookup.
type DeliveryNotFoundError struct {
	ID string
}

func (e *DeliveryNotFoundError) Error() string {
	return fmt.Sprintf("webhook: delivery not found: id=%s", e.ID)
}

// ToAppError converts the typed error into the wire envelope.
func (e *DeliveryNotFoundError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeWebhookDeliveryNotFound,
		"Webhook delivery not found",
		codes.NotFound,
		&apperrorv1.ErrorDetail{Code: apperror.CodeWebhookDeliveryNotFound},
	)
}

// IsDeliveryNotFoundError reports whether err's tree contains a *DeliveryNotFoundError.
func IsDeliveryNotFoundError(err error) bool {
	_, ok := errors.AsType[*DeliveryNotFoundError](err)
	return ok
}

// EndpointInactiveError reports an operation refused because the endpoint is disabled.
type EndpointInactiveError struct {
	ID string
}

func (e *EndpointInactiveError) Error() string {
	return fmt.Sprintf("webhook: endpoint inactive: id=%s", e.ID)
}

// ToAppError converts the typed error into the wire envelope.
func (e *EndpointInactiveError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeWebhookEndpointInactive,
		"The webhook endpoint is disabled",
		codes.FailedPrecondition,
		&apperrorv1.ErrorDetail{Code: apperror.CodeWebhookEndpointInactive},
	)
}

// IsEndpointInactiveError reports whether err's tree contains an *EndpointInactiveError.
func IsEndpointInactiveError(err error) bool {
	_, ok := errors.AsType[*EndpointInactiveError](err)
	return ok
}

// SecretConflictError reports a secret write refused because the stored secrets changed since they were loaded.
type SecretConflictError struct {
	ID string
}

func (e *SecretConflictError) Error() string {
	return fmt.Sprintf("webhook: secret changed concurrently: id=%s", e.ID)
}

// ToAppError converts the typed error into the wire envelope.
func (e *SecretConflictError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeWebhookSecretConflict,
		"The webhook secret changed concurrently; reload and retry",
		codes.FailedPrecondition,
		&apperrorv1.ErrorDetail{Code: apperror.CodeWebhookSecretConflict},
	)
}

// IsSecretConflictError reports whether err's tree contains a *SecretConflictError.
func IsSecretConflictError(err error) bool {
	_, ok := errors.AsType[*SecretConflictError](err)
	return ok
}

// InvalidAttemptError reports an attempt field that cannot be persisted under the tenant scope on ctx.
type InvalidAttemptError struct {
	Field  string
	Reason string
}

func (e *InvalidAttemptError) Error() string {
	return fmt.Sprintf("webhook: invalid attempt: %s %s", e.Field, e.Reason)
}

// IsInvalidAttemptError reports whether err's tree contains an *InvalidAttemptError.
func IsInvalidAttemptError(err error) bool {
	_, ok := errors.AsType[*InvalidAttemptError](err)
	return ok
}

// NoUnitOfWorkError reports a call that needs a transaction on ctx but found none.
type NoUnitOfWorkError struct{}

func (e *NoUnitOfWorkError) Error() string {
	return "webhook: no unit of work: the caller must run inside a transaction"
}

// IsNoUnitOfWorkError reports whether err's tree contains a *NoUnitOfWorkError.
func IsNoUnitOfWorkError(err error) bool {
	_, ok := errors.AsType[*NoUnitOfWorkError](err)
	return ok
}

// SecretUnavailableError reports that the endpoint's signing secret could not be opened.
type SecretUnavailableError struct {
	Cause error
}

func (e *SecretUnavailableError) Error() string {
	msg := "webhook: signing secret unavailable: rotate the endpoint secret to recover"
	if e.Cause == nil {
		return msg
	}
	return msg + ": " + e.Cause.Error()
}

func (e *SecretUnavailableError) Unwrap() error { return e.Cause }

// IsSecretUnavailableError reports whether err's tree contains a *SecretUnavailableError.
func IsSecretUnavailableError(err error) bool {
	_, ok := errors.AsType[*SecretUnavailableError](err)
	return ok
}

// DeliveryFailedError reports that the receiver answered with a non-2xx status.
type DeliveryFailedError struct {
	StatusCode int
}

func (e *DeliveryFailedError) Error() string {
	return fmt.Sprintf("webhook: delivery failed: status=%d", e.StatusCode)
}

// IsDeliveryFailedError reports whether err's tree contains a *DeliveryFailedError.
func IsDeliveryFailedError(err error) bool {
	_, ok := errors.AsType[*DeliveryFailedError](err)
	return ok
}
