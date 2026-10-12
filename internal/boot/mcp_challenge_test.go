package boot_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/boot"
	mcpinternal "altalune.id/opensheet/internal/mcp"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/tokens"
)

const challengeToken = "tok_boot_challenge"

func challengePath(token string) string { return mcpinternal.DefaultChallengePrefix + token }

func (c *captureLog) warned(substr string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, line := range c.lines {
		if strings.Contains(line.msg, substr) {
			return true
		}
	}
	return false
}

// TestMCPChallengeIsServedByBootsOwnMux runs every assertion against boot's real wiring, not a synthetic web.ServerOpts: a mount that exists only in a test harness guards nothing.
func TestMCPChallengeIsServedByBootsOwnMux(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true, challengeToken: challengeToken})

	got := f.get(t, challengePath(challengeToken), "", "")

	require.Equal(t, http.StatusOK, got.Code, "body=%s", got.Body.String())
	require.Equal(t, challengeToken, got.Body.String())
	require.Equal(t, "no-store", got.Header().Get("Cache-Control"))
	require.Equal(t, "text/plain; charset=utf-8", got.Header().Get("Content-Type"))
}

// TestMCPChallengeAnswersOnlyTheConfiguredToken is the security property under boot's wiring: the token is fixed in the registered pattern, so an attacker-supplied segment can never be echoed.
func TestMCPChallengeAnswersOnlyTheConfiguredToken(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true, challengeToken: challengeToken})
	require.Equal(t, http.StatusOK, f.get(t, challengePath(challengeToken), "", "").Code,
		"the configured token must answer, or the refusals below prove nothing")

	for _, attacker := range []string{"attacker_token", challengeToken + "_x", "tok_boot_challeng"} {
		t.Run(attacker, func(t *testing.T) {
			got := f.get(t, challengePath(attacker), "", "")

			require.Equal(t, http.StatusNotFound, got.Code,
				"an attacker-supplied token validated; another tenant could claim this resource URI")
			require.NotContains(t, got.Body.String(), attacker)
		})
	}
}

// TestMCPChallengeRunsOnTheProbeChain keeps the proof machine-facing: no CSP, no session cookie, nothing an SSR gate would put on it.
func TestMCPChallengeRunsOnTheProbeChain(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true, challengeToken: challengeToken})

	console := f.get(t, "/login", "", "")
	require.NotEmpty(t, console.Header().Get("Content-Security-Policy"),
		"the console chain must set CSP, or the assertions below prove nothing")

	got := f.get(t, challengePath(challengeToken), "", "")
	require.Empty(t, got.Header().Get("Content-Security-Policy"),
		"the challenge ran through the console chain; it is machine-facing and unauthenticated")
	require.Empty(t, got.Header().Values("Set-Cookie"),
		"the challenge touched the session; a machine surface issues no cookies")
}

func TestMCPChallengeIsNotMountedWithoutAToken(t *testing.T) {
	f := newMCPFixture(t, mcpOpts{enabled: true})

	require.Equal(t, http.StatusNotFound, f.get(t, challengePath(challengeToken), "", "").Code)
	require.True(t, f.log.warned("mcp challenge token unset"),
		"boot must warn: the proof is re-checked on a schedule, so the failure arrives long after boot")
}

func TestMCPChallengeIsNotMountedWhileMCPIsDisabled(t *testing.T) {
	cfg := newSmokeCfg(t)
	cfg.MCP = config.MCPConfig{ChallengeToken: challengeToken}

	srv, err := boot.BootServer(t.Context(), cfg, boot.WithScheduler(false))
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Close() })

	rec := httptest.NewRecorder()
	srv.Web.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, challengePath(challengeToken), nil))
	require.NotEqual(t, http.StatusOK, rec.Code, "a disabled MCP surface must publish no proof")
	require.NotContains(t, rec.Body.String(), challengeToken)
}

func TestMCPChallengeRefusesAnUnservableToken(t *testing.T) {
	cfg := newSmokeCfg(t)
	cfg.Tokens = tokens.Config{Issuer: stubIssuer(t), Audience: controlAudience}
	cfg.HTTP.BaseURL = "http://127.0.0.1"
	cfg.MCP = config.MCPConfig{Enabled: true, Audience: mcpAudience, ChallengeToken: "bad/token"}

	_, err := boot.BootServer(t.Context(), cfg, boot.WithScheduler(false))

	require.Error(t, err, "a malformed token must fail boot, not vanish into a 404 months later")
	require.True(t, mcpinternal.IsChallengeTokenInvalidError(err), "got %v", err)
}
