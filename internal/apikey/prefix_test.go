package apikey_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/fakes"
)

func failingUnexpected(t *testing.T) apperror.UnexpectedFunc {
	t.Helper()
	return func(_ context.Context, op string, err error, _ ...any) *apperror.AppError {
		t.Fatalf("unexpected error from %s: %v", op, err)
		return nil
	}
}

func TestSchemePrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		configured string
		want       string
	}{
		{name: "empty falls back to the default", configured: "", want: apikey.DefaultPrefix},
		{name: "default", configured: apikey.DefaultPrefix, want: "key_"},
		{name: "branded", configured: "ak_", want: "ak_"},
		{name: "multi segment", configured: "sk_live_", want: "sk_live_"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sc := apikey.NewScheme(tt.configured)
			require.Equal(t, tt.want, sc.Prefix())
			require.Equal(t, tt.want, sc.Authn().Prefix)
		})
	}
	require.Equal(t, apikey.DefaultPrefix, apikey.Scheme{}.Prefix())
}

func TestMintAndResolveAgreeUnderEveryPrefix(t *testing.T) {
	t.Parallel()

	for _, configured := range []string{"", "key_", "ak_", "sk_live_", "opensheet-"} {
		t.Run("prefix="+configured, func(t *testing.T) {
			t.Parallel()

			sc := apikey.NewScheme(configured)
			store := fakes.NewAPIKey()
			svc := apikey.NewService(store, sc, fakes.PermissiveMembers(), fakes.NewOrgProjects(), slog.New(slog.NewTextHandler(io.Discard, nil)), failingUnexpected(t))
			auth := apikey.NewAuthenticator(store, nil, sc, fakes.NewMembers())

			tc := tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New(), UserID: uuid.New()}
			k, plaintext, err := svc.Mint(tenant.Into(t.Context(), tc), "round trip", []string{authn.ScopeSheetsRead}, nil, in(time.Hour))
			require.NoError(t, err)

			require.True(t, strings.HasPrefix(plaintext, sc.Prefix()),
				"minted %q does not start with the configured prefix %q", plaintext, sc.Prefix())
			require.Equal(t, authn.ShapeAPIKey, auth.Scheme().Authn().Looks(plaintext),
				"the surface shape gate rejects a key this deployment minted")

			p, err := auth.Authenticate(t.Context(), plaintext)
			require.NoError(t, err)
			require.Equal(t, k.ID, p.KeyID)
		})
	}
}

func TestResolveRejectsAKeyMintedUnderAnotherPrefix(t *testing.T) {
	t.Parallel()

	store := fakes.NewAPIKey()
	k, plaintext, err := apikey.NewScheme("key_").Mint(uuid.New(), uuid.New(), "stale",
		[]string{authn.ScopeSheetsRead}, nil, nil, time.Now().UTC())
	require.NoError(t, err)
	store.Seed(k)

	_, err = apikey.NewAuthenticator(store, nil, apikey.NewScheme("ak_"), fakes.NewMembers()).Authenticate(t.Context(), plaintext)
	require.True(t, authn.IsUnauthorizedError(err), "got %v", err)
}
