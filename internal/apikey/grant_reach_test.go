package apikey_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/fakes"
)

func TestAPIKey_PersonalTokenOutsideTheGrantIsROL012(t *testing.T) {
	f := newPersonalFixture(t)
	ctx := f.ctx(t, f.alice)
	p := session.PrincipalFrom(ctx)
	p.Member = session.Grant{OrgID: f.orgID, ProjectIDs: []uuid.UUID{f.a}}
	ctx = session.PrincipalInto(ctx, p)

	_, _, err := f.svc.MintPersonal(ctx, "laptop", []string{authn.ScopeSheetsRead}, apikey.ProjectGrant{ProjectIDs: []uuid.UUID{f.b}}, in(24*time.Hour))
	require.True(t, org.IsProjectGrantEscalationError(err), "a project outside the member's grant, not ProjectNotInOrg: %T %v", err, err)

	_, _, err = f.svc.MintPersonal(ctx, "laptop", []string{authn.ScopeSheetsRead}, apikey.ProjectGrant{ProjectIDs: []uuid.UUID{f.a}}, in(24*time.Hour))
	require.NoError(t, err)

	_, _, err = f.svc.MintPersonal(f.ctx(t, f.bob), "laptop", []string{authn.ScopeSheetsRead}, apikey.ProjectGrant{ProjectIDs: []uuid.UUID{uuid.New()}}, in(24*time.Hour))
	require.True(t, apikey.IsProjectNotInOrgError(err), "an unrestricted member naming a foreign project keeps today's answer: %T %v", err, err)
}

func restrictedAdmin(t *testing.T, f *orgKeyFixture, project uuid.UUID, granted ...uuid.UUID) context.Context {
	t.Helper()
	tc := f.admin
	tc.ProjectID = project
	ctx := fakes.BindRole(t.Context(), tc, "admin")
	p := session.PrincipalFrom(ctx)
	p.Member = session.Grant{OrgID: f.orgID, ProjectIDs: granted}
	return session.PrincipalInto(ctx, p)
}

func TestAPIKey_RestrictedAdminMintsProjectKeyInGrant(t *testing.T) {
	f := newOrgKeyFixture(t)

	k, _, err := f.svc.Mint(restrictedAdmin(t, f, f.a, f.a), "ci", []string{authn.ScopeSheetsRead}, nil, in(time.Hour))
	require.NoError(t, err, "a project key in the admin's grant")

	_, _, err = f.svc.Mint(restrictedAdmin(t, f, f.b, f.a), "ci", []string{authn.ScopeSheetsRead}, nil, in(time.Hour))
	require.True(t, org.IsProjectGrantEscalationError(err), "a project key outside the grant: %T %v", err, err)

	err = f.svc.Revoke(restrictedAdmin(t, f, f.a, f.b), k.ID)
	require.True(t, apikey.IsNotFoundError(err), "a project key outside the grant reads as absent: %T %v", err, err)
	require.NoError(t, f.svc.Revoke(restrictedAdmin(t, f, f.a, f.a), k.ID))
}

func TestAPIKey_RestrictedAdminOrgKeyIsORG011(t *testing.T) {
	f := newOrgKeyFixture(t)
	_, _, err := f.svc.MintOrg(restrictedAdmin(t, f, uuid.Nil, f.a), "ci", []string{authn.ScopeSheetsRead}, apikey.ProjectGrant{ProjectIDs: []uuid.UUID{f.a}}, in(time.Hour))
	require.True(t, authz.IsDeniedError(err), "%T %v", err, err)
	ae, ok := apperror.AsAppError(err)
	require.True(t, ok)
	require.Equal(t, apperror.CodeOrgManagerRequired, ae.Code(), "an apikeys:write refusal keeps ORG011")
}

// SECURITY: owner decision: a restricted member may mint an all-projects personal token, and it still reaches only the member's own grant.
func TestAPIKey_RestrictedMemberAllProjectsTokenReachesOnlyTheGrant(t *testing.T) {
	f := newPersonalFixture(t)
	finance, marketing := f.a, f.b
	ctx := f.ctx(t, f.alice)
	p := session.PrincipalFrom(ctx)
	p.Member = session.Grant{OrgID: f.orgID, ProjectIDs: []uuid.UUID{finance}}
	ctx = session.PrincipalInto(ctx, p)

	_, plaintext, err := f.svc.MintPersonal(ctx, "laptop", []string{authn.ScopeSheetsRead}, apikey.ProjectGrant{All: true}, in(24*time.Hour))
	require.NoError(t, err, "a restricted member may mint an all-projects personal token")

	key, err := apikey.NewAuthenticator(f.store, nil, apikey.Scheme{}).Authenticate(t.Context(), plaintext)
	require.NoError(t, err)
	roles := fakes.NewRoles().Set(f.orgID, f.alice, "member").Restrict(f.orgID, f.alice, finance)
	bound, err := authz.NewResolver(roles).Bind(t.Context(), key, tenant.Context{OrgID: f.orgID})
	require.NoError(t, err)

	require.True(t, authz.ReachesProject(bound, f.orgID, finance), "the token reaches the member's granted project")
	require.False(t, authz.ReachesProject(bound, f.orgID, marketing), "the token never reaches past the member's grant, whatever its own All")
}
