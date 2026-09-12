//go:build integration

package sheet_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/sheet"
)

// NOTE: sheet_snapshots.sheet_id is an FK, and the shared conformance suite mints its own sheet ids, so the driver under test seeds a parent sheets row before each Put.
type seedingSnapshotStore struct {
	sheet.SnapshotStore

	t      *testing.T
	f      *pgFixture
	tree   orgTree
	mu     sync.Mutex
	seeded map[uuid.UUID]struct{}
}

func (s *seedingSnapshotStore) Put(ctx context.Context, k sheet.SnapshotKey, snap sheet.Snapshot, ttl time.Duration) error {
	s.seedParent(k.SheetID)
	return s.SnapshotStore.Put(ctx, k, snap, ttl)
}

func (s *seedingSnapshotStore) seedParent(sheetID uuid.UUID) {
	s.t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.seeded[sheetID]; ok {
		return
	}
	pgSeedSheetRow(s.t, s.f, s.tree, sheetID)
	s.seeded[sheetID] = struct{}{}
}

func pgSeedSheetRow(t *testing.T, f *pgFixture, tree orgTree, sheetID uuid.UUID) {
	t.Helper()
	now := time.Now().UTC()
	_, err := f.ownerDB.ExecContext(t.Context(),
		"INSERT INTO "+f.prefix+"sheets (id, org_id, project_id, spreadsheet_id, tab, slug, visibility, cache_ttl_secs, created_at, updated_at) "+
			"VALUES ($1,$2,$3,$4,'Q1',$5,'key',0,$6,$6)",
		sheetID, tree.orgID, tree.projectID, tree.spreadsheetID, "s-"+sheetID.String(), now)
	require.NoError(t, err)
}

func TestPostgres_SnapshotStore_Conformance(t *testing.T) {
	f := newPgFixture(t)
	runSnapshotConformance(t,
		func(t *testing.T) sheet.SnapshotStore {
			return &seedingSnapshotStore{
				SnapshotStore: f.snapshots,
				t:             t,
				f:             f,
				tree:          f.a,
				seeded:        map[uuid.UUID]struct{}{},
			}
		},
		func(ctx context.Context) context.Context {
			return tenant.Into(ctx, tenant.Context{
				OrgID: f.a.orgID, ProjectID: f.a.projectID, UserID: f.a.userID,
			})
		},
	)
}

// TestPostgres_SnapshotStore_AnotherOrgSeesNothing asserts RLS hides one org's snapshots from another under a NOBYPASSRLS role.
func TestPostgres_SnapshotStore_AnotherOrgSeesNothing(t *testing.T) {
	f := newPgFixture(t)
	ctxA, ctxB := f.a.ctx(t), f.b.ctx(t)

	sheetID := uuid.Must(uuid.NewV7())
	pgSeedSheetRow(t, f, f.a, sheetID)
	k := sheet.SnapshotKey{SheetID: sheetID, Tab: "Q1"}
	payload := []byte(`[{"a":"1"}]`)
	require.NoError(t, f.snapshots.Put(ctxA, k, sheet.Snapshot{ETag: "e", FetchedAt: time.Now().UTC(), Payload: payload}, time.Minute))

	_, ok, err := f.snapshots.Get(ctxB, k)
	require.NoError(t, err)
	assert.False(t, ok, "org B must not read org A's snapshot")

	require.NoError(t, f.snapshots.PurgeSheet(ctxB, sheetID))

	got, ok, err := f.snapshots.Get(ctxA, k)
	require.NoError(t, err)
	require.True(t, ok, "org A lost its snapshot to a cross-org PurgeSheet")
	assert.Equal(t, payload, got.Payload)

	var unscoped int
	require.NoError(t, f.appDB.QueryRowContext(t.Context(),
		"SELECT count(*) FROM public."+f.prefix+"sheet_snapshots").Scan(&unscoped))
	assert.Zero(t, unscoped, "an unscoped read must return zero rows under FORCE ROW LEVEL SECURITY")
}

func TestPostgres_SnapshotStore_RequiresTenantScope(t *testing.T) {
	f := newPgFixture(t)
	k := sheet.SnapshotKey{SheetID: uuid.Must(uuid.NewV7()), Tab: "Q1"}

	_, _, err := f.snapshots.Get(t.Context(), k)
	assert.True(t, tenant.IsMissingError(err), "Get want MissingError, got %T: %v", err, err)

	err = f.snapshots.Put(t.Context(), k, sheet.Snapshot{ETag: "e", Payload: []byte("[]")}, time.Minute)
	assert.True(t, tenant.IsMissingError(err), "Put want MissingError, got %T: %v", err, err)

	assert.True(t, tenant.IsMissingError(f.snapshots.PurgeSheet(t.Context(), k.SheetID)))
}

func TestPostgres_SnapshotStore_DeletingTheSheetCascades(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)

	sh, err := sheet.New(sheet.NewParams{OrgID: f.a.orgID, ProjectID: f.a.projectID, SpreadsheetID: f.a.spreadsheetID, Tab: "Q1", Slug: "prices", Visibility: sheet.VisibilityKey, CacheTTL: 0})
	require.NoError(t, err)
	require.NoError(t, f.store.Save(ctx, sh))

	k := sheet.SnapshotKey{SheetID: sh.ID, Tab: "Q1"}
	require.NoError(t, f.snapshots.Put(ctx, k, sheet.Snapshot{ETag: "e", FetchedAt: time.Now().UTC(), Payload: []byte("[]")}, time.Minute))
	require.NoError(t, f.store.Delete(ctx, sh.ID))

	_, ok, err := f.snapshots.Get(ctx, k)
	require.NoError(t, err)
	assert.False(t, ok, "deleting the sheet must cascade to its snapshots")
}
