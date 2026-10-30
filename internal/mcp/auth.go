// Package mcp is the app side of the MCP surface (S7): request authentication, per-tool scope resolution and the tools the surface publishes.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tokens"
	rootmcp "altalune.id/opensheet/mcp"
)

const (
	unauthorizedMessage = "unauthorized"
	maxPeekBytes        = 1 << 20
)

// ScopesFromContext resolves the scopes the caller's principal carries, for rootmcp.WithScopes. SECURITY: every principal is scope-checked on this surface, a JWT included — see the S7 row of R4 in ../../docs/surfaces/README.md.
func ScopesFromContext(ctx context.Context) []string {
	return slices.Clone(session.PrincipalFrom(ctx).Scopes)
}

// Authenticate authenticates every request reaching the MCP surface and puts the resulting principal in the request context, where ScopesFromContext reads it. SECURITY: a nil authenticator, an absent credential and a rejected credential all answer the same masked 401, so a caller learns nothing about why; only an outage answers 503.
func Authenticate(a authn.Authenticator, resource tokens.Resource, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, reason, err := principalFor(r, a)
			if authn.IsUnavailableError(err) {
				if log != nil {
					log.ErrorContext(r.Context(), "mcp: credential check unavailable", slog.String("reason", reason))
				}
				writeUnavailable(r.Context(), w)
				return
			}
			if err != nil {
				// SECURITY: the reason is logged, never returned.
				if log != nil {
					log.WarnContext(r.Context(), "mcp: credential rejected", slog.String("reason", reason))
				}
				writeUnauthorized(r.Context(), w, challengeFor(r, resource))
				return
			}
			if p.Source == session.SourceToken {
				if scope := missingToolScope(r, p.Scopes); scope != "" {
					writeInsufficientScope(r.Context(), w, resource.InsufficientScope(scope))
					return
				}
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
	if authn.IsUnavailableError(err) {
		return appError(apperror.CodeTokenUnavailable, authn.UnavailableMessage, codes.Unavailable, err)
	}
	if authn.IsUnauthorizedError(err) {
		return appError(apperror.CodeUnauthenticated, unauthorizedMessage, codes.Unauthenticated, err)
	}
	if invalid, ok := errors.AsType[*rootmcp.InvalidArgumentsError](err); ok {
		return appError(apperror.CodeValidation, "invalid tool arguments: "+invalid.Reason, codes.InvalidArgument, err)
	}
	return err
}

func principalFor(r *http.Request, a authn.Authenticator) (session.Principal, string, error) {
	if a == nil {
		return session.Principal{}, "no authenticator configured", &authn.UnauthorizedError{}
	}
	raw := authn.CredentialFrom(r)
	if raw == "" {
		return session.Principal{}, "no bearer credential on the request", &authn.UnauthorizedError{}
	}
	p, err := a.Authenticate(r.Context(), raw)
	if authn.IsUnavailableError(err) {
		return session.Principal{}, "the credential could not be checked: " + err.Error(), err
	}
	if err != nil {
		return session.Principal{}, "every authenticator rejected the credential: " + chainCauses(err), err
	}
	return p, "", nil
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

// SECURITY: RFC 6750 Section 3.1: only the error code tells "no token" from "token refused"; why it was refused stays masked.
func challengeFor(r *http.Request, resource tokens.Resource) string {
	if authn.CredentialFrom(r) == "" {
		return resource.Challenge()
	}
	return resource.InvalidToken()
}

// NOTE: RFC 6750 Section 3.1 step-up for OAuth tokens: a tools/call whose scope the token lacks is a 403 naming it, before dispatch; the body is restored.
func missingToolScope(r *http.Request, granted []string) string {
	if r.Method != http.MethodPost || r.Body == nil {
		return ""
	}
	head, err := io.ReadAll(io.LimitReader(r.Body, maxPeekBytes+1))
	r.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(head), r.Body), r.Body}
	if err != nil || len(head) > maxPeekBytes {
		return ""
	}
	var call struct {
		Method string `json:"method"`
		Params struct {
			Name string `json:"name"`
		} `json:"params"`
	}
	if json.Unmarshal(head, &call) != nil || call.Method != "tools/call" {
		return ""
	}
	scope := ScopeFor(call.Params.Name)
	if scope == "" || slices.Contains(granted, scope) {
		return ""
	}
	return scope
}

func writeInsufficientScope(ctx context.Context, w http.ResponseWriter, challenge string) {
	w.Header().Set("WWW-Authenticate", challenge)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(AttachContext(ctx, rootmcp.ErrorPayload{
		Code:    apperror.CodeForbidden,
		Message: "credential lacks the required scope",
	}))
}

// NOTE: no WWW-Authenticate, so a client keeps its token through an issuer or database outage.
func writeUnavailable(ctx context.Context, w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(AttachContext(ctx, rootmcp.ErrorPayload{
		Code:    apperror.CodeTokenUnavailable,
		Message: authn.UnavailableMessage,
	}))
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
