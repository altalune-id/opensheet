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

// NOTE: sheet_write_attempts.sheet_id is an FK and the shared contract suite mints its own sheet ids, so the driver under test seeds a parent sheets row before each Reserve.
type seedingIdempotencyStore struct {
	sheet.IdempotencyStore

	t      *testing.T
	f      *pgFixture
	tree   orgTree
	mu     sync.Mutex
	seeded map[uuid.UUID]struct{}
}

func (s *seedingIdempotencyStore) Reserve(
	ctx context.Context, k sheet.IdempotencyKey, bodyHash string, ttl time.Duration,
) (bool, sheet.Attempt, error) {
	s.seedParent(k.SheetID)
	return s.IdempotencyStore.Reserve(ctx, k, bodyHash, ttl)
}

func (s *seedingIdempotencyStore) seedParent(sheetID uuid.UUID) {
	s.t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.seeded[sheetID]; ok {
		return
	}
	pgSeedSheetRow(s.t, s.f, s.tree, sheetID)
	s.seeded[sheetID] = struct{}{}
}

func TestPgIdempotencyStore_Contract(t *testing.T) {
	f := newPgFixture(t)
	runIdempotencyContract(t,
		func(t *testing.T) sheet.IdempotencyStore {
			return &seedingIdempotencyStore{
				IdempotencyStore: f.attempts,
				t:                t,
				f:                f,
				tree:             f.a,
				seeded:           map[uuid.UUID]struct{}{},
			}
		},
		func(ctx context.Context) context.Context {
			return tenant.Into(ctx, tenant.Context{
				OrgID: f.a.orgID, ProjectID: f.a.projectID, UserID: f.a.userID,
			})
		},
	)
}

// TestPgIdempotencyStore_ExactlyOneConcurrentCallerClaims is the test the whole design rests on: only a real database proves that two simultaneous retries cannot both append.
func TestPgIdempotencyStore_ExactlyOneConcurrentCallerClaims(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)
	sheetID := uuid.Must(uuid.NewV7())
	pgSeedSheetRow(t, f, f.a, sheetID)
	k := sheet.IdempotencyKey{SheetID: sheetID, Tab: "Rates", Key: "idem-1"}

	const callers = 4
	type result struct {
		claimed bool
		err     error
	}
	results := make(chan result, callers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range callers {
		wg.Go(func() {
			<-start
			claimed, _, err := f.attempts.Reserve(ctx, k, "hash-a", sheet.IdempotencyTTL)
			results <- result{claimed: claimed, err: err}
		})
	}
	close(start)
	wg.Wait()
	close(results)

	claims := 0
	for r := range results {
		require.NoError(t, r.err)
		if r.claimed {
			claims++
		}
	}
	assert.Equal(t, 1, claims, "exactly one of %d concurrent callers may claim the reservation", callers)
}

// TestPgIdempotencyStore_AnotherOrgSeesNothing asserts RLS hides one org's attempts from another under a NOBYPASSRLS role.
func TestPgIdempotencyStore_AnotherOrgSeesNothing(t *testing.T) {
	f := newPgFixture(t)
	ctxA, ctxB := f.a.ctx(t), f.b.ctx(t)

	sheetID := uuid.Must(uuid.NewV7())
	pgSeedSheetRow(t, f, f.a, sheetID)
	k := sheet.IdempotencyKey{SheetID: sheetID, Tab: "Rates", Key: "idem-1"}

	claimed, _, err := f.attempts.Reserve(ctxA, k, "hash-a", sheet.IdempotencyTTL)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, f.attempts.Complete(ctxA, k, []byte(`{"appended":1}`), sheet.IdempotencyTTL))

	require.NoError(t, f.attempts.Release(ctxB, k))
	_, prior, err := f.attempts.Reserve(ctxA, k, "hash-a", sheet.IdempotencyTTL)
	require.NoError(t, err)
	require.True(t, prior.Done, "org A lost its attempt to a cross-org Release")

	var unscoped int
	require.NoError(t, f.appDB.QueryRowContext(t.Context(),
		"SELECT count(*) FROM public."+f.prefix+"sheet_write_attempts").Scan(&unscoped))
	assert.Zero(t, unscoped, "an unscoped read must return zero rows under FORCE ROW LEVEL SECURITY")
}

func TestPgIdempotencyStore_RequiresTenantScope(t *testing.T) {
	f := newPgFixture(t)
	k := sheet.IdempotencyKey{SheetID: uuid.Must(uuid.NewV7()), Tab: "Rates", Key: "idem-1"}

	_, _, err := f.attempts.Reserve(t.Context(), k, "hash-a", sheet.IdempotencyTTL)
	assert.True(t, tenant.IsMissingError(err), "Reserve want MissingError, got %T: %v", err, err)

	assert.True(t, tenant.IsMissingError(f.attempts.Complete(t.Context(), k, []byte("{}"), sheet.IdempotencyTTL)))
	assert.True(t, tenant.IsMissingError(f.attempts.Release(t.Context(), k)))

	_, err = f.attempts.DeleteExpired(t.Context())
	assert.True(t, tenant.IsMissingError(err), "DeleteExpired want MissingError, got %T: %v", err, err)
}

func TestPgIdempotencyStore_DeletingTheSheetCascades(t *testing.T) {
	f := newPgFixture(t)
	ctx := f.a.ctx(t)

	sh, err := sheet.New(sheet.NewParams{
		OrgID: f.a.orgID, ProjectID: f.a.projectID, SpreadsheetID: f.a.spreadsheetID,
		Tab: "Rates", Slug: "rates", Visibility: sheet.VisibilityKey, CacheTTL: 0,
	})
	require.NoError(t, err)
	require.NoError(t, f.store.Save(ctx, sh))

	k := sheet.IdempotencyKey{SheetID: sh.ID, Tab: "Rates", Key: "idem-1"}
	_, _, err = f.attempts.Reserve(ctx, k, "hash-a", sheet.IdempotencyTTL)
	require.NoError(t, err)
	require.NoError(t, f.store.Delete(ctx, sh.ID))

	var remaining int
	require.NoError(t, f.ownerDB.QueryRowContext(t.Context(),
		"SELECT count(*) FROM public."+f.prefix+"sheet_write_attempts WHERE sheet_id = $1", sh.ID).Scan(&remaining))
	assert.Zero(t, remaining, "deleting the sheet must cascade to its write attempts")
}
