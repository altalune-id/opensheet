package project_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/fakes"
)

func deniedFor(t *testing.T, err error, want authz.Permission) {
	t.Helper()
	d, ok := errors.AsType[*authz.DeniedError](err)
	if !ok {
		t.Fatalf("err = %v (%T), want *authz.DeniedError", err, err)
	}
	if d.Permission != want {
		t.Fatalf("denied %s, want %s", d.Permission, want)
	}
}

func asRole(orgID uuid.UUID, role string) context.Context {
	return fakes.BindRole(context.Background(), tenant.Context{OrgID: orgID, UserID: uuid.New()}, role)
}

func TestGatedMethods_RefuseEmptyHeldSet(t *testing.T) {
	orgID := uuid.New()
	svc, _ := newTestService(t)
	p, err := svc.Create(tenantCtx(orgID, uuid.New()), orgID, "web", "Web")
	if err != nil {
		t.Fatal(err)
	}
	empty := asRole(orgID, "")

	_, err = svc.Create(empty, orgID, "api", "API")
	deniedFor(t, err, authz.ProjectsWrite)
	_, err = svc.Rename(empty, p.ID, "")
	deniedFor(t, err, authz.ProjectsWrite)
}

func TestProject_MemberCreateIsROL001(t *testing.T) {
	orgID := uuid.New()
	svc, _ := newTestService(t)

	_, err := svc.Create(asRole(orgID, "member"), orgID, "web", "Web")
	deniedFor(t, err, authz.ProjectsWrite)
	d, _ := errors.AsType[*authz.DeniedError](err)
	app := d.ToAppError()
	if app.Code() != "ROL001" {
		t.Errorf("code = %s, want ROL001", app.Code())
	}
	want := "Your role in Acme doesn't allow you to create or rename projects."
	if app.Message() != want || err.Error() != want {
		t.Errorf("message = %q, error = %q, want %q", app.Message(), err.Error(), want)
	}
}

func TestProject_AdminCreates(t *testing.T) {
	orgID := uuid.New()
	svc, _ := newTestService(t)

	if _, err := svc.Create(asRole(orgID, "admin"), orgID, "web", "Web"); err != nil {
		t.Fatalf("admin create: %v", err)
	}
}

func TestProject_CreateInAnotherOrgRefused(t *testing.T) {
	orgID := uuid.New()
	svc, _ := newTestService(t)

	_, err := svc.Create(asRole(orgID, "owner"), uuid.New(), "web", "Web")
	deniedFor(t, err, authz.ProjectsWrite)
}

func TestProjectLookups_WorkBeforeBind(t *testing.T) {
	orgID := uuid.New()
	svc, _ := newTestService(t)
	p, err := svc.Create(tenantCtx(orgID, uuid.New()), orgID, "web", "Web")
	if err != nil {
		t.Fatal(err)
	}
	unbound := tenant.Into(context.Background(), tenant.Context{OrgID: orgID})
	if _, err := svc.BySlug(unbound, orgID, "web"); err != nil {
		t.Errorf("BySlug: %v", err)
	}
	if _, err := svc.ByID(unbound, p.ID); err != nil {
		t.Errorf("ByID: %v", err)
	}
	if _, err := svc.List(unbound, orgID); err != nil {
		t.Errorf("List: %v", err)
	}
}
