package sheet_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"altalune.id/opensheet/gworkspace/gsheet"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/sheet"
)

type otherOrg struct {
	tc            tenant.Context
	spreadsheetID uuid.UUID
}

func (f *fixture) seedOtherOrg(t *testing.T) otherOrg {
	t.Helper()
	userID, orgID := seedUserAndOrg(t, f.db, f.prefix)
	projectID := seedProject(t, f.db, f.prefix, orgID, userID, "attacker")
	credentialID := seedCredential(t, f.db, f.prefix, orgID, projectID, userID, "attacker")
	return otherOrg{
		tc:            tenant.Context{OrgID: orgID, ProjectID: projectID, UserID: userID},
		spreadsheetID: seedSpreadsheet(t, f.db, f.prefix, orgID, projectID, credentialID, "gfile-attacker"),
	}
}

func (f *fixture) seedVictim(t *testing.T) *sheet.Sheet {
	t.Helper()
	sh, err := sheet.New(sheet.NewParams{OrgID: f.orgID, ProjectID: f.projectID, SpreadsheetID: f.spreadsheetID, Tab: "Kamar", Slug: "prices", Visibility: sheet.VisibilityKey})
	if err != nil {
		t.Fatalf("sheet.New: %v", err)
	}
	if err := f.store.Save(f.ctx(), sh); err != nil {
		t.Fatalf("seed Save: %v", err)
	}
	return sh
}

func (f *fixture) requireSlug(t *testing.T, id uuid.UUID, want string) {
	t.Helper()
	got, err := f.store.ByID(f.ctx(), id)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if got.Slug != want {
		t.Fatalf("org A's sheet reads slug %q, want %q: another org rewrote it across the tenant boundary", got.Slug, want)
	}
}

func requireHijackBlocked(t *testing.T, err error, what string) {
	t.Helper()
	if !sheet.IsNotFoundError(err) {
		t.Fatalf("%s: got %v, want *sheet.NotFoundError", what, err)
	}
}

// TestSQLiteStore_Save_RejectsVerbatimHijack replays org A's row, id included, under org B's scope.
func TestSQLiteStore_Save_RejectsVerbatimHijack(t *testing.T) {
	f := newSQLiteFixture(t)
	victim := f.seedVictim(t)
	orgB := f.seedOtherOrg(t)

	attack := *victim
	attack.Slug = "hijacked"

	requireHijackBlocked(t, f.store.Save(tenant.Into(context.Background(), orgB.tc), &attack), "verbatim Save")
	f.requireSlug(t, victim.ID, "prices")
}

// TestSQLiteStore_Save_RejectsReskinnedHijack carries org B's own org, project and spreadsheet with org A's row id.
func TestSQLiteStore_Save_RejectsReskinnedHijack(t *testing.T) {
	f := newSQLiteFixture(t)
	victim := f.seedVictim(t)
	orgB := f.seedOtherOrg(t)

	attack, err := sheet.New(sheet.NewParams{OrgID: orgB.tc.OrgID, ProjectID: orgB.tc.ProjectID, SpreadsheetID: orgB.spreadsheetID, Tab: "Kamar", Slug: "hijacked", Visibility: sheet.VisibilityPublic})
	if err != nil {
		t.Fatalf("sheet.New: %v", err)
	}
	attack.ID = victim.ID

	requireHijackBlocked(t, f.store.Save(tenant.Into(context.Background(), orgB.tc), attack), "reskinned Save")
	f.requireSlug(t, victim.ID, "prices")
}

// TestSQLiteStore_Save_UpdatesOwnRow pins that the tenant predicate still lets the owning org update its row.
func TestSQLiteStore_Save_UpdatesOwnRow(t *testing.T) {
	f := newSQLiteFixture(t)
	sh := f.seedVictim(t)

	sh.Slug = "renamed"
	if err := f.store.Save(f.ctx(), sh); err != nil {
		t.Fatalf("owner Save: %v", err)
	}
	f.requireSlug(t, sh.ID, "renamed")
}

// TestSQLiteRowStore_UpsertRow_RejectsCrossTenantHijack writes org A's sheet and row id under org B's scope.
func TestSQLiteRowStore_UpsertRow_RejectsCrossTenantHijack(t *testing.T) {
	f := newSQLiteFixture(t)
	rows := newSQLiteRowStore(t, f)
	victim := f.seedVictim(t)
	k := sheet.SnapshotKey{SheetID: victim.ID, Tab: victim.Tab}
	if err := rows.UpsertRow(f.ctx(), k, sheet.ProjectedRow{RowID: "a", Data: gsheet.Row{"id": "a", "name": "ada"}}); err != nil {
		t.Fatalf("seed UpsertRow: %v", err)
	}
	orgB := f.seedOtherOrg(t)

	err := rows.UpsertRow(tenant.Into(context.Background(), orgB.tc), k,
		sheet.ProjectedRow{RowID: "a", Data: gsheet.Row{"id": "a", "name": "Hijacked"}})
	requireHijackBlocked(t, err, "cross-tenant UpsertRow")

	got, err := rows.RowByID(f.ctx(), k, "a")
	if err != nil {
		t.Fatalf("RowByID: %v", err)
	}
	if got.Data["name"] != "ada" {
		t.Fatalf("org A's row reads %v, want name=ada: another org rewrote it across the tenant boundary", got.Data)
	}
}
