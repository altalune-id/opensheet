//go:build integration

package sheet_test

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
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

func TestPgRowStore_RowByID_ReturnsTheRowWithItsTombstoneState(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)
	sh := pgSeedSheet(t, f, f.a, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	deletedAt := time.Date(2026, 9, 10, 4, 5, 6, 0, time.UTC)
	ok, err := f.rows.Replace(ctx, k, 0, []sheet.ProjectedRow{
		{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a", "name": "ada"}},
		{RowID: "b", RowIndex: 3, Data: gsheet.Row{"id": "b"}, DeletedAt: &deletedAt},
	}, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)

	live, err := f.rows.RowByID(ctx, k, "a")
	require.NoError(t, err)
	assert.Equal(t, sheet.ProjectedRow{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a", "name": "ada"}}, live)

	tombstoned, err := f.rows.RowByID(ctx, k, "b")
	require.NoError(t, err)
	require.NotNil(t, tombstoned.DeletedAt,
		"a tombstoned row must come back with its tombstone, not as absent")
	assert.True(t, tombstoned.DeletedAt.Equal(deletedAt), "DeletedAt = %v, want %v", tombstoned.DeletedAt, deletedAt)
	assert.Equal(t, 3, tombstoned.RowIndex)
}

func TestPgRowStore_RowByID_UnknownIDIsNotFound(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)
	sh := pgSeedSheet(t, f, f.a, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	ok, err := f.rows.Replace(ctx, k, 0,
		[]sheet.ProjectedRow{{RowID: "a", Data: gsheet.Row{"id": "a"}}}, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)

	_, err = f.rows.RowByID(ctx, k, "nobody")
	assert.True(t, sheet.IsRowNotFoundError(err), "want RowNotFoundError, got %T: %v", err, err)

	_, err = f.rows.RowByID(ctx, sheet.SnapshotKey{SheetID: sh.ID, Tab: "Other"}, "a")
	assert.True(t, sheet.IsRowNotFoundError(err), "the lookup must be scoped to one tab, got %T: %v", err, err)
}

func TestPgRowStore_StateOf_ReportsThePersistedState(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)
	sh := pgSeedSheet(t, f, f.a, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	ok, err := f.rows.Replace(ctx, k, 0, nil, sheet.ContractState{OK: true, SoftDelete: true})
	require.NoError(t, err)
	require.True(t, ok)

	got, err := f.rows.StateOf(ctx, sh.ID)
	require.NoError(t, err)
	assert.Equal(t, sheet.ContractState{OK: true, SoftDelete: true}, got.Contract)
	assert.NotEmpty(t, got.Digest, "a refresh must persist the digest of the rows it wrote")
	assert.EqualValues(t, 1, got.Generation)

	ok, err = f.rows.MarkContract(ctx, sh.ID, 1, sheet.ContractState{Reason: "no id column"})
	require.NoError(t, err)
	require.True(t, ok)

	drifted, err := f.rows.StateOf(ctx, sh.ID)
	require.NoError(t, err)
	assert.Equal(t, sheet.ContractState{Reason: "no id column"}, drifted.Contract,
		"the drift a refresh persisted is what a row read must refuse on")
	assert.Equal(t, got.Digest, drifted.Digest, "marking drift must leave the digest alone")
	assert.Equal(t, got.Generation, drifted.Generation)
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

	pageForB, err := f.rows.Query(ctxB, k, sheet.RowQuery{Window: sheet.RowWindow{Limit: 10}})
	require.NoError(t, err)
	assert.Empty(t, pageForB.Rows, "org B must query zero rows from org A's sheet")

	_, err = f.rows.RowByID(ctxB, k, "a")
	assert.True(t, sheet.IsRowNotFoundError(err),
		"org B must not read org A's row by id, got %T: %v", err, err)

	_, err = f.rows.StateOf(ctxB, sh.ID)
	assert.True(t, sheet.IsNotFoundError(err),
		"org B must not read org A's sheet state, got %T: %v", err, err)

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

	_, err = f.rows.RowByID(t.Context(), k, "a")
	assert.True(t, tenant.IsMissingError(err), "RowByID want MissingError, got %T: %v", err, err)

	_, err = f.rows.StateOf(t.Context(), k.SheetID)
	assert.True(t, tenant.IsMissingError(err), "StateOf want MissingError, got %T: %v", err, err)

	_, err = f.rows.ListLive(t.Context(), k)
	assert.True(t, tenant.IsMissingError(err), "ListLive want MissingError, got %T: %v", err, err)

	_, err = f.rows.Query(t.Context(), k, sheet.RowQuery{})
	assert.True(t, tenant.IsMissingError(err), "Query want MissingError, got %T: %v", err, err)

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

func pgRefreshedRows() []sheet.ProjectedRow {
	return []sheet.ProjectedRow{
		{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a", "name": "ada"}},
		{RowID: "b", RowIndex: 2, Data: gsheet.Row{"id": "b", "name": "bob"}},
	}
}

// TestPgRowStore_Refresh_DoesNotBumpTheGenerationWhenNothingChanged is why the bump is conditional: a 1000-page keyset walk spans several TTL windows, and a bump per window makes it uncompletable.
func TestPgRowStore_Refresh_DoesNotBumpTheGenerationWhenNothingChanged(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)
	sh := pgSeedSheet(t, f, f.a, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	ok, err := f.rows.Replace(ctx, k, 0, pgRefreshedRows(), sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)
	first, err := f.rows.StateOf(ctx, sh.ID)
	require.NoError(t, err)
	require.EqualValues(t, 1, first.Generation)
	require.NotEmpty(t, first.Digest)
	firstSheet, err := f.store.ByID(ctx, sh.ID)
	require.NoError(t, err)
	require.NotNil(t, firstSheet.ValidatedAt)

	ok, err = f.rows.Replace(ctx, k, first.Generation, pgRefreshedRows(), sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)

	second, err := f.rows.StateOf(ctx, sh.ID)
	require.NoError(t, err)
	assert.Equal(t, first.Generation, second.Generation,
		"a refresh that refetched identical rows must not move the generation")
	assert.Equal(t, first.Digest, second.Digest)

	secondSheet, err := f.store.ByID(ctx, sh.ID)
	require.NoError(t, err)
	require.NotNil(t, secondSheet.ValidatedAt)
	assert.True(t, secondSheet.ValidatedAt.After(*firstSheet.ValidatedAt),
		"the freshness gate still has to advance, or every page refetches from Google")
}

func TestPgRowStore_Refresh_BumpsWhenACellChanged(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)
	sh := pgSeedSheet(t, f, f.a, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	ok, err := f.rows.Replace(ctx, k, 0, pgRefreshedRows(), sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)
	first, err := f.rows.StateOf(ctx, sh.ID)
	require.NoError(t, err)

	edited := pgRefreshedRows()
	edited[1].Data = gsheet.Row{"id": "b", "name": "Bob"}
	ok, err = f.rows.Replace(ctx, k, first.Generation, edited, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)

	second, err := f.rows.StateOf(ctx, sh.ID)
	require.NoError(t, err)
	assert.EqualValues(t, first.Generation+1, second.Generation)
	assert.NotEqual(t, first.Digest, second.Digest)
}

func TestPgRowStore_Refresh_BumpsWhenARowIsTombstoned(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)
	sh := pgSeedSheet(t, f, f.a, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	ok, err := f.rows.Replace(ctx, k, 0, pgRefreshedRows(), sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)
	first, err := f.rows.StateOf(ctx, sh.ID)
	require.NoError(t, err)

	deletedAt := time.Date(2026, 9, 11, 4, 5, 6, 0, time.UTC)
	tombstoned := pgRefreshedRows()
	tombstoned[1].DeletedAt = &deletedAt
	ok, err = f.rows.Replace(ctx, k, first.Generation, tombstoned, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)

	second, err := f.rows.StateOf(ctx, sh.ID)
	require.NoError(t, err)
	assert.EqualValues(t, first.Generation+1, second.Generation,
		"a tombstone changes no row_id, no row_index and no cell, so only a tombstone-aware digest moves here")
	assert.NotEqual(t, first.Digest, second.Digest)
}

// TestPgRowStore_Replace_StaleRefreshCannotResurrectATombstonedRow is the interleaving a tombstone-blind digest breaks: the earlier of two refreshes at one generation would apply last and undo the delete.
func TestPgRowStore_Replace_StaleRefreshCannotResurrectATombstonedRow(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)
	sh := pgSeedSheet(t, f, f.a, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	ok, err := f.rows.Replace(ctx, k, 0, pgRefreshedRows(), sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)
	settled, err := f.rows.StateOf(ctx, sh.ID)
	require.NoError(t, err)

	deletedAt := time.Date(2026, 9, 11, 4, 5, 6, 0, time.UTC)
	tombstoned := pgRefreshedRows()
	tombstoned[1].DeletedAt = &deletedAt

	ok, err = f.rows.Replace(ctx, k, settled.Generation, tombstoned, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok, "the refresh that saw the tombstone applies first")

	ok, err = f.rows.Replace(ctx, k, settled.Generation, pgRefreshedRows(), sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.False(t, ok, "the refresh whose fetch predates the tombstone must be discarded")

	live, err := f.rows.ListLive(ctx, k)
	require.NoError(t, err)
	assert.Equal(t, []sheet.ProjectedRow{
		{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a", "name": "ada"}},
	}, live, "the deleted row must stay deleted")
}

func TestPgRowStore_UpsertRow_ClearsTheDigestSoOutstandingCursorsAreRefused(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)
	sh := pgSeedSheet(t, f, f.a, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}

	ok, err := f.rows.Replace(ctx, k, 0,
		[]sheet.ProjectedRow{{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a"}}},
		sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)
	settled, err := f.rows.StateOf(ctx, sh.ID)
	require.NoError(t, err)
	require.NotEmpty(t, settled.Digest, "a refresh must leave a digest")

	require.NoError(t, f.rows.UpsertRow(ctx, k,
		sheet.ProjectedRow{RowID: "a", RowIndex: 0, Data: gsheet.Row{"id": "a", "name": "ada"}}))

	after, err := f.rows.StateOf(ctx, sh.ID)
	require.NoError(t, err)
	assert.Empty(t, after.Digest,
		"a write must clear the digest, or a cursor issued before it still matches and the walk resumes across a mutation")
	assert.Greater(t, after.Generation, settled.Generation, "a write always bumps")
}

func pgSeedQueryFixture(t *testing.T, f *pgFixture, rows []sheet.ProjectedRow) sheet.SnapshotKey {
	t.Helper()
	sh := pgSeedSheet(t, f, f.a, "prices", "Rates")
	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Rates"}
	ok, err := f.rows.Replace(f.a.ctx(t), k, 0, rows, sheet.ContractState{OK: true})
	require.NoError(t, err)
	require.True(t, ok)
	return k
}

func TestPgRowStore_Query_EveryOperator(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)
	deletedAt := time.Date(2026, 9, 11, 4, 5, 6, 0, time.UTC)
	k := pgSeedQueryFixture(t, f, queryFixtureRows(&deletedAt))

	for _, tc := range queryCases() {
		t.Run(tc.name, func(t *testing.T) {
			page, err := f.rows.Query(ctx, k, queryOf(t, queryFixtureColumns(), 100, tc.where...))
			require.NoError(t, err)
			assert.Equal(t, tc.want, rowIDsOf(page.Rows), "?where=%v", tc.where)
			assert.False(t, page.More, "a page under the limit has nothing following it")
		})
	}
}

// TestPgRowStore_Query_KeysetVisitsEveryRowExactlyOnce is the assertion this method exists for: the fixture has an interior row_index gap and an interior tombstone, which a naive OFFSET or a row_index+1 cursor both get wrong.
func TestPgRowStore_Query_KeysetVisitsEveryRowExactlyOnce(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)
	deletedAt := time.Date(2026, 9, 11, 4, 5, 6, 0, time.UTC)
	k := pgSeedQueryFixture(t, f, queryFixtureRows(&deletedAt))

	live, err := f.rows.ListLive(ctx, k)
	require.NoError(t, err)
	require.Len(t, live, 6)

	walked := make([]string, 0, len(live))
	var cursor *sheet.RowCursor
	for pages := 0; ; pages++ {
		require.Less(t, pages, 20, "the walk must terminate")
		page, qErr := f.rows.Query(ctx, k, sheet.RowQuery{
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

func TestPgRowStore_Query_MoreReportsWhetherAPageFollows(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)
	deletedAt := time.Date(2026, 9, 11, 4, 5, 6, 0, time.UTC)
	k := pgSeedQueryFixture(t, f, queryFixtureRows(&deletedAt))

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
		page, err := f.rows.Query(ctx, k, sheet.RowQuery{Window: sheet.RowWindow{Limit: tc.limit}})
		require.NoError(t, err)
		assert.Len(t, page.Rows, tc.wantRows, "limit=%d", tc.limit)
		assert.Equal(t, tc.wantMore, page.More,
			"limit=%d: More must distinguish a full last page from a full page with more behind it", tc.limit)
	}
}

func TestPgRowStore_Query_AHostileColumnNameIsInert(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)
	k := pgSeedQueryFixture(t, f, hostileFixtureRows())

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
		page, err := f.rows.Query(ctx, k, q)
		require.NoError(t, err, "column %q must bind as a parameter, not as SQL", tc.column)
		assert.Equal(t, tc.want, rowIDsOf(page.Rows), "column %q", tc.column)
	}

	var count int
	require.NoError(t, f.ownerDB.QueryRowContext(t.Context(),
		"SELECT count(*) FROM public."+f.prefix+"sheet_rows").Scan(&count))
	assert.Equal(t, 2, count, "the projection table must still exist with its rows")

	still, err := f.rows.ListLive(ctx, k)
	require.NoError(t, err)
	assert.Len(t, still, 2)
}

func TestPgRowStore_Query_PagesAFilteredWalk(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)
	deletedAt := time.Date(2026, 9, 11, 4, 5, 6, 0, time.UTC)
	k := pgSeedQueryFixture(t, f, queryFixtureRows(&deletedAt))

	walked := make([]string, 0, 4)
	var cursor *sheet.RowCursor
	for pages := 0; ; pages++ {
		require.Less(t, pages, 20, "the walk must terminate")
		q := queryOf(t, queryFixtureColumns(), 2, "status:eq:open")
		q.Window.Cursor = cursor
		page, err := f.rows.Query(ctx, k, q)
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

func TestPgRowStore_Query_IsScopedToOneTab(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)
	k := pgSeedQueryFixture(t, f, queryFixtureRows(nil))

	page, err := f.rows.Query(ctx, sheet.SnapshotKey{SheetID: k.SheetID, Tab: "Extras"},
		sheet.RowQuery{Window: sheet.RowWindow{Limit: 10}})
	require.NoError(t, err)
	assert.Empty(t, page.Rows, "another tab's rows are not this tab's")
	assert.False(t, page.More)
}

// TestQuery_IsIdenticalAcrossDrivers is narrowed on purpose: LOWER is collation-dependent on Postgres and ASCII-only on SQLite, so only canonical text values are claimed to agree.
func TestQuery_IsIdenticalAcrossDrivers(t *testing.T) {
	pgf := newPgFixture(t)
	pgCtx := pgf.a.ctx(t)
	deletedAt := time.Date(2026, 9, 11, 4, 5, 6, 0, time.UTC)
	pgKey := pgSeedQueryFixture(t, pgf, queryFixtureRows(&deletedAt))

	lf := newSQLiteFixture(t)
	liteStore, liteKey := seedSQLiteQueryFixture(t, lf, queryFixtureRows(&deletedAt))

	for _, tc := range queryCases() {
		t.Run(tc.name, func(t *testing.T) {
			q := queryOf(t, queryFixtureColumns(), 100, tc.where...)
			pgPage, err := pgf.rows.Query(pgCtx, pgKey, q)
			require.NoError(t, err)
			litePage, err := liteStore.Query(lf.ctx(), liteKey, q)
			require.NoError(t, err)
			assert.Equal(t, pgPage, litePage, "?where=%v must answer identically on both drivers", tc.where)
			assert.Equal(t, tc.want, rowIDsOf(pgPage.Rows))
		})
	}

	for _, limit := range []int{2, 5, 6, 7} {
		q := sheet.RowQuery{Window: sheet.RowWindow{Limit: limit}}
		pgPage, err := pgf.rows.Query(pgCtx, pgKey, q)
		require.NoError(t, err)
		litePage, err := liteStore.Query(lf.ctx(), liteKey, q)
		require.NoError(t, err)
		assert.Equal(t, pgPage, litePage, "limit=%d must page identically on both drivers", limit)
	}
}

// pgNumGuard is the Postgres spelling of the whole num grammar: NumPattern bounds each digit run, the length test bounds the significant digits.
const pgNumGuard = `SELECT $1::text ~ $2::text
                      AND length(ltrim(translate($1::text, '-.', ''), '0')) <= $3::int`

// TestPgTypedGrammar_AgreesWithTheGoGrammar drives the Postgres half of the grammar off the same tables as ParseNum, MatchesDateShape and the two registered SQLite functions.
func TestPgTypedGrammar_AgreesWithTheGoGrammar(t *testing.T) {
	f := newPgFixture(t)

	t.Run("num", func(t *testing.T) {
		for _, tc := range numGrammarCases() {
			t.Run(tc.name, func(t *testing.T) {
				var admitted bool
				require.NoError(t, f.ownerDB.QueryRowContext(t.Context(), pgNumGuard,
					tc.in, sheet.NumPattern, sheet.NumSignificanceLimit).Scan(&admitted))
				require.Equal(t, tc.ok, admitted, "the Postgres guard must admit exactly what ParseNum admits")
				if !tc.ok {
					return
				}
				want, ok := sheet.ParseNum(tc.in)
				require.True(t, ok)
				var same bool
				require.NoError(t, f.ownerDB.QueryRowContext(t.Context(),
					`SELECT ($1::text)::numeric = ($2::text)::numeric`,
					tc.in, strconv.FormatFloat(want, 'f', -1, 64)).Scan(&same))
				require.True(t, same,
					"Postgres numeric is exact and Go is float64; inside the 15-digit bound they must be the same value")
			})
		}
	})

	t.Run("date", func(t *testing.T) {
		for _, tc := range dateGrammarCases() {
			t.Run(tc.name, func(t *testing.T) {
				var matched bool
				require.NoError(t, f.ownerDB.QueryRowContext(t.Context(),
					`SELECT $1::text ~ $2::text`, tc.in, sheet.DatePattern).Scan(&matched))
				require.Equal(t, tc.ok, matched, "DatePattern must read the same in Postgres ARE as in Go RE2")
			})
		}
	})

	// NumPattern alone bounds each digit run, not the significant digits across the point, so task 3's CASE needs both halves.
	t.Run("the shape regex alone is not the guard", func(t *testing.T) {
		const crossRun = "123456789012345.1"
		_, ok := sheet.ParseNum(crossRun)
		require.False(t, ok, "16 significant digits are outside the Go grammar")

		var shapeOnly, guarded bool
		require.NoError(t, f.ownerDB.QueryRowContext(t.Context(),
			`SELECT $1::text ~ $2::text`, crossRun, sheet.NumPattern).Scan(&shapeOnly))
		require.True(t, shapeOnly, "the shape regex admits it, so the regex alone would diverge from Go")
		require.NoError(t, f.ownerDB.QueryRowContext(t.Context(), pgNumGuard,
			crossRun, sheet.NumPattern, sheet.NumSignificanceLimit).Scan(&guarded))
		require.False(t, guarded, "the significance half closes the divergence")
	})

	t.Run("what the guard prevents", func(t *testing.T) {
		for _, tc := range []struct {
			in      string
			valid   bool
			because string
		}{
			{in: "NaN", valid: true, because: "pg_input_is_valid accepts NaN, which sorts above every finite numeric"},
			{in: "Infinity", valid: true, because: "pg_input_is_valid accepts Infinity"},
			{in: strings.Repeat("9", 309), valid: true, because: "a 309-digit numeric is exact in Postgres and ErrRange in Go"},
			{in: "0." + strings.Repeat("9", 16384), valid: false, because: "numeric overflows past 16,383 fractional digits, so a bare cast would raise"},
		} {
			t.Run(tc.because, func(t *testing.T) {
				var valid, admitted bool
				require.NoError(t, f.ownerDB.QueryRowContext(t.Context(),
					`SELECT pg_input_is_valid($1::text, 'numeric')`, tc.in).Scan(&valid))
				require.Equal(t, tc.valid, valid)
				require.NoError(t, f.ownerDB.QueryRowContext(t.Context(), pgNumGuard,
					tc.in, sheet.NumPattern, sheet.NumSignificanceLimit).Scan(&admitted))
				require.False(t, admitted, "the grammar must refuse it: %s", tc.because)
			})
		}
	})
}

func TestPgRowStore_Query_TypedOperators(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)
	k := pgSeedQueryFixture(t, f, typedFixtureRows())

	for _, tc := range typedQueryCases() {
		t.Run(tc.name, func(t *testing.T) {
			page, err := f.rows.Query(ctx, k, queryOf(t, typedFixtureColumns(), 100, tc.where...))
			require.NoError(t, err)
			assert.Equal(t, tc.want, rowIDsOf(page.Rows), "?where=%v", tc.where)
			assert.False(t, page.More, "a page under the limit has nothing following it")
		})
	}
}

// TestPgRowStore_Query_NumBoundaryTable drives the CASE guard off the same grammar table as ParseNum: a bare cast raises on the first unparseable cell and pg_input_is_valid admits NaN.
func TestPgRowStore_Query_NumBoundaryTable(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)
	k := pgSeedQueryFixture(t, f, numBoundaryFixtureRows())

	for _, operand := range []string{"1.5", "0"} {
		for _, tc := range numBoundaryCases(t, operand) {
			t.Run(tc.name, func(t *testing.T) {
				page, err := f.rows.Query(ctx, k, queryOf(t, numFixtureColumns(), 100, tc.where...))
				require.NoError(t, err)
				assert.Equal(t, tc.want, rowIDsOf(page.Rows), "?where=%v", tc.where)
			})
		}
	}
}

func TestPgRowStore_Query_NonFiniteCellsAreExcludedFromEveryNumComparison(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)
	k := pgSeedQueryFixture(t, f, nonFiniteFixtureRows())

	var dominates bool
	require.NoError(t, f.ownerDB.QueryRowContext(t.Context(),
		`SELECT pg_input_is_valid('NaN', 'numeric') AND 'NaN'::numeric > 1e308`).Scan(&dominates))
	require.True(t, dominates, "this is what the regex guard exists to exclude")

	for _, tc := range nonFiniteCases() {
		t.Run(tc.name, func(t *testing.T) {
			page, err := f.rows.Query(ctx, k, queryOf(t, numFixtureColumns(), 100, tc.where...))
			require.NoError(t, err)
			assert.Equal(t, tc.want, rowIDsOf(page.Rows), "?where=%v", tc.where)
		})
	}
}

// TestQueryTyped_IsIdenticalAcrossDrivers is spec section 9 assertion 1: one grammar, compiled twice, answering identically — including num.lt, the arm that returns too many rows rather than too few under the SQLite text-bind defect.
func TestQueryTyped_IsIdenticalAcrossDrivers(t *testing.T) {
	for _, fixture := range []struct {
		name    string
		rows    []sheet.ProjectedRow
		columns []string
		cases   []queryCase
	}{
		{"typed operators", typedFixtureRows(), typedFixtureColumns(), typedQueryCases()},
		{"num grammar boundary", numBoundaryFixtureRows(), numFixtureColumns(), numBoundaryCases(t, "1.5")},
		{"num grammar boundary at zero", numBoundaryFixtureRows(), numFixtureColumns(), numBoundaryCases(t, "0")},
		{"non-finite cells", nonFiniteFixtureRows(), numFixtureColumns(), nonFiniteCases()},
		{"significance boundary", significancePairRows(), numFixtureColumns(), significancePairCases()},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			pgf := newPgFixture(t)
			pgCtx := pgf.a.ctx(t)
			lf := newSQLiteFixture(t)
			pgKey := pgSeedQueryFixture(t, pgf, fixture.rows)
			liteStore, liteKey := seedSQLiteQueryFixture(t, lf, fixture.rows)
			for _, tc := range fixture.cases {
				t.Run(tc.name, func(t *testing.T) {
					q := queryOf(t, fixture.columns, 100, tc.where...)
					pgPage, err := pgf.rows.Query(pgCtx, pgKey, q)
					require.NoError(t, err)
					litePage, err := liteStore.Query(lf.ctx(), liteKey, q)
					require.NoError(t, err)
					assert.Equal(t, pgPage, litePage, "?where=%v must answer identically on both drivers", tc.where)
					assert.Equal(t, tc.want, rowIDsOf(pgPage.Rows))
				})
			}
		})
	}
}

// TestQueryTyped_SignificanceIsMeasuredNotAssumed re-measures the divergence the significance half of the guard closes, so the pair in significancePairRows keeps its reason.
func TestQueryTyped_SignificanceIsMeasuredNotAssumed(t *testing.T) {
	f := newPgFixture(t)
	const low, high = "123456789012345.1", "123456789012345.101"

	shape := regexp.MustCompile(sheet.NumPattern)
	for _, v := range []string{low, high} {
		require.True(t, shape.MatchString(v), "the shape regex admits %q, so the regex alone would be the whole guard", v)
		_, ok := sheet.ParseNum(v)
		require.False(t, ok, "%q carries more than 15 significant digits", v)
	}

	var distinct bool
	require.NoError(t, f.ownerDB.QueryRowContext(t.Context(),
		`SELECT ($1::text)::numeric <> ($2::text)::numeric`, low, high).Scan(&distinct))
	require.True(t, distinct, "Postgres numeric is exact and tells the pair apart")

	lowF, err := strconv.ParseFloat(low, 64)
	require.NoError(t, err)
	highF, err := strconv.ParseFloat(high, 64)
	require.NoError(t, err)
	require.Equal(t, lowF, highF, "float64 does not, which is the divergence the significance bound excludes")
}
