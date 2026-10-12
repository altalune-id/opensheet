package apikey_test

import (
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/fakes"
)

const benchKeyRows = 1000

func benchCredentials(b *testing.B) (*apikey.Authenticator, map[string]string) {
	b.Helper()

	store, _, tc := newAPIKeyStoreForTest(b)
	ctx := tenant.Into(b.Context(), tc)

	var valid string
	for i := range benchKeyRows {
		k, plaintext, err := apikey.Scheme{}.Mint(tc.OrgID, tc.ProjectID, "bench", []string{authn.ScopeSheetsRead}, nil, nil, time.Now().UTC())
		require.NoError(b, err)
		require.NoError(b, store.Save(ctx, k))
		if i == benchKeyRows/2 {
			valid = plaintext
		}
	}

	buf := make([]byte, 32)
	_, err := rand.Read(buf)
	require.NoError(b, err)

	return apikey.NewAuthenticator(store, nil, apikey.Scheme{}, fakes.NewMembers()), map[string]string{
		"malformed": "not-a-credential",
		"unknown":   apikey.DefaultPrefix + base64.RawURLEncoding.EncodeToString(buf),
		"valid":     valid,
	}
}

func BenchmarkAuthenticate(b *testing.B) {
	auth, creds := benchCredentials(b)
	ctx := b.Context()

	for _, name := range []string{"malformed", "unknown", "valid"} {
		raw := creds[name]
		b.Run(name, func(b *testing.B) {
			for b.Loop() {
				_, _ = auth.Authenticate(ctx, raw)
			}
		})
	}
}
