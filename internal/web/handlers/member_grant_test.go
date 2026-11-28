package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/invite"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/web/handlers"
)

func (x *roleFixture) restrict(t *testing.T, userID uuid.UUID, projects ...uuid.UUID) {
	t.Helper()
	require.NoError(t, x.OrgStore.SetProjectGrant(context.Background(), x.org.ID, userID, org.ProjectGrant{ProjectIDs: projects}))
}

func (x *roleFixture) alpha(t *testing.T) uuid.UUID {
	t.Helper()
	items, err := x.Projects.List(authz.AsSystem(setTenant(context.Background(), x.org.ID, x.owner)), x.org.ID)
	require.NoError(t, err)
	for _, p := range items {
		if p.Slug == "alpha" {
			return p.ID
		}
	}
	t.Fatal("alpha project not seeded")
	return uuid.Nil
}

func TestInvitesPage_GrantDefaultsToAllProjects(t *testing.T) {
	t.Parallel()
	x := newRoleFixture(t)

	body := x.do(t, x.admin, http.MethodGet, "/orgs/acme/invites", nil).Body.String()
	assert.Contains(t, body, "members.access_label")
	assert.Contains(t, body, "members.access_hint")
	assert.Regexp(t, `<input type="radio" name="grant" value="all" checked`, body)
	assert.Regexp(t, `<input type="radio" name="grant" value="selected"`, body)
	assert.Contains(t, body, `name="project_ids" value="`+x.alpha(t).String()+`"`)
	assert.Contains(t, body, "group-has-[input[value=all]:checked]:hidden")
}

func TestInvitesPage_NoProjectsHidesOnlyThese(t *testing.T) {
	t.Parallel()
	x := newRoleFixture(t)
	empty := x.seedOrg(t, "empty", x.admin)

	rec := httptest.NewRecorder()
	x.mux.ServeHTTP(rec, x.authedRequest(t, http.MethodGet, "/orgs/empty/invites", "", session.Principal{UserID: x.admin, ActiveOrgID: empty.ID}))
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "members.access_no_projects")
	assert.NotContains(t, body, `value="selected"`)
}

func TestMembersPage_ManagerEditsAccess(t *testing.T) {
	t.Parallel()
	x := newRoleFixture(t)
	alpha := x.alpha(t)
	x.restrict(t, x.member, alpha)

	body := x.do(t, x.owner, http.MethodGet, "/orgs/acme/members", nil).Body.String()
	assert.Contains(t, body, `action="/orgs/acme/members/`+x.member.String()+`/projects"`)
	assert.Regexp(t, `name="project_ids" value="`+alpha.String()+`" checked`, body)
	assert.Contains(t, body, "members.access_count")
	assert.Contains(t, body, "members.save_access")
	assert.Contains(t, body, "members.access_for")
	assert.NotContains(t, body, `action="/orgs/acme/members/`+x.owner.String()+`/projects"`, "an owner row has no access form")
	assert.Contains(t, body, "members.access_owner")
	assert.Contains(t, body, "group-has-[option[value=owner]:checked]/access:hidden")
}

func TestMembersPage_NonManagerSeesNoProjectNames(t *testing.T) {
	t.Parallel()
	x := newRoleFixture(t)
	x.restrict(t, x.admin, x.alpha(t))

	body := x.do(t, x.member, http.MethodGet, "/orgs/acme/members", nil).Body.String()
	assert.Regexp(t, `data-access[^>]*>\s*members.access_some_label`, body)
	assert.NotContains(t, body, "members.access_count")
	assert.NotRegexp(t, `/members/[^"]+/projects"`, body, "a non-manager gets no access form")
}

func (x *roleFixture) grantOf(t *testing.T, userID uuid.UUID) (bool, []uuid.UUID) {
	t.Helper()
	m, err := x.OrgStore.MembershipOf(context.Background(), x.org.ID, userID)
	require.NoError(t, err)
	return m.AllProjects, m.ProjectIDs
}

func TestMembers_SetAccessRestrictsAndWidens(t *testing.T) {
	t.Parallel()
	x := newRoleFixture(t)
	alpha := x.alpha(t)
	target := "/orgs/acme/members/" + x.member.String() + "/projects"

	rec := x.do(t, x.owner, http.MethodPost, target, url.Values{"grant": {"selected"}, "project_ids": {alpha.String()}})
	require.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Contains(t, rec.Header().Get("Location"), "access_updated=1")
	all, ids := x.grantOf(t, x.member)
	assert.False(t, all)
	assert.Equal(t, []uuid.UUID{alpha}, ids)

	rec = x.do(t, x.owner, http.MethodPost, target, url.Values{"grant": {"all"}, "project_ids": {alpha.String()}})
	require.Equal(t, http.StatusSeeOther, rec.Code)
	all, ids = x.grantOf(t, x.member)
	assert.True(t, all, "the all choice drops the hidden checkbox values")
	assert.Empty(t, ids)

	assert.Contains(t, x.do(t, x.owner, http.MethodGet, "/orgs/acme/members?access_updated=1", nil).Body.String(), "members.access_updated")
}

func TestMembers_SetAccessEmptyKeepsTheChoice(t *testing.T) {
	t.Parallel()
	x := newRoleFixture(t)

	rec := x.do(t, x.owner, http.MethodPost, "/orgs/acme/members/"+x.member.String()+"/projects", url.Values{"grant": {"selected"}})
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "members.error.access_empty")
	assert.Contains(t, body, "ROL010")
	assert.Regexp(t, `<details open[^>]*>\s*<summary`, body, "the edited row reopens")
	assert.Regexp(t, `name="grant" value="selected" checked`, body)
	all, _ := x.grantOf(t, x.member)
	assert.True(t, all)
}

func TestMembers_SetAccessOutsideTheOrgIsROL012(t *testing.T) {
	t.Parallel()
	x := newRoleFixture(t)
	other := x.seedOrg(t, "other", x.owner)
	foreign, err := x.Projects.Create(authz.AsSystem(setTenant(context.Background(), other.ID, x.owner)), other.ID, "beta", "Beta")
	require.NoError(t, err)

	rec := x.do(t, x.owner, http.MethodPost, "/orgs/acme/members/"+x.member.String()+"/projects", url.Values{"grant": {"selected"}, "project_ids": {foreign.ID.String()}})
	assert.Equal(t, http.StatusForbidden, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "members.error.access_escalation")
	assert.Contains(t, body, "ROL012")
	all, _ := x.grantOf(t, x.member)
	assert.True(t, all)
}

func TestMembers_SetAccessRefusedToRestrictedAdmin(t *testing.T) {
	t.Parallel()
	x := newRoleFixture(t)
	x.restrict(t, x.admin, x.alpha(t))

	rec := x.do(t, x.admin, http.MethodPost, "/orgs/acme/members/"+x.member.String()+"/projects", url.Values{"grant": {"selected"}, "project_ids": {x.alpha(t).String()}})
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "members.read_only")
	all, _ := x.grantOf(t, x.member)
	assert.True(t, all)
}

func TestMembers_SetAccessRefusedToMember(t *testing.T) {
	t.Parallel()
	x := newRoleFixture(t)

	rec := x.do(t, x.member, http.MethodPost, "/orgs/acme/members/"+x.admin.String()+"/projects", url.Values{"grant": {"selected"}, "project_ids": {x.alpha(t).String()}})
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "members.read_only")
	all, _ := x.grantOf(t, x.admin)
	assert.True(t, all)
}

func (x *roleFixture) pendingInvite(t *testing.T, email string) (bool, []uuid.UUID, bool) {
	t.Helper()
	items, err := x.Invites.ListPending(authz.AsSystem(setTenant(context.Background(), x.org.ID, x.owner)))
	require.NoError(t, err)
	for _, i := range items {
		if i.Email == email {
			return i.AllProjects, i.ProjectIDs, true
		}
	}
	return false, nil, false
}

func (x *roleFixture) withSender(t *testing.T) {
	t.Helper()
	send := invite.NewSendWorkflow(x.InvStore, nopMailer{}, "http://localhost", discardLogger(), passthroughUnexpected())
	x.Invites = invite.NewService(x.InvStore, send, nil, true, discardLogger(), passthroughUnexpected(), invite.WithMembers(x.OrgStore), invite.WithProjects(x.known))
	x.mux = http.NewServeMux()
	handlers.NewInviteHandler(x.Deps, x.Orgs, x.Invites).Register(x.mux)
}

func TestInvites_SendCarriesTheGrant(t *testing.T) {
	t.Parallel()
	x := newRoleFixture(t)
	x.withSender(t)
	alpha := x.alpha(t)

	rec := x.do(t, x.owner, http.MethodPost, "/orgs/acme/invites", url.Values{"email": {"some@example.com"}, "role": {"member"}, "grant": {"selected"}, "project_ids": {alpha.String()}})
	require.Equal(t, http.StatusSeeOther, rec.Code)
	all, ids, found := x.pendingInvite(t, "some@example.com")
	require.True(t, found)
	assert.False(t, all)
	assert.Equal(t, []uuid.UUID{alpha}, ids)

	rec = x.do(t, x.owner, http.MethodPost, "/orgs/acme/invites", url.Values{"email": {"every@example.com"}, "role": {"member"}, "grant": {"all"}, "project_ids": {alpha.String()}})
	require.Equal(t, http.StatusSeeOther, rec.Code)
	all, ids, _ = x.pendingInvite(t, "every@example.com")
	assert.True(t, all)
	assert.Empty(t, ids)
}

func TestInvites_SendEmptyGrantKeepsTheForm(t *testing.T) {
	t.Parallel()
	x := newRoleFixture(t)
	x.withSender(t)

	rec := x.do(t, x.owner, http.MethodPost, "/orgs/acme/invites", url.Values{"email": {"kept@example.com"}, "role": {"admin"}, "grant": {"selected"}})
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "members.error.access_empty")
	assert.Contains(t, body, "ROL010")
	assert.Contains(t, body, `value="kept@example.com"`)
	assert.Regexp(t, `<option value="admin" selected`, body)
	assert.Regexp(t, `name="grant" value="selected" checked`, body)
	_, _, found := x.pendingInvite(t, "kept@example.com")
	assert.False(t, found)
}

func TestMembers_SetAccessNeedsAGrantChoice(t *testing.T) {
	t.Parallel()
	x := newRoleFixture(t)
	alpha := x.alpha(t)
	x.restrict(t, x.member, alpha)
	target := "/orgs/acme/members/" + x.member.String() + "/projects"

	for name, form := range map[string]url.Values{
		"missing": {"project_ids": {alpha.String()}},
		"unknown": {"grant": {"every"}},
	} {
		t.Run(name, func(t *testing.T) {
			rec := x.do(t, x.owner, http.MethodPost, target, form)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
			all, ids := x.grantOf(t, x.member)
			assert.False(t, all)
			assert.Equal(t, []uuid.UUID{alpha}, ids)
		})
	}
}

func TestInvitesPage_RestrictedAdminSeesNoPicker(t *testing.T) {
	t.Parallel()
	x := newRoleFixture(t)
	_, err := x.Projects.Create(authz.AsSystem(setTenant(context.Background(), x.org.ID, x.owner)), x.org.ID, "beta", "Hidden Beta")
	require.NoError(t, err)
	x.restrict(t, x.admin, x.alpha(t))

	rec := x.do(t, x.admin, http.MethodGet, "/orgs/acme/invites", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.NotContains(t, body, `name="project_ids"`)
	assert.NotContains(t, body, "Hidden Beta")
	assert.NotContains(t, body, "members.access_label")
}

func TestInvites_SendOwnerConflictNamesTheRule(t *testing.T) {
	t.Parallel()
	x := newRoleFixture(t)
	x.withSender(t)

	rec := x.do(t, x.owner, http.MethodPost, "/orgs/acme/invites", url.Values{"email": {"boss@example.com"}, "role": {"owner"}, "grant": {"selected"}, "project_ids": {x.alpha(t).String()}})
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "members.access_owner")
	assert.NotContains(t, body, "members.error.access_empty")
	assert.Contains(t, body, "ROL011")
}

func TestMembers_UnlistedRoleSelectsNoOwner(t *testing.T) {
	t.Parallel()
	x := newRoleHandlerFixture(t)
	analyst := x.seedRole(t, "Analyst", authz.SheetsRead)
	x.holdCustom(t, x.member, analyst)
	x.remountOrg(refusedRoles{})

	body := x.do(t, x.owner, http.MethodGet, "/orgs/acme/members", nil).Body.String()
	form := body[strings.Index(body, "/members/"+x.member.String()+"/role"):]
	form = form[:strings.Index(form, "</select>")]
	assert.Regexp(t, `<select[^>]*>\s*<option value="" selected disabled>`, form)
	assert.NotRegexp(t, `<option value="owner" selected`, form)

	rec := x.do(t, x.owner, http.MethodPost, "/orgs/acme/members/"+x.member.String()+"/role", url.Values{"role": {""}})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	m, err := x.OrgStore.MembershipOf(context.Background(), x.org.ID, x.member)
	require.NoError(t, err)
	assert.Equal(t, analyst.ID, m.RoleID)
}

func TestInvites_RowShowsTheGrant(t *testing.T) {
	t.Parallel()
	x := newRoleFixture(t)
	x.withSender(t)
	require.Equal(t, http.StatusSeeOther, x.do(t, x.owner, http.MethodPost, "/orgs/acme/invites", url.Values{"email": {"some@example.com"}, "role": {"member"}, "grant": {"selected"}, "project_ids": {x.alpha(t).String()}}).Code)

	body := x.do(t, x.owner, http.MethodGet, "/orgs/acme/invites", nil).Body.String()
	assert.Regexp(t, `some@example.com</p>\s*<p[^>]*>[^<]*<span data-access>members.access_count</span>`, body)
}

func TestGrantFields_HintHidesWithAll(t *testing.T) {
	t.Parallel()
	x := newRoleFixture(t)

	body := x.do(t, x.admin, http.MethodGet, "/orgs/acme/invites", nil).Body.String()
	assert.Regexp(t, `<p class="[^"]*group-has-\[input\[value=all\]:checked\]:hidden[^"]*">members.access_hint</p>`, body)
}

func TestMembersPage_RestrictedAdminGetsNoControls(t *testing.T) {
	t.Parallel()
	x := newRoleFixture(t)
	x.restrict(t, x.admin, x.alpha(t))

	rec := x.do(t, x.admin, http.MethodGet, "/orgs/acme/members", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.NotContains(t, body, "/remove\"")
	assert.NotContains(t, body, `name="role"`)
	assert.NotRegexp(t, `/members/[^"]+/projects"`, body)
	assert.NotContains(t, body, "members.invite_button")
}
