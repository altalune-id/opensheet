package credential_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/credential"
	"altalune.id/opensheet/internal/platform/tenant"
)

func (f *sqliteFixture) seedOtherOrg(t *testing.T) tenant.Context {
	t.Helper()
	userID, orgID, projID := seedProject(t, f.sqlDB, f.prefix)
	return tenant.Context{OrgID: orgID, ProjectID: projID, UserID: userID}
}

func (f *sqliteFixture) requireName(t *testing.T, id uuid.UUID, want string) {
	t.Helper()
	got, err := f.store.ByID(f.ctx(), id)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if got.Name != want {
		t.Fatalf("org A's credential reads %q, want %q: another org rewrote it across the tenant boundary", got.Name, want)
	}
}

func requireHijackBlocked(t *testing.T, err error, what string) {
	t.Helper()
	if !credential.IsNotFoundError(err) {
		t.Fatalf("%s: got %v, want *credential.NotFoundError", what, err)
	}
}

// TestSQLiteStore_Save_RejectsVerbatimHijack replays org A's row, id included, under org B's scope.
func TestSQLiteStore_Save_RejectsVerbatimHijack(t *testing.T) {
	f := newSQLiteFixture(t)
	victim := f.newCredential(t, "org A's credential")
	if err := f.store.Save(f.ctx(), victim); err != nil {
		t.Fatalf("seed Save: %v", err)
	}
	orgB := f.seedOtherOrg(t)

	attack := *victim
	attack.Name = "Hijacked"

	requireHijackBlocked(t, f.store.Save(tenant.Into(context.Background(), orgB), &attack), "verbatim Save")
	f.requireName(t, victim.ID, "org A's credential")
}

// TestSQLiteStore_Save_RejectsReskinnedHijack carries org B's own org and project with org A's row id.
func TestSQLiteStore_Save_RejectsReskinnedHijack(t *testing.T) {
	f := newSQLiteFixture(t)
	victim := f.newCredential(t, "org A's credential")
	if err := f.store.Save(f.ctx(), victim); err != nil {
		t.Fatalf("seed Save: %v", err)
	}
	orgB := f.seedOtherOrg(t)

	attack, err := credential.New(uuid.Nil, orgB.OrgID, orgB.ProjectID, orgB.UserID,
		"Hijacked", credential.KindServiceAccount, "evil@x.iam.gserviceaccount.com", []byte{0x01})
	if err != nil {
		t.Fatalf("credential.New: %v", err)
	}
	attack.ID = victim.ID

	requireHijackBlocked(t, f.store.Save(tenant.Into(context.Background(), orgB), attack), "reskinned Save")
	f.requireName(t, victim.ID, "org A's credential")
}

// TestSQLiteStore_Save_UpdatesOwnRow pins that the tenant predicate still lets the owning org update its row.
func TestSQLiteStore_Save_UpdatesOwnRow(t *testing.T) {
	f := newSQLiteFixture(t)
	c := f.newCredential(t, "org A's credential")
	if err := f.store.Save(f.ctx(), c); err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	c.Name = "renamed by its owner"
	if err := f.store.Save(f.ctx(), c); err != nil {
		t.Fatalf("owner Save: %v", err)
	}
	f.requireName(t, c.ID, "renamed by its owner")
}
