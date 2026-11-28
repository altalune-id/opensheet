package boot

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/credential"
	"altalune.id/opensheet/internal/invite"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/role"
	"altalune.id/opensheet/internal/sheet"
	"altalune.id/opensheet/internal/spreadsheet"
	"altalune.id/opensheet/internal/testutil/fakes"
)

func seedOrg(t *testing.T, store *fakes.Org, ownerID uuid.UUID) *org.Org {
	t.Helper()
	o, err := org.NewOrg("acme", "Acme Inc", ownerID)
	require.NoError(t, err)
	require.NoError(t, store.Save(context.Background(), o))
	m, err := org.NewMembership(o.ID, ownerID, org.RoleOwner)
	require.NoError(t, err)
	require.NoError(t, store.SaveMembership(context.Background(), m))
	return o
}

func TestAuthzRoleSource_MemberGetsRoleNameAndWholeOrgGrant(t *testing.T) {
	store := fakes.NewOrg()
	owner, member := uuid.New(), uuid.New()
	o := seedOrg(t, store, owner)
	m, err := org.NewMembership(o.ID, member, org.RoleMember)
	require.NoError(t, err)
	require.NoError(t, store.SaveMembership(context.Background(), m))

	role, roleID, name, grant, err := authzRoleSource{orgs: store}.RoleOf(context.Background(), o.ID, member)
	require.NoError(t, err)
	assert.Equal(t, "member", role)
	assert.Equal(t, uuid.Nil, roleID)
	assert.Equal(t, "Acme Inc", name)
	assert.Equal(t, session.Grant{OrgID: o.ID, All: true}, grant)
}

func TestAuthzRoleSource_PassesTheMembershipGrant(t *testing.T) {
	store := fakes.NewOrg()
	owner, member, p1 := uuid.New(), uuid.New(), uuid.New()
	o := seedOrg(t, store, owner)
	m, err := org.NewMembership(o.ID, member, org.RoleMember)
	require.NoError(t, err)
	require.NoError(t, store.SaveMembership(context.Background(), m))
	require.NoError(t, store.SetProjectGrant(context.Background(), o.ID, member, org.ProjectGrant{ProjectIDs: []uuid.UUID{p1}}))

	_, _, _, grant, err := authzRoleSource{orgs: store}.RoleOf(context.Background(), o.ID, member)
	require.NoError(t, err)
	assert.Equal(t, session.Grant{OrgID: o.ID, ProjectIDs: []uuid.UUID{p1}}, grant)
}

func TestAuthzRoleSource_NonMemberHasNoRole(t *testing.T) {
	store := fakes.NewOrg()
	o := seedOrg(t, store, uuid.New())

	role, _, _, _, err := authzRoleSource{orgs: store}.RoleOf(context.Background(), o.ID, uuid.New())
	require.NoError(t, err)
	assert.Empty(t, role)
}

func TestAuthzRoleSource_MissingOrgHasNoRole(t *testing.T) {
	store := fakes.NewOrg()
	user, orgID := uuid.New(), uuid.New()
	m, err := org.NewMembership(orgID, user, org.RoleOwner)
	require.NoError(t, err)
	require.NoError(t, store.SaveMembership(context.Background(), m))

	role, _, _, _, err := authzRoleSource{orgs: store}.RoleOf(context.Background(), orgID, user)
	require.NoError(t, err)
	assert.Empty(t, role)
}

func TestAuthzRoleSource_StoreFailureSurfaces(t *testing.T) {
	store := fakes.NewOrg()
	o := seedOrg(t, store, uuid.New())
	store.MembershipOfErr = errors.New("db down")

	_, _, _, _, err := authzRoleSource{orgs: store}.RoleOf(context.Background(), o.ID, uuid.New())
	assert.ErrorIs(t, err, store.MembershipOfErr)
}

func TestAuthzResolver_BindsOverOrgStore(t *testing.T) {
	store := fakes.NewOrg()
	owner := uuid.New()
	o := seedOrg(t, store, owner)

	ctx, err := newAuthzResolver(store, fakes.NewRole()).Bind(context.Background(), session.Principal{UserID: owner, Source: session.SourceOIDC}, tenant.Context{OrgID: o.ID})
	require.NoError(t, err)
	require.NoError(t, authz.Can(ctx, authz.RolesWrite))

	_, err = newAuthzResolver(store, fakes.NewRole()).Bind(context.Background(), session.Principal{UserID: uuid.New(), Source: session.SourceOIDC}, tenant.Context{OrgID: o.ID})
	assert.True(t, authn.IsUnauthorizedError(err))
}

func TestAssertOpensheetWiring_RequiresAuthzResolver(t *testing.T) {
	s := opensheetServices{
		Credentials:      &credential.Service{},
		Spreadsheets:     &spreadsheet.Service{},
		Sheets:           &sheet.Service{},
		Provision:        &spreadsheet.ProvisionWorkflow{},
		Read:             &sheet.ReadWorkflow{},
		Write:            &sheet.WriteWorkflow{},
		Fix:              &sheet.FixWorkflow{},
		Connect:          &credential.ConnectWorkflow{},
		SnapshotListener: &sheet.SnapshotListener{},
	}
	err := assertOpensheetWiring(s)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "authz")

	s.Authz = newAuthzResolver(fakes.NewOrg(), fakes.NewRole())
	require.NoError(t, assertOpensheetWiring(s))
}

func TestAuthzRoleSource_CustomRoleHoldsItsStoredPermissions(t *testing.T) {
	store, roles := fakes.NewOrg(), fakes.NewRole()
	owner, clerk := uuid.New(), uuid.New()
	o := seedOrg(t, store, owner)
	r, err := role.New(o.ID, "Clerk", "", []authz.Permission{authz.SheetsRead, authz.CachePurge}, time.Now())
	require.NoError(t, err)
	require.NoError(t, roles.Save(context.Background(), r, 0))
	require.NoError(t, store.SaveMembership(context.Background(), &org.Membership{OrgID: o.ID, UserID: clerk, Role: org.RoleCustom, RoleID: r.ID}))

	got, roleID, _, _, err := authzRoleSource{orgs: store, roles: roles}.RoleOf(context.Background(), o.ID, clerk)
	require.NoError(t, err)
	assert.Equal(t, "custom", got)
	assert.Equal(t, r.ID, roleID)

	ctx, err := newAuthzResolver(store, roles).Bind(context.Background(), session.Principal{UserID: clerk, Source: session.SourceOIDC}, tenant.Context{OrgID: o.ID})
	require.NoError(t, err)
	assert.ElementsMatch(t, []authz.Permission{authz.SheetsRead, authz.CachePurge}, authz.Held(ctx))
}

func TestOrgStoreForInvite_KeepsRoleID(t *testing.T) {
	store := fakes.NewOrg()
	orgID, userID, roleID := uuid.New(), uuid.New(), uuid.New()
	shim := orgStoreForInvite{store: store}
	require.NoError(t, shim.SaveMembership(context.Background(), &invite.MembershipRef{OrgID: orgID, UserID: userID, Role: invite.RoleCustom, RoleID: roleID}))

	stored, err := store.MembershipOf(context.Background(), orgID, userID)
	require.NoError(t, err)
	assert.Equal(t, roleID, stored.RoleID)
	got, err := shim.MembershipOf(context.Background(), orgID, userID)
	require.NoError(t, err)
	assert.Equal(t, roleID, got.RoleID)
}
