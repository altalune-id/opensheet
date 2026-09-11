package sheet_test

import (
	"strconv"
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

func TestSQLiteRowStore_StateOf_ReportsThePersistedState(t *testing.T) {
	f := newSQLiteFixture(t)
	store := newSQLiteRowStore(t, f)
	sh := seedSQLiteSheet(t, f, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	ok, err := store.Replace(f.ctx(), k, 0, nil, sheet.ContractState{OK: true, SoftDelete: true})
	require.NoError(t, err)
	require.True(t, ok)

	got, err := store.StateOf(f.ctx(), sh.ID)
	require.NoError(t, err)
	assert.Equal(t, sheet.ContractState{OK: true, SoftDelete: true}, got.Contract)
	assert.NotEmpty(t, got.Digest, "a refresh must persist the digest of the rows it wrote")
	assert.EqualValues(t, 1, got.Generation)

	ok, err = store.MarkContract(f.ctx(), sh.ID, 1, sheet.ContractState{Reason: "no id column"})
	require.NoError(t, err)
	require.True(t, ok)

	drifted, err := store.StateOf(f.ctx(), sh.ID)
	require.NoError(t, err)
	assert.Equal(t, sheet.ContractState{Reason: "no id column"}, drifted.Contract,
		"the drift a refresh persisted is what a row read must refuse on")
	assert.Equal(t, got.Digest, drifted.Digest, "marking drift must leave the digest alone")
	assert.Equal(t, got.Generation, drifted.Generation)
}

func TestSQLiteRowStore_StateOf_UnknownSheetIsNotFound(t *testing.T) {
	f := newSQLiteFixture(t)
	store := newSQLiteRowStore(t, f)

	_, err := store.StateOf(f.ctx(), uuid.Must(uuid.NewV7()))
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

	_, err = store.StateOf(t.Context(), k.SheetID)
	assert.True(t, tenant.IsMissingError(err), "StateOf want MissingError, got %T: %v", err, err)

	_, err = store.ListLive(t.Context(), k)
	assert.True(t, tenant.IsMissingError(err), "ListLive want MissingError, got %T: %v", err, err)

	_, err = store.Query(t.Context(), k, sheet.RowQuery{})
	assert.True(t, tenant.IsMissingError(err), "Query want MissingError, got %T: %v", err, err)

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

func refreshedRows() []sheet.ProjectedRow {
	return []sheet.ProjectedRow{
		{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a", "name": "ada"}},
		{RowID: "b", RowIndex: 2, Data: gsheet.Row{"id": "b", "name": "bob"}},
	}
}

// TestSQLiteRowStore_Refresh_DoesNotBumpTheGenerationWhenNothingChanged is why the bump is conditional: a 1000-page keyset walk spans several TTL windows, and a bump per window makes it uncompletable.
func TestSQLiteRowStore_Refresh_DoesNotBumpTheGenerationWhenNothingChanged(t *testing.T) {
	f := newSQLiteFixture(t)
	store := newSQLiteRowStore(t, f)
	sh := seedSQLiteSheet(t, f, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	ok, err := store.Replace(f.ctx(), k, 0, refreshedRows(), sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)
	first, err := store.StateOf(f.ctx(), sh.ID)
	require.NoError(t, err)
	require.EqualValues(t, 1, first.Generation)
	require.NotEmpty(t, first.Digest)
	firstSheet, err := f.store.ByID(f.ctx(), sh.ID)
	require.NoError(t, err)
	require.NotNil(t, firstSheet.ValidatedAt)

	ok, err = store.Replace(f.ctx(), k, first.Generation, refreshedRows(), sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)

	second, err := store.StateOf(f.ctx(), sh.ID)
	require.NoError(t, err)
	assert.Equal(t, first.Generation, second.Generation,
		"a refresh that refetched identical rows must not move the generation")
	assert.Equal(t, first.Digest, second.Digest)

	secondSheet, err := f.store.ByID(f.ctx(), sh.ID)
	require.NoError(t, err)
	require.NotNil(t, secondSheet.ValidatedAt)
	assert.True(t, secondSheet.ValidatedAt.After(*firstSheet.ValidatedAt),
		"the freshness gate still has to advance, or every page refetches from Google")
}

func TestSQLiteRowStore_Refresh_BumpsWhenACellChanged(t *testing.T) {
	f := newSQLiteFixture(t)
	store := newSQLiteRowStore(t, f)
	sh := seedSQLiteSheet(t, f, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	ok, err := store.Replace(f.ctx(), k, 0, refreshedRows(), sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)
	first, err := store.StateOf(f.ctx(), sh.ID)
	require.NoError(t, err)

	edited := refreshedRows()
	edited[1].Data = gsheet.Row{"id": "b", "name": "Bob"}
	ok, err = store.Replace(f.ctx(), k, first.Generation, edited, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)

	second, err := store.StateOf(f.ctx(), sh.ID)
	require.NoError(t, err)
	assert.EqualValues(t, first.Generation+1, second.Generation)
	assert.NotEqual(t, first.Digest, second.Digest)
}

func TestSQLiteRowStore_Refresh_BumpsWhenARowIsTombstoned(t *testing.T) {
	f := newSQLiteFixture(t)
	store := newSQLiteRowStore(t, f)
	sh := seedSQLiteSheet(t, f, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	ok, err := store.Replace(f.ctx(), k, 0, refreshedRows(), sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)
	first, err := store.StateOf(f.ctx(), sh.ID)
	require.NoError(t, err)

	deletedAt := time.Date(2026, 9, 11, 4, 5, 6, 0, time.UTC)
	tombstoned := refreshedRows()
	tombstoned[1].DeletedAt = &deletedAt
	ok, err = store.Replace(f.ctx(), k, first.Generation, tombstoned, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)

	second, err := store.StateOf(f.ctx(), sh.ID)
	require.NoError(t, err)
	assert.EqualValues(t, first.Generation+1, second.Generation,
		"a tombstone changes no row_id, no row_index and no cell, so only a tombstone-aware digest moves here")
	assert.NotEqual(t, first.Digest, second.Digest)
}

// TestSQLiteRowStore_Replace_StaleRefreshCannotResurrectATombstonedRow is the interleaving a tombstone-blind digest breaks: the earlier of two refreshes at one generation would apply last and undo the delete.
func TestSQLiteRowStore_Replace_StaleRefreshCannotResurrectATombstonedRow(t *testing.T) {
	f := newSQLiteFixture(t)
	store := newSQLiteRowStore(t, f)
	sh := seedSQLiteSheet(t, f, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	ok, err := store.Replace(f.ctx(), k, 0, refreshedRows(), sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)
	settled, err := store.StateOf(f.ctx(), sh.ID)
	require.NoError(t, err)

	deletedAt := time.Date(2026, 9, 11, 4, 5, 6, 0, time.UTC)
	tombstoned := refreshedRows()
	tombstoned[1].DeletedAt = &deletedAt

	ok, err = store.Replace(f.ctx(), k, settled.Generation, tombstoned, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok, "the refresh that saw the tombstone applies first")

	ok, err = store.Replace(f.ctx(), k, settled.Generation, refreshedRows(), sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.False(t, ok, "the refresh whose fetch predates the tombstone must be discarded")

	live, err := store.ListLive(f.ctx(), k)
	require.NoError(t, err)
	assert.Equal(t, []sheet.ProjectedRow{
		{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a", "name": "ada"}},
	}, live, "the deleted row must stay deleted")
}

func TestSQLiteRowStore_UpsertRow_ClearsTheDigestSoOutstandingCursorsAreRefused(t *testing.T) {
	f := newSQLiteFixture(t)
	store := newSQLiteRowStore(t, f)
	sh := seedSQLiteSheet(t, f, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	ok, err := store.Replace(f.ctx(), k, 0,
		[]sheet.ProjectedRow{{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a"}}},
		sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)
	settled, err := store.StateOf(f.ctx(), sh.ID)
	require.NoError(t, err)
	require.NotEmpty(t, settled.Digest, "a refresh must leave a digest")

	require.NoError(t, store.UpsertRow(f.ctx(), k,
		sheet.ProjectedRow{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a", "name": "ada"}}))

	after, err := store.StateOf(f.ctx(), sh.ID)
	require.NoError(t, err)
	assert.Empty(t, after.Digest,
		"a write must clear the digest, or a cursor issued before it still matches and the walk resumes across a mutation")
	assert.Greater(t, after.Generation, settled.Generation, "a write always bumps")
}

// queryFixtureRows is the shared filter fixture both drivers seed: interior row_index gaps, one tombstone, an empty cell, a missing key, and values that become wildcards if a LIKE pattern is not escaped.
func queryFixtureRows(deletedAt *time.Time) []sheet.ProjectedRow {
	return []sheet.ProjectedRow{
		{RowID: "r1", RowIndex: 0, Data: gsheet.Row{
			"id": "r1", "status": "open", "owner": "ada", "note": "alpha", "qty": "10"}},
		{RowID: "r2", RowIndex: 2, Data: gsheet.Row{
			"id": "r2", "status": "closed", "owner": "bob", "note": "", "qty": "03"}},
		{RowID: "r3", RowIndex: 3, Data: gsheet.Row{
			"id": "r3", "status": "open", "owner": "cyd", "note": "100% s", "qty": "05"}, DeletedAt: deletedAt},
		{RowID: "r4", RowIndex: 5, Data: gsheet.Row{
			"id": "r4", "status": "open", "owner": "dee", "qty": "20"}},
		{RowID: "r5", RowIndex: 6, Data: gsheet.Row{
			"id": "r5", "status": "held", "owner": "eve", "note": "_wild", "qty": "02"}},
		{RowID: "r6", RowIndex: 8, Data: gsheet.Row{
			"id": "r6", "status": "open", "owner": "fay", "note": "100% s", "qty": "09"}},
		{RowID: "r7", RowIndex: 9, Data: gsheet.Row{
			"id": "r7", "status": "open", "owner": "gus", "note": "10023", "qty": "07"}},
	}
}

func queryFixtureColumns() []string {
	return []string{"id", "note", "owner", "qty", "status"}
}

func hostileFixtureColumns() []string {
	return []string{`a'; DROP TABLE opensheet_sheet_rows; --`, "id", `q"uote`, "weird.dotted"}
}

func hostileFixtureRows() []sheet.ProjectedRow {
	return []sheet.ProjectedRow{
		{RowID: "h1", RowIndex: 0, Data: gsheet.Row{
			"id": "h1", `a'; DROP TABLE opensheet_sheet_rows; --`: "boom", "weird.dotted": "dot", `q"uote`: "quoted"}},
		{RowID: "h2", RowIndex: 1, Data: gsheet.Row{
			"id": "h2", `a'; DROP TABLE opensheet_sheet_rows; --`: "safe", "weird.dotted": "other", `q"uote`: "else"}},
	}
}

func queryOf(t *testing.T, columns []string, limit int, where ...string) sheet.RowQuery {
	t.Helper()
	clauses, err := sheet.ParseRowClauses(where, columns, "Rates")
	require.NoError(t, err)
	return sheet.RowQuery{Clauses: clauses, Window: sheet.RowWindow{Limit: limit}}
}

func rowIDsOf(rows []sheet.ProjectedRow) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.RowID)
	}
	return out
}

type queryCase struct {
	name  string
	where []string
	want  []string
}

// queryCases is every operator against queryFixtureRows, asserted by row id in row_index order.
func queryCases() []queryCase {
	return []queryCase{
		{"eq", []string{"status:eq:open"}, []string{"r1", "r4", "r6", "r7"}},
		{"eq misses", []string{"status:eq:nobody"}, []string{}},
		{"ne", []string{"status:ne:open"}, []string{"r2", "r5"}},
		{"gt", []string{"owner:gt:cyd"}, []string{"r4", "r5", "r6", "r7"}},
		{"gte", []string{"owner:gte:dee"}, []string{"r4", "r5", "r6", "r7"}},
		{"lt", []string{"owner:lt:bob"}, []string{"r1"}},
		{"lte", []string{"owner:lte:bob"}, []string{"r1", "r2"}},
		{"gte on zero-padded numbers sorts numerically", []string{"qty:gte:09"}, []string{"r1", "r4", "r6"}},
		{"contains", []string{"owner:contains:d"}, []string{"r1", "r4"}},
		{"starts", []string{"owner:starts:a"}, []string{"r1"}},
		{"starts is case-insensitive for ascii", []string{"owner:starts:A"}, []string{"r1"}},
		{"in", []string{"status:in:open,held"}, []string{"r1", "r4", "r5", "r6", "r7"}},
		{"in with one item", []string{"status:in:held"}, []string{"r5"}},
		{"empty matches an empty string and a missing key", []string{"note:empty"}, []string{"r2", "r4"}},
		{"present is the negation of empty", []string{"note:present"}, []string{"r1", "r5", "r6", "r7"}},
		{"contains treats % literally", []string{"note:contains:100%"}, []string{"r6"}},
		{"starts treats % literally", []string{"note:starts:100%"}, []string{"r6"}},
		{"contains treats _ literally", []string{"note:contains:_w"}, []string{"r5"}},
		{"an unescaped _ would match alpha", []string{"note:contains:_l"}, []string{}},
		{"clauses AND", []string{"status:eq:open", "owner:gte:fay"}, []string{"r6", "r7"}},
		{"a tombstoned row is never returned", []string{"id:eq:r3"}, []string{}},
	}
}

func seedSQLiteQueryFixture(t *testing.T, f *fixture, rows []sheet.ProjectedRow) (sheet.RowStore, sheet.SnapshotKey) {
	t.Helper()
	store := newSQLiteRowStore(t, f)
	sh := seedSQLiteSheet(t, f, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}
	ok, err := store.Replace(f.ctx(), k, 0, rows, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)
	return store, k
}

func TestSQLiteRowStore_Query_EveryOperator(t *testing.T) {
	f := newSQLiteFixture(t)
	deletedAt := time.Date(2026, 9, 11, 4, 5, 6, 0, time.UTC)
	store, k := seedSQLiteQueryFixture(t, f, queryFixtureRows(&deletedAt))

	for _, tc := range queryCases() {
		t.Run(tc.name, func(t *testing.T) {
			page, err := store.Query(f.ctx(), k, queryOf(t, queryFixtureColumns(), 100, tc.where...))
			require.NoError(t, err)
			assert.Equal(t, tc.want, rowIDsOf(page.Rows), "?where=%v", tc.where)
			assert.False(t, page.More, "a page under the limit has nothing following it")
		})
	}
}

// TestSQLiteRowStore_Query_KeysetVisitsEveryRowExactlyOnce is the assertion this method exists for: the fixture has an interior row_index gap and an interior tombstone, which a naive OFFSET or a row_index+1 cursor both get wrong.
func TestSQLiteRowStore_Query_KeysetVisitsEveryRowExactlyOnce(t *testing.T) {
	f := newSQLiteFixture(t)
	deletedAt := time.Date(2026, 9, 11, 4, 5, 6, 0, time.UTC)
	store, k := seedSQLiteQueryFixture(t, f, queryFixtureRows(&deletedAt))

	live, err := store.ListLive(f.ctx(), k)
	require.NoError(t, err)
	require.Len(t, live, 6)

	walked := make([]string, 0, len(live))
	var cursor *sheet.RowCursor
	for pages := 0; ; pages++ {
		require.Less(t, pages, 20, "the walk must terminate")
		page, qErr := store.Query(f.ctx(), k, sheet.RowQuery{
			Window: sheet.RowWindow{Limit: 2, Cursor: cursor},
		})
		require.NoError(t, qErr)
		require.LessOrEqual(t, len(page.Rows), 2, "the limit+1 over-fetch must not leak into Rows")
		walked = append(walked, rowIDsOf(page.Rows)...)
		if !page.More {
			break
		}
		require.NotEmpty(t, page.Rows, "More cannot be true on an empty page")
		cursor = &sheet.RowCursor{Digest: "d", RowIndex: page.Rows[len(page.Rows)-1].RowIndex}
	}

	assert.Equal(t, rowIDsOf(live), walked,
		"the keyset walk must visit every live row exactly once, in row_index order")
}

func TestSQLiteRowStore_Query_MoreReportsWhetherAPageFollows(t *testing.T) {
	f := newSQLiteFixture(t)
	deletedAt := time.Date(2026, 9, 11, 4, 5, 6, 0, time.UTC)
	store, k := seedSQLiteQueryFixture(t, f, queryFixtureRows(&deletedAt))

	for _, tc := range []struct {
		limit    int
		wantRows int
		wantMore bool
	}{
		{2, 2, true},
		{5, 5, true},
		{6, 6, false},
		{7, 6, false},
		{0, 6, false},
	} {
		page, err := store.Query(f.ctx(), k, sheet.RowQuery{Window: sheet.RowWindow{Limit: tc.limit}})
		require.NoError(t, err)
		assert.Len(t, page.Rows, tc.wantRows, "limit=%d", tc.limit)
		assert.Equal(t, tc.wantMore, page.More,
			"limit=%d: More must distinguish a full last page from a full page with more behind it", tc.limit)
	}
}

func TestSQLiteRowStore_Query_AHostileColumnNameIsInert(t *testing.T) {
	f := newSQLiteFixture(t)
	store, k := seedSQLiteQueryFixture(t, f, hostileFixtureRows())

	for _, tc := range []struct {
		column string
		value  string
		want   []string
	}{
		{`a'; DROP TABLE opensheet_sheet_rows; --`, "boom", []string{"h1"}},
		{"weird.dotted", "other", []string{"h2"}},
		{`q"uote`, "quoted", []string{"h1"}},
	} {
		q := queryOf(t, hostileFixtureColumns(), 10, tc.column+":eq:"+tc.value)
		page, err := store.Query(f.ctx(), k, q)
		require.NoError(t, err, "column %q must bind as a parameter, not as SQL or a JSON path fragment", tc.column)
		assert.Equal(t, tc.want, rowIDsOf(page.Rows), "column %q", tc.column)
	}

	var count int
	require.NoError(t, f.db.QueryRow("SELECT count(*) FROM "+f.prefix+"sheet_rows").Scan(&count))
	assert.Equal(t, 2, count, "the projection table must still exist with its rows")

	still, err := store.ListLive(f.ctx(), k)
	require.NoError(t, err)
	assert.Len(t, still, 2)
}

// NOTE: sqlite has no RLS, so this is what holds the explicit org_id predicate in place; on postgres FORCE ROW LEVEL SECURITY is the backstop and no test can see the predicate go missing.
func TestSQLiteRowStore_Query_IsScopedToOneOrg(t *testing.T) {
	f := newSQLiteFixture(t)
	store, k := seedSQLiteQueryFixture(t, f, queryFixtureRows(nil))

	stranger := tenant.Into(t.Context(), tenant.Context{
		OrgID: uuid.New(), ProjectID: uuid.New(), UserID: uuid.New(),
	})
	page, err := store.Query(stranger, k, sheet.RowQuery{Window: sheet.RowWindow{Limit: 10}})
	require.NoError(t, err)
	assert.Empty(t, page.Rows, "another org must read zero rows, predicate first and RLS second")
	assert.False(t, page.More)
}

func TestSQLiteRowStore_Query_PagesAFilteredWalk(t *testing.T) {
	f := newSQLiteFixture(t)
	deletedAt := time.Date(2026, 9, 11, 4, 5, 6, 0, time.UTC)
	store, k := seedSQLiteQueryFixture(t, f, queryFixtureRows(&deletedAt))

	walked := make([]string, 0, 4)
	var cursor *sheet.RowCursor
	for pages := 0; ; pages++ {
		require.Less(t, pages, 20, "the walk must terminate")
		q := queryOf(t, queryFixtureColumns(), 2, "status:eq:open")
		q.Window.Cursor = cursor
		page, err := store.Query(f.ctx(), k, q)
		require.NoError(t, err)
		walked = append(walked, rowIDsOf(page.Rows)...)
		if !page.More {
			break
		}
		cursor = &sheet.RowCursor{Digest: "d", RowIndex: page.Rows[len(page.Rows)-1].RowIndex}
	}

	assert.Equal(t, []string{"r1", "r4", "r6", "r7"}, walked,
		"the cursor and the clauses must compose: every match once, no repeats")
}

func TestSQLiteRowStore_Query_IsScopedToOneTab(t *testing.T) {
	f := newSQLiteFixture(t)
	store, k := seedSQLiteQueryFixture(t, f, queryFixtureRows(nil))

	other := sheet.SnapshotKey{SheetID: k.SheetID, Tab: "Extras"}
	page, err := store.Query(f.ctx(), other, sheet.RowQuery{Window: sheet.RowWindow{Limit: 10}})
	require.NoError(t, err)
	assert.Empty(t, page.Rows, "another tab's rows are not this tab's")
	assert.False(t, page.More)
}

// typedFixtureRows is the shared typed-filter fixture both drivers seed: in-grammar numbers, out-of-grammar numbers, a NaN, date shapes the grammar admits and shapes it refuses, an empty cell and a missing key.
func typedFixtureRows() []sheet.ProjectedRow {
	return []sheet.ProjectedRow{
		{RowID: "t1", RowIndex: 0, Data: gsheet.Row{"id": "t1", "qty": "10", "net.qty": "12", "when": "2026-01-02"}},
		{RowID: "t2", RowIndex: 1, Data: gsheet.Row{"id": "t2", "qty": "9", "net.qty": "3", "when": "2026-01-02T00:00:00Z"}},
		{RowID: "t3", RowIndex: 2, Data: gsheet.Row{"id": "t3", "qty": "003", "when": "2026-01-01T23:00:00Z"}},
		{RowID: "t4", RowIndex: 4, Data: gsheet.Row{"id": "t4", "qty": "-4", "when": "2026-01-03"}},
		{RowID: "t5", RowIndex: 5, Data: gsheet.Row{"id": "t5", "qty": "-0", "when": "01/02/2026"}},
		{RowID: "t6", RowIndex: 6, Data: gsheet.Row{"id": "t6", "qty": "1.5", "when": "2026-01-02T00:00:00.5Z"}},
		{RowID: "t7", RowIndex: 7, Data: gsheet.Row{"id": "t7", "qty": "999999999999999", "when": "2026-01-02T10:00:00+07:00"}},
		{RowID: "t8", RowIndex: 8, Data: gsheet.Row{"id": "t8", "qty": "abc", "when": "2026-01-02 00:00:00"}},
		{RowID: "t9", RowIndex: 9, Data: gsheet.Row{"id": "t9", "qty": "NaN", "when": "2026-02-31"}},
		{RowID: "t10", RowIndex: 11, Data: gsheet.Row{"id": "t10", "qty": "1e3", "when": ""}},
		{RowID: "t11", RowIndex: 12, Data: gsheet.Row{"id": "t11"}},
		{RowID: "t12", RowIndex: 13, Data: gsheet.Row{"id": "t12", "qty": "", "when": "2026-01-02T23:59:59Z"}},
	}
}

func typedFixtureColumns() []string {
	return []string{"id", "net.qty", "qty", "when"}
}

// typedQueryCases is every hintable operator under both hints against typedFixtureRows, asserted by row id in row_index order.
func typedQueryCases() []queryCase {
	return []queryCase{
		{"num gt", []string{"qty:num.gt:9"}, []string{"t1", "t7"}},
		{"num gte", []string{"qty:num.gte:9"}, []string{"t1", "t2", "t7"}},
		{"num lt", []string{"qty:num.lt:9"}, []string{"t3", "t4", "t5", "t6"}},
		{"num lte", []string{"qty:num.lte:9"}, []string{"t2", "t3", "t4", "t5", "t6"}},
		{"num eq ignores leading zeros", []string{"qty:num.eq:3"}, []string{"t3"}},
		{"num eq matches a negative zero cell", []string{"qty:num.eq:0"}, []string{"t5"}},
		{"num eq ignores a trailing fractional zero", []string{"qty:num.eq:1.50"}, []string{"t6"}},
		{"num ne is not the complement of num eq", []string{"qty:num.ne:9"}, []string{"t1", "t3", "t4", "t5", "t6", "t7"}},
		{"num lt on a zero operand", []string{"qty:num.lt:0"}, []string{"t4"}},
		{"num gte on a negative zero operand", []string{"qty:num.gte:-0"}, []string{"t1", "t2", "t3", "t5", "t6", "t7"}},
		{"num gt at the fifteen digit bound", []string{"qty:num.gt:999999999999998"}, []string{"t7"}},
		{
			"num lte at the fifteen digit bound",
			[]string{"qty:num.lte:999999999999999"},
			[]string{"t1", "t2", "t3", "t4", "t5", "t6", "t7"},
		},
		{"the same operator without a hint compares text", []string{"qty:gt:9"}, []string{"t7", "t8", "t9"}},
		{"date eq", []string{"when:date.eq:2026-01-02"}, []string{"t1"}},
		{"date ne", []string{"when:date.ne:2026-01-02"}, []string{"t2", "t3", "t4", "t9", "t12"}},
		{"date gt", []string{"when:date.gt:2026-01-02"}, []string{"t2", "t4", "t9", "t12"}},
		{"date gte", []string{"when:date.gte:2026-01-02"}, []string{"t1", "t2", "t4", "t9", "t12"}},
		{"date lt", []string{"when:date.lt:2026-01-02"}, []string{"t3"}},
		{"date lte", []string{"when:date.lte:2026-01-02"}, []string{"t1", "t3"}},
		{"date gt an instant leaves the bare day below it", []string{"when:date.gt:2026-01-02T00:00:00Z"}, []string{"t4", "t9", "t12"}},
		{"a hinted clause ANDs with another", []string{"qty:num.gt:0", "when:date.gte:2026-01-02"}, []string{"t1", "t2"}},
		{"num gt on a dotted header", []string{"net.qty:num.gt:5"}, []string{"t1"}},
		{"num lt on a dotted header", []string{"net.qty:num.lt:5"}, []string{"t2"}},
	}
}

// numBoundaryFixtureRows lays every case in the num grammar table out as one row, so what a num comparison returns is exactly the set the grammar admits.
func numBoundaryFixtureRows() []sheet.ProjectedRow {
	cases := numGrammarCases()
	out := make([]sheet.ProjectedRow, 0, len(cases))
	for i, tc := range cases {
		id := "b" + strconv.Itoa(i)
		out = append(out, sheet.ProjectedRow{
			RowID: id, RowIndex: i, Data: gsheet.Row{"id": id, "qty": tc.in},
		})
	}
	return out
}

func numFixtureColumns() []string {
	return []string{"id", "qty"}
}

// numBoundaryCases asserts all six hinted operators against the grammar table, deriving the wanted ids from ParseNum so a driver that admits one value more or one fewer than the Go grammar fails.
func numBoundaryCases(t *testing.T, operand string) []queryCase {
	t.Helper()
	pivot, ok := sheet.ParseNum(operand)
	require.True(t, ok, "the operand must itself be in the grammar, or the parser refuses the clause")

	ops := []struct {
		op   string
		keep func(float64) bool
	}{
		{"eq", func(v float64) bool { return v == pivot }},
		{"ne", func(v float64) bool { return v != pivot }},
		{"gt", func(v float64) bool { return v > pivot }},
		{"gte", func(v float64) bool { return v >= pivot }},
		{"lt", func(v float64) bool { return v < pivot }},
		{"lte", func(v float64) bool { return v <= pivot }},
	}
	cases := numGrammarCases()
	out := make([]queryCase, 0, len(ops))
	for _, op := range ops {
		want := make([]string, 0, len(cases))
		for i, tc := range cases {
			v, inGrammar := sheet.ParseNum(tc.in)
			if inGrammar && op.keep(v) {
				want = append(want, "b"+strconv.Itoa(i))
			}
		}
		require.NotEmpty(t, want, "num.%s:%s must match something, or the case proves nothing", op.op, operand)
		out = append(out, queryCase{
			name:  "num." + op.op + ":" + operand,
			where: []string{"qty:num." + op.op + ":" + operand},
			want:  want,
		})
	}
	return out
}

// nonFiniteFixtureRows holds the values Postgres calls valid numerics and the grammar refuses: 'NaN'::numeric outranks every finite value, so one such cell would dominate under a pg_input_is_valid guard.
func nonFiniteFixtureRows() []sheet.ProjectedRow {
	return []sheet.ProjectedRow{
		{RowID: "n1", RowIndex: 0, Data: gsheet.Row{"id": "n1", "qty": "NaN"}},
		{RowID: "n2", RowIndex: 1, Data: gsheet.Row{"id": "n2", "qty": "Infinity"}},
		{RowID: "n3", RowIndex: 2, Data: gsheet.Row{"id": "n3", "qty": "-Infinity"}},
		{RowID: "n4", RowIndex: 3, Data: gsheet.Row{"id": "n4", "qty": "7"}},
	}
}

func nonFiniteCases() []queryCase {
	return []queryCase{
		{"eq", []string{"qty:num.eq:7"}, []string{"n4"}},
		{"ne", []string{"qty:num.ne:7"}, []string{}},
		{"gt", []string{"qty:num.gt:7"}, []string{}},
		{"gte", []string{"qty:num.gte:7"}, []string{"n4"}},
		{"lt", []string{"qty:num.lt:7"}, []string{}},
		{"lte", []string{"qty:num.lte:7"}, []string{"n4"}},
	}
}

// significancePairRows holds the pair spec section 2 measures: both match NumPattern, Postgres calls them distinct and float64 calls them equal, so only the significance half of the guard keeps the drivers from disagreeing.
func significancePairRows() []sheet.ProjectedRow {
	return []sheet.ProjectedRow{
		{RowID: "s1", RowIndex: 0, Data: gsheet.Row{"id": "s1", "qty": "123456789012345.1"}},
		{RowID: "s2", RowIndex: 1, Data: gsheet.Row{"id": "s2", "qty": "123456789012345.101"}},
		{RowID: "s3", RowIndex: 2, Data: gsheet.Row{"id": "s3", "qty": "123456789012345"}},
	}
}

func significancePairCases() []queryCase {
	return []queryCase{
		{"gt below the pair", []string{"qty:num.gt:123456789012344"}, []string{"s3"}},
		{"gte at the in-grammar value", []string{"qty:num.gte:123456789012345"}, []string{"s3"}},
		{"lt above the pair", []string{"qty:num.lt:999999999999999"}, []string{"s3"}},
		{"eq the in-grammar value", []string{"qty:num.eq:123456789012345"}, []string{"s3"}},
		{"ne the in-grammar value", []string{"qty:num.ne:123456789012345"}, []string{}},
	}
}

func TestSQLiteRowStore_Query_TypedOperators(t *testing.T) {
	f := newSQLiteFixture(t)
	store, k := seedSQLiteQueryFixture(t, f, typedFixtureRows())

	for _, tc := range typedQueryCases() {
		t.Run(tc.name, func(t *testing.T) {
			page, err := store.Query(f.ctx(), k, queryOf(t, typedFixtureColumns(), 100, tc.where...))
			require.NoError(t, err)
			assert.Equal(t, tc.want, rowIDsOf(page.Rows), "?where=%v", tc.where)
			assert.False(t, page.More, "a page under the limit has nothing following it")
		})
	}
}

// TestSQLiteRowStore_Query_NumBoundaryTable is the arm the storage-class trap breaks: under a text bind every num.gt returns nothing and every num.lt returns everything, with no error.
func TestSQLiteRowStore_Query_NumBoundaryTable(t *testing.T) {
	f := newSQLiteFixture(t)
	store, k := seedSQLiteQueryFixture(t, f, numBoundaryFixtureRows())

	for _, operand := range []string{"1.5", "0"} {
		for _, tc := range numBoundaryCases(t, operand) {
			t.Run(tc.name, func(t *testing.T) {
				page, err := store.Query(f.ctx(), k, queryOf(t, numFixtureColumns(), 100, tc.where...))
				require.NoError(t, err)
				assert.Equal(t, tc.want, rowIDsOf(page.Rows), "?where=%v", tc.where)
			})
		}
	}
}

func TestSQLiteRowStore_Query_NonFiniteCellsAreExcludedFromEveryNumComparison(t *testing.T) {
	f := newSQLiteFixture(t)
	store, k := seedSQLiteQueryFixture(t, f, nonFiniteFixtureRows())

	for _, tc := range nonFiniteCases() {
		t.Run(tc.name, func(t *testing.T) {
			page, err := store.Query(f.ctx(), k, queryOf(t, numFixtureColumns(), 100, tc.where...))
			require.NoError(t, err)
			assert.Equal(t, tc.want, rowIDsOf(page.Rows), "?where=%v", tc.where)
		})
	}
}

func TestSQLiteRowStore_Query_SignificanceBoundary(t *testing.T) {
	f := newSQLiteFixture(t)
	store, k := seedSQLiteQueryFixture(t, f, significancePairRows())

	for _, tc := range significancePairCases() {
		t.Run(tc.name, func(t *testing.T) {
			page, err := store.Query(f.ctx(), k, queryOf(t, numFixtureColumns(), 100, tc.where...))
			require.NoError(t, err)
			assert.Equal(t, tc.want, rowIDsOf(page.Rows), "?where=%v", tc.where)
		})
	}
}

// NOTE: sqlite has no RLS, so this is what holds the explicit org_id predicate in place for a typed clause too; on postgres FORCE ROW LEVEL SECURITY is the backstop and no test can see the predicate go missing.
func TestSQLiteRowStore_Query_ATypedFilterIsScopedToOneOrg(t *testing.T) {
	f := newSQLiteFixture(t)
	store, k := seedSQLiteQueryFixture(t, f, typedFixtureRows())

	stranger := tenant.Into(t.Context(), tenant.Context{
		OrgID: uuid.New(), ProjectID: uuid.New(), UserID: uuid.New(),
	})
	for _, where := range []string{"qty:num.gt:-999999999999999", "when:date.gte:2026-01-01"} {
		page, err := store.Query(stranger, k, queryOf(t, typedFixtureColumns(), 100, where))
		require.NoError(t, err)
		assert.Empty(t, page.Rows, "another org must read zero rows, predicate first and RLS second")
	}
}
