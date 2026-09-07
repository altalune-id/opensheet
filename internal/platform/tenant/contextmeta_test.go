package tenant_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"altalune.id/template/internal/platform/tenant"
	"altalune.id/template/reqid"
)

func TestContextMeta_Empty_OnBareCtx(t *testing.T) {
	m := tenant.ContextMeta(context.Background())
	if len(m) != 0 {
		t.Fatalf("want empty map, got %v", m)
	}
}

func TestContextMeta_RequestIDOnly(t *testing.T) {
	ctx := reqid.WithContext(context.Background(), "req-abc")
	m := tenant.ContextMeta(ctx)
	if m["request_id"] != "req-abc" {
		t.Errorf("request_id=%q", m["request_id"])
	}
	if _, ok := m["org_id"]; ok {
		t.Error("org_id must be absent when tenant missing")
	}
}

func TestContextMeta_TenantFieldsWhenPresent(t *testing.T) {
	org := uuid.New()
	proj := uuid.New()
	user := uuid.New()
	ctx := reqid.WithContext(context.Background(), "req-xyz")
	ctx = tenant.Into(ctx, tenant.Context{OrgID: org, ProjectID: proj, UserID: user})

	m := tenant.ContextMeta(ctx)
	if m["request_id"] != "req-xyz" {
		t.Errorf("request_id=%q", m["request_id"])
	}
	if m["org_id"] != org.String() {
		t.Errorf("org_id=%q want %q", m["org_id"], org.String())
	}
	if m["project_id"] != proj.String() {
		t.Errorf("project_id=%q want %q", m["project_id"], proj.String())
	}
	if m["user_id"] != user.String() {
		t.Errorf("user_id=%q want %q", m["user_id"], user.String())
	}
}

func TestContextMeta_OmitsZeroUUIDs(t *testing.T) {
	ctx := tenant.Into(context.Background(), tenant.Context{OrgID: uuid.New()})
	m := tenant.ContextMeta(ctx)
	if _, ok := m["project_id"]; ok {
		t.Error("zero ProjectID must be omitted")
	}
	if _, ok := m["user_id"]; ok {
		t.Error("zero UserID must be omitted")
	}
	if _, ok := m["org_id"]; !ok {
		t.Error("non-zero OrgID must be present")
	}
}
