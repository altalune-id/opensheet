package sheet_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/gworkspace/gsheet"
	"altalune.id/opensheet/internal/platform/db"
	sqliteent "altalune.id/opensheet/internal/platform/db/entity/sqlite"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/sheet"
)

func newSQLiteRowStore(t *testing.T, f *fixture) sheet.RowStore {
	t.Helper()
	return sheet.NewRowStore(
		db.DBConfig{Driver: db.DriverSQLite, TablePrefix: f.prefix},
		db.Pool{W: f.db, R: f.db},
		nil,
	)
}

func seedSQLiteSheet(t *testing.T, f *fixture, slug, tab string) *sheet.Sheet {
	t.Helper()
	sh, err := sheet.New(sheet.NewParams{
		OrgID: f.orgID, ProjectID: f.projectID, SpreadsheetID: f.spreadsheetID,
		Tab: tab, Slug: slug, Visibility: sheet.VisibilityKey,
	})
	require.NoError(t, err)
	require.NoError(t, f.store.Save(f.ctx(), sh))
	return sh
}

func bumpSQLiteGeneration(t *testing.T, f *fixture, sheetID uuid.UUID) {
	t.Helper()
	_, err := f.db.Exec(
		"UPDATE "+f.prefix+"sheets SET generation = generation + 1 WHERE id = ?", sheetID.String())
	require.NoError(t, err)
}

func TestSQLiteRowStore_Replace_RoundTripsRowsInIndexOrder(t *testing.T) {
	f := newSQLiteFixture(t)
	store := newSQLiteRowStore(t, f)
	sh := seedSQLiteSheet(t, f, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	rows := []sheet.ProjectedRow{
		{RowID: "b", RowIndex: 2, Data: gsheet.Row{"id": "b", "name": "bob"}},
		{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a", "name": "ada"}},
	}
	ok, err := store.Replace(f.ctx(), k, 0, rows, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)

	got, err := store.ListLive(f.ctx(), k)
	require.NoError(t, err)
	assert.Equal(t, []sheet.ProjectedRow{
		{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a", "name": "ada"}},
		{RowID: "b", RowIndex: 2, Data: gsheet.Row{"id": "b", "name": "bob"}},
	}, got, "ListLive must order by row_index and preserve the interior gap")
}

func TestSQLiteRowStore_Replace_DiscardsWhenGenerationMoved(t *testing.T) {
	f := newSQLiteFixture(t)
	store := newSQLiteRowStore(t, f)
	sh := seedSQLiteSheet(t, f, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	twoRows := []sheet.ProjectedRow{
		{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a"}},
		{RowID: "b", RowIndex: 1, Data: gsheet.Row{"id": "b"}},
	}
	ok, err := store.Replace(f.ctx(), k, 0, twoRows, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)

	bumpSQLiteGeneration(t, f, sh.ID)

	otherRows := []sheet.ProjectedRow{{RowID: "z", RowIndex: 0, Data: gsheet.Row{"id": "z"}}}
	ok, err = store.Replace(f.ctx(), k, 1, otherRows, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.False(t, ok, "a refresh whose generation moved must be discarded, not applied")

	got, err := store.ListLive(f.ctx(), k)
	require.NoError(t, err)
	assert.Equal(t, twoRows, got, "the discarded refresh must not have touched the rows")
}

func TestSQLiteRowStore_Replace_RoundTripsJSON(t *testing.T) {
	f := newSQLiteFixture(t)
	store := newSQLiteRowStore(t, f)
	sh := seedSQLiteSheet(t, f, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	data := gsheet.Row{
		"id":     "a",
		"ampers": "Bed & Breakfast",
		"angle":  "a < b",
		"nested": `{"looks":"like json","n":[1,2]}`,
		"quote":  `he said "hi"`,
	}
	ok, err := store.Replace(f.ctx(), k, 0,
		[]sheet.ProjectedRow{{RowID: "a", Data: data}}, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)

	got, err := store.ListLive(f.ctx(), k)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, data, got[0].Data, "every value must survive the json column byte for byte")
}

func TestSQLiteRowStore_Replace_WritesContractStateAndBumpsGeneration(t *testing.T) {
	f := newSQLiteFixture(t)
	store := newSQLiteRowStore(t, f)
	sh := seedSQLiteSheet(t, f, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	ok, err := store.Replace(f.ctx(), k, 0, nil, sheet.ContractState{OK: false, Reason: "no id column"})
	require.NoError(t, err)
	require.True(t, ok)

	got, err := f.store.ByID(f.ctx(), sh.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 1, got.Generation)
	assert.False(t, got.ContractOK)
	assert.Equal(t, "no id column", got.ContractReason)
	require.NotNil(t, got.ValidatedAt)
}

func TestSQLiteRowStore_UpsertRow_WritesOneRowAndBumpsGeneration(t *testing.T) {
	f := newSQLiteFixture(t)
	store := newSQLiteRowStore(t, f)
	sh := seedSQLiteSheet(t, f, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	require.NoError(t, store.UpsertRow(f.ctx(), k,
		sheet.ProjectedRow{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a", "name": "ada"}}))
	require.NoError(t, store.UpsertRow(f.ctx(), k,
		sheet.ProjectedRow{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a", "name": "grace"}}))

	got, err := store.ListLive(f.ctx(), k)
	require.NoError(t, err)
	assert.Equal(t, []sheet.ProjectedRow{
		{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a", "name": "grace"}},
	}, got, "the second write must update the row, not insert a second one")

	sh2, err := f.store.ByID(f.ctx(), sh.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 2, sh2.Generation, "every upsert bumps the generation")
}

func TestSQLiteRowStore_ListLive_ExcludesTombstones(t *testing.T) {
	f := newSQLiteFixture(t)
	store := newSQLiteRowStore(t, f)
	sh := seedSQLiteSheet(t, f, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	deletedAt := time.Now().UTC()
	ok, err := store.Replace(f.ctx(), k, 0, []sheet.ProjectedRow{
		{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a"}},
		{RowID: "b", RowIndex: 1, Data: gsheet.Row{"id": "b"}, DeletedAt: &deletedAt},
	}, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)

	got, err := store.ListLive(f.ctx(), k)
	require.NoError(t, err)
	assert.Equal(t, []sheet.ProjectedRow{
		{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a"}},
	}, got, "a tombstoned row is not live")
}

func TestSQLiteRowStore_RowByID_ReturnsTheRowWithItsTombstoneState(t *testing.T) {
	f := newSQLiteFixture(t)
	store := newSQLiteRowStore(t, f)
	sh := seedSQLiteSheet(t, f, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	deletedAt := time.Date(2026, 9, 10, 4, 5, 6, 7000, time.UTC)
	ok, err := store.Replace(f.ctx(), k, 0, []sheet.ProjectedRow{
		{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a", "name": "ada"}},
		{RowID: "b", RowIndex: 3, Data: gsheet.Row{"id": "b"}, DeletedAt: &deletedAt},
	}, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)

	live, err := store.RowByID(f.ctx(), k, "a")
	require.NoError(t, err)
	assert.Equal(t, sheet.ProjectedRow{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a", "name": "ada"}}, live)

	tombstoned, err := store.RowByID(f.ctx(), k, "b")
	require.NoError(t, err)
	require.NotNil(t, tombstoned.DeletedAt,
		"a tombstoned row must come back with its tombstone, not as absent")
	assert.Equal(t, deletedAt, *tombstoned.DeletedAt)
	assert.Equal(t, 3, tombstoned.RowIndex)
}

func TestSQLiteRowStore_RowByID_UnknownIDIsNotFound(t *testing.T) {
	f := newSQLiteFixture(t)
	store := newSQLiteRowStore(t, f)
	sh := seedSQLiteSheet(t, f, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	ok, err := store.Replace(f.ctx(), k, 0,
		[]sheet.ProjectedRow{{RowID: "a", Data: gsheet.Row{"id": "a"}}}, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)

	_, err = store.RowByID(f.ctx(), k, "nobody")
	assert.True(t, sheet.IsRowNotFoundError(err), "want RowNotFoundError, got %T: %v", err, err)

	_, err = store.RowByID(f.ctx(), sheet.SnapshotKey{SheetID: sh.ID, Tab: "Other"}, "a")
	assert.True(t, sheet.IsRowNotFoundError(err), "the lookup must be scoped to one tab, got %T: %v", err, err)
}

func TestSQLiteRowStore_ContractOf_ReportsThePersistedState(t *testing.T) {
	f := newSQLiteFixture(t)
	store := newSQLiteRowStore(t, f)
	sh := seedSQLiteSheet(t, f, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	ok, err := store.Replace(f.ctx(), k, 0, nil, sheet.ContractState{OK: true, SoftDelete: true})
	require.NoError(t, err)
	require.True(t, ok)

	got, err := store.ContractOf(f.ctx(), sh.ID)
	require.NoError(t, err)
	assert.Equal(t, sheet.ContractState{OK: true, SoftDelete: true}, got)

	ok, err = store.MarkContract(f.ctx(), sh.ID, 1, sheet.ContractState{Reason: "no id column"})
	require.NoError(t, err)
	require.True(t, ok)

	drifted, err := store.ContractOf(f.ctx(), sh.ID)
	require.NoError(t, err)
	assert.Equal(t, sheet.ContractState{Reason: "no id column"}, drifted,
		"the drift a refresh persisted is what a row read must refuse on")
}

func TestSQLiteRowStore_ContractOf_UnknownSheetIsNotFound(t *testing.T) {
	f := newSQLiteFixture(t)
	store := newSQLiteRowStore(t, f)

	_, err := store.ContractOf(f.ctx(), uuid.Must(uuid.NewV7()))
	assert.True(t, sheet.IsNotFoundError(err), "want NotFoundError, got %T: %v", err, err)
}

func TestSQLiteRowStore_DeletedAtIsStoredAsSortableText(t *testing.T) {
	f := newSQLiteFixture(t)
	store := newSQLiteRowStore(t, f)
	sh := seedSQLiteSheet(t, f, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	deletedAt := time.Date(2026, 9, 10, 4, 5, 6, 7000, time.UTC)
	require.NoError(t, store.UpsertRow(f.ctx(), k,
		sheet.ProjectedRow{RowID: "a", Data: gsheet.Row{"id": "a"}, DeletedAt: &deletedAt}))

	var raw string
	require.NoError(t, f.db.QueryRow(
		"SELECT deleted_at FROM "+f.prefix+"sheet_rows WHERE sheet_id = ? AND row_id = 'a'",
		sh.ID.String()).Scan(&raw))
	assert.Equal(t, sqliteent.SQLiteTime(deletedAt), raw,
		"deleted_at must go through SQLiteTime so text order matches chronological order")
}

func TestSQLiteRowStore_LockSheet_ReturnsTheCurrentGeneration(t *testing.T) {
	f := newSQLiteFixture(t)
	store := newSQLiteRowStore(t, f)
	sh := seedSQLiteSheet(t, f, "prices", "Rates")

	gen, err := store.LockSheet(f.ctx(), sh.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 0, gen)

	bumpSQLiteGeneration(t, f, sh.ID)

	gen, err = store.LockSheet(f.ctx(), sh.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 1, gen)

	_, err = store.LockSheet(f.ctx(), uuid.New())
	assert.True(t, sheet.IsNotFoundError(err), "want NotFoundError, got %T: %v", err, err)
}

func TestSQLiteRowStore_PurgeSheet_DropsEveryTab(t *testing.T) {
	f := newSQLiteFixture(t)
	store := newSQLiteRowStore(t, f)
	sh := seedSQLiteSheet(t, f, "prices", "Rates")
	first := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}
	second := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Extras"}

	ok, err := store.Replace(f.ctx(), first, 0,
		[]sheet.ProjectedRow{{RowID: "a", Data: gsheet.Row{"id": "a"}}}, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = store.Replace(f.ctx(), second, 1,
		[]sheet.ProjectedRow{{RowID: "b", Data: gsheet.Row{"id": "b"}}}, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)

	require.NoError(t, store.PurgeSheet(f.ctx(), sh.ID))

	for _, k := range []sheet.SnapshotKey{first, second} {
		got, lErr := store.ListLive(f.ctx(), k)
		require.NoError(t, lErr)
		assert.Empty(t, got, "PurgeSheet must clear tab %q", k.Tab)
	}
}

func TestSQLiteRowStore_ReplaceIsScopedToOneTab(t *testing.T) {
	f := newSQLiteFixture(t)
	store := newSQLiteRowStore(t, f)
	sh := seedSQLiteSheet(t, f, "prices", "Rates")
	first := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}
	second := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Extras"}

	kept := []sheet.ProjectedRow{{RowID: "a", Data: gsheet.Row{"id": "a"}}}
	ok, err := store.Replace(f.ctx(), first, 0, kept, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = store.Replace(f.ctx(), second, 1,
		[]sheet.ProjectedRow{{RowID: "b", Data: gsheet.Row{"id": "b"}}}, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)

	got, err := store.ListLive(f.ctx(), first)
	require.NoError(t, err)
	assert.Equal(t, kept, got, "replacing one tab must not delete another tab's rows")
}

func TestSQLiteRowStore_RequiresTenantScope(t *testing.T) {
	f := newSQLiteFixture(t)
	store := newSQLiteRowStore(t, f)
	k := sheet.SnapshotKey{SheetID: uuid.New(), Tab: "Rates"}

	_, err := store.LockSheet(t.Context(), k.SheetID)
	assert.True(t, tenant.IsMissingError(err), "LockSheet want MissingError, got %T: %v", err, err)

	_, err = store.Replace(t.Context(), k, 0, nil, sheet.ContractState{OK: true})
	assert.True(t, tenant.IsMissingError(err), "Replace want MissingError, got %T: %v", err, err)

	err = store.UpsertRow(t.Context(), k, sheet.ProjectedRow{RowID: "a"})
	assert.True(t, tenant.IsMissingError(err), "UpsertRow want MissingError, got %T: %v", err, err)

	_, err = store.RowByID(t.Context(), k, "a")
	assert.True(t, tenant.IsMissingError(err), "RowByID want MissingError, got %T: %v", err, err)

	_, err = store.ContractOf(t.Context(), k.SheetID)
	assert.True(t, tenant.IsMissingError(err), "ContractOf want MissingError, got %T: %v", err, err)

	_, err = store.ListLive(t.Context(), k)
	assert.True(t, tenant.IsMissingError(err), "ListLive want MissingError, got %T: %v", err, err)

	_, err = store.Stats(t.Context(), k.SheetID, k.Tab)
	assert.True(t, tenant.IsMissingError(err), "Stats want MissingError, got %T: %v", err, err)

	assert.True(t, tenant.IsMissingError(store.PurgeSheet(t.Context(), k.SheetID)))
}

func TestSQLiteRowStore_RefreshPersistsTheSoftDeleteOptIn(t *testing.T) {
	f := newSQLiteFixture(t)
	store := newSQLiteRowStore(t, f)
	sh := seedSQLiteSheet(t, f, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	rows := []sheet.ProjectedRow{{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a"}}}
	ok, err := store.Replace(f.ctx(), k, 0, rows, sheet.ContractState{OK: true, SoftDelete: true})
	require.NoError(t, err)
	require.True(t, ok)

	got, err := f.store.ByID(f.ctx(), sh.ID)
	require.NoError(t, err)
	assert.True(t, got.SoftDelete,
		"a refresh must persist the opt-in; store.Save drops it, so it has to travel with the contract")

	ok, err = store.MarkContract(f.ctx(), sh.ID, 1, sheet.ContractState{OK: false, Reason: "no id column"})
	require.NoError(t, err)
	require.True(t, ok)

	got, err = f.store.ByID(f.ctx(), sh.ID)
	require.NoError(t, err)
	assert.False(t, got.SoftDelete, "MarkContract writes what the header row said, not what it said last time")
}

func TestSQLiteRowStore_MarkContract_PersistsDriftWithoutTouchingTheRows(t *testing.T) {
	f := newSQLiteFixture(t)
	store := newSQLiteRowStore(t, f)
	sh := seedSQLiteSheet(t, f, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	rows := []sheet.ProjectedRow{{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a"}}}
	ok, err := store.Replace(f.ctx(), k, 0, rows, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = store.MarkContract(f.ctx(), sh.ID, 1, sheet.ContractState{OK: false, Reason: "no id column"})
	require.NoError(t, err)
	require.True(t, ok)

	got, err := f.store.ByID(f.ctx(), sh.ID)
	require.NoError(t, err)
	assert.False(t, got.ContractOK)
	assert.Equal(t, "no id column", got.ContractReason)
	assert.EqualValues(t, 1, got.Generation, "the projected rows did not change, so the generation must not move")
	require.NotNil(t, got.ValidatedAt)

	live, err := store.ListLive(f.ctx(), k)
	require.NoError(t, err)
	assert.Equal(t, rows, live, "MarkContract must leave the projected rows alone")
}

func TestSQLiteRowStore_MarkContract_DiscardsWhenGenerationMoved(t *testing.T) {
	f := newSQLiteFixture(t)
	store := newSQLiteRowStore(t, f)
	sh := seedSQLiteSheet(t, f, "prices", "Rates")

	bumpSQLiteGeneration(t, f, sh.ID)

	ok, err := store.MarkContract(f.ctx(), sh.ID, 0, sheet.ContractState{OK: false, Reason: "a finding from an older fetch"})
	require.NoError(t, err)
	require.False(t, ok, "a finding whose generation moved must not overwrite a newer verdict")

	got, err := f.store.ByID(f.ctx(), sh.ID)
	require.NoError(t, err)
	assert.True(t, got.ContractOK)
	assert.Empty(t, got.ContractReason)
}

func TestSQLiteRowStore_Stats_CountsLiveRowsAndNamesColumns(t *testing.T) {
	f := newSQLiteFixture(t)
	store := newSQLiteRowStore(t, f)
	sh := seedSQLiteSheet(t, f, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	deletedAt := time.Now().UTC()
	ok, err := store.Replace(f.ctx(), k, 0, []sheet.ProjectedRow{
		{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a", "name": "ada"}},
		{RowID: "b", RowIndex: 1, Data: gsheet.Row{"id": "b", "name": "bo"}, DeletedAt: &deletedAt},
		{RowID: "c", RowIndex: 2, Data: gsheet.Row{"id": "c", "name": "cyd"}},
	}, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)

	got, err := store.Stats(f.ctx(), sh.ID, "Rates")
	require.NoError(t, err)
	assert.Equal(t, sheet.TableStats{
		Tab:      "Rates",
		Columns:  []string{"id", "name"},
		RowCount: 2,
	}, got, "Stats counts live rows only")
}

func TestSQLiteRowStore_Stats_IsScopedToOneTab(t *testing.T) {
	f := newSQLiteFixture(t)
	store := newSQLiteRowStore(t, f)
	sh := seedSQLiteSheet(t, f, "prices", "Rates")

	// NOTE: sequential with explicit generations — ranging a map made the expected generation depend on iteration order.
	for _, step := range []struct {
		tab  string
		gen  int64
		rows []sheet.ProjectedRow
	}{
		{"Rates", 0, []sheet.ProjectedRow{
			{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a"}},
		}},
		{"Renamed", 1, []sheet.ProjectedRow{
			{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a"}},
			{RowID: "b", RowIndex: 1, Data: gsheet.Row{"id": "b"}},
		}},
	} {
		ok, err := store.Replace(f.ctx(), sheet.SnapshotKey{SheetID: sh.ID, Tab: step.tab},
			step.gen, step.rows, sheet.ContractState{OK: true})
		require.NoError(t, err)
		require.True(t, ok, "Replace(%q, gen=%d) must apply", step.tab, step.gen)
	}

	got, err := store.Stats(f.ctx(), sh.ID, "Rates")
	require.NoError(t, err)
	assert.EqualValues(t, 1, got.RowCount, "a renamed tab's orphaned rows are not this tab's")
}

// A sheet naming no tab means "the first tab", whose name only Google knows, so the projection answers for the tab it holds.
func TestSQLiteRowStore_Stats_UnnamedTabAnswersForTheProjectedTab(t *testing.T) {
	f := newSQLiteFixture(t)
	store := newSQLiteRowStore(t, f)
	sh := seedSQLiteSheet(t, f, "prices", "")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "First"}

	ok, err := store.Replace(f.ctx(), k, 0, []sheet.ProjectedRow{
		{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a"}},
	}, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)

	got, err := store.Stats(f.ctx(), sh.ID, "")
	require.NoError(t, err)
	assert.Equal(t, "First", got.Tab)
	assert.EqualValues(t, 1, got.RowCount)
}

func TestSQLiteRowStore_Stats_UnprojectedSheetCountsNothing(t *testing.T) {
	f := newSQLiteFixture(t)
	store := newSQLiteRowStore(t, f)
	sh := seedSQLiteSheet(t, f, "prices", "Rates")

	got, err := store.Stats(f.ctx(), sh.ID, "Rates")
	require.NoError(t, err)
	assert.Equal(t, sheet.TableStats{Tab: "Rates"}, got, "nothing projected is not an error")
}
