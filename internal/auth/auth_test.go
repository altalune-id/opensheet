package auth_test

import (
	"testing"

	"altalune.id/template/internal/auth"
	"altalune.id/template/internal/platform/session"
)

func TestPrincipalAlias(t *testing.T) {
	t.Parallel()
	var p auth.Principal = session.Principal{Email: "a@b.co"} //nolint:staticcheck // exercising the alias identity is the whole point.
	if p.Email != "a@b.co" {
		t.Fatalf("alias field mismatch: %+v", p)
	}
}

func TestGenesisConfigured(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		g    auth.Genesis
		want bool
	}{
		{name: "empty", g: auth.Genesis{}, want: false},
		{name: "email only", g: auth.Genesis{Email: "a@b.co"}, want: false},
		{name: "hash only", g: auth.Genesis{PasswordHash: "hash"}, want: false},
		{name: "both", g: auth.Genesis{Email: "a@b.co", PasswordHash: "hash"}, want: true},
		{name: "whitespace email", g: auth.Genesis{Email: "  ", PasswordHash: "hash"}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.g.Configured(); got != tc.want {
				t.Errorf("Configured() = %v, want %v", got, tc.want)
			}
		})
	}
}
