package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"

	authv1connect "altalune.id/template/gen/go/auth/v1/authv1connect"
	todov1connect "altalune.id/template/gen/go/todo/v1/todov1connect"
)

// DefaultClientTimeout is applied to the http.Client used by NewClient.
const DefaultClientTimeout = 30 * time.Second

// Client bundles the Connect clients for every service published by api.Server.
type Client struct {
	Auth authv1connect.AuthServiceClient
	Todo todov1connect.TodoServiceClient
}

// NewClient builds a Client pointing at baseURL; a non-empty token becomes the Bearer header.
func NewClient(baseURL, token string) *Client {
	httpClient := &http.Client{Timeout: DefaultClientTimeout}
	base := strings.TrimRight(baseURL, "/") + "/api"
	opts := []connect.ClientOption{
		connect.WithInterceptors(bearerInterceptor(token)),
	}
	return &Client{
		Auth: authv1connect.NewAuthServiceClient(httpClient, base, opts...),
		Todo: todov1connect.NewTodoServiceClient(httpClient, base, opts...),
	}
}

func bearerInterceptor(token string) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if token != "" {
				req.Header().Set("Authorization", "Bearer "+token)
			}
			return next(ctx, req)
		}
	}
}
