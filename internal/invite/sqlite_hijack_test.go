package invite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/invite"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/schema"
)

type hijackFixture struct {
	store invite.Store
	orgA  tenant.Context
	orgB  tenant.Context
}

func newHijackFixture(t *testing.T) hijackFixture {
	t.Helper()
	ctx := context.Background()
	cfg := config.Defaults()
	cfg.DB.Driver = db.DriverSQLite
	cfg.DB.DSN = filepath.Join(t.TempDir(), "hijack.db")

	sqlDB, err := db.Open(ctx, cfg.DB, nil)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := schema.MigrateUp(ctx, sqlDB, cfg); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	uidA, oidA := seedTenant(t, sqlDB, cfg.DB.TablePrefix)
	uidB, oidB := seedTenant(t, sqlDB, cfg.DB.TablePrefix)

	store := invite.NewStore(
		db.DBConfig{Driver: db.DriverSQLite, TablePrefix: cfg.DB.TablePrefix},
		db.Pool{W: sqlDB, R: sqlDB},
		nil,
	)
	return hijackFixture{
		store: store,
		orgA:  tenant.Context{OrgID: oidA, ProjectID: uuid.New(), UserID: uidA},
		orgB:  tenant.Context{OrgID: oidB, ProjectID: uuid.New(), UserID: uidB},
	}
}

func (f hijackFixture) seedVictim(t *testing.T) *invite.Invite {
	t.Helper()
	victim := newInviteForTest(t, f.orgA.OrgID, "victim@example.com", time.Hour)
	if err := f.store.Save(tenant.Into(context.Background(), f.orgA), victim); err != nil {
		t.Fatalf("seed Save: %v", err)
	}
	return victim
}

func (f hijackFixture) requireEmail(t *testing.T, id uuid.UUID, want string) {
	t.Helper()
	got, err := f.store.ByID(tenant.Into(context.Background(), f.orgA), id)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if got.Email != want {
		t.Fatalf("org A's row reads %q, want %q: another org rewrote it across the tenant boundary", got.Email, want)
	}
}

func requireBlocked(t *testing.T, err error, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: succeeded, want a cross-tenant write to be refused", what)
	}
	if !invite.IsNotFoundError(err) {
		t.Fatalf("%s: got %v, want *invite.NotFoundError", what, err)
	}
}

// TestSQLiteStore_Save_RejectsReskinnedHijack covers the handler-shaped attack: the attacker's own org carrying the victim's row id.
func TestSQLiteStore_Save_RejectsReskinnedHijack(t *testing.T) {
	f := newHijackFixture(t)
	victim := f.seedVictim(t)

	attack := newInviteForTest(t, f.orgB.OrgID, "attacker@example.com", time.Hour)
	attack.ID = victim.ID

	requireBlocked(t, f.store.Save(tenant.Into(context.Background(), f.orgB), attack), "reskinned Save")
	f.requireEmail(t, victim.ID, "victim@example.com")
}

// TestSQLiteStore_Save_RejectsVerbatimHijack covers the copied-row shape: the victim's org and row id replayed under the attacker's scope.
func TestSQLiteStore_Save_RejectsVerbatimHijack(t *testing.T) {
	f := newHijackFixture(t)
	victim := f.seedVictim(t)

	attack := *victim
	attack.Email = "attacker@example.com"

	requireBlocked(t, f.store.Save(tenant.Into(context.Background(), f.orgB), &attack), "verbatim Save")
	f.requireEmail(t, victim.ID, "victim@example.com")
}

// TestSQLiteStore_Save_UpdatesOwnRow pins that the tenant predicate still lets the owning org through — a WHERE(false) guard would pass every hijack test without it.
func TestSQLiteStore_Save_UpdatesOwnRow(t *testing.T) {
	f := newHijackFixture(t)
	victim := f.seedVictim(t)

	victim.Email = "victim+renamed@example.com"
	if err := f.store.Save(tenant.Into(context.Background(), f.orgA), victim); err != nil {
		t.Fatalf("owner Save: %v", err)
	}
	f.requireEmail(t, victim.ID, "victim+renamed@example.com")
}

// TestSQLiteStore_Delete_RejectsCrossTenant pins the delete path's org predicate.
func TestSQLiteStore_Delete_RejectsCrossTenant(t *testing.T) {
	f := newHijackFixture(t)
	victim := f.seedVictim(t)

	requireBlocked(t, f.store.Delete(tenant.Into(context.Background(), f.orgB), victim.ID), "cross-tenant Delete")
	f.requireEmail(t, victim.ID, "victim@example.com")
}
