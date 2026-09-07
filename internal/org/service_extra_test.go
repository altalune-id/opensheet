package org_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/template/internal/org"
	"altalune.id/template/internal/platform/tenant"
)

func TestBootstrapSingleton_CreatesWhenMissing(t *testing.T) {
	t.Parallel()
	svc, store := newTestService(t, false)
	owner := uuid.New()
	o, err := svc.BootstrapSingleton(context.Background(), "acme", "Acme", owner)
	require.NoError(t, err)
	require.NotNil(t, o)
	assert.True(t, o.System)

	m, err := store.MembershipOf(context.Background(), o.ID, owner)
	require.NoError(t, err)
	assert.True(t, m.System)
	assert.Equal(t, org.RoleOwner, m.Role)
}

func TestBootstrapSingleton_Idempotent(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t, false)
	owner := uuid.New()
	o1, err := svc.BootstrapSingleton(context.Background(), "acme", "Acme", owner)
	require.NoError(t, err)
	o2, err := svc.BootstrapSingleton(context.Background(), "acme", "Acme", owner)
	require.NoError(t, err)
	assert.Equal(t, o1.ID, o2.ID)
}

func TestBootstrapSingleton_PromotesExistingNonSystem(t *testing.T) {
	t.Parallel()
	svc, store := newTestService(t, true)
	owner := uuid.New()
	created, err := svc.Create(context.Background(), org.CreateRequest{Slug: "acme", Name: "Acme", OwnerID: owner})
	require.NoError(t, err)

	assertNotSystem(t, store, created.ID, owner)

	promoted, err := svc.BootstrapSingleton(context.Background(), "acme", "Acme", owner)
	require.NoError(t, err)
	assert.True(t, promoted.System)

	m, err := store.MembershipOf(context.Background(), promoted.ID, owner)
	require.NoError(t, err)
	assert.True(t, m.System)
}

func TestBySlug_HappyPath(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t, true)
	owner := uuid.New()
	created, err := svc.Create(context.Background(), org.CreateRequest{Slug: "acme", Name: "Acme", OwnerID: owner})
	require.NoError(t, err)

	got, err := svc.BySlug(context.Background(), "acme")
	require.NoError(t, err)
	assert.Equal(t, created.ID, got.ID)
}

func TestBySlug_NotFound(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t, true)
	_, err := svc.BySlug(context.Background(), "ghost")
	require.Error(t, err)
	assert.True(t, org.IsNotFoundError(err))
}

func TestByID_HappyPath(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t, true)
	owner := uuid.New()
	created, err := svc.Create(context.Background(), org.CreateRequest{Slug: "acme", Name: "Acme", OwnerID: owner})
	require.NoError(t, err)

	got, err := svc.ByID(context.Background(), created.ID)
	require.NoError(t, err)
	assert.Equal(t, created.Slug, got.Slug)
}

func TestByID_NotFound(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t, true)
	_, err := svc.ByID(context.Background(), uuid.New())
	require.Error(t, err)
	assert.True(t, org.IsNotFoundError(err))
}

func TestMembershipOf_MissingReturnsTypedError(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t, true)
	_, err := svc.MembershipOf(context.Background(), uuid.New(), uuid.New())
	require.Error(t, err)
	assert.True(t, org.IsMembershipMissingError(err))
}

func TestRename_ProtectedSystem(t *testing.T) {
	t.Parallel()
	svc, store := newTestService(t, true)
	owner := uuid.New()
	o, err := svc.BootstrapSingleton(context.Background(), "sys", "System", owner)
	require.NoError(t, err)
	_ = store

	_, err = svc.Rename(context.Background(), o.ID, "Renamed")
	require.Error(t, err)
	assert.True(t, org.IsSystemProtectedError(err))
}

func TestRemoveMember_ProtectedSystem(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t, true)
	owner := uuid.New()
	o, err := svc.BootstrapSingleton(context.Background(), "sys", "System", owner)
	require.NoError(t, err)

	// NOTE: the actor comes from the tenant scope, the way every transport supplies it.
	ctx := tenant.Into(context.Background(), tenant.Context{OrgID: o.ID, UserID: owner})
	err = svc.RemoveMember(ctx, o.ID, owner)
	require.Error(t, err)
	assert.True(t, org.IsSystemProtectedError(err))
}

func TestRemoveMember_HappyPath(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t, true)
	owner := uuid.New()
	o, err := svc.Create(context.Background(), org.CreateRequest{Slug: "acme", Name: "Acme", OwnerID: owner})
	require.NoError(t, err)

	newbie := uuid.New()
	_, err = svc.AddMember(context.Background(), o.ID, newbie, org.RoleMember)
	require.NoError(t, err)

	ctx := tenant.Into(context.Background(), tenant.Context{OrgID: o.ID, UserID: owner})
	require.NoError(t, svc.RemoveMember(ctx, o.ID, newbie))

	_, err = svc.MembershipOf(context.Background(), o.ID, newbie)
	assert.True(t, org.IsMembershipMissingError(err))
}

func TestRemoveMember_NotFound(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t, true)
	ctx := tenant.Into(context.Background(), tenant.Context{OrgID: uuid.New(), UserID: uuid.New()})
	err := svc.RemoveMember(ctx, uuid.New(), uuid.New())
	require.Error(t, err)
	assert.True(t, org.IsMembershipMissingError(err))
}

func TestListMembers_ReturnsRows(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t, true)
	owner := uuid.New()
	o, err := svc.Create(context.Background(), org.CreateRequest{Slug: "acme", Name: "Acme", OwnerID: owner})
	require.NoError(t, err)
	_, err = svc.AddMember(context.Background(), o.ID, uuid.New(), org.RoleMember)
	require.NoError(t, err)

	ms, err := svc.ListMembers(context.Background(), o.ID)
	require.NoError(t, err)
	assert.Len(t, ms, 2)
}

func TestList_ReturnsUserOrgs(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t, true)
	owner := uuid.New()
	_, err := svc.Create(context.Background(), org.CreateRequest{Slug: "one-org", Name: "A", OwnerID: owner})
	require.NoError(t, err)
	_, err = svc.Create(context.Background(), org.CreateRequest{Slug: "two-org", Name: "B", OwnerID: owner})
	require.NoError(t, err)

	out, err := svc.List(context.Background(), owner)
	require.NoError(t, err)
	assert.Len(t, out, 2)
}

func TestErrors_MessagesAndPredicates(t *testing.T) {
	t.Parallel()
	nf := &org.NotFoundError{Slug: "x"}
	assert.Contains(t, nf.Error(), "not found")
	assert.True(t, org.IsNotFoundError(nf))

	ae := &org.AlreadyExistsError{Slug: "x"}
	assert.Contains(t, ae.Error(), "already exists")
	assert.True(t, org.IsAlreadyExistsError(ae))

	mm := &org.MembershipMissingError{OrgID: "o", UserID: "u"}
	assert.NotEmpty(t, mm.Error())
	assert.True(t, org.IsMembershipMissingError(mm))

	sp := &org.SystemProtectedError{Op: "rename", OrgID: "o"}
	assert.NotEmpty(t, sp.Error())
	assert.True(t, org.IsSystemProtectedError(sp))
}

func assertNotSystem(t *testing.T, store interface {
	ByID(ctx context.Context, id uuid.UUID) (*org.Org, error)
}, id, owner uuid.UUID) {
	t.Helper()
	o, err := store.ByID(context.Background(), id)
	require.NoError(t, err)
	require.False(t, o.System)
	_ = owner
}
