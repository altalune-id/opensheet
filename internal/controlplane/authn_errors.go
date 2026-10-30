package controlplane

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/tokens"
)

// NOTE: authn/errors.go implements no ToAppError hop, so without this every auth failure surfaces as CodeInternal.
func translateAuthnErrors(resource *tokens.Resource) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			resp, err := next(ctx, req)
			if err == nil {
				return resp, nil
			}
			if scope, ok := errors.AsType[*authn.InsufficientScopeError](err); ok {
				ae := apperror.New(
					apperror.CodeForbidden,
					"credential lacks the required scope",
					codes.PermissionDenied,
					&apperrorv1.ErrorDetail{Code: apperror.CodeForbidden},
				).WithCause(err)
				return nil, withChallenge(connect.CodePermissionDenied, ae, resource, func(r *tokens.Resource) string {
					return r.InsufficientScope(scope.Scope)
				})
			}
			if authn.IsSessionRequiredError(err) {
				return nil, apperror.New(
					apperror.CodeForbidden,
					"only a signed-in person may call this procedure",
					codes.PermissionDenied,
					&apperrorv1.ErrorDetail{Code: apperror.CodeForbidden},
				).WithCause(err)
			}
			if authn.IsUnavailableError(err) {
				return nil, apperror.New(
					apperror.CodeTokenUnavailable,
					authn.UnavailableMessage,
					codes.Unavailable,
					&apperrorv1.ErrorDetail{Code: apperror.CodeTokenUnavailable},
				).WithCause(err)
			}
			if authn.IsUnauthorizedError(err) {
				ae := apperror.New(
					apperror.CodeUnauthenticated,
					"unauthorized",
					codes.Unauthenticated,
					&apperrorv1.ErrorDetail{Code: apperror.CodeUnauthenticated},
				).WithCause(err)
				return nil, withChallenge(connect.CodeUnauthenticated, ae, resource, (*tokens.Resource).Challenge)
			}
			return resp, err
		}
	}
}

// NOTE: RFC 6750 Section 3 and RFC 9728 Section 5.1: the error's metadata becomes the response's WWW-Authenticate header.
func withChallenge(code connect.Code, ae *apperror.AppError, resource *tokens.Resource, challenge func(*tokens.Resource) string) error {
	if resource == nil {
		return ae
	}
	cerr := connect.NewError(code, ae)
	cerr.Meta().Set("WWW-Authenticate", challenge(resource))
	return cerr
}
