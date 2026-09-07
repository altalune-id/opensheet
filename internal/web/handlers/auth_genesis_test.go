package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/template/internal/auth"
	"altalune.id/template/internal/password"
	"altalune.id/template/internal/platform/session"
	"altalune.id/template/internal/user"
	"altalune.id/template/internal/web"
	"altalune.id/template/internal/web/handlers"
)

func newAuthHandlerWithGenesis(t *testing.T, f *handlerFixture, email, plain string) *handlers.AuthHandler {
	t.Helper()
	hash, err := password.Hash(plain)
	require.NoError(t, err)
	local := auth.NewLocalLogin(
		&authUserStore{store: f.UserStore},
		auth.Genesis{Email: email, PasswordHash: hash, Name: "Root"},
		discardLogger(), passthroughUnexpected(),
		auth.WithLocalNotFound(user.IsNotFoundError),
	)
	authSvc := auth.NewService(local, nil, discardLogger(), passthroughUnexpected())
	users := user.NewService(f.UserStore, user.GenesisConfig{Email: email}, discardLogger(), passthroughUnexpected())
	return handlers.NewAuthHandler(f.Deps, authSvc, users, f.Orgs, f.Projects, nil, nil)
}

func postLogin(t *testing.T, f *handlerFixture, h *handlers.AuthHandler, email, plain string) session.Principal {
	t.Helper()
	mux := http.NewServeMux()
	h.Register(mux)

	body := "email=" + url.QueryEscape(email) + "&password=" + url.QueryEscape(plain)
	r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	require.Equal(t, http.StatusSeeOther, rec.Code, "login must succeed")

	var raw string
	for _, c := range rec.Result().Cookies() {
		if c.Name == web.SessionCookieName {
			raw = c.Value
		}
	}
	require.NotEmpty(t, raw, "a successful login must set a session cookie")
	sid, err := web.VerifyCookie([]byte(f.Cfg.HTTP.StateSecret), raw)
	require.NoError(t, err)
	p, ok, err := f.Sessions.Load(t.Context(), sid)
	require.NoError(t, err)
	require.True(t, ok)
	return p
}

func TestAuthHandler_PostLogin_GenesisAdminOnFirstLogin(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	seeded, err := f.Users.Create(t.Context(), user.CreateRequest{
		Email: "root@x.co", Name: "Root", Source: user.SourceOIDC, Password: "secret12",
	})
	require.NoError(t, err)
	require.False(t, seeded.IsAdmin, "the seed must start as an ordinary user")

	h := newAuthHandlerWithGenesis(t, f, "root@x.co", "unused-genesis-password")
	p := postLogin(t, f, h, "root@x.co", "secret12")

	assert.True(t, p.IsAdmin, "the genesis claim must reach the session on the FIRST login, not the second")

	after, err := f.UserStore.ByID(t.Context(), seeded.ID)
	require.NoError(t, err)
	assert.True(t, after.IsAdmin)
}

func TestAuthHandler_PostLogin_NonGenesisUserIsNotPromoted(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	other, err := f.Users.Create(t.Context(), user.CreateRequest{
		Email: "other@x.co", Name: "Other", Source: user.SourceOIDC, Password: "secret12",
	})
	require.NoError(t, err)

	h := newAuthHandlerWithGenesis(t, f, "root@x.co", "unused-genesis-password")
	p := postLogin(t, f, h, "other@x.co", "secret12")

	assert.False(t, p.IsAdmin, "a login by a non-genesis address must not grant admin")
	after, err := f.UserStore.ByID(t.Context(), other.ID)
	require.NoError(t, err)
	assert.False(t, after.IsAdmin)
	assert.Equal(t, 1, f.UserStore.Len(), "reconciling an unclaimed address must not create a user")
}

func TestLocalLogin_GenesisSeedsExactlyOnce(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	h := newAuthHandlerWithGenesis(t, f, "root@x.co", "correct-password")

	first := postLogin(t, f, h, "root@x.co", "correct-password")
	assert.True(t, first.IsAdmin, "the seeded genesis user is an admin")
	require.Equal(t, 1, f.UserStore.Len())

	second := postLogin(t, f, h, "root@x.co", "correct-password")
	assert.Equal(t, first.UserID, second.UserID, "a second login must reuse the row, not mint another")
	assert.Equal(t, 1, f.UserStore.Len(), "genesis is seed-if-absent, never overwrite")
	assert.True(t, second.IsAdmin)
}
