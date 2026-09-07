package tenant_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"altalune.id/template/internal/platform/tenant"
)

func TestFrom_Missing_ReturnsMissingError(t *testing.T) {
	_, err := tenant.From(context.Background())
	if err == nil {
		t.Fatal("expected error on bare ctx")
	}
	if !tenant.IsMissingError(err) {
		t.Fatalf("want *MissingError, got %T: %v", err, err)
	}
}

func TestInto_And_From_Roundtrip(t *testing.T) {
	want := tenant.Context{
		OrgID:     uuid.New(),
		ProjectID: uuid.New(),
		UserID:    uuid.New(),
	}
	ctx := tenant.Into(context.Background(), want)
	got, err := tenant.From(ctx)
	if err != nil {
		t.Fatalf("From returned error: %v", err)
	}
	if got != want {
		t.Errorf("got=%+v want=%+v", got, want)
	}
}

func TestFrom_ZeroOrgID_ReturnsUnscopedError(t *testing.T) {
	ctx := tenant.Into(context.Background(), tenant.Context{UserID: uuid.New()})
	_, err := tenant.From(ctx)
	if err == nil {
		t.Fatal("a scope naming no org must not read as usable")
	}
	if !tenant.IsUnscopedError(err) {
		t.Fatalf("want *UnscopedError, got %T: %v", err, err)
	}
	if tenant.IsMissingError(err) {
		t.Error("a present-but-empty scope is distinct from an absent one")
	}
}

func TestFrom_ZeroOrgID_IsRejectedEvenWithProjectAndUser(t *testing.T) {
	ctx := tenant.Into(context.Background(), tenant.Context{
		ProjectID: uuid.New(),
		UserID:    uuid.New(),
	})
	if _, err := tenant.From(ctx); !tenant.IsUnscopedError(err) {
		t.Fatalf("want *UnscopedError, got %T: %v", err, err)
	}
}

func TestWithOrg_KeepsProjectAndUser(t *testing.T) {
	projectID, userID, orgID := uuid.New(), uuid.New(), uuid.New()
	base := tenant.Into(context.Background(), tenant.Context{ProjectID: projectID, UserID: userID})

	got, err := tenant.From(tenant.WithOrg(base, orgID))
	if err != nil {
		t.Fatalf("From returned error: %v", err)
	}
	want := tenant.Context{OrgID: orgID, ProjectID: projectID, UserID: userID}
	if got != want {
		t.Errorf("got=%+v want=%+v", got, want)
	}
}

func TestWithOrg_RepointsAnExistingScope(t *testing.T) {
	userID, from, to := uuid.New(), uuid.New(), uuid.New()
	base := tenant.Into(context.Background(), tenant.Context{OrgID: from, UserID: userID})

	got, err := tenant.From(tenant.WithOrg(base, to))
	if err != nil {
		t.Fatalf("From returned error: %v", err)
	}
	if got.OrgID != to {
		t.Errorf("OrgID=%v want %v", got.OrgID, to)
	}
	if got.UserID != userID {
		t.Errorf("UserID must survive re-pointing, got %v", got.UserID)
	}
}

func TestWithOrg_OnBareContextStillYieldsAUsableScope(t *testing.T) {
	orgID := uuid.New()
	got, err := tenant.From(tenant.WithOrg(context.Background(), orgID))
	if err != nil {
		t.Fatalf("From returned error: %v", err)
	}
	if got.OrgID != orgID {
		t.Errorf("OrgID=%v want %v", got.OrgID, orgID)
	}
}
