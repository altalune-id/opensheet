package interceptor

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	apperrorv1 "altalune.id/template/gen/go/apperror/v1"
	"altalune.id/template/internal/apperror"
)

// Wrap converts errors from inner handlers into the canonical connect.Error envelope.
func Wrap(unexpected apperror.UnexpectedFunc) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			resp, err := next(ctx, req)
			if err == nil {
				return resp, nil
			}
			return resp, translate(ctx, err, unexpected)
		}
	}
}

func translate(ctx context.Context, err error, unexpected apperror.UnexpectedFunc) error {
	ae, ok := apperror.AsAppError(err)
	if !ok {
		if unexpected != nil {
			ae = unexpected(ctx, "api: unexpected", err)
		} else {
			ae = apperror.New(apperror.CodeUnexpectedError, "unexpected error", grpcInternal(),
				&apperrorv1.ErrorDetail{Code: apperror.CodeUnexpectedError}).WithCause(err)
		}
	}
	ae = apperror.AttachContext(ctx, ae)

	cerr := connect.NewError(connectCodeOf(ae), ae)
	for _, d := range ae.Details() {
		if det, derr := connect.NewErrorDetail(d); derr == nil {
			cerr.AddDetail(det)
		}
	}
	if up := ae.Upstream(); up != nil {
		if det, derr := connect.NewErrorDetail(up); derr == nil {
			cerr.AddDetail(det)
		}
	}
	var inner *connect.Error
	if errors.As(err, &inner) {
		for k, vs := range inner.Meta() {
			for _, v := range vs {
				cerr.Meta().Add(k, v)
			}
		}
	}
	return cerr
}
