// Package interceptor holds the Connect-RPC UnaryInterceptors composed by the api server.
package interceptor

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	"altalune.id/opensheet/reqid"
)

// RequestID extracts (or mints) an X-Request-Id and threads it through ctx and response metadata.
func RequestID() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			// NOTE: mint unconditionally rather than reqid.Ensure — the listener's BaseContext
			// carries the process-wide id, which Ensure would reuse for every call.
			id := reqid.FromHeader(req.Header())
			if id == "" {
				id = reqid.New()
			}
			ctx = reqid.WithContext(ctx, id)
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
