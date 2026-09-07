package project_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/platform/tenant"
	"altalune.id/template/internal/project"
	"altalune.id/template/internal/testutil/fakes"
)

func newTestService(t *testing.T) (*project.Service, *fakes.Project) {
	t.Helper()
	store := fakes.NewProject()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	unexpected := func(_ context.Context, msg string, cause error, _ ...any) *apperror.AppError {
		t.Helper()
		t.Errorf("unexpected() called: %s: %v", msg, cause)
		return nil
	}
	return project.NewService(store, log, unexpected), store
}

func tenantCtx(orgID, userID uuid.UUID) context.Context {
	return tenant.Into(context.Background(), tenant.Context{OrgID: orgID, UserID: userID})
}

func TestService_Create_OK(t *testing.T) {
	orgID, userID := uuid.New(), uuid.New()
	svc, _ := newTestService(t)
	ctx := tenantCtx(orgID, userID)

	p, err := svc.Create(ctx, orgID, "web", "Web")
	if err != nil {
		t.Fatal(err)
	}
	if p.Slug != "web" || p.Name != "Web" || p.OrgID != orgID {
		t.Errorf("unexpected project: %+v", p)
	}
}

func TestService_Create_MissingTenant(t *testing.T) {
	svc, _ := newTestService(t)
	_, err := svc.Create(context.Background(), uuid.New(), "web", "Web")
	if !tenant.IsMissingError(err) {
		t.Errorf("want *tenant.MissingError, got %T (%v)", err, err)
	}
}

func TestService_Create_SlugTaken(t *testing.T) {
	orgID, userID := uuid.New(), uuid.New()
	svc, _ := newTestService(t)
	ctx := tenantCtx(orgID, userID)
	if _, err := svc.Create(ctx, orgID, "web", "Web"); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Create(ctx, orgID, "web", "Web 2")
	if !project.IsAlreadyExistsError(err) {
		t.Errorf("want *AlreadyExistsError, got %T (%v)", err, err)
	}
}

func TestService_Create_InvalidSlug(t *testing.T) {
	orgID, userID := uuid.New(), uuid.New()
	svc, _ := newTestService(t)
	ctx := tenantCtx(orgID, userID)
	_, err := svc.Create(ctx, orgID, "BAD SLUG", "Name")
	if !project.IsInvalidSlugError(err) {
		t.Errorf("want *InvalidSlugError, got %T (%v)", err, err)
	}
}

func TestService_Create_InvalidName(t *testing.T) {
	orgID, userID := uuid.New(), uuid.New()
	svc, _ := newTestService(t)
	ctx := tenantCtx(orgID, userID)
	_, err := svc.Create(ctx, orgID, "abc", "   ")
	if !project.IsInvalidNameError(err) {
		t.Errorf("want *InvalidNameError, got %T (%v)", err, err)
	}
}

func TestService_List(t *testing.T) {
	orgID, userID := uuid.New(), uuid.New()
	svc, _ := newTestService(t)
	ctx := tenantCtx(orgID, userID)
	if _, err := svc.Create(ctx, orgID, "web", "Web"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, orgID, "api", "API"); err != nil {
		t.Fatal(err)
	}
	got, err := svc.List(ctx, orgID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("len=%d want 2", len(got))
	}
}

func TestService_List_MissingTenant(t *testing.T) {
	svc, _ := newTestService(t)
	_, err := svc.List(context.Background(), uuid.New())
	if !tenant.IsMissingError(err) {
		t.Errorf("want *tenant.MissingError, got %T", err)
	}
}

func TestService_Rename_OK(t *testing.T) {
	orgID, userID := uuid.New(), uuid.New()
	svc, _ := newTestService(t)
	ctx := tenantCtx(orgID, userID)
	p, err := svc.Create(ctx, orgID, "web", "Old")
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.Rename(ctx, p.ID, "New")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "New" {
		t.Errorf("name=%q want New", got.Name)
	}
	if got.Slug != "web" {
		t.Errorf("slug=%q must not change", got.Slug)
	}
}

func TestService_Rename_SystemProtected(t *testing.T) {
	orgID, userID := uuid.New(), uuid.New()
	svc, store := newTestService(t)
	ctx := tenantCtx(orgID, userID)
	p, err := svc.Create(ctx, orgID, "web", "Old")
	if err != nil {
		t.Fatal(err)
	}
	p.System = true
	if err := store.Save(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Rename(ctx, p.ID, "New"); !project.IsSystemProtectedError(err) {
		t.Fatalf("want *SystemProtectedError, got %T (%v)", err, err)
	}
}

func TestService_BootstrapSystem_CreatesAndPromotes(t *testing.T) {
	orgID, userID := uuid.New(), uuid.New()
	svc, _ := newTestService(t)
	ctx := tenantCtx(orgID, userID)
	p, err := svc.BootstrapSystem(ctx, orgID, "def", "Default")
	if err != nil {
		t.Fatal(err)
	}
	if !p.System {
		t.Error("System = false, want true on fresh bootstrap")
	}

	svc2, store2 := newTestService(t)
	ctx2 := tenantCtx(orgID, userID)
	prior, err := svc2.Create(ctx2, orgID, "def", "Default")
	if err != nil {
		t.Fatal(err)
	}
	if prior.System {
		t.Fatal("precondition: fresh non-system project")
	}
	got, err := svc2.BootstrapSystem(ctx2, orgID, "def", "Default")
	if err != nil {
		t.Fatal(err)
	}
	if !got.System {
		t.Error("BootstrapSystem did not promote existing project to system")
	}
	reload, err := store2.ByID(ctx2, prior.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reload.System {
		t.Error("existing project not persisted as system")
	}
}

func TestService_Rename_NotFound(t *testing.T) {
	orgID, userID := uuid.New(), uuid.New()
	svc, _ := newTestService(t)
	ctx := tenantCtx(orgID, userID)
	_, err := svc.Rename(ctx, uuid.New(), "New")
	if !project.IsNotFoundError(err) {
		t.Errorf("want *NotFoundError, got %T", err)
	}
}

func TestService_Rename_InvalidName(t *testing.T) {
	orgID, userID := uuid.New(), uuid.New()
	svc, _ := newTestService(t)
	ctx := tenantCtx(orgID, userID)
	p, err := svc.Create(ctx, orgID, "web", "Old")
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Rename(ctx, p.ID, "")
	if !project.IsInvalidNameError(err) {
		t.Errorf("want *InvalidNameError, got %T", err)
	}
}

func TestService_ByID(t *testing.T) {
	orgID, userID := uuid.New(), uuid.New()
	svc, _ := newTestService(t)
	ctx := tenantCtx(orgID, userID)
	created, err := svc.Create(ctx, orgID, "web", "Web")
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.ByID(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != created.ID {
		t.Errorf("id mismatch")
	}
	if _, err := svc.ByID(ctx, uuid.New()); !project.IsNotFoundError(err) {
		t.Errorf("want *NotFoundError, got %T", err)
	}
}

func TestService_BySlug(t *testing.T) {
	orgID, userID := uuid.New(), uuid.New()
	svc, _ := newTestService(t)
	ctx := tenantCtx(orgID, userID)
	created, err := svc.Create(ctx, orgID, "web", "Web")
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.BySlug(ctx, orgID, "web")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != created.ID {
		t.Errorf("id mismatch")
	}
	if _, err := svc.BySlug(ctx, orgID, "missing"); !project.IsNotFoundError(err) {
		t.Errorf("want *NotFoundError, got %T", err)
	}
}

func TestService_Unexpected_Wraps(t *testing.T) {
	orgID, userID := uuid.New(), uuid.New()
	sentinel := errors.New("boom")
	store := &erroringStore{err: sentinel}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	var called bool
	unexpected := func(_ context.Context, _ string, cause error, _ ...any) *apperror.AppError {
		called = true
		if !errors.Is(cause, sentinel) {
			t.Errorf("cause=%v want sentinel", cause)
		}
		return apperror.New(apperror.CodeUnexpectedError, "unexpected", 0)
	}
	svc := project.NewService(store, log, unexpected)
	ctx := tenantCtx(orgID, userID)
	if _, err := svc.List(ctx, orgID); err == nil {
		t.Fatal("expected error")
	}
	if !called {
		t.Error("unexpected() was not called")
	}
}

type erroringStore struct{ err error }

func (s *erroringStore) Save(context.Context, *project.Project) error { return s.err }
func (s *erroringStore) ByID(context.Context, uuid.UUID) (*project.Project, error) {
	return nil, s.err
}
func (s *erroringStore) BySlug(context.Context, uuid.UUID, string) (*project.Project, error) {
	return nil, s.err
}
func (s *erroringStore) List(context.Context, uuid.UUID) ([]*project.Project, error) {
	return nil, s.err
}
