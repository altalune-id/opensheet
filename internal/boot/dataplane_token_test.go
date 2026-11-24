package boot_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/user"
)

const tabsURL = "/api/v1/orgs/mcp-org/projects/mcp-project/spreadsheets/"

const missingSheetBody = `{"error":{"code":"` + apperror.CodeSheetNotFound + `","message":"Sheet not found"}}`

func (f *mcpFixture) dataGet(t *testing.T, credential string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, tabsURL+uuid.NewString()+"/tabs", nil)
	req.Header.Set("Authorization", "Bearer "+credential)
	rec := httptest.NewRecorder()
	f.srv.Web.ServeHTTP(rec, req)
	return rec
}

// TestTokenAuthz_MemberWithScopeReads proves a member's token gets past the authorizer.
func TestTokenAuthz_MemberWithScopeReads(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{dataPlane: true})
	token := f.issuer.mint(t, controlAudience, []string{authn.ScopeSpreadsheetsRead})

	rec := f.dataGet(t, token)
	require.Equal(t, http.StatusNotFound, rec.Code, "body=%s", rec.Body.String())
	require.NotContains(t, rec.Body.String(), apperror.CodeSheetNotFound, "a member's token was refused by the authorizer")
}

// SECURITY: runs on SQLite, so the membership check is the only guard.
func TestTokenAuthz_NonMemberIs404(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{dataPlane: true})
	_, err := f.srv.Users.EnsureFromOIDC(t.Context(), user.Claims{
		Issuer: f.issuer.url, Subject: "outsider-subject", Email: "outsider@example.com", EmailVerified: true, Name: "Outsider",
	})
	require.NoError(t, err)
	token := f.issuer.mintWith(t, controlAudience, []string{authn.ScopeSpreadsheetsRead}, map[string]any{"sub": "outsider-subject"})

	rec := f.dataGet(t, token)
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.JSONEq(t, missingSheetBody, rec.Body.String())
}

func TestTokenAuthz_UnknownPersonIs404(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{dataPlane: true})
	token := f.issuer.mintWith(t, controlAudience, []string{authn.ScopeSpreadsheetsRead}, map[string]any{"sub": "nobody-here"})

	rec := f.dataGet(t, token)
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.JSONEq(t, missingSheetBody, rec.Body.String())
}

func TestTokenAuthz_KeyPathUnchangedOnRealWiring(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{dataPlane: true})
	rec := f.dataGet(t, f.readKey)
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.JSONEq(t, missingSheetBody, rec.Body.String(), "a sheets:read key has no spreadsheets:read and must stay refused")
}

func withKid(t *testing.T, token, kid string) string {
	t.Helper()
	parts := strings.Split(token, ".")
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	require.NoError(t, err)
	header := map[string]any{}
	require.NoError(t, json.Unmarshal(raw, &header))
	header["kid"] = kid
	raw, err = json.Marshal(header)
	require.NoError(t, err)
	parts[0] = base64.RawURLEncoding.EncodeToString(raw)
	return strings.Join(parts, ".")
}

const invalidTokenChallenge = `Bearer resource_metadata="http://127.0.0.1` + apiMetadataPath + `", error="invalid_token"`

// SECURITY: a token the verifier refuses gets a 401 decided before the path resolves, so it tells a prober nothing about which slugs exist.
func TestTokenAuthz_InvalidTokenIs401(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true, dataPlane: true})
	good := f.issuer.mint(t, controlAudience, []string{authn.ScopeSpreadsheetsRead})
	now := time.Now()
	invalid := map[string]string{
		"unknown kid":    withKid(t, good, "no-such-kid"),
		"bad signature":  good[:len(good)-4] + "AAAA",
		"mcp audience":   f.issuer.mint(t, mcpAudience, []string{authn.ScopeSpreadsheetsRead}),
		"other audience": f.issuer.mint(t, "https://other-app.example.com/api", []string{authn.ScopeSpreadsheetsRead}),
		"id token": f.issuer.idp.IDToken(map[string]any{
			"iss": f.issuer.url, "sub": tokenSubject, "aud": controlAudience, "client_id": "test-client", "jti": "j1",
			"scope": authn.ScopeSpreadsheetsRead, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
		}),
	}
	for name, token := range invalid {
		rec := f.dataGet(t, token)
		require.Equal(t, http.StatusUnauthorized, rec.Code, "%s: body=%s", name, rec.Body.String())
		require.Equal(t, invalidTokenChallenge, rec.Header().Get("WWW-Authenticate"), name)
		require.JSONEq(t, `{"error":{"code":"`+apperror.CodeUnauthenticated+`","message":"Invalid access token"}}`, rec.Body.String(), name)
	}
}

func TestTokenAuthz_ExpiredTokenIs401(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{dataPlane: true})
	past := time.Now().Add(-2 * time.Hour)
	token := f.issuer.mintWith(t, controlAudience, []string{authn.ScopeSpreadsheetsRead},
		map[string]any{"iat": past.Unix(), "nbf": past.Unix(), "exp": past.Add(time.Hour).Unix()})

	rec := f.dataGet(t, token)
	require.Equal(t, http.StatusUnauthorized, rec.Code, "body=%s", rec.Body.String())
	require.Equal(t, invalidTokenChallenge, rec.Header().Get("WWW-Authenticate"))
	require.JSONEq(t, `{"error":{"code":"`+apperror.CodeTokenExpired+`","message":"Access token expired"}}`, rec.Body.String())
}

func TestTokenAuthz_InvalidTokenChallengesWithAPIOff(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{dataPlane: true, apiOff: true})
	token := withKid(t, f.issuer.mint(t, controlAudience, []string{authn.ScopeSpreadsheetsRead}), "no-such-kid")

	rec := f.dataGet(t, token)
	require.Equal(t, http.StatusUnauthorized, rec.Code, "body=%s", rec.Body.String())
	require.Equal(t, invalidTokenChallenge, rec.Header().Get("WWW-Authenticate"))
}

// SECURITY: an audience that names no RFC 9728 document leaves the data plane keys only, so no 401 goes out without its challenge.
func TestTokenAuthz_NonHTTPAudienceIsKeysOnly(t *testing.T) {
	const audience = "opensheet-api"
	f := newMCPFixture(t, mcpOpts{dataPlane: true, audience: audience})
	for name, token := range map[string]string{
		"valid":   f.issuer.mint(t, audience, []string{authn.ScopeSpreadsheetsRead}),
		"invalid": withKid(t, f.issuer.mint(t, audience, []string{authn.ScopeSpreadsheetsRead}), "no-such-kid"),
	} {
		rec := f.dataGet(t, token)
		require.Equal(t, http.StatusNotFound, rec.Code, "%s: body=%s", name, rec.Body.String())
		require.JSONEq(t, missingSheetBody, rec.Body.String(), name)
		require.Empty(t, rec.Header().Get("WWW-Authenticate"), name)
	}
}

// SECURITY: the same invalid token answers byte for byte alike on a path that resolves and one that does not.
func TestTokenAuthz_InvalidTokenAnswerIgnoresPath(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{dataPlane: true})
	token := withKid(t, f.issuer.mint(t, controlAudience, []string{authn.ScopeSpreadsheetsRead}), "no-such-kid")
	answer := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		f.srv.Web.ServeHTTP(rec, req)
		return rec
	}
	existing := answer("/api/v1/orgs/mcp-org/projects/mcp-project/sheets/orders")
	missing := answer("/api/v1/orgs/no-such-org/projects/no-such-project/sheets/orders")
	require.Equal(t, http.StatusUnauthorized, existing.Code)
	require.Equal(t, existing.Code, missing.Code)
	require.Equal(t, existing.Header().Get("WWW-Authenticate"), missing.Header().Get("WWW-Authenticate"))
	require.Equal(t, existing.Body.String(), missing.Body.String())
}

func TestTokenAuthz_IssuerOutageIs503(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{dataPlane: true})
	token := f.issuer.mint(t, controlAudience, []string{authn.ScopeSpreadsheetsRead})
	f.issuer.idp.Close()

	rec := f.dataGet(t, token)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code, "body=%s", rec.Body.String())
	require.JSONEq(t, `{"error":{"code":"TKN002","message":"credential verification unavailable"}}`, rec.Body.String())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/orgs/no-such-org/projects/no-such-project/sheets/orders", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	missing := httptest.NewRecorder()
	f.srv.Web.ServeHTTP(missing, req)
	require.Equal(t, http.StatusServiceUnavailable, missing.Code, "an outage must answer before the path resolves; body=%s", missing.Body.String())
}

func TestTokenAuthz_TokenInXAPIKeyIsRefused(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{dataPlane: true})
	token := f.issuer.mint(t, controlAudience, []string{authn.ScopeSpreadsheetsRead})

	req := httptest.NewRequest(http.MethodGet, tabsURL+uuid.NewString()+"/tabs", nil)
	req.Header.Set("X-API-Key", token)
	rec := httptest.NewRecorder()
	f.srv.Web.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
	require.JSONEq(t, missingSheetBody, rec.Body.String())
}
