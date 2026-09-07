package api

import (
	"context"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/template/gen/go/apperror/v1"
	authv1 "altalune.id/template/gen/go/auth/v1"
	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/org"
	"altalune.id/template/internal/platform/session"
)

// AuthService implements auth.v1.AuthService by reflecting the caller's Principal back to the client.
type AuthService struct {
	orgs *org.Service
}

// NewAuthService wires the handler with the org service used to resolve the active-org slug.
func NewAuthService(orgs *org.Service) *AuthService {
	return &AuthService{orgs: orgs}
}

// Whoami returns identity + active-org info for the caller's Principal.
func (s *AuthService) Whoami(ctx context.Context, _ *connect.Request[authv1.WhoamiRequest]) (*connect.Response[authv1.WhoamiResponse], error) {
	p := session.PrincipalFrom(ctx)
	if p.Email == "" && p.UserID == uuid.Nil {
		return nil, apperror.New(
			apperror.CodeUnauthenticated,
			"No principal in context",
			codes.Unauthenticated,
			&apperrorv1.ErrorDetail{Code: apperror.CodeUnauthenticated},
		)
	}

	resp := &authv1.WhoamiResponse{
		UserId: p.UserID.String(),
		Email:  p.Email,
		Name:   p.Name,
		Scopes: append([]string(nil), p.Scopes...),
	}
	if p.ActiveOrgID != uuid.Nil {
		resp.ActiveOrgId = p.ActiveOrgID.String()
		if s.orgs != nil {
			if o, err := s.orgs.ByID(ctx, p.ActiveOrgID); err == nil {
				resp.ActiveOrgSlug = o.Slug
			}
		}
	}
	return connect.NewResponse(resp), nil
}
