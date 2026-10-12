package spreadsheet_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/spreadsheet"
)

func (f *sqliteFixture) seedOtherOrg(t *testing.T) (tenant.Context, uuid.UUID) {
	t.Helper()
	tc := tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New(), UserID: uuid.New()}
	seedSQLiteTenant(t, f.db, f.prefix, tc)
	return tc, seedSQLiteCredential(t, f.db, f.prefix, tc, "attacker")
}

func (f *sqliteFixture) requireTitle(t *testing.T, id uuid.UUID, want string) {
	t.Helper()
	got, err := f.store.ByID(f.ctx(), id)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if got.Title != want {
		t.Fatalf("org A's spreadsheet reads %q, want %q: another org rewrote it across the tenant boundary", got.Title, want)
	}
}

func requireHijackBlocked(t *testing.T, err error, what string) {
	t.Helper()
	if !spreadsheet.IsNotFoundError(err) {
		t.Fatalf("%s: got %v, want *spreadsheet.NotFoundError", what, err)
	}
}

// TestSQLiteStore_Save_RejectsVerbatimHijack replays org A's row, id included, under org B's scope.
func TestSQLiteStore_Save_RejectsVerbatimHijack(t *testing.T) {
	f := newSQLiteFixture(t)
	victim := f.save(t, goodFileID, "Prices")
	orgB, _ := f.seedOtherOrg(t)

	attack := *victim
	attack.Title = "Hijacked"

	requireHijackBlocked(t, f.store.Save(tenant.Into(context.Background(), orgB), &attack), "verbatim Save")
	f.requireTitle(t, victim.ID, "Prices")
}

// TestSQLiteStore_Save_RejectsReskinnedHijack carries org B's own org, project and credential with org A's row id.
func TestSQLiteStore_Save_RejectsReskinnedHijack(t *testing.T) {
	f := newSQLiteFixture(t)
	victim := f.save(t, goodFileID, "Prices")
	orgB, credB := f.seedOtherOrg(t)

	attack, err := spreadsheet.New(orgB.OrgID, orgB.ProjectID, credB, goodFileID, "Hijacked")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	attack.ID = victim.ID

	requireHijackBlocked(t, f.store.Save(tenant.Into(context.Background(), orgB), attack), "reskinned Save")
	f.requireTitle(t, victim.ID, "Prices")
}

// TestSQLiteStore_Save_UpdatesOwnRow pins that the tenant predicate still lets the owning org update its row.
func TestSQLiteStore_Save_UpdatesOwnRow(t *testing.T) {
	f := newSQLiteFixture(t)
	sp := f.save(t, goodFileID, "Prices")

	sp.Title = "renamed by its owner"
	if err := f.store.Save(f.ctx(), sp); err != nil {
		t.Fatalf("owner Save: %v", err)
	}
	f.requireTitle(t, sp.ID, "renamed by its owner")
}
