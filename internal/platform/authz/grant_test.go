package authz_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/fakes"
)

func TestBind_CarriesTheMemberGrant(t *testing.T) {
	orgID, user, p1 := uuid.New(), uuid.New(), uuid.New()
	src := fakes.NewRoles().Set(orgID, user, "member").Restrict(orgID, user, p1)

	ctx := bindIn(t, src, orgID, session.Principal{UserID: user, Source: session.SourceOIDC})
	assert.Equal(t, session.Grant{OrgID: orgID, ProjectIDs: []uuid.UUID{p1}}, session.PrincipalFrom(ctx).Member)
}

func TestBind_OwnerAndCreatorlessKeyReachEveryProject(t *testing.T) {
	orgID, owner, p1 := uuid.New(), uuid.New(), uuid.New()
	src := fakes.NewRoles().Set(orgID, owner, "owner").Restrict(orgID, owner, p1)

	ctx := bindIn(t, src, orgID, session.Principal{UserID: owner, Source: session.SourceOIDC})
	assert.Equal(t, session.Grant{OrgID: orgID, All: true}, session.PrincipalFrom(ctx).Member, "an owner always reaches every project")

	key := session.Principal{Source: session.SourceAPIKey, ActiveOrgID: orgID, AllProjects: true, Scopes: []string{"sheets:read"}}
	ctx = bindIn(t, src, orgID, key)
	assert.Equal(t, session.Grant{OrgID: orgID, All: true}, session.PrincipalFrom(ctx).Member, "a key with no recorded creator keeps its own grant only")
}

func TestBind_KeyCarriesItsCreatorsGrant(t *testing.T) {
	orgID, creator, p1, p2 := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	src := fakes.NewRoles().Set(orgID, creator, "admin").Restrict(orgID, creator, p1)
	key := session.Principal{Source: session.SourceAPIKey, ActiveOrgID: orgID, KeyOwnerID: creator, AllProjects: true, Scopes: []string{"sheets:read"}}

	p := session.PrincipalFrom(bindIn(t, src, orgID, key))
	assert.Equal(t, session.Grant{OrgID: orgID, ProjectIDs: []uuid.UUID{p1}}, p.Member)
	assert.True(t, p.ReachesProject(orgID, p1))
	assert.False(t, p.ReachesProject(orgID, p2), "an all-projects key stops at its creator's grant")
}

func TestCanOrgWide_RestrictedAdminDenied(t *testing.T) {
	orgID, admin, p1 := uuid.New(), uuid.New(), uuid.New()
	src := fakes.NewRoles().Set(orgID, admin, "admin")
	ctx := bindIn(t, src, orgID, session.Principal{UserID: admin, Source: session.SourceOIDC})
	require.NoError(t, authz.CanOrgWide(ctx, authz.MembersWrite), "an unrestricted admin acts org-wide")

	src.Restrict(orgID, admin, p1)
	ctx = bindIn(t, src, orgID, session.Principal{UserID: admin, Source: session.SourceOIDC})
	require.NoError(t, authz.Can(ctx, authz.MembersWrite), "the role still holds the permission")
	err := authz.CanOrgWide(ctx, authz.MembersWrite)
	assert.True(t, authz.IsDeniedError(err), "%T %v", err, err)

	assert.NoError(t, authz.CanOrgWide(authz.AsSystem(context.Background()), authz.MembersWrite))
}

func TestCanOrgWide_KeyNeedsAllProjects(t *testing.T) {
	orgID, creator, p1 := uuid.New(), uuid.New(), uuid.New()
	src := fakes.NewRoles().Set(orgID, creator, "admin")
	scopes := []string{"sheets:read"}

	all := session.Principal{Source: session.SourceAPIKey, ActiveOrgID: orgID, KeyOwnerID: creator, AllProjects: true, Scopes: scopes}
	require.NoError(t, authz.CanOrgWide(bindIn(t, src, orgID, all), authz.SheetsRead))

	selected := all
	selected.AllProjects, selected.ProjectIDs = false, []uuid.UUID{p1}
	err := authz.CanOrgWide(bindIn(t, src, orgID, selected), authz.SheetsRead)
	assert.True(t, authz.IsDeniedError(err), "%T %v", err, err)
}

func TestReach_RestrictedCustomRoleHolderGrantOnly(t *testing.T) {
	orgID, user, roleID, p1, p2 := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	src := fakes.NewRoles().SetCustom(orgID, user, roleID, authz.SheetsWrite, authz.MembersWrite).Restrict(orgID, user, p1)

	ctx := bindIn(t, src, orgID, session.Principal{UserID: user, Source: session.SourceOIDC})
	p := session.PrincipalFrom(ctx)
	assert.True(t, p.ReachesProject(orgID, p1))
	assert.False(t, p.ReachesProject(orgID, p2))
	require.NoError(t, authz.Can(ctx, authz.SheetsWrite))
	assert.True(t, authz.IsDeniedError(authz.CanOrgWide(ctx, authz.MembersWrite)), "a restricted holder loses the role's org-level permissions")
}

func TestReachesProject_SystemUnboundAndBound(t *testing.T) {
	orgID, user, p1, p2 := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	assert.True(t, authz.ReachesProject(authz.AsSystem(context.Background()), orgID, p1), "the system reaches every project")
	assert.False(t, authz.ReachesProject(context.Background(), orgID, p1), "nothing bound reaches nothing")

	unbound := session.PrincipalInto(tenant.Into(context.Background(), tenant.Context{OrgID: orgID, UserID: user}),
		session.Principal{UserID: user, Source: session.SourceOIDC, ActiveOrgID: orgID, Member: session.Grant{OrgID: orgID, All: true}})
	assert.False(t, authz.ReachesProject(unbound, orgID, p1), "a principal Bind never saw reaches nothing")

	src := fakes.NewRoles().Set(orgID, user, "member").Restrict(orgID, user, p1)
	ctx := bindIn(t, src, orgID, session.Principal{UserID: user, Source: session.SourceOIDC})
	assert.True(t, authz.ReachesProject(ctx, orgID, p1))
	assert.False(t, authz.ReachesProject(ctx, orgID, p2))
}
