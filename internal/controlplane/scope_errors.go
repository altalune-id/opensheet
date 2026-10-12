package controlplane

import (
	"errors"

	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/internal/apperror"
)

// ProjectUnresolvedError signals a request omitted project_id while its principal carries no active project.
type ProjectUnresolvedError struct{}

func (*ProjectUnresolvedError) Error() string {
	return "controlplane: project_id omitted and the principal carries no active project"
}

// ToAppError maps ProjectUnresolvedError to a FailedPrecondition envelope naming the way out.
func (*ProjectUnresolvedError) ToAppError() *apperror.AppError {
	return apperror.New(
		apperror.CodeProjectUnresolved,
		"No project was given and this credential has no active project. Call project_list to get a projectId, then pass it as projectId.",
		codes.FailedPrecondition,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeProjectUnresolved,
			Meta: map[string]string{"field": "project_id"},
		},
	)
}

// IsProjectUnresolvedError reports whether err's tree contains a *ProjectUnresolvedError.
func IsProjectUnresolvedError(err error) bool {
	_, ok := errors.AsType[*ProjectUnresolvedError](err)
	return ok
}
