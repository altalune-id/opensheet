package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/template/internal/platform/session"
	"altalune.id/template/internal/user"
	"altalune.id/template/internal/web/handlers"
)

func TestOnboardHandler_PostOIDCComplete_PromotesWithGenesisEmailSet(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.Cfg.Genesis.Email = "root@x.co"
	ctx := context.Background()
	u, err := f.Users.Create(ctx, user.CreateRequest{Email: "someone@x.co", Name: "Someone", Source: user.SourceOIDC})
	require.NoError(t, err)
	require.False(t, u.IsAdmin)

	req := &atomicBoolWrapper{}
	req.b.Store(true)
	h := handlers.NewOnboardHandler(f.Deps, f.Users, f.Orgs, f.Projects, f.Onboards, &req.b, "")
	mux := http.NewServeMux()
	h.Register(mux)

	rec := httptest.NewRecorder()
	body := "org_slug=acme&org_name=Acme&project_slug=default&project_name=Default+Project"
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodPost, "/onboard/complete", body, session.Principal{UserID: u.ID, Email: u.Email}))
	require.Equal(t, http.StatusSeeOther, rec.Code)

	after, err := f.UserStore.ByID(ctx, u.ID)
	require.NoError(t, err)
	assert.True(t, after.IsAdmin, "the onboarding user is by definition the first admin")
}
