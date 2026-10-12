// Package mcp is the app side of the MCP surface (S7): request authentication, per-tool scope resolution, the RFC 9728 metadata document and the tools the surface publishes.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/session"
	rootmcp "altalune.id/opensheet/mcp"
)

const unauthorizedMessage = "unauthorized"

// ScopesFromContext resolves the scopes the caller's principal carries, for rootmcp.WithScopes. SECURITY: every principal is scope-checked on this surface, a JWT included — see the S7 row of R4 in ../../docs/surfaces/README.md.
func ScopesFromContext(ctx context.Context) []string {
	return slices.Clone(session.PrincipalFrom(ctx).Scopes)
}

// WWWAuthenticate builds the challenge a 401 carries, naming the protected-resource metadata document per RFC 9728.
func WWWAuthenticate(resourceMetadataURL string) string {
	return fmt.Sprintf("Bearer resource_metadata=%q", resourceMetadataURL)
}

// Authenticate authenticates every request reaching the MCP surface and puts the resulting principal in the request context, where ScopesFromContext reads it. SECURITY: a nil authenticator, an absent credential, a credential of an unrecognized shape and a rejected credential all answer the same masked 401, so a caller learns nothing about why.
func Authenticate(a authn.Authenticator, scheme authn.Scheme, resourceMetadataURL string, log *slog.Logger) func(http.Handler) http.Handler {
	challenge := WWWAuthenticate(resourceMetadataURL)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, reason, ok := principalFor(r, a, scheme)
			if !ok {
				// SECURITY: the reason is logged, never returned — the response stays one masked 401 whatever the cause.
				if log != nil {
					log.WarnContext(r.Context(), "mcp: credential rejected", slog.String("reason", reason))
				}
				writeUnauthorized(r.Context(), w, challenge)
				return
			}
			next.ServeHTTP(w, r.WithContext(session.PrincipalInto(r.Context(), p)))
		})
	}
}

// TranslateError maps an MCP authentication or scope failure onto the canonical AppError envelope every other surface answers with.
func TranslateError(err error) error {
	if err == nil {
		return nil
	}
	if rootmcp.IsScopeDeniedError(err) || rootmcp.IsScopeUndeclaredError(err) || authn.IsInsufficientScopeError(err) {
		return appError(apperror.CodeForbidden, "credential lacks the required scope", codes.PermissionDenied, err)
	}
	if authn.IsUnauthorizedError(err) {
		return appError(apperror.CodeUnauthenticated, unauthorizedMessage, codes.Unauthenticated, err)
	}
	if invalid, ok := errors.AsType[*rootmcp.InvalidArgumentsError](err); ok {
		return appError(apperror.CodeValidation, "invalid tool arguments: "+invalid.Reason, codes.InvalidArgument, err)
	}
	return err
}

func principalFor(r *http.Request, a authn.Authenticator, scheme authn.Scheme) (session.Principal, string, bool) {
	if a == nil {
		return session.Principal{}, "no authenticator configured", false
	}
	raw := authn.CredentialFrom(r)
	if raw == "" {
		return session.Principal{}, "no bearer credential on the request", false
	}
	if scheme.Looks(raw) == authn.ShapeUnknown {
		return session.Principal{}, "credential matched neither the api key prefix nor the JWT shape", false
	}
	p, err := a.Authenticate(r.Context(), raw)
	if err != nil {
		return session.Principal{}, "every authenticator rejected the credential: " + chainCauses(err), false
	}
	return p, "", true
}

// chainCauses renders why each authenticator refused. SECURITY: logged only — the caller still receives one masked 401.
func chainCauses(err error) string {
	var u *authn.UnauthorizedError
	if errors.As(err, &u) {
		if c := u.Causes(); c != nil {
			return strings.ReplaceAll(c.Error(), "\n", "; ")
		}
	}
	return err.Error()
}

func appError(code, message string, grpcCode codes.Code, cause error) error {
	return apperror.New(code, message, grpcCode, &apperrorv1.ErrorDetail{Code: code}).WithCause(cause)
}

// NOTE: the body is rootmcp.ErrorPayload, the same struct an in-result tool failure carries, so one client parser reads both.
func writeUnauthorized(ctx context.Context, w http.ResponseWriter, challenge string) {
	w.Header().Set("WWW-Authenticate", challenge)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusUnauthorized)
	payload := AttachContext(ctx, rootmcp.ErrorPayload{
		Code:    apperror.CodeMCPUnauthenticated,
		Message: unauthorizedMessage,
	})
	_ = json.NewEncoder(w).Encode(payload)
}
