package apikey_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/fakes"
)

func newUsageFixture(t *testing.T) (*apikey.UsageWorker, *fakes.APIKey, tenant.Context) {
	t.Helper()
	store := fakes.NewAPIKey()
	tc := tenant.Context{OrgID: uuid.Must(uuid.NewV7()), ProjectID: uuid.Must(uuid.NewV7())}
	return apikey.NewUsageWorker(store, slog.New(slog.DiscardHandler), time.Now), store, tc
}

func seedUsageKey(t *testing.T, store *fakes.APIKey, tc tenant.Context) *apikey.APIKey {
	t.Helper()
	k, _ := mintFor(t, tc, []string{authn.ScopeSheetsRead}, nil, nil)
	if err := store.Save(t.Context(), k); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return k
}

func lastUsed(t *testing.T, store *fakes.APIKey, id uuid.UUID) *time.Time {
	t.Helper()
	k, err := store.ByID(t.Context(), id)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	return k.LastUsedAt
}

func startUsageWorker(t *testing.T, w *apikey.UsageWorker) (context.CancelFunc, chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	stopped := make(chan struct{})
	go func() {
		done <- w.Run(ctx)
		close(stopped)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(5 * time.Second):
			t.Error("Run did not return after cancel")
		}
	})
	return cancel, done
}

func TestUsageWorker_Name(t *testing.T) {
	w, _, _ := newUsageFixture(t)
	if got := w.Name(); got != "apikey-lastused" {
		t.Errorf("Name = %q, want apikey-lastused", got)
	}
}

func TestUsageWorker_CoalescesToTheNewestTimestampPerKey(t *testing.T) {
	w, store, tc := newUsageFixture(t)
	first := seedUsageKey(t, store, tc)
	second := seedUsageKey(t, store, tc)

	base := time.Now().UTC().Truncate(time.Second)
	newest := base.Add(3 * time.Minute)
	for _, at := range []time.Time{base.Add(time.Minute), newest, base} {
		w.Record(tc.OrgID, tc.ProjectID, first.ID, at)
	}
	w.Record(tc.OrgID, tc.ProjectID, second.ID, base)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := w.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got := lastUsed(t, store, first.ID)
	if got == nil || !got.Equal(newest) {
		t.Errorf("LastUsedAt = %v, want the newest sample %v", got, newest)
	}
	if other := lastUsed(t, store, second.ID); other == nil || !other.Equal(base) {
		t.Errorf("the second key's LastUsedAt = %v, want %v", other, base)
	}
}

func TestUsageWorker_RecordNeverBlocksAndCountsDrops(t *testing.T) {
	w := apikey.NewUsageWorker(fakes.NewAPIKey(), slog.New(slog.DiscardHandler), time.Now)
	org, proj, id := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	done := make(chan struct{})
	go func() {
		for range 8192 {
			w.Record(org, proj, id, time.Now())
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Record blocked once the buffer filled; the auth path must never block")
	}
	if w.Dropped() == 0 {
		t.Fatal("Dropped() = 0 after overflowing the buffer")
	}
}

func TestUsageWorker_FlushesOnceMoreAtShutdown(t *testing.T) {
	w, store, tc := newUsageFixture(t)
	k := seedUsageKey(t, store, tc)
	at := time.Now().UTC().Truncate(time.Second)

	cancel, done := startUsageWorker(t, w)
	w.Record(tc.OrgID, tc.ProjectID, k.ID, at)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	got := lastUsed(t, store, k.ID)
	if got == nil || !got.Equal(at) {
		t.Fatalf("LastUsedAt = %v, want %v — SIGTERM lost the last window", got, at)
	}
}

func TestUsageWorker_RunReturnsOnContextCancel(t *testing.T) {
	w, _, _ := newUsageFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return on a cancelled context")
	}
}

func TestUsageWorker_FlushesAtTheSampleThreshold(t *testing.T) {
	w, store, tc := newUsageFixture(t)
	ids := make([]uuid.UUID, 0, 256)
	for range 256 {
		ids = append(ids, seedUsageKey(t, store, tc).ID)
	}
	at := time.Now().UTC().Truncate(time.Second)

	startUsageWorker(t, w)
	for _, id := range ids {
		w.Record(tc.OrgID, tc.ProjectID, id, at)
	}

	// The batch threshold must fire well inside the 5s ticker, so a 1s budget proves it was not the tick.
	deadline := time.Now().Add(time.Second)
	for {
		if lastUsed(t, store, ids[len(ids)-1]) != nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the batch threshold did not flush; only the ticker would have")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestUsageWorker_FlushesOnTheTicker(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := fakes.NewAPIKey()
		tc := tenant.Context{OrgID: uuid.Must(uuid.NewV7()), ProjectID: uuid.Must(uuid.NewV7())}
		w := apikey.NewUsageWorker(store, slog.New(slog.DiscardHandler), time.Now)
		k := seedUsageKey(t, store, tc)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- w.Run(ctx) }()

		at := time.Now().UTC()
		w.Record(tc.OrgID, tc.ProjectID, k.ID, at)
		time.Sleep(6 * time.Second)
		synctest.Wait()

		got := lastUsed(t, store, k.ID)
		if got == nil || !got.Equal(at) {
			t.Fatalf("LastUsedAt = %v, want %v — the periodic flush never fired", got, at)
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatalf("Run = %v, want nil", err)
		}
	})
}

func TestUsageWorker_FlushFailuresDoNotEscapeRun(t *testing.T) {
	w, store, tc := newUsageFixture(t)
	k := seedUsageKey(t, store, tc)
	store.StickyError = true
	store.TouchLastUsedErr = errors.New("connection refused")

	cancel, done := startUsageWorker(t, w)
	w.Record(tc.OrgID, tc.ProjectID, k.ID, time.Now().UTC())
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v, want nil — a database outage must not kill the process", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestUsageWorker_ZeroTimestampFallsBackToTheClock(t *testing.T) {
	store := fakes.NewAPIKey()
	tc := tenant.Context{OrgID: uuid.Must(uuid.NewV7()), ProjectID: uuid.Must(uuid.NewV7())}
	frozen := time.Date(2026, 9, 8, 10, 30, 0, 0, time.UTC)
	w := apikey.NewUsageWorker(store, slog.New(slog.DiscardHandler), func() time.Time { return frozen })
	k := seedUsageKey(t, store, tc)

	w.Record(tc.OrgID, tc.ProjectID, k.ID, time.Time{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := w.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := lastUsed(t, store, k.ID)
	if got == nil || !got.Equal(frozen) {
		t.Errorf("LastUsedAt = %v, want the clock's %v", got, frozen)
	}
}

func TestUsageWorker_SamplesForAnotherTenantDoNotWrite(t *testing.T) {
	w, store, tc := newUsageFixture(t)
	k := seedUsageKey(t, store, tc)

	w.Record(uuid.Must(uuid.NewV7()), tc.ProjectID, k.ID, time.Now().UTC())
	w.Record(tc.OrgID, uuid.Must(uuid.NewV7()), k.ID, time.Now().UTC())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := w.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := lastUsed(t, store, k.ID); got != nil {
		t.Errorf("LastUsedAt = %v, want nil — a foreign tenant's sample wrote the row", got)
	}
}

func TestNewUsageWorker_NilClockFallsBackToTimeNow(t *testing.T) {
	store := fakes.NewAPIKey()
	tc := tenant.Context{OrgID: uuid.Must(uuid.NewV7()), ProjectID: uuid.Must(uuid.NewV7())}
	w := apikey.NewUsageWorker(store, slog.New(slog.DiscardHandler), nil)
	k := seedUsageKey(t, store, tc)

	w.Record(tc.OrgID, tc.ProjectID, k.ID, time.Time{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := w.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if lastUsed(t, store, k.ID) == nil {
		t.Error("LastUsedAt = nil, want a timestamp from the fallback clock")
	}
}
