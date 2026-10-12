package org_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/tenant"
)

// SECURITY: RequireManager is the one owner/admin gate every surface asks, so each denial must be the same typed error.
func TestRequireManager(t *testing.T) {
	t.Parallel()
	svc, store := newTestService(t, false)
	ctx := context.Background()
	orgID := uuid.New()

	seat := func(role org.Role) uuid.UUID {
		t.Helper()
		userID := uuid.New()
		m, err := org.NewMembership(orgID, userID, role)
		require.NoError(t, err)
		require.NoError(t, store.SaveMembership(ctx, m))
		return userID
	}

	tests := []struct {
		name   string
		userID uuid.UUID
		allow  bool
	}{
		{"an owner manages", seat(org.RoleOwner), true},
		{"an admin manages", seat(org.RoleAdmin), true},
		{"a member does not", seat(org.RoleMember), false},
		{"a non-member does not", uuid.New(), false},
		{"a machine principal with no user does not", uuid.Nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := svc.RequireManager(ctx, orgID, tt.userID)
			if tt.allow {
				require.NoError(t, err)
				return
			}
			require.True(t, org.IsNotManagerError(err), "got %T: %v", err, err)
		})
	}
}

func TestRoleCanManage(t *testing.T) {
	t.Parallel()
	require.True(t, org.RoleOwner.CanManage())
	require.True(t, org.RoleAdmin.CanManage())
	require.False(t, org.RoleMember.CanManage())
	require.False(t, org.Role("").CanManage())
}

func TestRequireMember(t *testing.T) {
	t.Parallel()
	svc, store := newTestService(t, false)
	ctx := context.Background()
	orgID, member := uuid.New(), uuid.New()
	m, err := org.NewMembership(orgID, member, org.RoleMember)
	require.NoError(t, err)
	require.NoError(t, store.SaveMembership(ctx, m))

	require.NoError(t, svc.RequireMember(ctx, orgID, member))
	require.True(t, org.IsMembershipMissingError(svc.RequireMember(ctx, orgID, uuid.New())))
	require.True(t, org.IsMembershipMissingError(svc.RequireMember(ctx, orgID, uuid.Nil)))
}

func TestRemoveMemberRunsItsHooks(t *testing.T) {
	t.Parallel()
	svc, store := newTestService(t, true)
	ctx := context.Background()
	owner, member := uuid.New(), uuid.New()
	o, err := svc.Create(ctx, org.CreateRequest{Slug: "hooks", Name: "Hooks", OwnerID: owner})
	require.NoError(t, err)
	m, err := org.NewMembership(o.ID, member, org.RoleMember)
	require.NoError(t, err)
	require.NoError(t, store.SaveMembership(ctx, m))

	var got [][2]uuid.UUID
	svc.OnMemberRemoved(func(_ context.Context, orgID, userID uuid.UUID) error {
		got = append(got, [2]uuid.UUID{orgID, userID})
		return nil
	})
	actor := tenant.Into(ctx, tenant.Context{OrgID: o.ID, UserID: owner})
	require.NoError(t, svc.RemoveMember(actor, o.ID, member))
	require.Equal(t, [][2]uuid.UUID{{o.ID, member}}, got, "every hook runs once with the removed membership")

	failing := true
	svc.OnMemberRemoved(func(context.Context, uuid.UUID, uuid.UUID) error {
		if failing {
			return errors.New("boom")
		}
		return nil
	})
	m2, err := org.NewMembership(o.ID, member, org.RoleMember)
	require.NoError(t, err)
	require.NoError(t, store.SaveMembership(ctx, m2))
	require.Error(t, svc.RemoveMember(actor, o.ID, member), "a failing hook must not be swallowed")
	_, err = store.MembershipOf(ctx, o.ID, member)
	require.NoError(t, err, "SECURITY: a failed hook must leave the member in place, so a retry can still revoke what they hold")

	failing = false
	require.NoError(t, svc.RemoveMember(actor, o.ID, member), "the retry finishes the removal")
}
