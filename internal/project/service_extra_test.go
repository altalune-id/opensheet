package project_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/template/internal/platform/tenant"
	"altalune.id/template/internal/project"
)

func TestService_BootstrapSystem_CreatesFreshDefault(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	orgID, userID := uuid.New(), uuid.New()
	ctx := tenantCtx(orgID, userID)

	p, err := svc.BootstrapSystem(ctx, orgID, "default", "Default")
	require.NoError(t, err)
	assert.True(t, p.System)
	assert.Equal(t, "default", p.Slug)
}

func TestService_BootstrapSystem_PromotesExisting(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	orgID, userID := uuid.New(), uuid.New()
	ctx := tenantCtx(orgID, userID)

	created, err := svc.Create(ctx, orgID, "default", "Default")
	require.NoError(t, err)
	assert.False(t, created.System)

	promoted, err := svc.BootstrapSystem(ctx, orgID, "default", "Default")
	require.NoError(t, err)
	assert.True(t, promoted.System)
	assert.Equal(t, created.ID, promoted.ID)
}

func TestService_BootstrapSystem_MissingTenant(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	_, err := svc.BootstrapSystem(context.Background(), uuid.New(), "default", "Default")
	require.Error(t, err)
	assert.True(t, tenant.IsMissingError(err))
}

func TestService_BootstrapSystem_InvalidSlug(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	ctx := tenantCtx(uuid.New(), uuid.New())
	_, err := svc.BootstrapSystem(ctx, uuid.New(), "", "X")
	require.Error(t, err)
}

func TestService_Rename_SystemProtected_ViaBootstrap(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	orgID, userID := uuid.New(), uuid.New()
	ctx := tenantCtx(orgID, userID)
	p, err := svc.BootstrapSystem(ctx, orgID, "default", "Default")
	require.NoError(t, err)

	_, err = svc.Rename(ctx, p.ID, "Renamed")
	require.Error(t, err)
	assert.True(t, project.IsSystemProtectedError(err))
}

func TestService_Rename_NotFound_TypedError(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	ctx := tenantCtx(uuid.New(), uuid.New())
	_, err := svc.Rename(ctx, uuid.New(), "X")
	require.Error(t, err)
	assert.True(t, project.IsNotFoundError(err))
}

func TestService_Rename_MissingTenant(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	_, err := svc.Rename(context.Background(), uuid.New(), "X")
	require.Error(t, err)
	assert.True(t, tenant.IsMissingError(err))
}

func TestService_ByID_NotFound(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	ctx := tenantCtx(uuid.New(), uuid.New())
	_, err := svc.ByID(ctx, uuid.New())
	require.Error(t, err)
	assert.True(t, project.IsNotFoundError(err))
}

func TestService_ByID_MissingTenant(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	_, err := svc.ByID(context.Background(), uuid.New())
	require.Error(t, err)
	assert.True(t, tenant.IsMissingError(err))
}

func TestService_BySlug_MissingTenant(t *testing.T) {
	t.Parallel()
	svc, _ := newTestService(t)
	_, err := svc.BySlug(context.Background(), uuid.New(), "default")
	require.Error(t, err)
	assert.True(t, tenant.IsMissingError(err))
}

func TestErrors_SystemProtectedMessage(t *testing.T) {
	t.Parallel()
	e := &project.SystemProtectedError{Op: "rename", ProjectID: "x"}
	assert.NotEmpty(t, e.Error())
	assert.True(t, project.IsSystemProtectedError(e))

	empty := &project.SystemProtectedError{}
	assert.NotEmpty(t, empty.Error())

	var nilErr *project.SystemProtectedError
	assert.NotEmpty(t, nilErr.Error())
}

func TestErrors_ToAppError(t *testing.T) {
	t.Parallel()
	require.NotNil(t, (&project.SystemProtectedError{Op: "rename", ProjectID: "x"}).ToAppError())
	require.NotNil(t, (&project.NotFoundError{ID: "x"}).ToAppError())
	require.NotNil(t, (&project.AlreadyExistsError{Field: "slug", Value: "x"}).ToAppError())
	require.NotNil(t, (&project.InvalidSlugError{Slug: "!!", Reason: "bad"}).ToAppError())
	require.NotNil(t, (&project.InvalidNameError{Reason: "empty"}).ToAppError())
}
