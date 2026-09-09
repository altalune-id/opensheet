package org_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/schema"
)

func newSQLiteStoreForTest(t *testing.T) (org.Store, *sql.DB, string) {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sqlite open: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if _, err := sqlDB.Exec("PRAGMA foreign_keys = ON;"); err != nil {
		t.Fatalf("foreign_keys pragma: %v", err)
	}
	cfg := config.Defaults()
	if err := schema.MigrateUp(context.Background(), sqlDB, cfg); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	store := org.NewStore(db.DBConfig{Driver: db.DriverSQLite, TablePrefix: cfg.DB.TablePrefix}, db.Pool{W: sqlDB, R: sqlDB}, nil)
	return store, sqlDB, cfg.DB.TablePrefix
}

func seedUser(t *testing.T, sqlDB *sql.DB, prefix string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := sqlDB.Exec(
		"INSERT INTO "+prefix+"users (id, email, name, avatar_url, is_admin, created_at, updated_at) "+
			"VALUES (?, ?, '', '', 0, ?, ?)",
		id.String(), id.String()+"@x.com", now, now,
	); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestSQLite_SaveAndLookup(t *testing.T) {
	store, sqlDB, prefix := newSQLiteStoreForTest(t)
	owner := seedUser(t, sqlDB, prefix)

	o, err := org.NewOrg("acme", "Acme", owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), o); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.BySlug(context.Background(), "acme")
	if err != nil {
		t.Fatalf("BySlug: %v", err)
	}
	if got.ID != o.ID || got.Name != "Acme" || got.OwnerID != owner {
		t.Errorf("mismatch: %+v", got)
	}

	got2, err := store.ByID(context.Background(), o.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if got2.Slug != "acme" {
		t.Errorf("slug = %q", got2.Slug)
	}
}

func TestSQLite_NotFound(t *testing.T) {
	store, _, _ := newSQLiteStoreForTest(t)
	if _, err := store.ByID(context.Background(), uuid.New()); !org.IsNotFoundError(err) {
		t.Errorf("ByID: want NotFoundError, got %T: %v", err, err)
	}
	if _, err := store.BySlug(context.Background(), "missing"); !org.IsNotFoundError(err) {
		t.Errorf("BySlug: want NotFoundError, got %T: %v", err, err)
	}
}

func TestSQLite_SaveIsUpsert(t *testing.T) {
	store, sqlDB, prefix := newSQLiteStoreForTest(t)
	owner := seedUser(t, sqlDB, prefix)

	o, _ := org.NewOrg("acme", "Acme", owner)
	if err := store.Save(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	o.Name = "Acme Corp"
	if err := store.Save(context.Background(), o); err != nil {
		t.Fatalf("Save (update): %v", err)
	}
	got, err := store.ByID(context.Background(), o.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Acme Corp" {
		t.Errorf("name = %q", got.Name)
	}
}

func TestSQLite_MembershipsRoundtrip(t *testing.T) {
	store, sqlDB, prefix := newSQLiteStoreForTest(t)
	owner := seedUser(t, sqlDB, prefix)

	o, _ := org.NewOrg("acme", "Acme", owner)
	if err := store.Save(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	m, err := org.NewMembership(o.ID, owner, org.RoleOwner)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMembership(context.Background(), m); err != nil {
		t.Fatalf("SaveMembership: %v", err)
	}

	got, err := store.MembershipOf(context.Background(), o.ID, owner)
	if err != nil {
		t.Fatalf("MembershipOf: %v", err)
	}
	if got.Role != org.RoleOwner {
		t.Errorf("role = %q", got.Role)
	}

	orgs, err := store.List(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	if len(orgs) != 1 || orgs[0].ID != o.ID {
		t.Errorf("List: %+v", orgs)
	}

	list, err := store.ListMembers(context.Background(), o.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Errorf("ListMembers: want 1 got %d", len(list))
	}

	if err := store.RemoveMember(context.Background(), o.ID, owner); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	if _, err := store.MembershipOf(context.Background(), o.ID, owner); !org.IsMembershipMissingError(err) {
		t.Errorf("MembershipOf after delete: want MembershipMissingError, got %T: %v", err, err)
	}
}

func TestSQLite_ListMembers_TiedCreatedAtOrdersByUserID(t *testing.T) {
	store, sqlDB, prefix := newSQLiteStoreForTest(t)
	ctx := context.Background()
	lo, hi := seedUser(t, sqlDB, prefix), seedUser(t, sqlDB, prefix)
	if lo.String() > hi.String() {
		lo, hi = hi, lo
	}

	o, err := org.NewOrg("acme", "Acme", lo)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, o); err != nil {
		t.Fatalf("Save: %v", err)
	}

	tie := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	for _, userID := range []uuid.UUID{hi, lo} {
		m, mErr := org.NewMembership(o.ID, userID, org.RoleMember)
		if mErr != nil {
			t.Fatal(mErr)
		}
		m.CreatedAt = tie
		if sErr := store.SaveMembership(ctx, m); sErr != nil {
			t.Fatalf("SaveMembership: %v", sErr)
		}
	}

	for i := range 5 {
		members, mErr := store.ListMembers(ctx, o.ID)
		if mErr != nil {
			t.Fatalf("ListMembers %d: %v", i, mErr)
		}
		if len(members) != 2 {
			t.Fatalf("ListMembers %d: got %d rows, want 2", i, len(members))
		}
		// NOTE: plan-dependent — TestSQLiteStore_MemberListStatementsCarryTheUserIDTiebreak locks the emitted ORDER BY.
		if members[0].UserID != lo || members[1].UserID != hi {
			t.Fatalf("ListMembers %d: tied created_at must order by user_id ascending, got %v then %v", i, members[0].UserID, members[1].UserID)
		}

		profiles, pErr := store.ListMemberProfiles(ctx, o.ID)
		if pErr != nil {
			t.Fatalf("ListMemberProfiles %d: %v", i, pErr)
		}
		if len(profiles) != 2 {
			t.Fatalf("ListMemberProfiles %d: got %d rows, want 2", i, len(profiles))
		}
		if profiles[0].UserID != lo || profiles[1].UserID != hi {
			t.Fatalf("ListMemberProfiles %d: tied created_at must order by user_id ascending, got %v then %v", i, profiles[0].UserID, profiles[1].UserID)
		}
	}
}

func TestSQLite_RemoveMember_Missing(t *testing.T) {
	store, _, _ := newSQLiteStoreForTest(t)
	err := store.RemoveMember(context.Background(), uuid.New(), uuid.New())
	if !org.IsMembershipMissingError(err) {
		t.Errorf("want MembershipMissingError, got %T: %v", err, err)
	}
}

func TestSQLite_SystemFlag_Roundtrip(t *testing.T) {
	store, sqlDB, prefix := newSQLiteStoreForTest(t)
	owner := seedUser(t, sqlDB, prefix)
	ctx := context.Background()

	o, _ := org.NewOrg("acme", "Acme", owner)
	o.System = true
	if err := store.Save(ctx, o); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := store.ByID(ctx, o.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.System {
		t.Errorf("Org.System = false, want true")
	}

	m, _ := org.NewMembership(o.ID, owner, org.RoleOwner)
	m.System = true
	if err := store.SaveMembership(ctx, m); err != nil {
		t.Fatal(err)
	}
	gm, err := store.MembershipOf(ctx, o.ID, owner)
	if err != nil {
		t.Fatal(err)
	}
	if !gm.System {
		t.Errorf("Membership.System = false, want true")
	}
}

func TestSQLite_ListMemberProfiles(t *testing.T) {
	store, sqlDB, prefix := newSQLiteStoreForTest(t)
	owner := seedUser(t, sqlDB, prefix)
	ctx := context.Background()

	o, _ := org.NewOrg("acme", "Acme", owner)
	if err := store.Save(ctx, o); err != nil {
		t.Fatal(err)
	}
	m, _ := org.NewMembership(o.ID, owner, org.RoleOwner)
	m.System = true
	if err := store.SaveMembership(ctx, m); err != nil {
		t.Fatal(err)
	}

	profiles, err := store.ListMemberProfiles(ctx, o.ID)
	if err != nil {
		t.Fatalf("ListMemberProfiles: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("len=%d want 1", len(profiles))
	}
	got := profiles[0]
	if got.UserID != owner {
		t.Errorf("UserID = %v want %v", got.UserID, owner)
	}
	wantEmail := owner.String() + "@x.com"
	if got.Email != wantEmail {
		t.Errorf("Email = %q want %q", got.Email, wantEmail)
	}
	if got.Role != org.RoleOwner {
		t.Errorf("Role = %q want %q", got.Role, org.RoleOwner)
	}
	if !got.System {
		t.Errorf("System = false, want true")
	}
}

func TestSQLite_SaveMembership_UpdatesRole(t *testing.T) {
	store, sqlDB, prefix := newSQLiteStoreForTest(t)
	owner := seedUser(t, sqlDB, prefix)

	o, _ := org.NewOrg("acme", "Acme", owner)
	if err := store.Save(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	m1, _ := org.NewMembership(o.ID, owner, org.RoleMember)
	if err := store.SaveMembership(context.Background(), m1); err != nil {
		t.Fatal(err)
	}
	m2, _ := org.NewMembership(o.ID, owner, org.RoleOwner)
	if err := store.SaveMembership(context.Background(), m2); err != nil {
		t.Fatalf("SaveMembership upsert: %v", err)
	}
	got, err := store.MembershipOf(context.Background(), o.ID, owner)
	if err != nil {
		t.Fatal(err)
	}
	if got.Role != org.RoleOwner {
		t.Errorf("role after upsert = %q want %q", got.Role, org.RoleOwner)
	}
}
