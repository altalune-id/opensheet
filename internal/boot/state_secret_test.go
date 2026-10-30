package boot_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/boot"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/web"
)

// TestBootServer_EveryStateUsesTheResolvedSecret pins one key under the sign-in state cookie and the Google connect state; the session cookie and the picker state take it from the same web Deps, pinned by TestBootServer_SessionCookiesUseTheResolvedStateSecret.
func TestBootServer_EveryStateUsesTheResolvedSecret(t *testing.T) {
	raw := bytes.Repeat([]byte{0x6B}, 32)
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	srv, _ := newScopeProbeServer(t, config.ModeCloud, func(c *config.Config) {
		c.HTTP.StateSecret = encoded
		c.Google.OAuth.ClientID = "google-client"
		c.Google.OAuth.ClientSecret = "google-secret"
	})
	key := srv.Platform.OIDCStateSecret
	require.Equal(t, raw, key, "boot must decode http.stateSecret once and share those bytes")

	rec := httptest.NewRecorder()
	srv.Web.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/login/oidc", nil))
	var state *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "opensheet_oidc_state" {
			state = c
		}
	}
	require.NotNil(t, state, "status=%d body=%s", rec.Code, rec.Body.String())
	_, err := web.VerifyCookie(key, state.Value)
	require.NoError(t, err, "the sign-in state must be signed with the resolved secret")
	_, err = web.VerifyCookie([]byte(encoded), state.Value)
	require.Error(t, err)

	payload, err := json.Marshal(map[string]any{
		"o": uuid.New(), "p": uuid.New(), "u": uuid.New(), "n": "nonce", "i": time.Now().UTC(), "r": "/projects",
	})
	require.NoError(t, err)
	value := base64.RawURLEncoding.EncodeToString(payload)
	returnTo, err := srv.Connect.ReturnTo(session.Sign(key, value))
	require.NoError(t, err, "the Google connect state must verify with the resolved secret")
	assert.Equal(t, "/projects", returnTo)
	_, err = srv.Connect.ReturnTo(session.Sign([]byte(encoded), value))
	require.Error(t, err, "a connect state signed with the raw config string must not verify")
}

// TestBootServer_RefusesAShortStateSecretWithOIDC is the boot-time half of resolveStateSecret's length rule: sign-in never starts on a key too short to sign its state.
func TestBootServer_RefusesAShortStateSecretWithOIDC(t *testing.T) {
	cfg := newSmokeCfg(t)
	cfg.OIDC = config.OIDCConfig{Issuer: stubIssuer(t), ClientID: "probe-client", ClientSecret: "probe-secret"}
	cfg.HTTP.StateSecret = base64.RawURLEncoding.EncodeToString([]byte("short"))
	_, err := boot.BootServer(context.Background(), cfg, boot.WithLogger(slog.New(slog.DiscardHandler)))
	require.Error(t, err)
}

func TestResolveStateSecret_EmptyMintsAnEphemeralKeyAndWarns(t *testing.T) {
	t.Parallel()
	capture := &captureLog{}
	got, err := boot.ResolveStateSecret(&config.Config{}, slog.New(capture))
	require.NoError(t, err)
	assert.Len(t, got, config.StateSecretMinLen)
	assert.NotEqual(t, make([]byte, config.StateSecretMinLen), got, "the ephemeral key must be random, never zero")
	assert.True(t, capture.warned("using an ephemeral secret"))
}
