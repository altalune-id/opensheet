package project

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/platform/config"
	pdb "altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/schema"
)

type hijackFixture struct {
	store *sqliteStore
	ctxA  context.Context
	ctxB  context.Context
	orgA  uuid.UUID
	orgB  uuid.UUID
}

func newHijackFixture(t *testing.T) hijackFixture {
	t.Helper()
	ctx := context.Background()
	cfg := config.Defaults()
	cfg.DB.Driver = pdb.DriverSQLite
	cfg.DB.DSN = filepath.Join(t.TempDir(), "hijack.db")

	sqlDB, err := pdb.Open(ctx, cfg.DB, nil)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := schema.MigrateUp(ctx, sqlDB, cfg); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	uidA, oidA := seedUserAndOrg(t, sqlDB)
	uidB, oidB := seedUserAndOrg(t, sqlDB)
	return hijackFixture{
		store: newSQLiteStore(sqlDB, cfg.DB.TablePrefix),
		ctxA:  tenantCtx(oidA, uidA),
		ctxB:  tenantCtx(oidB, uidB),
		orgA:  oidA,
		orgB:  oidB,
	}
}

func (f hijackFixture) seedVictim(t *testing.T) *Project {
	t.Helper()
	victim, err := New(f.orgA, "web", "org A's project")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := f.store.Save(f.ctxA, victim); err != nil {
		t.Fatalf("seed Save: %v", err)
	}
	return victim
}

func (f hijackFixture) requireName(t *testing.T, id uuid.UUID, want string) {
	t.Helper()
	got, err := f.store.ByID(f.ctxA, id)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if got.Name != want {
		t.Fatalf("org A's row reads %q, want %q: another org rewrote it across the tenant boundary", got.Name, want)
	}
}

func requireBlocked(t *testing.T, err error, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: succeeded, want a cross-tenant write to be refused", what)
	}
	if !IsNotFoundError(err) {
		t.Fatalf("%s: got %v, want *project.NotFoundError", what, err)
	}
}

// TestSQLite_Save_RejectsReskinnedHijack covers the handler-shaped attack: the attacker's own org carrying the victim's row id.
func TestSQLite_Save_RejectsReskinnedHijack(t *testing.T) {
	f := newHijackFixture(t)
	victim := f.seedVictim(t)

	attack, err := New(f.orgB, "web", "Hijacked")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	attack.ID = victim.ID

	requireBlocked(t, f.store.Save(f.ctxB, attack), "reskinned Save")
	f.requireName(t, victim.ID, "org A's project")
}

// TestSQLite_Save_RejectsVerbatimHijack covers the copied-row shape: the victim's org and row id replayed under the attacker's scope.
func TestSQLite_Save_RejectsVerbatimHijack(t *testing.T) {
	f := newHijackFixture(t)
	victim := f.seedVictim(t)

	attack := *victim
	attack.Name = "Hijacked"

	requireBlocked(t, f.store.Save(f.ctxB, &attack), "verbatim Save")
	f.requireName(t, victim.ID, "org A's project")
}

// TestSQLite_Save_UpdatesOwnRow pins that the tenant predicate still lets the owning org through — a WHERE(false) guard would pass every hijack test without it.
func TestSQLite_Save_UpdatesOwnRow(t *testing.T) {
	f := newHijackFixture(t)
	victim := f.seedVictim(t)

	victim.Name = "renamed by its owner"
	if err := f.store.Save(f.ctxA, victim); err != nil {
		t.Fatalf("owner Save: %v", err)
	}
	f.requireName(t, victim.ID, "renamed by its owner")
}
