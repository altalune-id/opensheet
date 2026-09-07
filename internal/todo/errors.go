package todo

import (
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/template/gen/go/apperror/v1"
	"altalune.id/template/internal/apperror"
)

// NotFoundError reports that no todo matches ID.
type NotFoundError struct{ ID string }

func (e *NotFoundError) Error() string { return fmt.Sprintf("todo: %q: not found", e.ID) }

// ToAppError converts the typed error into the wire envelope.
func (e *NotFoundError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeTodoNotFound,
		fmt.Sprintf("Todo %q not found", e.ID),
		codes.NotFound,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeTodoNotFound,
			Meta: map[string]string{"todo_id": e.ID},
		},
	)
}

// IsNotFoundError reports whether err's chain contains a *NotFoundError.
func IsNotFoundError(err error) bool {
	_, ok := errors.AsType[*NotFoundError](err)
	return ok
}

// InvalidTitleError reports that title violates a creation invariant.
type InvalidTitleError struct{ Reason string }

func (e *InvalidTitleError) Error() string { return "todo: title: " + e.Reason }

// ToAppError converts the typed error into the wire envelope.
func (e *InvalidTitleError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeTodoInvalidTitle,
		"Invalid todo title: "+e.Reason,
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeTodoInvalidTitle,
			Meta: map[string]string{"reason": e.Reason},
		},
	)
}

// IsInvalidTitleError reports whether err's chain contains an *InvalidTitleError.
func IsInvalidTitleError(err error) bool {
	_, ok := errors.AsType[*InvalidTitleError](err)
	return ok
}
