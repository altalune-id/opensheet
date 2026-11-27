package controlplane

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/testutil/fakes"
)

func TestPrincipal(t *testing.T) {
	tests := []struct {
		name    string
		in      session.Principal
		wantErr bool
	}{
		{
			name: "user principal with UserID is admitted",
			in: session.Principal{
				UserID:      uuid.New(),
				Source:      session.SourceOIDC,
				ActiveOrgID: uuid.New(),
			},
			wantErr: false,
		},
		{
			name: "user principal without UserID is rejected",
			in: session.Principal{
				Source:      session.SourceOIDC,
				ActiveOrgID: uuid.New(),
			},
			wantErr: true,
		},
		{
			name: "key principal with ActiveOrgID is admitted",
			in: session.Principal{
				Source:      session.SourceAPIKey,
				ActiveOrgID: uuid.New(),
			},
			wantErr: false,
		},
		{
			name: "key principal without ActiveOrgID is rejected",
			in: session.Principal{
				Source: session.SourceAPIKey,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			roles := fakes.NewRoles().Set(tt.in.ActiveOrgID, tt.in.UserID, "member")
			ctx := session.PrincipalInto(context.Background(), tt.in)
			_, got, err := principal(ctx, authz.NewResolver(roles))

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				var appErr *apperror.AppError
				if !errors.As(err, &appErr) {
					t.Fatalf("expected *apperror.AppError, got %T: %v", err, err)
				}
				if appErr.Code() != apperror.CodeUnauthenticated {
					t.Errorf("code = %q, want %q", appErr.Code(), apperror.CodeUnauthenticated)
				}
				if appErr.GRPCCode() != codes.Unauthenticated {
					t.Errorf("grpc code = %v, want Unauthenticated", appErr.GRPCCode())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.UserID != tt.in.UserID || got.ActiveOrgID != tt.in.ActiveOrgID || got.Source != tt.in.Source {
				t.Errorf("got=%+v want=%+v", got, tt.in)
			}
		})
	}
}
