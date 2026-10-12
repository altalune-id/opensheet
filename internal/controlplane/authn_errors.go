package controlplane

import (
	"context"

	"connectrpc.com/connect"
	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/authn"
)

// NOTE: authn/errors.go implements no ToAppError hop, so without this every auth failure surfaces as CodeInternal.
func translateAuthnErrors() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			resp, err := next(ctx, req)
			if err == nil {
				return resp, nil
			}
			if authn.IsInsufficientScopeError(err) {
				return nil, apperror.New(
					apperror.CodeForbidden,
					"credential lacks the required scope",
					codes.PermissionDenied,
					&apperrorv1.ErrorDetail{Code: apperror.CodeForbidden},
				).WithCause(err)
			}
			if authn.IsUnauthorizedError(err) {
				return nil, apperror.New(
					apperror.CodeUnauthenticated,
					"unauthorized",
					codes.Unauthenticated,
					&apperrorv1.ErrorDetail{Code: apperror.CodeUnauthenticated},
				).WithCause(err)
			}
			return resp, err
		}
	}
}
