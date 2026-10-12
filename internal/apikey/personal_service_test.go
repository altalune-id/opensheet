package apikey_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/apikey"
	orgpkg "altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/fakes"
)

type personalFixture struct {
	svc     *apikey.Service
	store   *fakes.APIKey
	members *fakes.Members
	orgID   uuid.UUID
	a, b    uuid.UUID
	alice   uuid.UUID
	bob     uuid.UUID
	admin   uuid.UUID
}

func newPersonalFixture(t *testing.T) *personalFixture {
	t.Helper()
	store := fakes.NewAPIKey()
	members := fakes.NewMembers()
	projects := fakes.NewOrgProjects()
	f := &personalFixture{store: store, members: members, orgID: uuid.New(), a: uuid.New(), b: uuid.New(), alice: uuid.New(), bob: uuid.New(), admin: uuid.New()}
	projects.Add(f.orgID, f.a)
	projects.Add(f.orgID, f.b)
	members.SeatMember(f.orgID, f.alice)
	members.SeatMember(f.orgID, f.bob)
	members.SeatManager(f.orgID, f.admin)
	f.svc = apikey.NewService(store, apikey.Scheme{}, members, projects, slog.New(slog.NewTextHandler(io.Discard, nil)), failingUnexpected(t))
	return f
}

func (f *personalFixture) ctx(t *testing.T, userID uuid.UUID) context.Context {
	return tenant.Into(t.Context(), tenant.Context{OrgID: f.orgID, UserID: userID})
}

func (f *personalFixture) mint(t *testing.T, owner uuid.UUID, grant apikey.ProjectGrant) (*apikey.APIKey, string) {
	t.Helper()
	k, plaintext, err := f.svc.MintPersonal(f.ctx(t, owner), "laptop", []string{authn.ScopeSheetsRead}, grant, in(30*24*time.Hour))
	require.NoError(t, err)
	return k, plaintext
}

func TestAMemberMintsAPersonalToken(t *testing.T) {
	f := newPersonalFixture(t)
	k, _ := f.mint(t, f.alice, apikey.ProjectGrant{ProjectIDs: []uuid.UUID{f.a}})
	assert.Equal(t, apikey.KindPersonal, k.Kind)
	assert.Equal(t, f.alice, k.CreatedBy)
	assert.Equal(t, uuid.Nil, k.ProjectID)

	mine, err := f.svc.ListPersonal(f.ctx(t, f.alice))
	require.NoError(t, err)
	require.Len(t, mine, 1)
	theirs, err := f.svc.ListPersonal(f.ctx(t, f.bob))
	require.NoError(t, err)
	assert.Empty(t, theirs, "a person lists only their own tokens")
	orgKeys, err := f.svc.ListOrg(f.ctx(t, f.admin))
	require.NoError(t, err)
	assert.Empty(t, orgKeys, "a personal token is never listed as an org key")
}

func TestAPersonalTokenNeedsAMemberAndALifetime(t *testing.T) {
	f := newPersonalFixture(t)
	all := apikey.ProjectGrant{All: true}

	_, _, err := f.svc.MintPersonal(f.ctx(t, uuid.New()), "x", nil, all, in(time.Hour))
	assert.True(t, orgpkg.IsMembershipMissingError(err), "a non-member: %v", err)
	_, _, err = f.svc.MintPersonal(f.ctx(t, uuid.Nil), "x", nil, all, in(time.Hour))
	assert.True(t, orgpkg.IsMembershipMissingError(err), "a machine principal: %v", err)
	_, _, err = f.svc.MintPersonal(f.ctx(t, f.alice), "x", nil, all, nil)
	assert.True(t, apikey.IsExpiryRequiredError(err), "no expiry: %v", err)
	_, _, err = f.svc.MintPersonal(f.ctx(t, f.alice), "x", []string{authn.ScopeAPIKeysWrite}, all, in(time.Hour))
	assert.True(t, apikey.IsRetiredScopeError(err), "a retired scope: %v", err)
	_, _, err = f.svc.MintPersonal(f.ctx(t, f.alice), "x", nil, apikey.ProjectGrant{ProjectIDs: []uuid.UUID{uuid.New()}}, in(time.Hour))
	assert.True(t, apikey.IsProjectNotInOrgError(err), "a foreign project: %v", err)
	assert.Empty(t, f.store.All())
}

// SECURITY: only the owner changes a personal token; to everyone else, admins included, it does not exist.
func TestOnlyTheOwnerChangesAPersonalToken(t *testing.T) {
	f := newPersonalFixture(t)
	k, _ := f.mint(t, f.alice, apikey.ProjectGrant{ProjectIDs: []uuid.UUID{f.a}})

	for name, who := range map[string]uuid.UUID{"another member": f.bob, "an admin": f.admin, "a machine": uuid.Nil} {
		t.Run(name, func(t *testing.T) {
			ctx := f.ctx(t, who)
			_, err := f.svc.GrantProjects(ctx, k.ID, []uuid.UUID{f.b})
			assert.True(t, apikey.IsNotFoundError(err), "GrantProjects: %v", err)
			_, err = f.svc.GrantAllProjects(ctx, k.ID)
			assert.True(t, apikey.IsNotFoundError(err), "GrantAllProjects: %v", err)
			assert.True(t, apikey.IsNotFoundError(f.svc.Revoke(ctx, k.ID)), "Revoke")
		})
	}
	stored, err := f.store.ByID(t.Context(), k.ID)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{f.a}, stored.ProjectIDs)
	assert.Nil(t, stored.RevokedAt)

	owner := f.ctx(t, f.alice)
	got, err := f.svc.GrantProjects(owner, k.ID, []uuid.UUID{f.b})
	require.NoError(t, err)
	assert.ElementsMatch(t, []uuid.UUID{f.a, f.b}, got.ProjectIDs)
	require.NoError(t, f.svc.Revoke(owner, k.ID))
}

// SECURITY: a personal token acts for its owner, so it stops the moment the owner leaves the org, and it fails closed when nothing can check.
func TestAPersonalTokenDiesWithItsOwnersMembership(t *testing.T) {
	f := newPersonalFixture(t)
	_, plaintext := f.mint(t, f.alice, apikey.ProjectGrant{All: true})

	auth := apikey.NewAuthenticator(f.store, nil, apikey.Scheme{}, f.members)
	p, err := auth.Authenticate(t.Context(), plaintext)
	require.NoError(t, err)
	assert.True(t, p.ReachesProject(f.orgID, f.b))
	assert.Equal(t, uuid.Nil, p.UserID, "a token is never mistaken for a signed-in person")

	f.members.Remove(f.orgID, f.alice)
	_, err = auth.Authenticate(t.Context(), plaintext)
	assert.True(t, authn.IsUnauthorizedError(err), "a departed owner's token must be refused, got %v", err)

	f.members.SeatMember(f.orgID, f.alice)
	_, err = apikey.NewAuthenticator(f.store, nil, apikey.Scheme{}, nil).Authenticate(t.Context(), plaintext)
	assert.True(t, authn.IsUnauthorizedError(err), "without a membership check a personal token must be refused, got %v", err)
}

// SECURITY: leaving an org revokes the member's personal tokens there for good; re-joining never revives one.
func TestRevokePersonalOfIsPermanentAndScoped(t *testing.T) {
	f := newPersonalFixture(t)
	aliceKey, plaintext := f.mint(t, f.alice, apikey.ProjectGrant{All: true})
	bobKey, _ := f.mint(t, f.bob, apikey.ProjectGrant{All: true})
	orgKey, _, err := f.svc.MintOrg(f.ctx(t, f.admin), "ci", nil, apikey.ProjectGrant{All: true}, in(time.Hour))
	require.NoError(t, err)

	require.NoError(t, f.svc.RevokePersonalOf(t.Context(), f.orgID, f.alice))
	require.NoError(t, f.svc.RevokePersonalOf(t.Context(), f.orgID, f.alice), "revoking twice is harmless")

	for name, tc := range map[string]struct {
		id      uuid.UUID
		revoked bool
	}{"the leaver's token": {aliceKey.ID, true}, "another member's token": {bobKey.ID, false}, "an org key": {orgKey.ID, false}} {
		stored, err := f.store.ByID(t.Context(), tc.id)
		require.NoError(t, err)
		assert.Equal(t, tc.revoked, stored.RevokedAt != nil, name)
	}

	f.members.Remove(f.orgID, f.alice)
	f.members.SeatMember(f.orgID, f.alice)
	_, err = apikey.NewAuthenticator(f.store, nil, apikey.Scheme{}, f.members).Authenticate(t.Context(), plaintext)
	assert.True(t, authn.IsUnauthorizedError(err), "a re-invited member's old token must stay dead, got %v", err)
}

func TestAPersonalTokenWhoseOwnerWasDeletedIsRefused(t *testing.T) {
	f := newPersonalFixture(t)
	k, plaintext := f.mint(t, f.alice, apikey.ProjectGrant{All: true})
	k.CreatedBy = uuid.Nil
	f.store.Seed(k)

	_, err := apikey.NewAuthenticator(f.store, nil, apikey.Scheme{}, f.members).Authenticate(t.Context(), plaintext)
	assert.True(t, authn.IsUnauthorizedError(err), "a token with no owner must be refused, got %v", err)
}

// SECURITY: an org-level scope acts on the org itself, so only an owner or admin may hold it on a personal token — at mint and on every use.
func TestAnOrgLevelScopeNeedsAManagerOwner(t *testing.T) {
	f := newPersonalFixture(t)
	org := []string{authn.ScopeMembersRead}
	all := apikey.ProjectGrant{All: true}

	_, _, err := f.svc.MintPersonal(f.ctx(t, f.alice), "x", org, all, in(time.Hour))
	assert.True(t, orgpkg.IsNotManagerError(err), "a plain member must not mint an org-level scope, got %v", err)

	_, plaintext, err := f.svc.MintPersonal(f.ctx(t, f.admin), "admin", org, all, in(time.Hour))
	require.NoError(t, err)
	auth := apikey.NewAuthenticator(f.store, nil, apikey.Scheme{}, f.members)
	_, err = auth.Authenticate(t.Context(), plaintext)
	require.NoError(t, err)

	f.members.SeatMember(f.orgID, f.admin)
	_, err = auth.Authenticate(t.Context(), plaintext)
	assert.True(t, authn.IsUnauthorizedError(err), "a demoted owner's org-level token must stop working, got %v", err)
}

func TestAProjectKeyRefusesAnOrgLevelScope(t *testing.T) {
	f := newPersonalFixture(t)
	ctx := tenant.Into(t.Context(), tenant.Context{OrgID: f.orgID, ProjectID: f.a, UserID: f.admin})
	_, _, err := f.svc.Mint(ctx, "x", []string{authn.ScopeMembersRead}, nil, in(time.Hour))
	assert.True(t, apikey.IsScopeLevelError(err), "got %v", err)
	_, _, err = f.svc.MintOrg(f.ctx(t, f.admin), "org", []string{authn.ScopeMembersRead}, apikey.ProjectGrant{All: true}, in(time.Hour))
	require.NoError(t, err, "an org key may hold an org-level scope")
}
