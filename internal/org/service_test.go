package org_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/template/gen/go/apperror/v1"
	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/org"
	"altalune.id/template/internal/platform/capabilities"
	"altalune.id/template/internal/platform/tenant"
	"altalune.id/template/internal/testutil/fakes"
)

func newTestService(t *testing.T, orgCreation bool) (*org.Service, *fakes.Org) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := fakes.NewOrg()
	unexpected := func(_ context.Context, msg string, cause error, _ ...any) *apperror.AppError {
		return apperror.New("altempl.unexpected", msg, codes.Internal, &apperrorv1.ErrorDetail{Code: "altempl.unexpected"}).WithCause(cause)
	}
	svc := org.NewService(store, capabilities.Capabilities{OrgCreation: orgCreation}, log, unexpected)
	return svc, store
}

func TestService_Create(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		svc, store := newTestService(t, true)
		owner := uuid.New()
		o, err := svc.Create(context.Background(), org.CreateRequest{Slug: "acme", Name: "Acme", OwnerID: owner})
		require.NoError(t, err)
		require.NotNil(t, o)
		assert.Equal(t, "acme", o.Slug)
		assert.Equal(t, owner, o.OwnerID)
		_, err = store.MembershipOf(context.Background(), o.ID, owner)
		assert.NoError(t, err, "owner membership must exist")
	})

	t.Run("creation disabled", func(t *testing.T) {
		svc, _ := newTestService(t, false)
		_, err := svc.Create(context.Background(), org.CreateRequest{Slug: "acme", Name: "Acme", OwnerID: uuid.New()})
		assert.True(t, org.IsCreationDisabledError(err), "want CreationDisabledError, got %T: %v", err, err)
	})

	t.Run("slug taken", func(t *testing.T) {
		svc, _ := newTestService(t, true)
		owner := uuid.New()
		_, err := svc.Create(context.Background(), org.CreateRequest{Slug: "acme", Name: "Acme", OwnerID: owner})
		require.NoError(t, err)
		_, err = svc.Create(context.Background(), org.CreateRequest{Slug: "acme", Name: "Acme Two", OwnerID: uuid.New()})
		assert.True(t, org.IsAlreadyExistsError(err), "want AlreadyExistsError, got %T: %v", err, err)
	})

	t.Run("invalid slug propagates", func(t *testing.T) {
		svc, _ := newTestService(t, true)
		_, err := svc.Create(context.Background(), org.CreateRequest{Slug: "X", Name: "Acme", OwnerID: uuid.New()})
		assert.True(t, org.IsInvalidSlugError(err), "want InvalidSlugError, got %T: %v", err, err)
	})

	t.Run("invalid name propagates", func(t *testing.T) {
		svc, _ := newTestService(t, true)
		_, err := svc.Create(context.Background(), org.CreateRequest{Slug: "acme", Name: "", OwnerID: uuid.New()})
		assert.True(t, org.IsInvalidNameError(err), "want InvalidNameError, got %T: %v", err, err)
	})
}

func TestService_List(t *testing.T) {
	svc, _ := newTestService(t, true)
	owner := uuid.New()
	other := uuid.New()
	for _, slug := range []string{"acme", "beta"} {
		_, err := svc.Create(context.Background(), org.CreateRequest{Slug: slug, Name: slug, OwnerID: owner})
		require.NoError(t, err)
	}
	list, err := svc.List(context.Background(), owner)
	require.NoError(t, err)
	assert.Len(t, list, 2)

	empty, err := svc.List(context.Background(), other)
	require.NoError(t, err)
	assert.Empty(t, empty)
}

func TestService_Rename(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		svc, _ := newTestService(t, true)
		o, err := svc.Create(context.Background(), org.CreateRequest{Slug: "acme", Name: "Acme", OwnerID: uuid.New()})
		require.NoError(t, err)
		renamed, err := svc.Rename(context.Background(), o.ID, "Acme Corp")
		require.NoError(t, err)
		assert.Equal(t, "Acme Corp", renamed.Name)
	})

	t.Run("not found", func(t *testing.T) {
		svc, _ := newTestService(t, true)
		_, err := svc.Rename(context.Background(), uuid.New(), "Acme")
		assert.True(t, org.IsNotFoundError(err), "want NotFoundError, got %T: %v", err, err)
	})

	t.Run("invalid name", func(t *testing.T) {
		svc, _ := newTestService(t, true)
		o, err := svc.Create(context.Background(), org.CreateRequest{Slug: "acme", Name: "Acme", OwnerID: uuid.New()})
		require.NoError(t, err)
		_, err = svc.Rename(context.Background(), o.ID, "")
		assert.True(t, org.IsInvalidNameError(err), "want InvalidNameError, got %T: %v", err, err)
	})
}

func TestService_Members(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t, true)
	owner := uuid.New()
	o, err := svc.Create(ctx, org.CreateRequest{Slug: "acme", Name: "Acme", OwnerID: owner})
	require.NoError(t, err)
	// NOTE: the actor comes from the tenant scope, the way every transport supplies it.
	ctx = tenant.Into(ctx, tenant.Context{OrgID: o.ID, UserID: owner})

	t.Run("add ok", func(t *testing.T) {
		u := uuid.New()
		m, err := svc.AddMember(ctx, o.ID, u, org.RoleAdmin)
		require.NoError(t, err)
		assert.Equal(t, org.RoleAdmin, m.Role)
	})

	t.Run("add existing", func(t *testing.T) {
		_, err := svc.AddMember(ctx, o.ID, owner, org.RoleOwner)
		assert.True(t, org.IsMembershipExistsError(err), "want MembershipExistsError, got %T: %v", err, err)
	})

	t.Run("add invalid role", func(t *testing.T) {
		_, err := svc.AddMember(ctx, o.ID, uuid.New(), "root")
		assert.True(t, org.IsInvalidRoleError(err), "want InvalidRoleError, got %T: %v", err, err)
	})

	t.Run("list", func(t *testing.T) {
		ms, err := svc.ListMembers(ctx, o.ID)
		require.NoError(t, err)
		assert.NotEmpty(t, ms, "expected at least owner in members")
	})

	t.Run("remove ok", func(t *testing.T) {
		u := uuid.New()
		_, err := svc.AddMember(ctx, o.ID, u, org.RoleMember)
		require.NoError(t, err)
		assert.NoError(t, svc.RemoveMember(ctx, o.ID, u))
	})

	t.Run("remove missing", func(t *testing.T) {
		err := svc.RemoveMember(ctx, o.ID, uuid.New())
		assert.True(t, org.IsMembershipMissingError(err), "want MembershipMissingError, got %T: %v", err, err)
	})
}

func TestService_Rename_SystemProtected(t *testing.T) {
	ctx := context.Background()
	svc, store := newTestService(t, true)
	owner := uuid.New()
	o, err := svc.Create(ctx, org.CreateRequest{Slug: "acme", Name: "Acme", OwnerID: owner})
	require.NoError(t, err)
	o.System = true
	require.NoError(t, store.Save(ctx, o))

	_, err = svc.Rename(ctx, o.ID, "New Name")
	assert.True(t, org.IsSystemProtectedError(err), "want SystemProtectedError, got %T: %v", err, err)
}

func TestService_RemoveMember_SystemProtected(t *testing.T) {
	ctx := context.Background()
	svc, store := newTestService(t, true)
	owner := uuid.New()
	other := uuid.New()
	o, err := svc.Create(ctx, org.CreateRequest{Slug: "acme", Name: "Acme", OwnerID: owner})
	require.NoError(t, err)
	m, err := org.NewMembership(o.ID, other, org.RoleMember)
	require.NoError(t, err)
	m.System = true
	require.NoError(t, store.SaveMembership(ctx, m))

	err = svc.RemoveMember(tenant.Into(ctx, tenant.Context{OrgID: o.ID, UserID: owner}), o.ID, other)
	assert.True(t, org.IsSystemProtectedError(err), "want SystemProtectedError, got %T: %v", err, err)
}

func TestService_ListMemberProfiles(t *testing.T) {
	ctx := context.Background()
	svc, store := newTestService(t, true)
	owner := uuid.New()
	store.SetUser(owner, "owner@example.com", "Owner Name")
	o, err := svc.Create(ctx, org.CreateRequest{Slug: "acme", Name: "Acme", OwnerID: owner})
	require.NoError(t, err)

	got, err := svc.ListMemberProfiles(ctx, o.ID)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "owner@example.com", got[0].Email)
	assert.Equal(t, "Owner Name", got[0].Name)
	assert.Equal(t, org.RoleOwner, got[0].Role)
}

func TestService_Errors_ChainThroughAppError(t *testing.T) {
	err := &org.NotFoundError{ID: "x"}
	ae, ok := apperror.AsAppError(err)
	require.True(t, ok, "AsAppError chain broken")
	assert.Equal(t, apperror.CodeOrgNotFound, ae.Code())
	var zero *org.NotFoundError
	if errors.Is(err, zero) {
		t.Log("errors.Is with nil pointer: ok")
	}
}

type erroringStore struct {
	inner           org.Store
	bySlugErr       error
	saveErr         error
	byIDErr         error
	listErr         error
	memberOfErr     error
	saveMembErr     error
	listMembsErr    error
	listProfilesErr error
	removeErr       error
}

func (e *erroringStore) Save(ctx context.Context, o *org.Org) error {
	if e.saveErr != nil {
		return e.saveErr
	}
	return e.inner.Save(ctx, o)
}
func (e *erroringStore) BySlug(ctx context.Context, s string) (*org.Org, error) {
	if e.bySlugErr != nil {
		return nil, e.bySlugErr
	}
	return e.inner.BySlug(ctx, s)
}
func (e *erroringStore) ByID(ctx context.Context, id uuid.UUID) (*org.Org, error) {
	if e.byIDErr != nil {
		return nil, e.byIDErr
	}
	return e.inner.ByID(ctx, id)
}
func (e *erroringStore) List(ctx context.Context, u uuid.UUID) ([]*org.Org, error) {
	if e.listErr != nil {
		return nil, e.listErr
	}
	return e.inner.List(ctx, u)
}
func (e *erroringStore) SaveMembership(ctx context.Context, m *org.Membership) error {
	if e.saveMembErr != nil {
		return e.saveMembErr
	}
	return e.inner.SaveMembership(ctx, m)
}
func (e *erroringStore) MembershipOf(ctx context.Context, o, u uuid.UUID) (*org.Membership, error) {
	if e.memberOfErr != nil {
		return nil, e.memberOfErr
	}
	return e.inner.MembershipOf(ctx, o, u)
}
func (e *erroringStore) ListMembers(ctx context.Context, o uuid.UUID) ([]*org.Membership, error) {
	if e.listMembsErr != nil {
		return nil, e.listMembsErr
	}
	return e.inner.ListMembers(ctx, o)
}
func (e *erroringStore) ListMemberProfiles(ctx context.Context, o uuid.UUID) ([]*org.MemberProfile, error) {
	if e.listProfilesErr != nil {
		return nil, e.listProfilesErr
	}
	return e.inner.ListMemberProfiles(ctx, o)
}
func (e *erroringStore) RemoveMember(ctx context.Context, o, u uuid.UUID) error {
	if e.removeErr != nil {
		return e.removeErr
	}
	return e.inner.RemoveMember(ctx, o, u)
}

func newServiceWithStore(t *testing.T, store org.Store) *org.Service {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	unexpected := func(_ context.Context, msg string, cause error, _ ...any) *apperror.AppError {
		return apperror.New("altempl.unexpected", msg, codes.Internal, &apperrorv1.ErrorDetail{Code: "altempl.unexpected"}).WithCause(cause)
	}
	return org.NewService(store, capabilities.Capabilities{OrgCreation: true}, log, unexpected)
}

func TestService_UnexpectedPaths(t *testing.T) {
	inner := fakes.NewOrg()
	sentinel := errors.New("boom")

	assertAppError := func(t *testing.T, err error) {
		t.Helper()
		_, ok := apperror.AsAppError(err)
		assert.True(t, ok, "want AppError, got %T: %v", err, err)
	}

	t.Run("Create: BySlug fails", func(t *testing.T) {
		svc := newServiceWithStore(t, &erroringStore{inner: inner, bySlugErr: sentinel})
		_, err := svc.Create(context.Background(), org.CreateRequest{Slug: "acme", Name: "Acme", OwnerID: uuid.New()})
		assertAppError(t, err)
	})

	t.Run("Create: Save fails", func(t *testing.T) {
		svc := newServiceWithStore(t, &erroringStore{inner: inner, saveErr: sentinel})
		_, err := svc.Create(context.Background(), org.CreateRequest{Slug: "unique-a", Name: "A", OwnerID: uuid.New()})
		assertAppError(t, err)
	})

	t.Run("Create: SaveMembership fails", func(t *testing.T) {
		svc := newServiceWithStore(t, &erroringStore{inner: inner, saveMembErr: sentinel})
		_, err := svc.Create(context.Background(), org.CreateRequest{Slug: "unique-b", Name: "B", OwnerID: uuid.New()})
		assertAppError(t, err)
	})

	t.Run("List: store fails", func(t *testing.T) {
		svc := newServiceWithStore(t, &erroringStore{inner: inner, listErr: sentinel})
		_, err := svc.List(context.Background(), uuid.New())
		assertAppError(t, err)
	})

	t.Run("Rename: ByID fails", func(t *testing.T) {
		svc := newServiceWithStore(t, &erroringStore{inner: inner, byIDErr: sentinel})
		_, err := svc.Rename(context.Background(), uuid.New(), "New")
		assertAppError(t, err)
	})

	t.Run("Rename: Save fails", func(t *testing.T) {
		fresh := fakes.NewOrg()
		o, err := org.NewOrg("acme", "Acme", uuid.New())
		require.NoError(t, err)
		require.NoError(t, fresh.Save(context.Background(), o))
		svc := newServiceWithStore(t, &erroringStore{inner: fresh, saveErr: sentinel})
		_, err = svc.Rename(context.Background(), o.ID, "New")
		assertAppError(t, err)
	})

	t.Run("AddMember: MembershipOf unexpected", func(t *testing.T) {
		svc := newServiceWithStore(t, &erroringStore{inner: inner, memberOfErr: sentinel})
		_, err := svc.AddMember(context.Background(), uuid.New(), uuid.New(), org.RoleMember)
		assertAppError(t, err)
	})

	t.Run("AddMember: SaveMembership fails", func(t *testing.T) {
		fresh := fakes.NewOrg()
		svc := newServiceWithStore(t, &erroringStore{inner: fresh, saveMembErr: sentinel})
		_, err := svc.AddMember(context.Background(), uuid.New(), uuid.New(), org.RoleMember)
		assertAppError(t, err)
	})

	t.Run("RemoveMember: store fails", func(t *testing.T) {
		svc := newServiceWithStore(t, &erroringStore{inner: inner, removeErr: sentinel})
		err := svc.RemoveMember(context.Background(), uuid.New(), uuid.New())
		assertAppError(t, err)
	})

	t.Run("ListMembers: store fails", func(t *testing.T) {
		svc := newServiceWithStore(t, &erroringStore{inner: inner, listMembsErr: sentinel})
		_, err := svc.ListMembers(context.Background(), uuid.New())
		assertAppError(t, err)
	})
}
