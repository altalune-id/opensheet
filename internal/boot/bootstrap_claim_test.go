package boot

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/onboard"
	"altalune.id/template/internal/platform/config"
	"altalune.id/template/internal/testutil/fakes"
	"altalune.id/template/internal/user"
	"altalune.id/template/logger"
)

func noopUnexpected() apperror.UnexpectedFunc {
	return func(_ context.Context, message string, cause error, _ ...any) *apperror.AppError {
		return apperror.New(apperror.CodeUnexpectedError, message, 0).WithCause(cause)
	}
}

func newBootstrapFixture(t *testing.T, genesisEmail string) (
	*bytes.Buffer, *fakes.User, *fakes.Onboard, *user.Service, *onboard.Service, *slog.Logger,
) {
	t.Helper()
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	userStore := fakes.NewUser()
	onboardStore := fakes.NewOnboard()
	users := user.NewService(userStore, user.GenesisConfig{Email: genesisEmail}, log, noopUnexpected())
	onboards := onboard.NewService(onboardStore, log, noopUnexpected())
	return &buf, userStore, onboardStore, users, onboards, log
}

func seedBootstrapUser(t *testing.T, store *fakes.User, email string, admin bool) *user.User {
	t.Helper()
	u, err := user.New(email, "Seed", user.SourceOIDC)
	require.NoError(t, err)
	u.IsAdmin = admin
	require.NoError(t, store.Save(t.Context(), u))
	return u
}

func TestBootstrap_UnclaimedGenesisWarnsAndWritesNothing(t *testing.T) {
	buf, userStore, _, users, onboards, log := newBootstrapFixture(t, "root@x.co")
	cfg := config.Defaults()
	cfg.Genesis.Email = "root@x.co"

	onboarded, err := bootstrap(t.Context(), cfg, users, onboards, log)
	require.NoError(t, err)
	require.False(t, onboarded, "an unclaimed genesis address must not mark the deployment onboarded")
	require.Zero(t, userStore.Len(), "boot must not create a user")
	require.Contains(t, buf.String(), "root@x.co", "the unclaimed address must be named in the warning")
	require.Contains(t, buf.String(), "not yet claimed")
}

func TestBootstrap_ClaimedGenesisPromotesInPlace(t *testing.T) {
	buf, userStore, _, users, onboards, log := newBootstrapFixture(t, "root@x.co")
	seeded := seedBootstrapUser(t, userStore, "root@x.co", false)
	cfg := config.Defaults()
	cfg.Genesis.Email = "root@x.co"

	onboarded, err := bootstrap(t.Context(), cfg, users, onboards, log)
	require.NoError(t, err)
	require.False(t, onboarded)
	require.Equal(t, 1, userStore.Len(), "reconciliation must promote in place, never insert")

	after, err := userStore.ByID(t.Context(), seeded.ID)
	require.NoError(t, err)
	require.True(t, after.IsAdmin)
	require.Contains(t, buf.String(), "genesis admin promoted")
}

func TestBootstrap_ReportsOnboardedWhenBootstrapRowExists(t *testing.T) {
	_, _, _, users, onboards, log := newBootstrapFixture(t, "")
	cfg := config.Defaults()
	_, err := onboards.Complete(t.Context(), uuid.New(), onboard.MethodCLIInit)
	require.NoError(t, err)

	onboarded, bErr := bootstrap(t.Context(), cfg, users, onboards, log)
	require.NoError(t, bErr)
	require.True(t, onboarded)
}

func TestSetupToken_PinnedValueIsUsedVerbatim(t *testing.T) {
	cfg := config.Defaults()
	cfg.Onboard.SetupToken = "pinned-value"

	got, err := setupToken(cfg)
	require.NoError(t, err)
	require.Equal(t, "pinned-value", got)
}

func TestSetupToken_MintsWhenUnset(t *testing.T) {
	cfg := config.Defaults()

	first, err := setupToken(cfg)
	require.NoError(t, err)
	second, err := setupToken(cfg)
	require.NoError(t, err)

	require.NotEqual(t, first, second, "each boot mints a fresh token")
	require.Len(t, first, setupTokenLen)
}

func TestLogSetupToken_PinnedTokenIsNeverLogged(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo, ReplaceAttr: logger.Redact(logger.Config{})}))
	cfg := config.Defaults()
	cfg.HTTP.BaseURL = "https://altempl.example/"
	cfg.HTTP.BasePath = "/app"
	cfg.Onboard.SetupToken = "pinned-value"

	logSetupToken(cfg, log, "pinned-value")

	require.NotContains(t, buf.String(), "pinned-value", "a pinned token must never reach the logs")
	require.Contains(t, buf.String(), "https://altempl.example/app/onboard")
}

func TestLogSetupToken_MintedTokenIsLoggedWithTheOnboardURL(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo, ReplaceAttr: logger.Redact(logger.Config{})}))
	cfg := config.Defaults()
	cfg.HTTP.BaseURL = "https://altempl.example"
	cfg.HTTP.BasePath = ""

	logSetupToken(cfg, log, "minted-value")

	// SECURITY: logger.Redact masks any attr key matching /token/, so a redacted value here would lock a fresh deployment out of /onboard.
	require.NotContains(t, buf.String(), "<redacted>")
	require.Contains(t, buf.String(), "minted-value")
	require.Contains(t, buf.String(), "https://altempl.example/onboard")
}
