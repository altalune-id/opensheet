//go:build integration

package sheet_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/gworkspace/gsheet"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/sheet"
)

func pgSeedSheet(t *testing.T, f *pgFixture, tree orgTree, slug, tab string) *sheet.Sheet {
	t.Helper()
	sh, err := sheet.New(sheet.NewParams{
		OrgID: tree.orgID, ProjectID: tree.projectID, SpreadsheetID: tree.spreadsheetID,
		Tab: tab, Slug: slug, Visibility: sheet.VisibilityKey,
	})
	require.NoError(t, err)
	require.NoError(t, f.store.Save(tree.ctx(t), sh))
	return sh
}

func pgBumpGeneration(t *testing.T, f *pgFixture, sheetID uuid.UUID) {
	t.Helper()
	_, err := f.ownerDB.ExecContext(t.Context(),
		"UPDATE public."+f.prefix+"sheets SET generation = generation + 1 WHERE id = $1", sheetID)
	require.NoError(t, err)
}

func TestPgRowStore_Replace_RoundTripsRowsInIndexOrder(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)
	sh := pgSeedSheet(t, f, f.a, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	ok, err := f.rows.Replace(ctx, k, 0, []sheet.ProjectedRow{
		{RowID: "b", RowIndex: 2, Data: gsheet.Row{"id": "b", "name": "bob"}},
		{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a", "name": "ada"}},
	}, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)

	got, err := f.rows.ListLive(ctx, k)
	require.NoError(t, err)
	assert.Equal(t, []sheet.ProjectedRow{
		{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a", "name": "ada"}},
		{RowID: "b", RowIndex: 2, Data: gsheet.Row{"id": "b", "name": "bob"}},
	}, got, "ListLive must order by row_index and preserve the interior gap")
}

// TestPgRowStore_Replace_DiscardsWhenGenerationMoved is the test the refresh path rests on: a fetch that raced a write must not overwrite the newer rows.
func TestPgRowStore_Replace_DiscardsWhenGenerationMoved(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)
	sh := pgSeedSheet(t, f, f.a, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	twoRows := []sheet.ProjectedRow{
		{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a"}},
		{RowID: "b", RowIndex: 1, Data: gsheet.Row{"id": "b"}},
	}
	ok, err := f.rows.Replace(ctx, k, 0, twoRows, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok, "the first refresh matches the seeded generation")

	pgBumpGeneration(t, f, sh.ID)

	otherRows := []sheet.ProjectedRow{{RowID: "z", RowIndex: 0, Data: gsheet.Row{"id": "z"}}}
	ok, err = f.rows.Replace(ctx, k, 1, otherRows, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.False(t, ok, "a refresh whose generation moved must be discarded, not applied")

	got, err := f.rows.ListLive(ctx, k)
	require.NoError(t, err)
	require.Equal(t, twoRows, got, "the discarded refresh must not have touched the rows")
}

// TestPgRowStore_Replace_RoundTripsJSON pins the jsonb-as-ColumnString binding: no other binding in the repo maps a jsonb column.
func TestPgRowStore_Replace_RoundTripsJSON(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)
	sh := pgSeedSheet(t, f, f.a, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	data := gsheet.Row{
		"id":     "a",
		"ampers": "Bed & Breakfast",
		"angle":  "a < b",
		"nested": `{"looks":"like json","n":[1,2]}`,
		"quote":  `he said "hi"`,
		"utf8":   "kamar — Ámbar ☕",
	}
	ok, err := f.rows.Replace(ctx, k, 0,
		[]sheet.ProjectedRow{{RowID: "a", Data: data}}, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)

	got, err := f.rows.ListLive(ctx, k)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, data, got[0].Data, "every value must survive the jsonb column byte for byte")

	for _, key := range []string{"ampers", "angle", "nested", "quote", "utf8"} {
		var extracted string
		require.NoError(t, f.ownerDB.QueryRowContext(t.Context(),
			fmt.Sprintf("SELECT data->>'%s' FROM public.%ssheet_rows WHERE sheet_id = $1", key, f.prefix),
			sh.ID).Scan(&extracted))
		assert.Equal(t, data[key], extracted,
			"data must be stored as a jsonb object so spec 3 can filter on data->>'%s'", key)
	}
}

func TestPgRowStore_Replace_WritesContractStateAndBumpsGeneration(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)
	sh := pgSeedSheet(t, f, f.a, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	ok, err := f.rows.Replace(ctx, k, 0, nil, sheet.ContractState{OK: false, Reason: "no id column"})
	require.NoError(t, err)
	require.True(t, ok)

	got, err := f.store.ByID(ctx, sh.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 1, got.Generation)
	assert.False(t, got.ContractOK, "Replace writes contract state through its own UPDATE, not through Save")
	assert.Equal(t, "no id column", got.ContractReason)
	require.NotNil(t, got.ValidatedAt)
}

func TestPgRowStore_Replace_InsertsMoreRowsThanOneChunk(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)
	sh := pgSeedSheet(t, f, f.a, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	const want = 2500
	rows := make([]sheet.ProjectedRow, 0, want)
	for i := range want {
		id := fmt.Sprintf("r%05d", i)
		rows = append(rows, sheet.ProjectedRow{RowID: id, RowIndex: i, Data: gsheet.Row{"id": id}})
	}
	ok, err := f.rows.Replace(ctx, k, 0, rows, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)

	got, err := f.rows.ListLive(ctx, k)
	require.NoError(t, err)
	require.Len(t, got, want, "every chunk must land")
	assert.Equal(t, rows[0], got[0])
	assert.Equal(t, rows[want-1], got[want-1])
}

func TestPgRowStore_UpsertRow_WritesOneRowAndBumpsGeneration(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)
	sh := pgSeedSheet(t, f, f.a, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	require.NoError(t, f.rows.UpsertRow(ctx, k,
		sheet.ProjectedRow{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a", "name": "ada"}}))
	require.NoError(t, f.rows.UpsertRow(ctx, k,
		sheet.ProjectedRow{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a", "name": "grace"}}))

	got, err := f.rows.ListLive(ctx, k)
	require.NoError(t, err)
	assert.Equal(t, []sheet.ProjectedRow{
		{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a", "name": "grace"}},
	}, got, "the second write must update the row, not insert a second one")

	reread, err := f.store.ByID(ctx, sh.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 2, reread.Generation, "every upsert bumps the generation")

	deletedAt := time.Now().UTC()
	require.NoError(t, f.rows.UpsertRow(ctx, k, sheet.ProjectedRow{
		RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a", "name": "grace"}, DeletedAt: &deletedAt,
	}))
	got, err = f.rows.ListLive(ctx, k)
	require.NoError(t, err)
	assert.Empty(t, got, "an upsert that sets deleted_at tombstones the row")
}

func TestPgRowStore_ListLive_ExcludesTombstones(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)
	sh := pgSeedSheet(t, f, f.a, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	deletedAt := time.Now().UTC()
	ok, err := f.rows.Replace(ctx, k, 0, []sheet.ProjectedRow{
		{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a"}},
		{RowID: "b", RowIndex: 1, Data: gsheet.Row{"id": "b"}, DeletedAt: &deletedAt},
	}, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)

	got, err := f.rows.ListLive(ctx, k)
	require.NoError(t, err)
	assert.Equal(t, []sheet.ProjectedRow{
		{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a"}},
	}, got, "a tombstoned row is not live")
}

func TestPgRowStore_ReplaceIsScopedToOneTab(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)
	sh := pgSeedSheet(t, f, f.a, "prices", "Rates")
	first := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}
	second := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Extras"}

	kept := []sheet.ProjectedRow{{RowID: "a", Data: gsheet.Row{"id": "a"}}}
	ok, err := f.rows.Replace(ctx, first, 0, kept, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = f.rows.Replace(ctx, second, 1,
		[]sheet.ProjectedRow{{RowID: "b", Data: gsheet.Row{"id": "b"}}}, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)

	got, err := f.rows.ListLive(ctx, first)
	require.NoError(t, err)
	assert.Equal(t, kept, got, "replacing one tab must not delete another tab's rows")
}

func TestPgRowStore_LockSheet_ReturnsTheCurrentGeneration(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)
	sh := pgSeedSheet(t, f, f.a, "prices", "Rates")

	gen, err := f.rows.LockSheet(ctx, sh.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 0, gen)

	pgBumpGeneration(t, f, sh.ID)

	gen, err = f.rows.LockSheet(ctx, sh.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 1, gen)

	_, err = f.rows.LockSheet(ctx, uuid.New())
	assert.True(t, sheet.IsNotFoundError(err), "want NotFoundError, got %T: %v", err, err)
}

func TestPgRowStore_PurgeSheet_DropsEveryTab(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)
	sh := pgSeedSheet(t, f, f.a, "prices", "Rates")
	first := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}
	second := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Extras"}

	ok, err := f.rows.Replace(ctx, first, 0,
		[]sheet.ProjectedRow{{RowID: "a", Data: gsheet.Row{"id": "a"}}}, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = f.rows.Replace(ctx, second, 1,
		[]sheet.ProjectedRow{{RowID: "b", Data: gsheet.Row{"id": "b"}}}, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)

	require.NoError(t, f.rows.PurgeSheet(ctx, sh.ID))

	for _, k := range []sheet.SnapshotKey{first, second} {
		got, lErr := f.rows.ListLive(ctx, k)
		require.NoError(t, lErr)
		assert.Empty(t, got, "PurgeSheet must clear tab %q", k.Tab)
	}
}

// TestPgRowStore_IsTenantScoped asserts RLS hides one org's projected rows from another under a NOBYPASSRLS role.
func TestPgRowStore_IsTenantScoped(t *testing.T) {
	f := newPgFixture(t)
	ctxA, ctxB := f.a.ctx(t), f.b.ctx(t)
	sh := pgSeedSheet(t, f, f.a, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	rows := []sheet.ProjectedRow{{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a"}}}
	ok, err := f.rows.Replace(ctxA, k, 0, rows, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)

	got, err := f.rows.ListLive(ctxB, k)
	require.NoError(t, err)
	assert.Empty(t, got, "org B must read zero rows from org A's sheet")

	statsForB, err := f.rows.Stats(ctxB, sh.ID, "Rates")
	require.NoError(t, err)
	assert.Equal(t, sheet.TableStats{Tab: "Rates"}, statsForB, "org B must count zero rows in org A's sheet")

	_, err = f.rows.LockSheet(ctxB, sh.ID)
	assert.True(t, sheet.IsNotFoundError(err), "cross-org LockSheet want NotFoundError, got %T: %v", err, err)

	appliedByB, err := f.rows.Replace(ctxB, k, 0, nil, sheet.ContractState{OK: false, Reason: "cross-org"})
	assert.False(t, appliedByB, "org B must not be able to replace org A's rows")
	assert.True(t, sheet.IsNotFoundError(err), "cross-org Replace want NotFoundError, got %T: %v", err, err)

	require.NoError(t, f.rows.PurgeSheet(ctxB, sh.ID))

	stillThere, err := f.rows.ListLive(ctxA, k)
	require.NoError(t, err)
	assert.Equal(t, rows, stillThere, "org A lost its rows to a cross-org purge")

	var unscoped int
	require.NoError(t, f.appDB.QueryRowContext(t.Context(),
		"SELECT count(*) FROM public."+f.prefix+"sheet_rows").Scan(&unscoped))
	assert.Zero(t, unscoped, "an unscoped read must return zero rows under FORCE ROW LEVEL SECURITY")
}

func TestPgRowStore_RequiresTenantScope(t *testing.T) {
	f := newPgFixture(t)
	k := sheet.SnapshotKey{SheetID: uuid.Must(uuid.NewV7()), Tab: "Rates"}

	_, err := f.rows.LockSheet(t.Context(), k.SheetID)
	assert.True(t, tenant.IsMissingError(err), "LockSheet want MissingError, got %T: %v", err, err)

	_, err = f.rows.Replace(t.Context(), k, 0, nil, sheet.ContractState{OK: true})
	assert.True(t, tenant.IsMissingError(err), "Replace want MissingError, got %T: %v", err, err)

	err = f.rows.UpsertRow(t.Context(), k, sheet.ProjectedRow{RowID: "a"})
	assert.True(t, tenant.IsMissingError(err), "UpsertRow want MissingError, got %T: %v", err, err)

	_, err = f.rows.ListLive(t.Context(), k)
	assert.True(t, tenant.IsMissingError(err), "ListLive want MissingError, got %T: %v", err, err)

	_, err = f.rows.Stats(t.Context(), k.SheetID, k.Tab)
	assert.True(t, tenant.IsMissingError(err), "Stats want MissingError, got %T: %v", err, err)

	assert.True(t, tenant.IsMissingError(f.rows.PurgeSheet(t.Context(), k.SheetID)))
}

func TestPgRowStore_DeletingTheSheetCascades(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)
	sh := pgSeedSheet(t, f, f.a, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	ok, err := f.rows.Replace(ctx, k, 0,
		[]sheet.ProjectedRow{{RowID: "a", Data: gsheet.Row{"id": "a"}}}, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, f.store.Delete(ctx, sh.ID))

	got, err := f.rows.ListLive(ctx, k)
	require.NoError(t, err)
	assert.Empty(t, got, "deleting the sheet must cascade to its projected rows")
}

func TestPgRowStore_RefreshPersistsTheSoftDeleteOptIn(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)
	sh := pgSeedSheet(t, f, f.a, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	rows := []sheet.ProjectedRow{{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a"}}}
	ok, err := f.rows.Replace(ctx, k, 0, rows, sheet.ContractState{OK: true, SoftDelete: true})
	require.NoError(t, err)
	require.True(t, ok)

	got, err := f.store.ByID(ctx, sh.ID)
	require.NoError(t, err)
	assert.True(t, got.SoftDelete,
		"a refresh must persist the opt-in; store.Save drops it, so it has to travel with the contract")

	ok, err = f.rows.MarkContract(ctx, sh.ID, 1, sheet.ContractState{OK: false, Reason: "no id column"})
	require.NoError(t, err)
	require.True(t, ok)

	got, err = f.store.ByID(ctx, sh.ID)
	require.NoError(t, err)
	assert.False(t, got.SoftDelete, "MarkContract writes what the header row said, not what it said last time")
}

func TestPgRowStore_MarkContract_PersistsDriftWithoutTouchingTheRows(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)
	sh := pgSeedSheet(t, f, f.a, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	rows := []sheet.ProjectedRow{{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a"}}}
	ok, err := f.rows.Replace(ctx, k, 0, rows, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = f.rows.MarkContract(ctx, sh.ID, 1, sheet.ContractState{OK: false, Reason: "no id column"})
	require.NoError(t, err)
	require.True(t, ok)

	got, err := f.store.ByID(ctx, sh.ID)
	require.NoError(t, err)
	assert.False(t, got.ContractOK)
	assert.Equal(t, "no id column", got.ContractReason)
	assert.EqualValues(t, 1, got.Generation, "the projected rows did not change, so the generation must not move")
	require.NotNil(t, got.ValidatedAt)

	live, err := f.rows.ListLive(ctx, k)
	require.NoError(t, err)
	assert.Equal(t, rows, live, "MarkContract must leave the projected rows alone")
}

func TestPgRowStore_MarkContract_DiscardsWhenGenerationMoved(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)
	sh := pgSeedSheet(t, f, f.a, "prices", "Rates")

	pgBumpGeneration(t, f, sh.ID)

	ok, err := f.rows.MarkContract(ctx, sh.ID, 0, sheet.ContractState{OK: false, Reason: "a finding from an older fetch"})
	require.NoError(t, err)
	require.False(t, ok, "a finding whose generation moved must not overwrite a newer verdict")

	got, err := f.store.ByID(ctx, sh.ID)
	require.NoError(t, err)
	assert.True(t, got.ContractOK)
	assert.Empty(t, got.ContractReason)
}

func TestPgRowStore_Stats_CountsLiveRowsAndNamesColumns(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)
	sh := pgSeedSheet(t, f, f.a, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	deletedAt := time.Now().UTC()
	ok, err := f.rows.Replace(ctx, k, 0, []sheet.ProjectedRow{
		{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a", "name": "ada"}},
		{RowID: "b", RowIndex: 1, Data: gsheet.Row{"id": "b", "name": "bo"}, DeletedAt: &deletedAt},
		{RowID: "c", RowIndex: 2, Data: gsheet.Row{"id": "c", "name": "cyd"}},
	}, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)

	got, err := f.rows.Stats(ctx, sh.ID, "Rates")
	require.NoError(t, err)
	assert.Equal(t, sheet.TableStats{
		Tab:      "Rates",
		Columns:  []string{"id", "name"},
		RowCount: 2,
	}, got, "Stats counts live rows only")
}

func TestPgRowStore_Stats_IsScopedToOneTab(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)
	sh := pgSeedSheet(t, f, f.a, "prices", "Rates")

	ok, err := f.rows.Replace(ctx, sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}, 0,
		[]sheet.ProjectedRow{{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a"}}},
		sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = f.rows.Replace(ctx, sheet.SnapshotKey{SheetID: sh.ID, Tab: "Renamed"}, 1, []sheet.ProjectedRow{
		{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a"}},
		{RowID: "b", RowIndex: 1, Data: gsheet.Row{"id": "b"}},
	}, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)

	got, err := f.rows.Stats(ctx, sh.ID, "Rates")
	require.NoError(t, err)
	assert.EqualValues(t, 1, got.RowCount, "a renamed tab's orphaned rows are not this tab's")
}

// A sheet naming no tab means "the first tab", whose name only Google knows, so the projection answers for the tab it holds.
func TestPgRowStore_Stats_UnnamedTabAnswersForTheProjectedTab(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)
	sh := pgSeedSheet(t, f, f.a, "prices", "")

	ok, err := f.rows.Replace(ctx, sheet.SnapshotKey{SheetID: sh.ID, Tab: "First"}, 0,
		[]sheet.ProjectedRow{{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a"}}},
		sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)

	got, err := f.rows.Stats(ctx, sh.ID, "")
	require.NoError(t, err)
	assert.Equal(t, "First", got.Tab)
	assert.EqualValues(t, 1, got.RowCount)
}

func TestPgRowStore_Stats_UnprojectedSheetCountsNothing(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)
	sh := pgSeedSheet(t, f, f.a, "prices", "Rates")

	got, err := f.rows.Stats(ctx, sh.ID, "Rates")
	require.NoError(t, err)
	assert.Equal(t, sheet.TableStats{Tab: "Rates"}, got, "nothing projected is not an error")
}
