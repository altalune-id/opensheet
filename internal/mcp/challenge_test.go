package mcp_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	mcpinternal "altalune.id/opensheet/internal/mcp"
)

func serveChallenge(t *testing.T, basePath, prefix, token, requestPath string) *httptest.ResponseRecorder {
	t.Helper()
	routes, err := mcpinternal.ChallengeRoutes(basePath, prefix, token)
	require.NoError(t, err)
	require.NotEmpty(t, routes, "no route registered, so the request below proves nothing")

	mux := http.NewServeMux()
	for pattern, h := range routes {
		mux.Handle(pattern, h)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, requestPath, nil))
	return rec
}

func TestChallengeRoutes_ServesTheConfiguredToken(t *testing.T) {
	const token = "tok_configured"
	rec := serveChallenge(t, "", "", token, mcpinternal.DefaultChallengePrefix+token)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, token, rec.Body.String())
	require.Equal(t, "text/plain; charset=utf-8", rec.Header().Get("Content-Type"))
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
}

// TestChallengeRoutes_RefusesAnyTokenButTheConfiguredOne is the whole security property: the token lives in the registered pattern, so a handler cannot be tricked into echoing an attacker's value.
func TestChallengeRoutes_RefusesAnyTokenButTheConfiguredOne(t *testing.T) {
	routes, err := mcpinternal.ChallengeRoutes("", "", "the-real-token")
	require.NoError(t, err)
	mux := http.NewServeMux()
	for pattern, h := range routes {
		mux.Handle(pattern, h)
	}

	for _, attacker := range []string{"attacker-token", "the-real-token-x", "", "the-real-toke"} {
		t.Run("token="+attacker, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, mcpinternal.DefaultChallengePrefix+attacker, nil)
			mux.ServeHTTP(rec, req)

			require.Equal(t, http.StatusNotFound, rec.Code,
				"an unconfigured token was answered; another tenant could claim this resource URI")
			if attacker != "" {
				require.NotContains(t, rec.Body.String(), attacker)
			}
		})
	}
}

func TestChallengeRoutes_AnswersUnderBasePathToo(t *testing.T) {
	const token = "tok_basepath"
	for _, path := range []string{
		mcpinternal.DefaultChallengePrefix + token,
		"/app" + mcpinternal.DefaultChallengePrefix + token,
	} {
		t.Run(path, func(t *testing.T) {
			rec := serveChallenge(t, "/app", "", token, path)
			require.Equal(t, http.StatusOK, rec.Code)
			require.Equal(t, token, rec.Body.String())
		})
	}
}

func TestChallengeRoutes_HonoursAConfiguredPrefix(t *testing.T) {
	const token = "tok_prefix"
	rec := serveChallenge(t, "", "/.well-known/other-challenge/", token, "/.well-known/other-challenge/"+token)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, token, rec.Body.String())
}

func TestChallengeRoutes_NoTokenMeansNoRoutes(t *testing.T) {
	for _, token := range []string{"", "   "} {
		t.Run("token="+token, func(t *testing.T) {
			routes, err := mcpinternal.ChallengeRoutes("", "", token)
			require.NoError(t, err)
			require.Nil(t, routes)
		})
	}
}

func TestChallengeRoutes_TrimsSurroundingWhitespace(t *testing.T) {
	routes, err := mcpinternal.ChallengeRoutes("", "", "  padded  ")
	require.NoError(t, err)
	require.Contains(t, routes, "GET "+mcpinternal.DefaultChallengePrefix+"padded")
}

// TestChallengeRoutes_RejectsAnUnservableToken keeps a malformed token out of the ServeMux pattern, where a wildcard or an escape would change what the registered route matches.
func TestChallengeRoutes_RejectsAnUnservableToken(t *testing.T) {
	tests := map[string]string{
		"slash":      "a/b",
		"wildcard":   "{id}",
		"percent":    "a%2fb",
		"query":      "a?b",
		"fragment":   "a#b",
		"space":      "a b",
		"newline":    "a\nb",
		"non-ascii":  "aé",
		"overlong":   string(make([]byte, 513)),
		"tab-inside": "a\tb",
	}
	for name, token := range tests {
		t.Run(name, func(t *testing.T) {
			routes, err := mcpinternal.ChallengeRoutes("", "", token)
			require.Nil(t, routes)
			require.Error(t, err)
			require.True(t, mcpinternal.IsChallengeTokenInvalidError(err), "got %v", err)
		})
	}
}

func TestChallengeRoutes_RejectsAnUnservablePrefix(t *testing.T) {
	tests := map[string]string{
		"unrooted":     "well-known/x/",
		"no-slash-end": "/.well-known/x",
		"wildcard":     "/{a}/",
		"space":        "/a b/",
		"percent":      "/a%2f/",
		"query":        "/a?b/",
	}
	for name, prefix := range tests {
		t.Run(name, func(t *testing.T) {
			routes, err := mcpinternal.ChallengeRoutes("", prefix, "tok")
			require.Nil(t, routes)
			require.Error(t, err)
			require.True(t, mcpinternal.IsChallengePrefixInvalidError(err), "got %v", err)
		})
	}
}

// TestChallengeRoutes_RegisteredPatternsAreServable keeps every returned pattern one http.ServeMux accepts: a pattern it panics on would take the whole server down at boot.
func TestChallengeRoutes_RegisteredPatternsAreServable(t *testing.T) {
	routes, err := mcpinternal.ChallengeRoutes("/app", "", "tok_servable")
	require.NoError(t, err)
	require.Len(t, routes, 2)

	mux := http.NewServeMux()
	require.NotPanics(t, func() {
		for pattern, h := range routes {
			mux.Handle(pattern, h)
		}
	})
}
