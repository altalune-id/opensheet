package org_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/org"
)

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
	actor := boundAs(t, o.ID, owner, org.RoleOwner)
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
