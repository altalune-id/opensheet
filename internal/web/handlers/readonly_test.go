package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/credential"
	"altalune.id/opensheet/internal/invite"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/capabilities"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/sheet"
	"altalune.id/opensheet/internal/testutil/fakes"
	"altalune.id/opensheet/internal/web/handlers"
	"altalune.id/opensheet/internal/web/templates"
)

type roleFixture struct {
	*handlerFixture
	mux    *http.ServeMux
	org    *org.Org
	owner  uuid.UUID
	admin  uuid.UUID
	member uuid.UUID
	known  *fakes.OrgProjects
}

func newRoleFixture(t *testing.T) *roleFixture {
	t.Helper()
	f := newFixture(t)
	f.Deps.Caps = capabilities.Capabilities{OrgCreation: true, LocalIdentity: true, InvitesEnabled: true}
	owner, admin, member := uuid.New(), uuid.New(), uuid.New()
	o := f.seedOrg(t, "acme", owner)
	for id, role := range map[uuid.UUID]org.Role{admin: org.RoleAdmin, member: org.RoleMember} {
		m, err := org.NewMembership(o.ID, id, role)
		require.NoError(t, err)
		require.NoError(t, f.OrgStore.SaveMembership(context.Background(), m))
	}
	alpha, err := f.Projects.Create(authz.AsSystem(setTenant(context.Background(), o.ID, owner)), o.ID, "alpha", "Alpha")
	require.NoError(t, err)
	known := fakes.NewOrgProjects()
	known.Add(o.ID, alpha.ID)
	f.Orgs = org.NewService(f.OrgStore, f.Deps.Caps, discardLogger(), passthroughUnexpected(), fakes.UnitOfWork, org.WithProjects(known))
	mux := http.NewServeMux()
	handlers.NewOrgHandler(f.Deps, f.Orgs).Register(mux)
	handlers.NewInviteHandler(f.Deps, f.Orgs, f.Invites).Register(mux)
	handlers.NewProjectHandler(f.Deps, f.Projects).Register(mux)
	return &roleFixture{handlerFixture: f, mux: mux, org: o, owner: owner, admin: admin, member: member, known: known}
}

func (x *roleFixture) do(t *testing.T, as uuid.UUID, method, target string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	x.mux.ServeHTTP(rec, x.authedRequest(t, method, target, form.Encode(), session.Principal{UserID: as, ActiveOrgID: x.org.ID}))
	return rec
}

func TestMembersPage_MemberSeesReadOnlyReason(t *testing.T) {
	t.Parallel()
	x := newRoleFixture(t)

	rec := x.do(t, x.member, http.MethodGet, "/orgs/acme/members", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "members.read_only")
	assert.NotContains(t, body, "/remove", "a member gets no remove button")
	assert.NotContains(t, body, "members.invite_button", "a member gets no invite button")

	rec = x.do(t, x.admin, http.MethodGet, "/orgs/acme/members", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "members.read_only")
	assert.Contains(t, rec.Body.String(), "/remove")
}

func TestOrg_MemberWritesRefusedBeforeValidation(t *testing.T) {
	t.Parallel()
	x := newRoleFixture(t)
	for name, tc := range map[string]struct {
		target string
		form   url.Values
		reason string
	}{
		"rename with an empty name": {"/orgs/acme/rename", url.Values{"name": {""}}, "org.rename_managers_only"},
		"remove with a bad id":      {"/orgs/acme/members/not-a-uuid/remove", nil, "members.read_only"},
	} {
		t.Run(name, func(t *testing.T) {
			rec := x.do(t, x.member, http.MethodPost, tc.target, tc.form)
			assert.Equal(t, http.StatusForbidden, rec.Code)
			assert.Contains(t, rec.Body.String(), tc.reason)
		})
	}
	got, err := x.Orgs.ByID(context.Background(), x.org.ID)
	require.NoError(t, err)
	assert.Equal(t, "ACME", got.Name)
}

func TestInvitesPage_MemberSeesNoPendingInvites(t *testing.T) {
	t.Parallel()
	x := newRoleFixture(t)
	inv, err := invite.New(invite.NewParams{OrgID: x.org.ID, Email: "pending@example.com", Role: invite.RoleMember, TTL: time.Hour, Token: "tok"})
	require.NoError(t, err)
	require.NoError(t, x.InvStore.Save(context.Background(), inv))

	rec := x.do(t, x.member, http.MethodGet, "/orgs/acme/invites", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "invites.read_only")
	assert.NotContains(t, body, "pending@example.com", "pending invites are for owners and admins only")
	assert.NotContains(t, body, `name="email"`, "a member gets no invite form")

	rec = x.do(t, x.admin, http.MethodGet, "/orgs/acme/invites", nil)
	assert.Contains(t, rec.Body.String(), "pending@example.com")
	assert.NotContains(t, rec.Body.String(), "invites.read_only")
}

func TestInvites_MemberSendRefusedBeforeValidation(t *testing.T) {
	t.Parallel()
	x := newRoleFixture(t)
	rec := x.do(t, x.member, http.MethodPost, "/orgs/acme/invites", url.Values{"email": {"x@example.com"}, "role": {"bogus"}})
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "invites.read_only")

	rec = x.do(t, x.member, http.MethodPost, "/orgs/acme/invites/not-a-uuid/revoke", nil)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "invites.read_only")

	pending, err := x.Invites.ListPending(authz.AsSystem(setTenant(context.Background(), x.org.ID, x.owner)))
	require.NoError(t, err)
	assert.Empty(t, pending)
}

func TestProjectsPage_MemberCannotCreate(t *testing.T) {
	t.Parallel()
	x := newRoleFixture(t)

	rec := x.do(t, x.member, http.MethodGet, "/orgs/acme/projects", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "projects.create_managers_only")
	assert.NotContains(t, rec.Body.String(), "/projects/new")

	rec = x.do(t, x.member, http.MethodGet, "/orgs/acme/projects/new", nil)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "projects.create_managers_only")

	rec = x.do(t, x.member, http.MethodPost, "/orgs/acme/projects", url.Values{"slug": {""}, "name": {""}})
	assert.Equal(t, http.StatusForbidden, rec.Code, "the role check runs before slug validation")
	assert.Contains(t, rec.Body.String(), "projects.create_managers_only")

	rec = x.do(t, x.member, http.MethodPost, "/orgs/acme/projects/alpha/rename", url.Values{"name": {""}})
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "projects.rename_managers_only")

	rec = x.do(t, x.admin, http.MethodGet, "/orgs/acme/projects", nil)
	assert.Contains(t, rec.Body.String(), "/projects/new")
	assert.NotContains(t, rec.Body.String(), "projects.create_managers_only")
}

func TestAPIKeyPage_DeniedShowsNotManager(t *testing.T) {
	t.Parallel()
	for _, p := range []authz.Permission{authz.APIKeysWrite, authz.ProjectsWrite} {
		assert.Equal(t, templates.APIKeyErrorNotManager, handlers.APIKeyErrorKindOf(&authz.DeniedError{Permission: p, Side: authz.SideRole}), string(p))
	}
}

func (x *roleFixture) roleOf(t *testing.T, id uuid.UUID) org.Role {
	t.Helper()
	m, err := x.OrgStore.MembershipOf(context.Background(), x.org.ID, id)
	require.NoError(t, err)
	return m.Role
}

func TestMembers_SetRoleRoute(t *testing.T) {
	t.Parallel()
	x := newRoleFixture(t)
	post := func(as, target uuid.UUID, role string) *httptest.ResponseRecorder {
		return x.do(t, as, http.MethodPost, "/orgs/acme/members/"+target.String()+"/role", url.Values{"role": {role}})
	}

	rec := post(x.member, x.admin, "bogus")
	assert.Equal(t, http.StatusForbidden, rec.Code, "the role check runs before the role is validated")
	assert.Contains(t, rec.Body.String(), "members.read_only")

	rec = post(x.admin, x.member, "owner")
	assert.Equal(t, http.StatusForbidden, rec.Code, "only an owner makes an owner")
	assert.Contains(t, rec.Body.String(), "authz.escalation")
	assert.Equal(t, org.RoleMember, x.roleOf(t, x.member))

	rec = post(x.admin, x.owner, "member")
	assert.Equal(t, http.StatusForbidden, rec.Code, "an admin cannot demote an owner")

	rec = post(x.owner, x.owner, "admin")
	assert.Equal(t, http.StatusConflict, rec.Code, "the last owner cannot step down")
	assert.Contains(t, rec.Body.String(), "authz.last_owner")

	rec = post(x.owner, x.member, "bogus")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "members.error.bad_role")

	assert.Equal(t, http.StatusSeeOther, post(x.admin, x.member, "admin").Code)
	assert.Equal(t, org.RoleAdmin, x.roleOf(t, x.member))
	assert.Equal(t, http.StatusSeeOther, post(x.owner, x.member, "owner").Code)
	assert.Equal(t, http.StatusSeeOther, post(x.owner, x.owner, "admin").Code, "with a second owner the first may step down")
	assert.Equal(t, org.RoleAdmin, x.roleOf(t, x.owner))
}

func TestMembersPage_RoleSelectFollowsTheCallersRole(t *testing.T) {
	t.Parallel()
	x := newRoleFixture(t)
	form := func(id uuid.UUID) string { return `/orgs/acme/members/` + id.String() + `/role` }

	body := x.do(t, x.owner, http.MethodGet, "/orgs/acme/members", nil).Body.String()
	assert.Contains(t, body, form(x.member))
	assert.Contains(t, body, form(x.admin))
	assert.Contains(t, body, `value="owner"`, "an owner may grant owner")
	assert.Contains(t, body, "authz.last_owner", "the last owner's role is shown locked with the reason")
	assert.NotContains(t, body, form(x.owner), "the last owner gets no role form")

	body = x.do(t, x.admin, http.MethodGet, "/orgs/acme/members", nil).Body.String()
	assert.Contains(t, body, form(x.member))
	assert.NotContains(t, body, form(x.owner), "an admin cannot change an owner")
	assert.NotContains(t, body, `value="owner"`, "an admin cannot grant owner")

	body = x.do(t, x.member, http.MethodGet, "/orgs/acme/members", nil).Body.String()
	assert.NotContains(t, body, `/role"`)
	assert.NotContains(t, body, "<select")
}

func (f *sheetsFixture) actAsMember(t *testing.T) uuid.UUID {
	t.Helper()
	m := uuid.New()
	_, err := f.Orgs.AddMember(setTenant(context.Background(), f.Org.ID, f.Principal.UserID), f.Org.ID, m, org.RoleMember)
	require.NoError(t, err)
	f.Principal = session.Principal{UserID: m, ActiveOrgID: f.Org.ID, ActiveProjectID: f.Project.ID}
	return m
}

func TestSheetHandler_MemberTurnsWritesOff(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedCredential(t, credential.KindServiceAccount, "Prod")
	sh := f.seedWritableSheet(t, f.seedSpreadsheet(t, c.ID).ID, "q1")
	f.actAsMember(t)

	page := f.do(t, http.MethodGet, f.path("/sheets/"+sh.ID.String()), "")
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	assert.NotRegexp(t, `<input data-writable[^>]* disabled`, page.Body.String(), "a member may turn writes off")
	assert.NotContains(t, page.Body.String(), "writes.managers_only")

	rec := f.do(t, http.MethodPost, f.path("/sheets/"+sh.ID.String()),
		url.Values{"tab": {""}, "visibility": {"key"}, "cache_ttl": {"60"}}.Encode())
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	got, err := f.Sheets.ByID(f.ctx(), sh.ID)
	require.NoError(t, err)
	assert.False(t, got.Writable)
	assert.Equal(t, time.Minute, got.CacheTTL)
}

func TestSheetHandler_MemberCannotTurnWritesOn(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedCredential(t, credential.KindServiceAccount, "Prod")
	sh := f.seedSheet(t, f.seedSpreadsheet(t, c.ID).ID, "q1", sheet.VisibilityKey)
	f.actAsMember(t)

	page := f.do(t, http.MethodGet, f.path("/sheets/"+sh.ID.String()), "")
	assert.Regexp(t, `<input data-writable[^>]* disabled`, page.Body.String())
	assert.Contains(t, page.Body.String(), "writes.managers_only")

	rec := f.do(t, http.MethodPost, f.path("/sheets/"+sh.ID.String()),
		url.Values{"tab": {""}, "visibility": {"key"}, "cache_ttl": {"0"}, "writable": {"1"}}.Encode())
	assert.Contains(t, rec.Body.String(), "writes.managers_only", "the service refuses a forged post")
	got, err := f.Sheets.ByID(f.ctx(), sh.ID)
	require.NoError(t, err)
	assert.False(t, got.Writable)
}

func TestSpreadsheetHandler_MemberTurningWritesOnSeesTheReason(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedCredential(t, credential.KindServiceAccount, "Prod")
	sp := f.seedSpreadsheet(t, c.ID)
	f.actAsMember(t)

	page := f.do(t, http.MethodGet, f.path("/spreadsheets/"+sp.ID.String()), "")
	assert.Regexp(t, `<input data-spreadsheet-writable[^>]* disabled`, page.Body.String())
	assert.Contains(t, page.Body.String(), "writes.managers_only")

	rec := f.do(t, http.MethodPost, f.path("/spreadsheets/"+sp.ID.String()),
		url.Values{"title": {sp.Title}, "credential_id": {c.ID.String()}, "writable": {"1"}}.Encode())
	assert.Contains(t, rec.Body.String(), "writes.managers_only")
	assert.NotContains(t, rec.Body.String(), "Could not register that spreadsheet.")
	got, err := f.Sprds.ByID(f.ctx(), sp.ID)
	require.NoError(t, err)
	assert.False(t, got.Writable)
}

func TestCredentialHandler_MemberDeletingAnothersCredentialSeesTheReason(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedCredential(t, credential.KindServiceAccount, "Prod")
	f.actAsMember(t)

	rec := f.do(t, http.MethodPost, f.path("/credentials/"+c.ID.String()+"/delete"), "")
	assert.Contains(t, rec.Body.String(), "credentials.delete_managers_only")
	assert.NotContains(t, rec.Body.String(), "Could not delete that credential.")
	items, err := f.Creds.List(f.ctx())
	require.NoError(t, err)
	assert.Len(t, items, 1)
}
