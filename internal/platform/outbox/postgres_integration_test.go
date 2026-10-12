//go:build integration

package outbox_test

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/outbox"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/pgtest"
	"altalune.id/opensheet/schema"
)

func newPostgresStore(t *testing.T) (outbox.Store, context.Context, tenant.Context) {
	t.Helper()
	h := pgtest.New(t)
	sqlDB := h.OpenDB(t)

	cfg := config.Defaults()
	cfg.DB.Driver = db.DriverPostgres
	cfg.DB.DSN = h.DSN
	cfg.DB.Schema = h.Schema
	cfg.DB.AllowBypassRLS = true
	require.NoError(t, schema.MigrateUp(t.Context(), sqlDB, cfg))

	tc := seedPgTenant(t, sqlDB, cfg.DB.TablePrefix)
	store := outbox.NewStore(
		db.DBConfig{Driver: db.DriverPostgres, Schema: h.Schema, TablePrefix: cfg.DB.TablePrefix},
		db.Pool{W: sqlDB, R: sqlDB},
		tenant.NewPgConn(sqlDB),
	)
	return store, tenant.Into(t.Context(), tc), tc
}

func seedPgTenant(t *testing.T, sqlDB *sql.DB, tablePrefix string) tenant.Context {
	t.Helper()
	userID, orgID, projID := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC()
	_, err := sqlDB.ExecContext(t.Context(),
		"INSERT INTO "+tablePrefix+"users (id, email, name, avatar_url, is_admin, created_at, updated_at) "+
			"VALUES ($1, $2, '', '', false, $3, $3)",
		userID, userID.String()+"@example.com", now)
	require.NoError(t, err)
	_, err = sqlDB.ExecContext(t.Context(),
		"INSERT INTO "+tablePrefix+"orgs (id, slug, name, created_by, created_at, updated_at) "+
			"VALUES ($1, $2, 'Org', $3, $4, $4)",
		orgID, orgID.String()[:8], userID, now)
	require.NoError(t, err)
	_, err = sqlDB.ExecContext(t.Context(),
		"INSERT INTO "+tablePrefix+"projects (id, org_id, slug, name, created_by, created_at, updated_at) "+
			"VALUES ($1, $2, $3, 'Web', $4, $5, $5)",
		projID, orgID, projID.String()[:8], userID, now)
	require.NoError(t, err)
	return tenant.Context{OrgID: orgID, ProjectID: projID, UserID: userID}
}

// NOTE: two replicas must never claim one entry; FOR UPDATE SKIP LOCKED is what holds this on Postgres.
func TestPostgresClaimDueIsExclusive(t *testing.T) {
	s, ctx, tc := newPostgresStore(t)
	require.NoError(t, s.Enqueue(ctx, entry(tc, uuid.New())))

	var a, b []outbox.Entry
	var errA, errB error
	var wg sync.WaitGroup
	wg.Go(func() { a, errA = s.ClaimDue(ctx, time.Now(), 10) })
	wg.Go(func() { b, errB = s.ClaimDue(ctx, time.Now(), 10) })
	wg.Wait()

	require.NoError(t, errA)
	require.NoError(t, errB)
	if len(a)+len(b) != 1 {
		t.Fatalf("claimed %d entries across two callers, want exactly 1", len(a)+len(b))
	}
}

func TestPostgresClaimDueSplitsABatchWithoutOverlap(t *testing.T) {
	s, ctx, tc := newPostgresStore(t)
	const total = 20
	for range total {
		require.NoError(t, s.Enqueue(ctx, entry(tc, uuid.New())))
	}

	const workers = 4
	claimed := make([][]outbox.Entry, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Go(func() { claimed[i], errs[i] = s.ClaimDue(ctx, time.Now(), total) })
	}
	wg.Wait()

	seen := map[uuid.UUID]int{}
	got := 0
	for i := range workers {
		require.NoError(t, errs[i])
		for _, e := range claimed[i] {
			seen[e.ID]++
			got++
		}
	}
	assert.Equal(t, total, got)
	for id, n := range seen {
		assert.Equal(t, 1, n, "entry %s claimed %d times", id, n)
	}
}

func TestPostgresEnqueueIsIdempotentPerEventAndTarget(t *testing.T) {
	s, ctx, tc := newPostgresStore(t)
	eventID := uuid.New()
	first := entry(tc, eventID)
	require.NoError(t, s.Enqueue(ctx, first))
	require.NoError(t, s.Enqueue(ctx, entry(tc, eventID)))

	due, err := s.ClaimDue(ctx, time.Now(), 10)
	require.NoError(t, err)
	require.Len(t, due, 1, "a redelivered domain event became two deliveries")
	assert.Equal(t, first.ID, due[0].ID)
}

func TestPostgresFailSchedulesRetryAndSettlesWhenAttemptsRunOut(t *testing.T) {
	s, ctx, tc := newPostgresStore(t)
	e := entry(tc, uuid.New())
	require.NoError(t, s.Enqueue(ctx, e))

	retryAt := time.Now().Add(time.Minute).UTC()
	require.NoError(t, s.Fail(ctx, e, retryAt, "502 from endpoint"))
	early, err := s.ClaimDue(ctx, time.Now(), 10)
	require.NoError(t, err)
	require.Empty(t, early, "a failed entry was claimable before its retry time")

	at := retryAt.Add(time.Second)
	for i := range outbox.MaxAttempts {
		claimed, cErr := s.ClaimDue(ctx, at, 10)
		require.NoError(t, cErr, "attempt %d", i+1)
		require.Len(t, claimed, 1, "attempt %d", i+1)
		if i == 0 {
			assert.Equal(t, "502 from endpoint", claimed[0].LastError)
		}
		require.NoError(t, s.Fail(ctx, claimed[0], at, "endpoint keeps refusing"))
	}

	settled, err := s.ClaimDue(ctx, at.Add(time.Hour), 10)
	require.NoError(t, err)
	assert.Empty(t, settled, "a permanently failing entry is still spinning")
	assert.True(t, outbox.IsTerminalStateError(s.Fail(ctx, e, at, "again")))
}

func TestPostgresSucceedSettlesTheEntry(t *testing.T) {
	s, ctx, tc := newPostgresStore(t)
	e := entry(tc, uuid.New())
	require.NoError(t, s.Enqueue(ctx, e))

	require.NoError(t, s.Succeed(ctx, e, time.Now()))
	none, err := s.ClaimDue(ctx, time.Now().Add(time.Hour), 10)
	require.NoError(t, err)
	assert.Empty(t, none)
	assert.True(t, outbox.IsTerminalStateError(s.Fail(ctx, e, time.Now(), "late")))
	assert.True(t, outbox.IsNotFoundError(s.Succeed(ctx, outbox.Entry{ID: uuid.New()}, time.Now())))
}

// TestPostgresClaimDueIgnoresAnotherOrgsEntries proves RLS plus the explicit org predicate keep an entry invisible outside the org that owns it.
func TestPostgresClaimDueIgnoresAnotherOrgsEntries(t *testing.T) {
	s, ctx, tc := newPostgresStore(t)
	require.NoError(t, s.Enqueue(ctx, entry(tc, uuid.New())))

	other := tenant.Into(t.Context(), tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New()})
	claimed, err := s.ClaimDue(other, time.Now(), 10)
	require.NoError(t, err)
	assert.Empty(t, claimed, "an entry leaked across the tenant boundary")

	mine, err := s.ClaimDue(ctx, time.Now(), 10)
	require.NoError(t, err)
	assert.Len(t, mine, 1)
}

// TestPostgresClaimDueLeavesAnInFlightFinalAttemptAlone proves the reap waits for the lease, so a sweep during the last delivery cannot settle an entry another dispatcher is still working.
func TestPostgresClaimDueLeavesAnInFlightFinalAttemptAlone(t *testing.T) {
	s, ctx, tc := newPostgresStore(t)
	require.NoError(t, s.Enqueue(ctx, entry(tc, uuid.New())))

	at := time.Now().UTC()
	var last outbox.Entry
	for i := 1; i <= outbox.MaxAttempts; i++ {
		claimed, err := s.ClaimDue(ctx, at, 10)
		require.NoError(t, err, "attempt %d", i)
		require.Len(t, claimed, 1, "attempt %d", i)
		last = claimed[0]
		at = at.Add(outbox.ClaimLease + time.Second)
	}
	require.Equal(t, outbox.MaxAttempts, last.Attempt)

	_, err := s.ClaimDue(ctx, last.NextAttemptAt.Add(-time.Minute), 10)
	require.NoError(t, err)

	require.NoError(t, s.Succeed(ctx, last, time.Now()),
		"a sweep inside the lease reaped an entry whose final delivery was still in flight")
}

// TestPostgresSettleFromAStaleClaimIsRefused proves the settle is fenced on the claimed attempt.
func TestPostgresSettleFromAStaleClaimIsRefused(t *testing.T) {
	s, ctx, tc := newPostgresStore(t)
	require.NoError(t, s.Enqueue(ctx, entry(tc, uuid.New())))

	now := time.Now().UTC()
	first, err := s.ClaimDue(ctx, now, 10)
	require.NoError(t, err)
	require.Len(t, first, 1)

	later := now.Add(outbox.ClaimLease + time.Second)
	second, err := s.ClaimDue(ctx, later, 10)
	require.NoError(t, err)
	require.Len(t, second, 1)
	require.Equal(t, 2, second[0].Attempt)

	staleErr := s.Fail(ctx, first[0], later.Add(2*time.Second), "A finished after its lease")
	require.Error(t, staleErr, "a settle from an expired claim was accepted")
	assert.True(t, outbox.IsStaleClaimError(staleErr), "got %v", staleErr)

	none, err := s.ClaimDue(ctx, later.Add(time.Minute), 10)
	require.NoError(t, err)
	assert.Empty(t, none, "a stale settle released the live lease and manufactured a third delivery")
}

// TestPostgresFailTruncatesACauseOnARuneBoundary proves Postgres accepts an over-long cause; a cut mid-rune is rejected as an invalid UTF-8 byte sequence and loses the delivery outcome entirely.
func TestPostgresFailTruncatesACauseOnARuneBoundary(t *testing.T) {
	s, ctx, tc := newPostgresStore(t)
	require.NoError(t, s.Enqueue(ctx, entry(tc, uuid.New())))

	now := time.Now().UTC()
	claimed, err := s.ClaimDue(ctx, now, 10)
	require.NoError(t, err)
	require.Len(t, claimed, 1)

	cause := strings.Repeat("a", outbox.MaxCauseLen-1) + "é" + strings.Repeat("b", 64)
	require.False(t, utf8.ValidString(cause[:outbox.MaxCauseLen]), "the fixture does not straddle the cut")
	require.NoError(t, s.Fail(ctx, claimed[0], now, cause))

	after, err := s.ClaimDue(ctx, now.Add(outbox.ClaimLease+time.Second), 10)
	require.NoError(t, err)
	require.Len(t, after, 1)
	assert.True(t, utf8.ValidString(after[0].LastError))
	assert.LessOrEqual(t, len(after[0].LastError), outbox.MaxCauseLen)
}

// TestPostgresEnqueueRejectsAnotherProjectsEntry proves the project scope is checked in Go; the FK runs as the table owner and bypasses RLS, so the database would accept another org's project.
func TestPostgresEnqueueRejectsAnotherProjectsEntry(t *testing.T) {
	s, ctx, tc := newPostgresStore(t)
	e := entry(tc, uuid.New())
	e.ProjectID = uuid.New()

	err := s.Enqueue(ctx, e)
	require.Error(t, err)
	assert.True(t, outbox.IsInvalidEntryError(err), "got %v", err)
}

func TestPostgresRequeueResetsAFailedEntry(t *testing.T) {
	s, ctx, tc := newPostgresStore(t)
	e := entry(tc, uuid.New())
	require.NoError(t, s.Enqueue(ctx, e))
	failToTerminal(ctx, t, s, e)

	require.NoError(t, s.Requeue(ctx, e.ID, e.Target))

	listed, err := s.ListByTarget(ctx, e.Target, outbox.MaxListLimit)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, outbox.StatusPending, listed[0].Status)
	assert.Equal(t, 0, listed[0].Attempt)
	assert.Empty(t, listed[0].LastError)

	claimed, err := s.ClaimDue(ctx, time.Now(), 10)
	require.NoError(t, err)
	require.Len(t, claimed, 1, "a requeued entry was not claimable")
	assert.Equal(t, e.ID, claimed[0].ID)
}

func TestPostgresRequeueWithTheWrongTargetIsNotFound(t *testing.T) {
	s, ctx, tc := newPostgresStore(t)
	e := entry(tc, uuid.New())
	require.NoError(t, s.Enqueue(ctx, e))
	failToTerminal(ctx, t, s, e)

	err := s.Requeue(ctx, e.ID, "another-endpoint")
	require.Error(t, err)
	assert.True(t, outbox.IsNotFoundError(err), "got %v", err)
}

func TestPostgresRequeueOnANonFailedRowIsRefused(t *testing.T) {
	t.Run("pending", func(t *testing.T) {
		s, ctx, tc := newPostgresStore(t)
		e := entry(tc, uuid.New())
		require.NoError(t, s.Enqueue(ctx, e))

		err := s.Requeue(ctx, e.ID, e.Target)
		require.Error(t, err)
		assert.True(t, outbox.IsNotFailedError(err), "got %v", err)
	})

	t.Run("delivered", func(t *testing.T) {
		s, ctx, tc := newPostgresStore(t)
		e := entry(tc, uuid.New())
		require.NoError(t, s.Enqueue(ctx, e))
		claimed, err := s.ClaimDue(ctx, time.Now(), 10)
		require.NoError(t, err)
		require.Len(t, claimed, 1)
		require.NoError(t, s.Succeed(ctx, claimed[0], time.Now()))

		err = s.Requeue(ctx, e.ID, e.Target)
		require.Error(t, err)
		assert.True(t, outbox.IsNotFailedError(err), "got %v", err)
	})
}

// TestPostgresRequeueIgnoresAnotherOrgsEntry proves RLS plus the explicit org predicate keep a failed entry invisible outside the org that owns it.
func TestPostgresRequeueIgnoresAnotherOrgsEntry(t *testing.T) {
	s, ctx, tc := newPostgresStore(t)
	e := entry(tc, uuid.New())
	require.NoError(t, s.Enqueue(ctx, e))
	failToTerminal(ctx, t, s, e)

	other := tenant.Into(t.Context(), tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New()})
	err := s.Requeue(other, e.ID, e.Target)
	require.Error(t, err)
	assert.True(t, outbox.IsNotFoundError(err), "an entry was requeued across the tenant boundary")
}

func TestPostgresRequeueFailedRequeuesOnlyFailedRowsOfOneTargetInOneOrg(t *testing.T) {
	s, ctx, tc := newPostgresStore(t)

	failedA := entry(tc, uuid.New())
	require.NoError(t, s.Enqueue(ctx, failedA))
	failToTerminal(ctx, t, s, failedA)

	failedB := entry(tc, uuid.New())
	require.NoError(t, s.Enqueue(ctx, failedB))
	failToTerminal(ctx, t, s, failedB)

	otherTarget := entry(tc, uuid.New())
	otherTarget.Target = "another-endpoint"
	require.NoError(t, s.Enqueue(ctx, otherTarget))
	failToTerminal(ctx, t, s, otherTarget)

	stillPending := entry(tc, uuid.New())
	require.NoError(t, s.Enqueue(ctx, stillPending))

	n, err := s.RequeueFailed(ctx, failedA.Target)
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	claimed, err := s.ClaimDue(ctx, time.Now(), 10)
	require.NoError(t, err)
	ids := map[uuid.UUID]bool{}
	for _, c := range claimed {
		ids[c.ID] = true
	}
	assert.True(t, ids[failedA.ID])
	assert.True(t, ids[failedB.ID])
	assert.True(t, ids[stillPending.ID])
	assert.False(t, ids[otherTarget.ID], "RequeueFailed touched another target's row")

	other := tenant.Into(t.Context(), tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New()})
	n, err = s.RequeueFailed(other, otherTarget.Target)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "RequeueFailed touched another org's row")
}

func TestPostgresListByTargetOrdersNewestFirstAndIsOrgScoped(t *testing.T) {
	s, ctx, tc := newPostgresStore(t)
	ids := make([]uuid.UUID, 0, 5)
	for range 5 {
		e := entry(tc, uuid.New())
		require.NoError(t, s.Enqueue(ctx, e))
		ids = append(ids, e.ID)
		time.Sleep(time.Millisecond)
	}

	list, err := s.ListByTarget(ctx, "delivery-endpoint", outbox.MaxListLimit)
	require.NoError(t, err)
	require.Len(t, list, 5)
	for i := range list {
		assert.Equal(t, ids[len(ids)-1-i], list[i].ID, "position %d not newest-first", i)
		assert.False(t, list[i].CreatedAt.IsZero(), "CreatedAt was not filled")
	}

	limited, err := s.ListByTarget(ctx, "delivery-endpoint", 2)
	require.NoError(t, err)
	require.Len(t, limited, 2)
	assert.Equal(t, ids[4], limited[0].ID)
	assert.Equal(t, ids[3], limited[1].ID)

	overLimit, err := s.ListByTarget(ctx, "delivery-endpoint", 1000)
	require.NoError(t, err)
	assert.Len(t, overLimit, 5, "a limit above MaxListLimit must still be accepted, just clamped")

	other := tenant.Into(t.Context(), tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New()})
	leaked, err := s.ListByTarget(other, "delivery-endpoint", outbox.MaxListLimit)
	require.NoError(t, err)
	assert.Empty(t, leaked, "ListByTarget leaked another org's rows")
}

func TestPostgresByIDReturnsTheEntryOfItsTarget(t *testing.T) {
	s, ctx, tc := newPostgresStore(t)
	e := entry(tc, uuid.New())
	require.NoError(t, s.Enqueue(ctx, e))

	got, err := s.ByID(ctx, e.ID, e.Target)
	require.NoError(t, err)
	assert.Equal(t, e.ID, got.ID)
	assert.Equal(t, e.EventID, got.EventID)
	assert.Equal(t, e.Payload, got.Payload)
	assert.Equal(t, outbox.StatusPending, got.Status)
	assert.False(t, got.CreatedAt.IsZero(), "CreatedAt was not filled")
}

func TestPostgresByIDIsScopedToOrgAndTarget(t *testing.T) {
	s, ctx, tc := newPostgresStore(t)
	e := entry(tc, uuid.New())
	require.NoError(t, s.Enqueue(ctx, e))

	_, err := s.ByID(ctx, uuid.New(), e.Target)
	assert.True(t, outbox.IsNotFoundError(err), "unknown id: got %v", err)

	_, err = s.ByID(ctx, e.ID, "another-endpoint")
	assert.True(t, outbox.IsNotFoundError(err), "another target: got %v", err)

	other := tenant.Into(t.Context(), tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New()})
	_, err = s.ByID(other, e.ID, e.Target)
	assert.True(t, outbox.IsNotFoundError(err), "another org: got %v", err)
}
