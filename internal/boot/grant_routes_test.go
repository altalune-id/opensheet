package boot_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/boot"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/user"
)

type grantWorld struct {
	srv     *boot.Server
	org     *org.Org
	target  *project.Project
	granted *project.Project
	owner   uuid.UUID
	member  uuid.UUID
}

func newGrantWorld(t *testing.T) *grantWorld {
	t.Helper()
	srv, _ := newScopeProbeServer(t, config.ModeCloud)
	ctx := context.Background()
	owner, err := srv.Users.Create(ctx, user.CreateRequest{Email: "grant-owner@example.com", Name: "Owner", Source: user.SourceOIDC})
	require.NoError(t, err)
	member, err := srv.Users.Create(ctx, user.CreateRequest{Email: "grant-member@example.com", Name: "Member", Source: user.SourceOIDC})
	require.NoError(t, err)
	o, err := srv.Orgs.Create(ctx, org.CreateRequest{Slug: "probe-org", Name: "Probe Org", OwnerID: owner.ID})
	require.NoError(t, err)
	ownerCtx := bindAs(t, srv, tenant.Context{OrgID: o.ID, UserID: owner.ID})
	target, err := srv.Projects.Create(ownerCtx, o.ID, "probe-project", "Probe Project")
	require.NoError(t, err)
	granted, err := srv.Projects.Create(ownerCtx, o.ID, "granted", "Granted")
	require.NoError(t, err)
	_, err = srv.Orgs.AddMember(tenant.WithOrg(ctx, o.ID), o.ID, member.ID, org.RoleMember)
	require.NoError(t, err)
	require.NoError(t, srv.Orgs.SetProjectGrant(ownerCtx, o.ID, member.ID, org.ProjectGrant{ProjectIDs: []uuid.UUID{granted.ID}}))
	return &grantWorld{srv: srv, org: o, target: target, granted: granted, owner: owner.ID, member: member.ID}
}

func TestConsoleRoutes_RestrictedMemberOutsideGrantIs404(t *testing.T) {
	w := newGrantWorld(t)
	cookie := probeCookie(t, w.srv, session.Principal{UserID: w.member, ActiveOrgID: w.org.ID, ActiveProjectID: w.granted.ID, TermsAcceptedAt: time.Now()})
	walked := 0
	for _, rt := range probeRoutes() {
		if !strings.Contains(rt.path, "/projects/probe-project") {
			continue
		}
		walked++
		var req *http.Request
		if rt.form == nil {
			req = httptest.NewRequest(rt.method, rt.path, nil)
		} else {
			req = httptest.NewRequest(rt.method, rt.path, strings.NewReader(rt.form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		w.srv.Web.ServeHTTP(rec, req)
		require.Equal(t, http.StatusNotFound, rec.Code, "%s %s must answer as missing outside the grant; body=%s", rt.method, rt.path, rec.Body.String())
	}
	require.NotZero(t, walked, "no project-scoped probe route was walked")

	var first string
	for _, rt := range probeRoutes() {
		if rt.method == http.MethodGet && strings.Contains(rt.path, "/projects/probe-project") {
			first = strings.Replace(rt.path, "/projects/probe-project", "/projects/granted", 1)
			break
		}
	}
	req := httptest.NewRequest(http.MethodGet, first, nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	w.srv.Web.ServeHTTP(rec, req)
	require.Less(t, rec.Code, 400, "the granted project still opens at %s; body=%s", first, rec.Body.String())
}
