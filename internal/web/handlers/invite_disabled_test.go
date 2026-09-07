package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/template/internal/invite"
	"altalune.id/template/internal/org"
	"altalune.id/template/internal/platform/capabilities"
	"altalune.id/template/internal/platform/session"
	"altalune.id/template/internal/web/handlers"
)

func TestInviteHandler_GetList_ShowsDisabledBanner(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.Deps.Caps = capabilities.Capabilities{OrgCreation: true, LocalIdentity: true, InvitesEnabled: false}
	ctx := context.Background()
	uid := uuid.New()
	_, err := f.Orgs.Create(ctx, org.CreateRequest{Slug: "acme", Name: "Acme", OwnerID: uid})
	require.NoError(t, err)

	h := handlers.NewInviteHandler(f.Deps, f.Orgs, f.Invites)
	mux := http.NewServeMux()
	h.Register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodGet, "/orgs/acme/invites", "", session.Principal{UserID: uid}))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "invites.disabled_banner")
	assert.NotContains(t, rec.Body.String(), `name="email"`)
}

func TestInviteHandler_PostSend_ReturnsConflictWhenDisabled(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := context.Background()
	uid := uuid.New()
	_, err := f.Orgs.Create(ctx, org.CreateRequest{Slug: "acme", Name: "Acme", OwnerID: uid})
	require.NoError(t, err)

	sendWorkflow := invite.NewSendWorkflow(f.InvStore, nopMailer{}, "http://localhost", discardLogger(), passthroughUnexpected())
	invites := invite.NewService(f.InvStore, sendWorkflow, nil, false, discardLogger(), passthroughUnexpected())

	h := handlers.NewInviteHandler(f.Deps, f.Orgs, invites)
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, f.authedRequest(t, http.MethodPost, "/orgs/acme/invites", "email=b@c.co&role=member", session.Principal{UserID: uid}))
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "disabled")
}
