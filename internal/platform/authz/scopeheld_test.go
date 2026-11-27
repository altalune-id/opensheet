package authz_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/fakes"
)

func TestScopeHeld(t *testing.T) {
	member := authz.Builtin("member")
	owner := authz.Builtin("owner")
	cases := []struct {
		name  string
		held  []authz.Permission
		scope string
		want  bool
	}{
		{"member puts sheets:admin through its base permission", member, authn.ScopeSheetsAdmin, true},
		{"member puts members:read", member, authn.ScopeMembersRead, true},
		{"empty held set holds no project scope", nil, authn.ScopeSheetsRead, false},
		{"empty held set holds no org scope", nil, authn.ScopeMembersRead, false},
		{"apikeys:write needs the console permission", member, authn.ScopeAPIKeysWrite, false},
		{"owner puts apikeys:write", owner, authn.ScopeAPIKeysWrite, true},
		{"held set without sheets:write refuses it", []authz.Permission{authz.SheetsRead}, authn.ScopeSheetsWrite, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, authz.ScopeHeld(c.held, c.scope))
		})
	}
}

func TestCanPutScope(t *testing.T) {
	tc := tenant.Context{OrgID: uuid.New(), UserID: uuid.New()}
	member := fakes.BindRole(context.Background(), tc, "member")

	assert.NoError(t, authz.CanPutScope(member, authn.ScopeSheetsAdmin))
	err := authz.CanPutScope(fakes.BindRole(context.Background(), tc, ""), authn.ScopeSheetsWrite)
	d, ok := errors.AsType[*authz.DeniedError](err)
	if assert.True(t, ok, "err = %v", err) {
		assert.Equal(t, authz.SheetsWrite, d.Permission)
		assert.Equal(t, authz.SideRole, d.Side)
	}
	other := tenant.WithOrg(member, uuid.New())
	assert.Error(t, authz.CanPutScope(other, authn.ScopeSheetsRead), "a held set answers only for its own org")
	assert.NoError(t, authz.CanPutScope(authz.AsSystem(context.Background()), authn.ScopeSheetsWrite))
}

func TestScopeHeld_UnknownScopeNotHeld(t *testing.T) {
	assert.False(t, authz.ScopeHeld(authz.Builtin("owner"), "nope:unknown"))
}

func TestCanPutScope_TokenLackingScopeGetsScopeSide(t *testing.T) {
	tc := tenant.Context{OrgID: uuid.New(), UserID: uuid.New()}
	ctx := session.PrincipalInto(tenant.Into(context.Background(), tc), session.Principal{
		UserID:    tc.UserID,
		Source:    session.SourceToken,
		Scopes:    []string{authn.ScopeSheetsRead},
		Held:      []string{string(authz.SheetsRead)},
		HeldOrgID: tc.OrgID,
	})
	d, ok := errors.AsType[*authz.DeniedError](authz.CanPutScope(ctx, authn.ScopeSheetsWrite))
	if assert.True(t, ok) {
		assert.Equal(t, authz.SideScope, d.Side)
	}
}
