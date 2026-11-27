package handlers_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/role"
	"altalune.id/opensheet/internal/testutil/fakes"
	"altalune.id/opensheet/internal/web/handlers"
	"altalune.id/opensheet/internal/web/templates"
)

type roleSvc struct {
	store *fakes.Role
	staff bool
}

func (s roleSvc) Allowed(context.Context) (bool, error) { return !s.staff, nil }

func (s roleSvc) List(ctx context.Context) ([]*role.Role, error) { return s.store.List(ctx) }

func (s roleSvc) HeldBy(ctx context.Context) (map[uuid.UUID]int, error) { return s.store.HeldBy(ctx) }

func (s roleSvc) Get(ctx context.Context, id uuid.UUID) (*role.Role, error) {
	return s.store.ByID(ctx, id)
}

func (s roleSvc) Create(ctx context.Context, name, description string, perms []authz.Permission) (*role.Role, error) {
	if err := authz.Can(ctx, authz.RolesWrite); err != nil {
		return nil, err
	}
	tc, err := tenant.From(ctx)
	if err != nil {
		return nil, err
	}
	r, err := role.New(tc.OrgID, name, description, perms, time.Now())
	if err != nil {
		return nil, err
	}
	if !authz.Covers(authz.Held(ctx), r.Permissions...) {
		return nil, &authz.EscalationError{Role: r.Name}
	}
	return r, s.store.Save(ctx, r, 0)
}

func (s roleSvc) Update(ctx context.Context, id uuid.UUID, name, description string, perms []authz.Permission, ifVersion int64) (*role.Role, error) {
	if err := authz.Can(ctx, authz.RolesWrite); err != nil {
		return nil, err
	}
	r, err := s.store.ByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := r.Revise(name, description, perms, time.Now()); err != nil {
		return nil, err
	}
	if !authz.Covers(authz.Held(ctx), r.Permissions...) {
		return nil, &authz.EscalationError{Role: r.Name}
	}
	return r, s.store.Save(ctx, r, ifVersion)
}

func (s roleSvc) Delete(ctx context.Context, id uuid.UUID) error {
	if err := authz.Can(ctx, authz.RolesWrite); err != nil {
		return err
	}
	return s.store.Delete(ctx, id)
}

type roleHandlerFixture struct {
	*roleFixture
	store *fakes.Role
}

func newRoleHandlerFixture(t *testing.T) *roleHandlerFixture {
	t.Helper()
	x := newRoleFixture(t)
	store := fakes.NewRole()
	handlers.NewRoleHandler(x.Deps, roleSvc{store: store}).Register(x.mux)
	return &roleHandlerFixture{roleFixture: x, store: store}
}

func (x *roleHandlerFixture) seedRole(t *testing.T, name string, perms ...authz.Permission) *role.Role {
	t.Helper()
	r, err := role.New(x.org.ID, name, "", perms, time.Now())
	require.NoError(t, err)
	require.NoError(t, x.store.Save(context.Background(), r, 0))
	return r
}

func TestRoleHandler_EveryRouteRequiresOrg(t *testing.T) {
	t.Parallel()
	x := newRoleHandlerFixture(t)
	stranger := uuid.New()
	x.seedOrg(t, "other", stranger)
	r := x.seedRole(t, "Analyst", authz.SheetsRead)

	routes := []struct{ method, path string }{
		{http.MethodGet, "/orgs/acme/roles"},
		{http.MethodGet, "/orgs/acme/roles/new"},
		{http.MethodPost, "/orgs/acme/roles"},
		{http.MethodGet, "/orgs/acme/roles/" + r.ID.String()},
		{http.MethodPost, "/orgs/acme/roles/" + r.ID.String()},
		{http.MethodPost, "/orgs/acme/roles/" + r.ID.String() + "/delete"},
	}
	for _, rt := range routes {
		rec := x.do(t, stranger, rt.method, rt.path, url.Values{"name": {"X"}, "perm": {"sheets:read"}})
		assert.Equal(t, http.StatusNotFound, rec.Code, "%s %s", rt.method, rt.path)
	}
	got, err := x.store.ByID(context.Background(), r.ID)
	require.NoError(t, err)
	assert.Equal(t, "Analyst", got.Name)
}

func TestRoleHandler_AdminSeesNoEditor(t *testing.T) {
	t.Parallel()
	x := newRoleHandlerFixture(t)
	r := x.seedRole(t, "Analyst", authz.SheetsRead)

	rec := x.do(t, x.admin, http.MethodGet, "/orgs/acme/roles", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "roles.read_only")
	assert.Contains(t, body, "Analyst")
	assert.NotContains(t, body, "/roles/new")
	assert.NotContains(t, body, "/roles/"+r.ID.String())

	assert.Equal(t, http.StatusForbidden, x.do(t, x.admin, http.MethodGet, "/orgs/acme/roles/new", nil).Code)
	assert.Equal(t, http.StatusForbidden, x.do(t, x.admin, http.MethodGet, "/orgs/acme/roles/"+r.ID.String(), nil).Code)
	rec = x.do(t, x.admin, http.MethodPost, "/orgs/acme/roles", url.Values{"name": {""}, "perm": {"nope"}})
	assert.Equal(t, http.StatusForbidden, rec.Code, "the role check runs before input validation")
	assert.NotContains(t, rec.Body.String(), "role_edit.error_invalid_name")

	rec = x.do(t, x.owner, http.MethodGet, "/orgs/acme/roles", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "roles.read_only")
	assert.Contains(t, rec.Body.String(), "/roles/new")
}

func TestRoleHandler_StaffOrgShowsReasonWithoutControls(t *testing.T) {
	t.Parallel()
	x := newRoleFixture(t)
	store := fakes.NewRole()
	handlers.NewRoleHandler(x.Deps, roleSvc{store: store, staff: true}).Register(x.mux)

	rec := x.do(t, x.owner, http.MethodGet, "/orgs/acme/roles", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "roles.staff_org")
	assert.NotContains(t, body, "/roles/new")
	assert.NotContains(t, body, "roles.read_only")
	assert.NotContains(t, body, "data-custom-roles")
	assert.Contains(t, body, "roles.builtin_heading")
}

func TestRoleHandler_OwnerCreatesWithAnyCataloguePermission(t *testing.T) {
	t.Parallel()
	x := newRoleHandlerFixture(t)

	rec := x.do(t, x.owner, http.MethodPost, "/orgs/acme/roles", url.Values{
		"name": {"Delegate"}, "description": {"Runs roles"}, "perm": {"roles:write", "sheets:read"},
	})
	require.Equal(t, http.StatusSeeOther, rec.Code)
	items, err := x.store.List(context.Background())
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.ElementsMatch(t, []authz.Permission{authz.RolesWrite, authz.SheetsRead}, items[0].Permissions)

	rec = x.do(t, x.owner, http.MethodGet, "/orgs/acme/roles/"+items[0].ID.String(), nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Regexp(t, `name="version" value="1"`, rec.Body.String())
	assert.Regexp(t, `value="roles:write"[^>]*checked`, rec.Body.String())
}

func TestRoleEdit_StaleVersionShowsReload(t *testing.T) {
	t.Parallel()
	x := newRoleHandlerFixture(t)
	r := x.seedRole(t, "Analyst", authz.SheetsRead)
	path := "/orgs/acme/roles/" + r.ID.String()

	require.Equal(t, http.StatusSeeOther, x.do(t, x.owner, http.MethodPost, path, url.Values{
		"name": {"Analyst"}, "perm": {"sheets:read"}, "version": {strconv.FormatInt(r.Version, 10)},
	}).Code)

	rec := x.do(t, x.owner, http.MethodPost, path, url.Values{
		"name": {"Renamed"}, "perm": {"sheets:read"}, "version": {strconv.FormatInt(r.Version, 10)},
	})
	assert.Equal(t, http.StatusConflict, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "role_edit.error_stale")
	assert.Contains(t, body, `href="`+path+`"`, "the stale message links to a fresh copy")
	assert.Contains(t, body, "role_edit.reload")
	got, err := x.store.ByID(context.Background(), r.ID)
	require.NoError(t, err)
	assert.Equal(t, "Analyst", got.Name)
}

type refusedRoles struct{}

func (refusedRoles) List(context.Context) ([]*role.Role, error) {
	return nil, &authz.DeniedError{Permission: authz.MembersWrite, Side: authz.SideRole}
}

func (x *roleHandlerFixture) remountOrg(roles handlers.RoleLister) {
	x.Deps.Roles = roles
	x.Deps.Authz = authz.NewResolver(fakes.OrgRoles{Orgs: x.Orgs, Roles: x.store})
	orgs := org.NewService(x.OrgStore, x.Deps.Caps, discardLogger(), passthroughUnexpected(), fakes.UnitOfWork, org.WithPermissions(x.Deps.Authz))
	x.mux = http.NewServeMux()
	handlers.NewOrgHandler(x.Deps, orgs).Register(x.mux)
}

func (x *roleHandlerFixture) holdCustom(t *testing.T, userID uuid.UUID, r *role.Role) {
	t.Helper()
	require.NoError(t, x.OrgStore.SaveMembership(context.Background(), &org.Membership{
		OrgID: x.org.ID, UserID: userID, Role: org.RoleCustom, RoleID: r.ID, CreatedAt: time.Now().UTC(),
	}))
}

func TestMembers_RoleSelectListsCustomRoles(t *testing.T) {
	t.Parallel()
	x := newRoleHandlerFixture(t)
	analyst := x.seedRole(t, "Analyst", authz.SheetsRead)
	delegate := x.seedRole(t, "Delegate", authz.RolesWrite, authz.MembersWrite)
	x.remountOrg(roleSvc{store: x.store})

	body := x.do(t, x.owner, http.MethodGet, "/orgs/acme/members", nil).Body.String()
	assert.Contains(t, body, `value="member"`)
	assert.Contains(t, body, `<option value="`+analyst.ID.String()+`">Analyst</option>`)
	assert.Contains(t, body, `value="`+delegate.ID.String()+`"`)

	body = x.do(t, x.admin, http.MethodGet, "/orgs/acme/members", nil).Body.String()
	assert.Contains(t, body, `value="`+analyst.ID.String()+`"`)
	assert.NotContains(t, body, `value="`+delegate.ID.String()+`"`, "an admin cannot grant a role holding roles:write")

	x.remountOrg(refusedRoles{})
	body = x.do(t, x.owner, http.MethodGet, "/orgs/acme/members", nil).Body.String()
	assert.Contains(t, body, `value="member"`, "a refused role list falls back to the built-in roles")
	assert.Contains(t, body, `value="admin"`)
	assert.NotContains(t, body, analyst.ID.String())
}

func TestMembers_SetCustomRoleByID(t *testing.T) {
	t.Parallel()
	x := newRoleHandlerFixture(t)
	analyst := x.seedRole(t, "Analyst", authz.SheetsRead)
	x.remountOrg(roleSvc{store: x.store})

	rec := x.do(t, x.owner, http.MethodPost, "/orgs/acme/members/"+x.member.String()+"/role", url.Values{"role": {analyst.ID.String()}})
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	m, err := x.OrgStore.MembershipOf(context.Background(), x.org.ID, x.member)
	require.NoError(t, err)
	assert.Equal(t, org.RoleCustom, m.Role)
	assert.Equal(t, analyst.ID, m.RoleID)
}

func TestMembers_CustomRoleBeyondCallerNotEditable(t *testing.T) {
	t.Parallel()
	x := newRoleHandlerFixture(t)
	delegate := x.seedRole(t, "Delegate", authz.RolesWrite, authz.MembersWrite)
	x.holdCustom(t, x.member, delegate)
	x.remountOrg(roleSvc{store: x.store})
	roleForm := "/members/" + x.member.String() + "/role\""
	removeForm := "/members/" + x.member.String() + "/remove\""

	body := x.do(t, x.admin, http.MethodGet, "/orgs/acme/members", nil).Body.String()
	assert.Contains(t, body, "Delegate", "the row names the custom role")
	assert.NotContains(t, body, roleForm, "an admin cannot change a role holding more than theirs")
	assert.NotContains(t, body, removeForm, "an admin cannot remove a member holding more than theirs")

	body = x.do(t, x.owner, http.MethodGet, "/orgs/acme/members", nil).Body.String()
	assert.Contains(t, body, roleForm)
	assert.Contains(t, body, removeForm)
}

func TestInvites_FormListsGrantableCustomRoles(t *testing.T) {
	t.Parallel()
	x := newRoleHandlerFixture(t)
	analyst := x.seedRole(t, "Analyst", authz.SheetsRead)
	delegate := x.seedRole(t, "Delegate", authz.RolesWrite)
	x.Deps.Roles = roleSvc{store: x.store}
	x.mux = http.NewServeMux()
	handlers.NewInviteHandler(x.Deps, x.Orgs, x.Invites).Register(x.mux)

	body := x.do(t, x.admin, http.MethodGet, "/orgs/acme/invites", nil).Body.String()
	assert.Contains(t, body, `value="member"`)
	assert.Contains(t, body, `<option value="`+analyst.ID.String()+`">Analyst</option>`)
	assert.NotContains(t, body, delegate.ID.String(), "an admin cannot invite into a role holding roles:write")

	body = x.do(t, x.owner, http.MethodGet, "/orgs/acme/invites", nil).Body.String()
	assert.Contains(t, body, delegate.ID.String())
}

func TestAPIKeys_ScopeNotHeldHasItsOwnBanner(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("mint: %w", &apikey.ScopeNotHeldError{Scope: "sheets:write"})
	assert.Equal(t, templates.APIKeyErrorScopeNotHeld, handlers.APIKeyErrorKindOf(err))
}

func TestRoleEdit_SaveBeyondOwnPermissionsIsRefused(t *testing.T) {
	t.Parallel()
	x := newRoleHandlerFixture(t)
	delegate := x.seedRole(t, "Delegate", authz.RolesWrite, authz.MembersRead, authz.SheetsRead)
	analyst := x.seedRole(t, "Analyst", authz.SheetsRead)
	x.holdCustom(t, x.member, delegate)
	x.remountOrg(roleSvc{store: x.store})
	handlers.NewRoleHandler(x.Deps, roleSvc{store: x.store}).Register(x.mux)
	beyond := url.Values{"name": {"Keys"}, "perm": {"sheets:read", "apikeys:write"}}

	rec := x.do(t, x.member, http.MethodPost, "/orgs/acme/roles", beyond)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "roles.error_escalation")

	rec = x.do(t, x.member, http.MethodPost, "/orgs/acme/roles/"+analyst.ID.String(), beyond)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "roles.error_escalation")
	got, err := x.store.ByID(context.Background(), analyst.ID)
	require.NoError(t, err)
	assert.Equal(t, []authz.Permission{authz.SheetsRead}, got.Permissions)

	rec = x.do(t, x.member, http.MethodPost, "/orgs/acme/roles", url.Values{"name": {"Reader"}, "perm": {"sheets:read"}})
	assert.Equal(t, http.StatusSeeOther, rec.Code, "a save within the caller's own set still works")
}

func TestMembersPage_CustomRoleWithoutMembersReadSeesReason(t *testing.T) {
	t.Parallel()
	x := newRoleHandlerFixture(t)
	x.OrgStore.SetUser(x.admin, "admin@example.com", "Admin")
	reader := x.seedRole(t, "Reader", authz.SheetsRead)
	x.holdCustom(t, x.member, reader)
	x.remountOrg(roleSvc{store: x.store})

	rec := x.do(t, x.member, http.MethodGet, "/orgs/acme/members", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "members.view_denied")
	assert.NotContains(t, body, "admin@example.com", "no member is listed")
	assert.NotContains(t, body, "members.empty")

	rec = x.do(t, x.admin, http.MethodGet, "/orgs/acme/members", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "members.view_denied")
}
