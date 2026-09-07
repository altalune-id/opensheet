// Package interceptor holds the Connect-RPC UnaryInterceptors composed by the api server.
package interceptor

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	"altalune.id/template/reqid"
)

// RequestID extracts (or mints) an X-Request-Id and threads it through ctx and response metadata.
func RequestID() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			ctx, id := reqid.Ensure(reqid.WithContext(ctx, req.Header().Get(reqid.Header)))
			resp, err := next(ctx, req)
			switch {
			case resp != nil:
				resp.Header().Set(reqid.Header, id)
			case err != nil:
				var cerr *connect.Error
				if errors.As(err, &cerr) {
					cerr.Meta().Set(reqid.Header, id)
				}
			}
			return resp, err
		}
	}
}
