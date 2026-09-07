package auth

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel"

	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/platform/session"
)

//nolint:gochecknoglobals // OTel tracer is a package-level fixture, not runtime state.
var tracer = otel.Tracer("altalune.id/template/internal/auth")

// Service is the driving port for login use cases.
type Service struct {
	local      *LocalLogin
	oidc       *OIDCLogin
	log        *slog.Logger
	unexpected apperror.UnexpectedFunc
}

// NewService binds the service to its login workflows.
func NewService(local *LocalLogin, oidc *OIDCLogin, log *slog.Logger, unexpected apperror.UnexpectedFunc) *Service {
	return &Service{
		local:      local,
		oidc:       oidc,
		log:        log.With("module", "auth"),
		unexpected: unexpected,
	}
}

// LocalConfigured reports whether the local-login form should be exposed.
func (s *Service) LocalConfigured() bool {
	return s.local != nil && s.local.Configured()
}

// LoginLocal authenticates the caller against the genesis credentials.
func (s *Service) LoginLocal(ctx context.Context, creds Credentials) (session.Principal, error) {
	ctx, span := tracer.Start(ctx, "auth.LoginLocal")
	defer span.End()
	if s.local == nil {
		return session.Principal{}, &InvalidCredentialsError{}
	}
	return s.local.Execute(ctx, creds)
}

// LoginOIDC authenticates the caller against the third-party identity claims.
func (s *Service) LoginOIDC(ctx context.Context, claims OIDCClaims) (session.Principal, error) {
	ctx, span := tracer.Start(ctx, "auth.LoginOIDC")
	defer span.End()
	if s.oidc == nil {
		return session.Principal{}, &OIDCUnavailableError{}
	}
	return s.oidc.Execute(ctx, claims)
}
