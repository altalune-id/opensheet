package sheet_test

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/sheet"
)

func runSnapshotConformance(t *testing.T, newStore func(*testing.T) sheet.SnapshotStore, scoped func(context.Context) context.Context) {
	t.Helper()
	newKey := func(tab string) sheet.SnapshotKey {
		return sheet.SnapshotKey{SheetID: uuid.Must(uuid.NewV7()), Tab: tab}
	}

	t.Run("miss reports not found", func(t *testing.T) {
		s := newStore(t)
		if _, ok, err := s.Get(scoped(t.Context()), newKey("Q1")); err != nil || ok {
			t.Fatalf("Get = ok:%v err:%v, want false/nil", ok, err)
		}
	})

	t.Run("put then get returns a fresh snapshot", func(t *testing.T) {
		s := newStore(t)
		ctx := scoped(t.Context())
		k := newKey("Q1")
		want := sheet.Snapshot{ETag: "abc123", FetchedAt: time.Now().UTC(), Payload: []byte(`[{"a":"1"}]`)}
		if err := s.Put(ctx, k, want, time.Minute); err != nil {
			t.Fatalf("Put: %v", err)
		}
		got, ok, err := s.Get(ctx, k)
		if err != nil || !ok {
			t.Fatalf("Get = ok:%v err:%v", ok, err)
		}
		if got.ETag != want.ETag {
			t.Errorf("ETag = %q, want %q", got.ETag, want.ETag)
		}
		if !bytes.Equal(got.Payload, want.Payload) {
			t.Errorf("Payload = %q, want %q", got.Payload, want.Payload)
		}
		if !got.ExpiresAt.After(time.Now().UTC()) {
			t.Errorf("ExpiresAt = %v, want future", got.ExpiresAt)
		}
	})

	// ReadWorkflow's stale-on-outage path depends on this exact contract.
	t.Run("an expired snapshot is a hit, not a miss", func(t *testing.T) {
		s := newStore(t)
		ctx := scoped(t.Context())
		k := newKey("Q1")
		if err := s.Put(ctx, k, sheet.Snapshot{ETag: "old", Payload: []byte("[]")}, -time.Minute); err != nil {
			t.Fatalf("Put: %v", err)
		}
		got, ok, err := s.Get(ctx, k)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if !ok {
			t.Fatal("expired snapshot treated as a miss; stale-on-failure serving becomes impossible")
		}
		if got.ExpiresAt.After(time.Now().UTC()) {
			t.Errorf("ExpiresAt = %v, want past", got.ExpiresAt)
		}
	})

	t.Run("payload bytes round-trip verbatim", func(t *testing.T) {
		s := newStore(t)
		ctx := scoped(t.Context())
		k := newKey("Q1")
		raw := []byte(`[{"z":"1","a":"2","n":"1.50"}]`)
		if err := s.Put(ctx, k, sheet.Snapshot{ETag: "e", Payload: raw}, time.Minute); err != nil {
			t.Fatalf("Put: %v", err)
		}
		got, _, err := s.Get(ctx, k)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if !bytes.Equal(got.Payload, raw) {
			t.Fatalf("Payload = %s, want %s byte-identical", got.Payload, raw)
		}
	})

	t.Run("put overwrites the same key", func(t *testing.T) {
		s := newStore(t)
		ctx := scoped(t.Context())
		k := newKey("Q1")
		_ = s.Put(ctx, k, sheet.Snapshot{ETag: "first", Payload: []byte("[1]")}, time.Minute)
		_ = s.Put(ctx, k, sheet.Snapshot{ETag: "second", Payload: []byte("[2]")}, time.Minute)
		got, _, err := s.Get(ctx, k)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.ETag != "second" {
			t.Fatalf("ETag = %q, want second", got.ETag)
		}
	})

	t.Run("PurgeSheet drops every tab of that sheet only", func(t *testing.T) {
		s := newStore(t)
		ctx := scoped(t.Context())
		id, other := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
		for _, k := range []sheet.SnapshotKey{{SheetID: id, Tab: "Q1"}, {SheetID: id, Tab: "Q2"}, {SheetID: other, Tab: "Q1"}} {
			if err := s.Put(ctx, k, sheet.Snapshot{ETag: "e", Payload: []byte("[]")}, time.Minute); err != nil {
				t.Fatalf("Put: %v", err)
			}
		}
		if err := s.PurgeSheet(ctx, id); err != nil {
			t.Fatalf("PurgeSheet: %v", err)
		}
		for _, tab := range []string{"Q1", "Q2"} {
			if _, ok, _ := s.Get(ctx, sheet.SnapshotKey{SheetID: id, Tab: tab}); ok {
				t.Errorf("tab %q survived PurgeSheet", tab)
			}
		}
		if _, ok, _ := s.Get(ctx, sheet.SnapshotKey{SheetID: other, Tab: "Q1"}); !ok {
			t.Error("PurgeSheet removed another sheet's snapshot")
		}
	})

	t.Run("PurgeSheet on an absent sheet is not an error", func(t *testing.T) {
		if err := newStore(t).PurgeSheet(scoped(t.Context()), uuid.Must(uuid.NewV7())); err != nil {
			t.Fatalf("PurgeSheet: %v", err)
		}
	})
}

func TestSnapshotStore_Memory(t *testing.T) {
	runSnapshotConformance(t,
		func(*testing.T) sheet.SnapshotStore { return sheet.NewMemorySnapshotStoreForTest(1 << 20) },
		func(ctx context.Context) context.Context { return ctx },
	)
}

func TestMemorySnapshotStore_EvictsOldestByTotalPayloadBytes(t *testing.T) {
	s := sheet.NewMemorySnapshotStoreForTest(100)
	first := sheet.SnapshotKey{SheetID: uuid.Must(uuid.NewV7()), Tab: "Q1"}
	second := sheet.SnapshotKey{SheetID: uuid.Must(uuid.NewV7()), Tab: "Q1"}

	// Budget accounts len(Payload) only: 60 + 60 > 100, so the first must go.
	_ = s.Put(t.Context(), first, sheet.Snapshot{ETag: "a", Payload: make([]byte, 60)}, time.Minute)
	_ = s.Put(t.Context(), second, sheet.Snapshot{ETag: "b", Payload: make([]byte, 60)}, time.Minute)

	if _, ok, _ := s.Get(t.Context(), first); ok {
		t.Error("oldest entry survived past the byte budget")
	}
	if _, ok, _ := s.Get(t.Context(), second); !ok {
		t.Error("newest entry was evicted")
	}
}

func TestMemorySnapshotStore_RejectsAPayloadOverTheWholeBudget(t *testing.T) {
	s := sheet.NewMemorySnapshotStoreForTest(16)
	err := s.Put(t.Context(), sheet.SnapshotKey{SheetID: uuid.Must(uuid.NewV7()), Tab: "Q1"},
		sheet.Snapshot{ETag: "a", Payload: make([]byte, 64)}, time.Minute)
	if err == nil {
		t.Fatal("Put accepted a payload larger than the entire budget")
	}
	if !sheet.IsSnapshotTooLargeError(err) {
		t.Fatalf("Put error = %T: %v, want *sheet.SnapshotTooLargeError", err, err)
	}
}

func TestMemorySnapshotStore_GetPromotesToMostRecentlyUsed(t *testing.T) {
	s := sheet.NewMemorySnapshotStoreForTest(100)
	first := sheet.SnapshotKey{SheetID: uuid.Must(uuid.NewV7()), Tab: "Q1"}
	second := sheet.SnapshotKey{SheetID: uuid.Must(uuid.NewV7()), Tab: "Q1"}
	third := sheet.SnapshotKey{SheetID: uuid.Must(uuid.NewV7()), Tab: "Q1"}

	for _, k := range []sheet.SnapshotKey{first, second} {
		if err := s.Put(t.Context(), k, sheet.Snapshot{ETag: "e", Payload: make([]byte, 40)}, time.Minute); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	if _, ok, _ := s.Get(t.Context(), first); !ok {
		t.Fatal("first entry missing before promotion")
	}
	if err := s.Put(t.Context(), third, sheet.Snapshot{ETag: "e", Payload: make([]byte, 40)}, time.Minute); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if _, ok, _ := s.Get(t.Context(), second); ok {
		t.Error("second entry survived; Get did not promote the first past it")
	}
	if _, ok, _ := s.Get(t.Context(), first); !ok {
		t.Error("promoted entry was evicted")
	}
	if _, ok, _ := s.Get(t.Context(), third); !ok {
		t.Error("newest entry was evicted")
	}
}

func TestMemorySnapshotStore_OverwriteReleasesTheOldPayloadBytes(t *testing.T) {
	s := sheet.NewMemorySnapshotStoreForTest(100)
	k := sheet.SnapshotKey{SheetID: uuid.Must(uuid.NewV7()), Tab: "Q1"}
	other := sheet.SnapshotKey{SheetID: uuid.Must(uuid.NewV7()), Tab: "Q1"}

	if err := s.Put(t.Context(), k, sheet.Snapshot{ETag: "big", Payload: make([]byte, 90)}, time.Minute); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Put(t.Context(), k, sheet.Snapshot{ETag: "small", Payload: make([]byte, 10)}, time.Minute); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Put(t.Context(), other, sheet.Snapshot{ETag: "e", Payload: make([]byte, 80)}, time.Minute); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if _, ok, _ := s.Get(t.Context(), k); !ok {
		t.Error("overwritten entry was evicted, so the old payload size was never released")
	}
	if _, ok, _ := s.Get(t.Context(), other); !ok {
		t.Error("newest entry was evicted")
	}
}

func TestMemorySnapshotStore_CachedPayloadIsIsolatedFromCallerMutation(t *testing.T) {
	s := sheet.NewMemorySnapshotStoreForTest(1 << 20)
	k := sheet.SnapshotKey{SheetID: uuid.Must(uuid.NewV7()), Tab: "Q1"}
	raw := []byte(`[{"a":"1"}]`)

	if err := s.Put(t.Context(), k, sheet.Snapshot{ETag: "e", Payload: raw}, time.Minute); err != nil {
		t.Fatalf("Put: %v", err)
	}
	raw[2] = 'X'

	got, _, err := s.Get(t.Context(), k)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got.Payload, []byte(`[{"a":"1"}]`)) {
		t.Fatalf("Payload = %s, want the bytes as they were at Put", got.Payload)
	}

	got.Payload[2] = 'Y'
	again, _, err := s.Get(t.Context(), k)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(again.Payload, []byte(`[{"a":"1"}]`)) {
		t.Fatalf("Payload = %s, want a cached copy the caller cannot edit", again.Payload)
	}
}

func TestMemorySnapshotStore_ZeroBudgetCachesNothing(t *testing.T) {
	s := sheet.NewMemorySnapshotStoreForTest(0)
	k := sheet.SnapshotKey{SheetID: uuid.Must(uuid.NewV7()), Tab: "Q1"}

	if err := s.Put(t.Context(), k, sheet.Snapshot{ETag: "e", Payload: []byte("[]")}, time.Minute); !sheet.IsSnapshotTooLargeError(err) {
		t.Fatalf("Put error = %T: %v, want *sheet.SnapshotTooLargeError", err, err)
	}
	if _, ok, _ := s.Get(t.Context(), k); ok {
		t.Error("a zero budget must cache nothing")
	}
}

func TestMemorySnapshotStore_CloseDropsEveryEntry(t *testing.T) {
	s := sheet.NewMemorySnapshotStoreForTest(1 << 20)
	k := sheet.SnapshotKey{SheetID: uuid.Must(uuid.NewV7()), Tab: "Q1"}
	if err := s.Put(t.Context(), k, sheet.Snapshot{ETag: "e", Payload: []byte("[]")}, time.Minute); err != nil {
		t.Fatalf("Put: %v", err)
	}

	closer, ok := s.(io.Closer)
	if !ok {
		t.Fatalf("memory driver is %T, want an io.Closer for kernel.AddCloser", s)
	}
	if err := closer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, ok, _ := s.Get(t.Context(), k); ok {
		t.Error("entry survived Close")
	}
	if err := s.Put(t.Context(), k, sheet.Snapshot{ETag: "e", Payload: []byte("[]")}, time.Minute); err != nil {
		t.Fatalf("Put after Close: %v", err)
	}
}

func TestNewSnapshotStore_Dispatch(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	pc := tenant.NewPgConn(&sql.DB{})

	tests := []struct {
		name     string
		cache    config.CacheConfig
		dbDriver db.Driver
		pc       *tenant.PgConn
		wantMem  bool
	}{
		{name: "auto on sqlite is memory", cache: config.CacheConfig{Driver: config.CacheDriverAuto}, dbDriver: db.DriverSQLite, wantMem: true},
		{name: "empty driver on sqlite is memory", dbDriver: db.DriverSQLite, wantMem: true},
		{name: "memory is honoured on postgres", cache: config.CacheConfig{Driver: config.CacheDriverMemory}, dbDriver: db.DriverPostgres, pc: pc, wantMem: true},
		{name: "auto on postgres is postgres", cache: config.CacheConfig{Driver: config.CacheDriverAuto}, dbDriver: db.DriverPostgres, pc: pc},
		{name: "postgres is honoured on postgres", cache: config.CacheConfig{Driver: config.CacheDriverPostgres}, dbDriver: db.DriverPostgres, pc: pc},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.cache.MaxBytes = 1 << 20
			got, err := sheet.NewSnapshotStore(tc.cache, db.DBConfig{Driver: tc.dbDriver}, tc.pc, log)
			if err != nil {
				t.Fatalf("NewSnapshotStore: %v", err)
			}
			if got == nil {
				t.Fatal("NewSnapshotStore = nil, want a store")
			}
			_, isCloser := got.(io.Closer)
			if isCloser != tc.wantMem {
				t.Errorf("store %T implements io.Closer = %v, want %v", got, isCloser, tc.wantMem)
			}
		})
	}
}

func TestNewSnapshotStore_PostgresWithoutTenantConnectionErrors(t *testing.T) {
	got, err := sheet.NewSnapshotStore(
		config.CacheConfig{Driver: config.CacheDriverPostgres, MaxBytes: 1 << 20},
		db.DBConfig{Driver: db.DriverPostgres},
		nil, slog.New(slog.DiscardHandler),
	)
	if !sheet.IsCacheUnavailableError(err) {
		t.Fatalf("NewSnapshotStore error = %T: %v, want *sheet.CacheUnavailableError", err, err)
	}
	if got != nil {
		t.Errorf("NewSnapshotStore = %T, want nil on error", got)
	}
}

func TestSnapshotTooLargeError(t *testing.T) {
	err := &sheet.SnapshotTooLargeError{Bytes: 64, MaxBytes: 16}

	if got, want := err.Error(), "sheet: snapshot payload: 64 bytes over the 16 byte cache budget"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if !sheet.IsSnapshotTooLargeError(err) {
		t.Error("IsSnapshotTooLargeError = false, want true")
	}
	if sheet.IsSnapshotTooLargeError(nil) {
		t.Error("IsSnapshotTooLargeError(nil) = true, want false")
	}

	app := err.ToAppError()
	if app.Code() != apperror.CodeSheetPayloadTooLarge {
		t.Errorf("Code() = %q, want %q", app.Code(), apperror.CodeSheetPayloadTooLarge)
	}
	if app.HTTPStatus() != 413 {
		t.Errorf("HTTPStatus() = %d, want 413", app.HTTPStatus())
	}
}

func TestSnapshotCacheUnavailableError(t *testing.T) {
	err := &sheet.CacheUnavailableError{Driver: "postgres", Reason: "no tenant-scoped connection"}

	if got, want := err.Error(), "sheet: snapshot cache driver postgres: unavailable: no tenant-scoped connection"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if !sheet.IsCacheUnavailableError(err) {
		t.Error("IsCacheUnavailableError = false, want true")
	}
	if sheet.IsCacheUnavailableError(nil) {
		t.Error("IsCacheUnavailableError(nil) = true, want false")
	}

	app := err.ToAppError()
	if app.Code() != apperror.CodeUnexpectedError {
		t.Errorf("Code() = %q, want %q", app.Code(), apperror.CodeUnexpectedError)
	}
}
