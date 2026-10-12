package apikey_test

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/fakes"
)

type orgKeyFixture struct {
	svc      *apikey.Service
	store    *fakes.APIKey
	managers *fakes.Members
	orgID    uuid.UUID
	a, b     uuid.UUID
	admin    tenant.Context
}

func newOrgKeyFixture(t *testing.T) *orgKeyFixture {
	t.Helper()
	store := fakes.NewAPIKey()
	managers := fakes.NewMembers()
	projects := fakes.NewOrgProjects()
	orgID, a, b := uuid.New(), uuid.New(), uuid.New()
	projects.Add(orgID, a)
	projects.Add(orgID, b)
	admin := tenant.Context{OrgID: orgID, UserID: uuid.New()}
	managers.SeatManager(orgID, admin.UserID)
	svc := apikey.NewService(store, apikey.Scheme{}, managers, projects, slog.New(slog.NewTextHandler(io.Discard, nil)), failingUnexpected(t))
	return &orgKeyFixture{svc: svc, store: store, managers: managers, orgID: orgID, a: a, b: b, admin: admin}
}

func TestMintOrgKey(t *testing.T) {
	f := newOrgKeyFixture(t)
	ctx := tenant.Into(t.Context(), f.admin)

	k, plaintext, err := f.svc.MintOrg(ctx, "ci", []string{authn.ScopeSheetsRead}, apikey.ProjectGrant{ProjectIDs: []uuid.UUID{f.a}}, in(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, f.admin.UserID, k.CreatedBy)

	p, err := apikey.NewAuthenticator(f.store, nil, apikey.Scheme{}, fakes.NewMembers()).Authenticate(t.Context(), plaintext)
	require.NoError(t, err)
	assert.True(t, p.ReachesProject(f.orgID, f.a), "the granted project must be reachable")
	assert.False(t, p.ReachesProject(f.orgID, f.b), "an ungranted project must not be")

	orgKeys, err := f.svc.ListOrg(ctx)
	require.NoError(t, err)
	require.Len(t, orgKeys, 1)
	projectKeys, err := f.svc.List(ctx, f.a)
	require.NoError(t, err)
	assert.Empty(t, projectKeys, "an org key is never listed as a project key")
}

// SECURITY: a grant may only name the org's own projects, whatever the caller submits.
func TestMintOrgKeyRefusesAForeignProject(t *testing.T) {
	f := newOrgKeyFixture(t)
	ctx := tenant.Into(t.Context(), f.admin)

	_, _, err := f.svc.MintOrg(ctx, "x", nil, apikey.ProjectGrant{ProjectIDs: []uuid.UUID{f.a, uuid.New()}}, in(time.Hour))
	assert.True(t, apikey.IsProjectNotInOrgError(err), "got %v", err)
	assert.Empty(t, f.store.All())
}

func TestPromoteOrgKey(t *testing.T) {
	f := newOrgKeyFixture(t)
	ctx := tenant.Into(t.Context(), f.admin)
	k, plaintext, err := f.svc.MintOrg(ctx, "ci", []string{authn.ScopeSheetsRead}, apikey.ProjectGrant{ProjectIDs: []uuid.UUID{f.a}}, in(time.Hour))
	require.NoError(t, err)

	_, err = f.svc.GrantProjects(ctx, k.ID, []uuid.UUID{uuid.New()})
	assert.True(t, apikey.IsProjectNotInOrgError(err), "got %v", err)

	got, err := f.svc.GrantProjects(ctx, k.ID, []uuid.UUID{f.b})
	require.NoError(t, err)
	assert.ElementsMatch(t, []uuid.UUID{f.a, f.b}, got.ProjectIDs)

	got, err = f.svc.GrantAllProjects(ctx, k.ID)
	require.NoError(t, err)
	assert.True(t, got.AllProjects)

	p, err := apikey.NewAuthenticator(f.store, nil, apikey.Scheme{}, fakes.NewMembers()).Authenticate(t.Context(), plaintext)
	require.NoError(t, err)
	assert.True(t, p.ReachesProject(f.orgID, uuid.New()), "an all-projects key reaches a project created later")
}

// SECURITY: an org key seen from a project reads as absent, so a project page can never revoke or widen it.
func TestOrgKeyIsAbsentFromAProjectScope(t *testing.T) {
	f := newOrgKeyFixture(t)
	k, _, err := f.svc.MintOrg(tenant.Into(t.Context(), f.admin), "ci", nil, apikey.ProjectGrant{All: true}, in(time.Hour))
	require.NoError(t, err)

	fromProject := tenant.Into(t.Context(), tenant.Context{OrgID: f.orgID, ProjectID: f.a, UserID: f.admin.UserID})
	assert.True(t, apikey.IsNotFoundError(f.svc.Revoke(fromProject, k.ID)))
	_, err = f.svc.GrantAllProjects(fromProject, k.ID)
	assert.True(t, apikey.IsNotFoundError(err))
}

// SECURITY: every write asks the owner/admin gate; a member and a machine principal are refused before anything is stored.
func TestEveryKeyWriteRequiresAManager(t *testing.T) {
	f := newOrgKeyFixture(t)
	k, _, err := f.svc.MintOrg(tenant.Into(t.Context(), f.admin), "ci", nil, apikey.ProjectGrant{ProjectIDs: []uuid.UUID{f.a}}, in(time.Hour))
	require.NoError(t, err)

	for name, tc := range map[string]tenant.Context{
		"member":  {OrgID: f.orgID, UserID: uuid.New()},
		"machine": {OrgID: f.orgID},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := tenant.Into(t.Context(), tc)
			_, _, err := f.svc.MintOrg(ctx, "x", nil, apikey.ProjectGrant{All: true}, in(time.Hour))
			assert.True(t, org.IsNotManagerError(err), "MintOrg: %v", err)
			_, err = f.svc.GrantProjects(ctx, k.ID, []uuid.UUID{f.b})
			assert.True(t, org.IsNotManagerError(err), "GrantProjects: %v", err)
			_, err = f.svc.GrantAllProjects(ctx, k.ID)
			assert.True(t, org.IsNotManagerError(err), "GrantAllProjects: %v", err)
			assert.True(t, org.IsNotManagerError(f.svc.Revoke(ctx, k.ID)), "Revoke")
			projCtx := tenant.Into(t.Context(), tenant.Context{OrgID: f.orgID, ProjectID: f.a, UserID: tc.UserID})
			_, _, err = f.svc.Mint(projCtx, "x", nil, nil, in(time.Hour))
			assert.True(t, org.IsNotManagerError(err), "Mint: %v", err)
		})
	}
	stored, err := f.store.ByID(t.Context(), k.ID)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{f.a}, stored.ProjectIDs, "a refused promotion must leave the grant as it was")
	assert.Nil(t, stored.RevokedAt)
	assert.Len(t, f.store.All(), 1)
}
