package auth

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/template/internal/platform/session"
)

func TestPrincipalFromRef_CarriesIsAdmin(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	cases := []struct {
		name    string
		isAdmin bool
	}{
		{"admin", true},
		{"ordinary", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ref := &UserRef{ID: uuid.New(), Email: "a@x.co", Name: "A", IsAdmin: tc.isAdmin}
			p := principalFromRef(ref, session.SourceLocal, now)
			require.Equal(t, tc.isAdmin, p.IsAdmin, "admin status must survive into the session")
		})
	}
}

func TestOIDCLogin_CarriesIsAdmin(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		isAdmin bool
	}{
		{"admin", true},
		{"ordinary", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ref := &UserRef{ID: uuid.New(), Email: "a@x.co", Name: "A", Source: "oidc", IsAdmin: tc.isAdmin}
			o := NewOIDCLogin(
				func(_ context.Context, _ EnsureClaims) (*UserRef, bool, error) { return ref, false, nil },
				nil,
				nil,
				nil,
			)
			p, err := o.Execute(t.Context(), OIDCClaims{Issuer: "https://idp", Subject: "sub", Email: "a@x.co"})
			require.NoError(t, err)
			require.Equal(t, tc.isAdmin, p.IsAdmin, "admin status must survive into the session")
		})
	}
}
