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

// TestBootServer_EveryStateUsesItsOwnDerivedKey pins the sign-in state and the Google connect state to their own purpose keys, so a value signed for the session or an invite never verifies as either.
func TestBootServer_EveryStateUsesItsOwnDerivedKey(t *testing.T) {
	raw := bytes.Repeat([]byte{0x6B}, 32)
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	srv, _ := newScopeProbeServer(t, config.ModeCloud, func(c *config.Config) {
		c.HTTP.StateSecret = encoded
		c.Google.OAuth.ClientID = "google-client"
		c.Google.OAuth.ClientSecret = "google-secret"
	})
	key := srv.Platform.StateSecret
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
	_, err := web.VerifyCookie(web.DeriveKey(key, web.PurposeOIDCState), state.Value)
	require.NoError(t, err, "the sign-in state must be signed with the oidc-state key")
	for _, other := range [][]byte{key, []byte(encoded), web.DeriveKey(key, web.PurposeSession)} {
		_, err = web.VerifyCookie(other, state.Value)
		require.Error(t, err)
	}

	payload, err := json.Marshal(map[string]any{
		"o": uuid.New(), "p": uuid.New(), "u": uuid.New(), "n": "nonce", "i": time.Now().UTC(), "r": "/projects",
	})
	require.NoError(t, err)
	value := base64.RawURLEncoding.EncodeToString(payload)
	target, err := srv.Connect.Target(session.Sign(web.DeriveKey(key, web.PurposeGoogleConnect), value))
	require.NoError(t, err, "the Google connect state must verify with the google-connect key")
	assert.Equal(t, "/projects", target.ReturnTo)
	for name, other := range map[string][]byte{
		"raw secret":    key,
		"config string": []byte(encoded),
		"session key":   web.DeriveKey(key, web.PurposeSession),
		"invite key":    web.DeriveKey(key, web.PurposeInvite),
		"picker key":    web.DeriveKey(key, web.PurposeGooglePicker),
	} {
		_, err = srv.Connect.Target(session.Sign(other, value))
		require.Error(t, err, "a connect state signed with the %s must not verify", name)
	}
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
