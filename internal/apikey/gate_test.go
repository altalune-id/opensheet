package apikey_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/fakes"
)

func deniedWith(t *testing.T, err error, want authz.Permission, code string) {
	t.Helper()
	d, ok := errors.AsType[*authz.DeniedError](err)
	require.True(t, ok, "err = %v (%T), want *authz.DeniedError", err, err)
	assert.Equal(t, want, d.Permission)
	assert.Equal(t, code, d.ToAppError().Code())
}

func scopeNotHeld(t *testing.T, err error, scope string) {
	t.Helper()
	e, ok := errors.AsType[*apikey.ScopeNotHeldError](err)
	require.True(t, ok, "err = %v (%T), want *apikey.ScopeNotHeldError", err, err)
	assert.True(t, apikey.IsScopeNotHeldError(err))
	assert.Equal(t, scope, e.Scope)
	ae, ok := apperror.AsAppError(err)
	require.True(t, ok)
	assert.Equal(t, apperror.CodeAPIKeyScopeNotHeld, ae.Code())
}

func withHeld(ctx context.Context, held ...authz.Permission) context.Context {
	p := session.PrincipalFrom(ctx)
	p.Held = nil
	for _, h := range held {
		p.Held = append(p.Held, string(h))
	}
	return session.PrincipalInto(ctx, p)
}

func TestGatedMethods_RefuseEmptyHeldSet(t *testing.T) {
	f := newOrgKeyFixture(t)
	admin := fakes.BindRole(t.Context(), f.admin, "admin")
	orgKey, _, err := f.svc.MintOrg(admin, "ci", nil, apikey.ProjectGrant{ProjectIDs: []uuid.UUID{f.a}}, in(time.Hour))
	require.NoError(t, err)
	projTC := tenant.Context{OrgID: f.orgID, ProjectID: f.a, UserID: f.admin.UserID}
	projKey, _, err := f.svc.Mint(fakes.BindRole(t.Context(), projTC, "admin"), "k", nil, nil, in(time.Hour))
	require.NoError(t, err)

	emptyOrg := fakes.BindRole(t.Context(), f.admin, "")
	emptyProj := fakes.BindRole(t.Context(), projTC, "")
	cases := []struct {
		name string
		want authz.Permission
		code string
		call func() error
	}{
		{"Mint", authz.APIKeysWrite, apperror.CodeOrgManagerRequired, func() error {
			_, _, err := f.svc.Mint(emptyProj, "x", nil, nil, in(time.Hour))
			return err
		}},
		{"MintOrg", authz.APIKeysWrite, apperror.CodeOrgManagerRequired, func() error {
			_, _, err := f.svc.MintOrg(emptyOrg, "x", nil, apikey.ProjectGrant{All: true}, in(time.Hour))
			return err
		}},
		{"Revoke", authz.APIKeysWrite, apperror.CodeOrgManagerRequired, func() error { return f.svc.Revoke(emptyProj, projKey.ID) }},
		{"GrantProjects", authz.APIKeysWrite, apperror.CodeOrgManagerRequired, func() error {
			_, err := f.svc.GrantProjects(emptyOrg, orgKey.ID, []uuid.UUID{f.b})
			return err
		}},
		{"GrantAllProjects", authz.APIKeysWrite, apperror.CodeOrgManagerRequired, func() error {
			_, err := f.svc.GrantAllProjects(emptyOrg, orgKey.ID)
			return err
		}},
		{"List", authz.APIKeysRead, apperror.CodePermissionDenied, func() error {
			_, err := f.svc.List(emptyProj, f.a)
			return err
		}},
		{"ListOrg", authz.APIKeysRead, apperror.CodePermissionDenied, func() error {
			_, err := f.svc.ListOrg(emptyOrg)
			return err
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { deniedWith(t, c.call(), c.want, c.code) })
	}
}

func TestKeyMint_ScopeBeyondHeldRefused(t *testing.T) {
	f := newOrgKeyFixture(t)
	projTC := tenant.Context{OrgID: f.orgID, ProjectID: f.a, UserID: f.admin.UserID}
	ctx := withHeld(fakes.BindRole(t.Context(), projTC, "admin"), authz.APIKeysWrite, authz.SheetsRead)

	_, _, err := f.svc.Mint(ctx, "x", []string{authn.ScopeSheetsWrite}, nil, in(time.Hour))
	scopeNotHeld(t, err, authn.ScopeSheetsWrite)
	_, _, err = f.svc.Mint(ctx, "x", []string{authn.ScopeSheetsRead}, nil, in(time.Hour))
	require.NoError(t, err)
}

func TestPersonalToken_MemberStillMints(t *testing.T) {
	f := newPersonalFixture(t)
	k, _, err := f.svc.MintPersonal(f.ctx(t, f.alice), "laptop",
		[]string{authn.ScopeSheetsRead, authn.ScopeSheetsWrite, authn.ScopeSheetsAdmin, authn.ScopeCredentialsWrite}, apikey.ProjectGrant{All: true}, in(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, f.alice, k.CreatedBy)
}

func TestPersonalToken_ScopeBeyondHeldRefused(t *testing.T) {
	f := newPersonalFixture(t)
	ctx := withHeld(f.ctx(t, f.alice), authz.SheetsRead)

	_, _, err := f.svc.MintPersonal(ctx, "x", []string{authn.ScopeSheetsWrite}, apikey.ProjectGrant{All: true}, in(time.Hour))
	scopeNotHeld(t, err, authn.ScopeSheetsWrite)
	assert.Empty(t, f.store.All())
}

func TestPersonalToken_HeldSetOfAnotherOrgHoldsNothing(t *testing.T) {
	f := newPersonalFixture(t)
	ctx := tenant.WithOrg(f.ctx(t, f.alice), f.orgID)
	p := session.PrincipalFrom(ctx)
	p.HeldOrgID = uuid.New()
	ctx = session.PrincipalInto(ctx, p)

	_, _, err := f.svc.MintPersonal(ctx, "x", []string{authn.ScopeSheetsRead}, apikey.ProjectGrant{All: true}, in(time.Hour))
	scopeNotHeld(t, err, authn.ScopeSheetsRead)
}
