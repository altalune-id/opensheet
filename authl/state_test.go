package authl

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestClient(t *testing.T) *Client {
	t.Helper()
	return &Client{
		cfg: Config{
			ClientID:                 "cid",
			RedirectURL:              "https://example.com/cb",
			StateSecret:              []byte("0123456789abcdef0123456789abcdef"),
			StateCookie:              "authl_state",
			StateMaxAge:              10 * time.Minute,
			LastUserCookie:           "authl_last_user",
			LastUserMaxAge:           24 * time.Hour,
			LastUserCookieJSReadable: false,
			CookiePath:               "/",
		},
	}
}

func TestNewPKCE_UniqueAndDeterministicChallenge(t *testing.T) {
	p1, err := newPKCE()
	require.NoError(t, err)
	require.NotNil(t, p1)

	p2, err := newPKCE()
	require.NoError(t, err)

	assert.NotEqual(t, p1.state, p2.state)
	assert.NotEqual(t, p1.nonce, p2.nonce)
	assert.NotEqual(t, p1.verifier, p2.verifier)

	sum := sha256.Sum256([]byte(p1.verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	assert.Equal(t, want, p1.challenge)
}

func TestEncodeDecodeStateCookie_RoundTrip(t *testing.T) {
	c := newTestClient(t)
	p, err := newPKCE()
	require.NoError(t, err)

	cookie, err := c.encodeStateCookie(p, "/dashboard")
	require.NoError(t, err)
	require.Contains(t, cookie, ".")

	pl, err := c.decodeStateCookie(cookie)
	require.NoError(t, err)
	assert.Equal(t, p.state, pl.State)
	assert.Equal(t, p.nonce, pl.Nonce)
	assert.Equal(t, p.verifier, pl.Verifier)
	assert.Equal(t, "/dashboard", pl.Return)
	assert.Greater(t, pl.Exp, time.Now().Unix())
}

func TestDecodeStateCookie_Errors(t *testing.T) {
	c := newTestClient(t)

	valid, err := func() (string, error) {
		p, err := newPKCE()
		require.NoError(t, err)
		return c.encodeStateCookie(p, "")
	}()
	require.NoError(t, err)

	parts := strings.Split(valid, ".")
	require.Len(t, parts, 2)

	t.Run("no dot", func(t *testing.T) {
		_, err := c.decodeStateCookie("no-dot-here")
		assert.ErrorContains(t, err, "malformed")
	})

	t.Run("payload not base64", func(t *testing.T) {
		_, err := c.decodeStateCookie("!!!." + parts[1])
		assert.ErrorContains(t, err, "payload not base64url")
	})

	t.Run("signature not base64", func(t *testing.T) {
		_, err := c.decodeStateCookie(parts[0] + ".!!!")
		assert.ErrorContains(t, err, "signature not base64url")
	})

	t.Run("wrong signature", func(t *testing.T) {
		bogus := base64.RawURLEncoding.EncodeToString([]byte("bogus"))
		_, err := c.decodeStateCookie(parts[0] + "." + bogus)
		assert.ErrorContains(t, err, "signature mismatch")
	})

	t.Run("payload not json", func(t *testing.T) {
		raw := []byte("not json")
		sig := hmac.New(sha256.New, c.cfg.StateSecret)
		sig.Write(raw)
		bad := base64.RawURLEncoding.EncodeToString(raw) + "." +
			base64.RawURLEncoding.EncodeToString(sig.Sum(nil))
		_, err := c.decodeStateCookie(bad)
		assert.ErrorContains(t, err, "not json")
	})

	t.Run("expired", func(t *testing.T) {
		pl := statePayload{
			State:    "s",
			Nonce:    "n",
			Verifier: "v",
			Exp:      time.Now().Add(-1 * time.Hour).Unix(),
		}
		raw, err := json.Marshal(pl)
		require.NoError(t, err)
		sig := hmac.New(sha256.New, c.cfg.StateSecret)
		sig.Write(raw)
		bad := base64.RawURLEncoding.EncodeToString(raw) + "." +
			base64.RawURLEncoding.EncodeToString(sig.Sum(nil))
		_, err = c.decodeStateCookie(bad)
		assert.ErrorContains(t, err, "expired")
	})
}

func TestSetStateCookie_Attributes(t *testing.T) {
	c := newTestClient(t)
	c.cfg.CookieSecure = true
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	c.setStateCookie(rec, req, "abc.def")

	res := rec.Result()
	defer res.Body.Close()
	cookies := res.Cookies()
	require.Len(t, cookies, 1)
	ck := cookies[0]
	assert.Equal(t, "authl_state", ck.Name)
	assert.Equal(t, "abc.def", ck.Value)
	assert.Equal(t, "/", ck.Path)
	assert.True(t, ck.HttpOnly)
	assert.True(t, ck.Secure)
	assert.Equal(t, http.SameSiteLaxMode, ck.SameSite)
	assert.Greater(t, ck.MaxAge, 0)
}

func TestClearStateCookie_ExpiresCookie(t *testing.T) {
	c := newTestClient(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	c.clearStateCookie(rec, req)

	res := rec.Result()
	defer res.Body.Close()
	cookies := res.Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, -1, cookies[0].MaxAge)
	assert.Equal(t, "", cookies[0].Value)
}

func TestSetLastUserCookie_EmptyEmailNoOp(t *testing.T) {
	c := newTestClient(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	c.setLastUserCookie(rec, req, "")

	res := rec.Result()
	defer res.Body.Close()
	assert.Empty(t, res.Cookies())
}

func TestSetLastUserCookie_Attributes(t *testing.T) {
	c := newTestClient(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	c.setLastUserCookie(rec, req, "alice@example.com")

	res := rec.Result()
	defer res.Body.Close()
	cookies := res.Cookies()
	require.Len(t, cookies, 1)
	ck := cookies[0]
	assert.Equal(t, "authl_last_user", ck.Name)
	assert.Equal(t, "alice@example.com", ck.Value)
	assert.True(t, ck.HttpOnly)
	assert.Equal(t, http.SameSiteLaxMode, ck.SameSite)
	assert.Greater(t, ck.MaxAge, 0)
}

func TestSetLastUserCookie_JSReadable(t *testing.T) {
	c := newTestClient(t)
	c.cfg.LastUserCookieJSReadable = true
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	c.setLastUserCookie(rec, req, "alice@example.com")

	res := rec.Result()
	defer res.Body.Close()
	cookies := res.Cookies()
	require.Len(t, cookies, 1)
	assert.False(t, cookies[0].HttpOnly)
}

func TestClearLastUserCookie_ExpiresCookie(t *testing.T) {
	c := newTestClient(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	c.clearLastUserCookie(rec, req)

	res := rec.Result()
	defer res.Body.Close()
	cookies := res.Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, -1, cookies[0].MaxAge)
}

func TestRandB64URL_LengthDistinct(t *testing.T) {
	a, err := randB64URL(32)
	require.NoError(t, err)
	b, err := randB64URL(32)
	require.NoError(t, err)

	assert.NotEqual(t, a, b)
	decoded, err := base64.RawURLEncoding.DecodeString(a)
	require.NoError(t, err)
	assert.Len(t, decoded, 32)
}
