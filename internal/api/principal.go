package api

import (
	"context"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/session"
)

func principal(ctx context.Context) (session.Principal, error) {
	p := session.PrincipalFrom(ctx)
	if !usablePrincipal(p) {
		return session.Principal{}, apperror.New(
			apperror.CodeUnauthenticated,
			"No principal in context",
			codes.Unauthenticated,
			&apperrorv1.ErrorDetail{Code: apperror.CodeUnauthenticated},
		)
	}
	return p, nil
}

// SECURITY: an API key is not a person, so a key principal carries no UserID and is admitted on its ActiveOrgID instead - spec 6.2.
func usablePrincipal(p session.Principal) bool {
	if p.Source == session.SourceAPIKey {
		return p.ActiveOrgID != uuid.Nil
	}
	return p.UserID != uuid.Nil
}

func parseUUID(field, raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, apperror.New(
			apperror.CodeValidation,
			field+" must be a uuid",
			codes.InvalidArgument,
			&apperrorv1.ErrorDetail{
				Code: apperror.CodeValidation,
				Meta: map[string]string{"field": field},
			},
		).WithCause(err)
	}
	return id, nil
}

func forbiddenErr(msg, field, value string) error {
	return apperror.New(
		apperror.CodeForbidden,
		msg,
		codes.PermissionDenied,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeForbidden,
			Meta: map[string]string{field: value},
		},
	)
}
