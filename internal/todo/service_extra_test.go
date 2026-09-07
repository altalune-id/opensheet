package todo_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/template/internal/platform/tenant"
	"altalune.id/template/internal/testutil/fakes"
	"altalune.id/template/internal/todo"
)

func TestService_ByID_HappyPath(t *testing.T) {
	t.Parallel()
	store := fakes.NewTodo()
	svc, _ := newSvc(t, store)
	ctx, _ := tenantCtx(t)
	created, err := svc.Create(ctx, "milk")
	require.NoError(t, err)
	got, err := svc.ByID(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, got.ID)
}

func TestService_ByID_MissingReturnsNotFound(t *testing.T) {
	t.Parallel()
	svc, _ := newSvc(t, fakes.NewTodo())
	ctx, _ := tenantCtx(t)
	_, err := svc.ByID(ctx, uuid.New())
	require.Error(t, err)
	assert.True(t, todo.IsNotFoundError(err))
}

func TestService_ByID_MissingTenant(t *testing.T) {
	t.Parallel()
	svc, _ := newSvc(t, fakes.NewTodo())
	_, err := svc.ByID(context.Background(), uuid.New())
	require.Error(t, err)
	assert.True(t, tenant.IsMissingError(err))
}

func TestService_ByID_CrossTenantReturnsNotFound(t *testing.T) {
	t.Parallel()
	store := fakes.NewTodo()
	svc, _ := newSvc(t, store)
	ctxA, _ := tenantCtx(t)
	td, err := svc.Create(ctxA, "milk")
	require.NoError(t, err)

	ctxB, _ := tenantCtx(t)
	_, err = svc.ByID(ctxB, td.ID)
	require.Error(t, err)
	assert.True(t, todo.IsNotFoundError(err))
}

func TestService_Delete_NotFoundNoUnexpected(t *testing.T) {
	t.Parallel()
	svc, unex := newSvc(t, fakes.NewTodo())
	ctx, _ := tenantCtx(t)
	err := svc.Delete(ctx, uuid.New())
	require.Error(t, err)
	assert.True(t, todo.IsNotFoundError(err))
	assert.Equal(t, 0, *unex)
}

func TestService_Delete_MissingTenant(t *testing.T) {
	t.Parallel()
	svc, _ := newSvc(t, fakes.NewTodo())
	err := svc.Delete(context.Background(), uuid.New())
	require.Error(t, err)
	assert.True(t, tenant.IsMissingError(err))
}

func TestService_Toggle_MissingTenant(t *testing.T) {
	t.Parallel()
	svc, _ := newSvc(t, fakes.NewTodo())
	_, err := svc.Toggle(context.Background(), uuid.New())
	require.Error(t, err)
	assert.True(t, tenant.IsMissingError(err))
}

func TestService_ClearDone_MissingTenant(t *testing.T) {
	t.Parallel()
	svc, _ := newSvc(t, fakes.NewTodo())
	_, err := svc.ClearDone(context.Background())
	require.Error(t, err)
	assert.True(t, tenant.IsMissingError(err))
}

func TestService_List_MissingTenant(t *testing.T) {
	t.Parallel()
	svc, _ := newSvc(t, fakes.NewTodo())
	_, err := svc.List(context.Background(), todo.ListOpts{})
	require.Error(t, err)
	assert.True(t, tenant.IsMissingError(err))
}

func TestErrors_MessagesAndPredicates(t *testing.T) {
	t.Parallel()
	nf := &todo.NotFoundError{ID: "x"}
	assert.NotEmpty(t, nf.Error())
	assert.True(t, todo.IsNotFoundError(nf))

	it := &todo.InvalidTitleError{Reason: "empty"}
	assert.NotEmpty(t, it.Error())
	assert.True(t, todo.IsInvalidTitleError(it))
}

func TestService_ByID_StoreErrorRoutesUnexpected(t *testing.T) {
	t.Parallel()
	svc, unex := newSvc(t, &brokenByIDStore{})
	ctx, _ := tenantCtx(t)
	_, err := svc.ByID(ctx, uuid.New())
	require.Error(t, err)
	assert.Equal(t, 1, *unex)
}

type brokenByIDStore struct{}

func (b *brokenByIDStore) Save(_ context.Context, _ *todo.Todo) error { return nil }
func (b *brokenByIDStore) ByID(_ context.Context, _ uuid.UUID) (*todo.Todo, error) {
	return nil, errors.New("boom")
}
func (b *brokenByIDStore) List(_ context.Context, _, _ uuid.UUID, _ todo.ListOpts) ([]*todo.Todo, error) {
	return nil, nil
}
func (b *brokenByIDStore) Delete(_ context.Context, _ uuid.UUID) error { return nil }
func (b *brokenByIDStore) ClearDone(_ context.Context, _, _ uuid.UUID) (int, error) {
	return 0, nil
}
func (b *brokenByIDStore) MarkDoneOlderThan(_ context.Context, _ uuid.UUID, _ time.Time, _ int) (int, error) {
	return 0, nil
}
