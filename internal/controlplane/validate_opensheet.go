package controlplane

import (
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/internal/apperror"
)

func validationErr(field, msg string) error {
	return apperror.New(
		apperror.CodeValidation,
		msg,
		codes.InvalidArgument,
		&apperrorv1.ErrorDetail{
			Code: apperror.CodeValidation,
			Meta: map[string]string{"field": field},
		},
	)
}

func parseUUIDs(field string, raw []string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(raw))
	for _, r := range raw {
		id, err := parseUUID(field, r)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}
