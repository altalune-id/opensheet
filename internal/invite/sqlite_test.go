package invite_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"altalune.id/template/internal/invite"
	"altalune.id/template/internal/platform/config"
	"altalune.id/template/internal/platform/db"
	"altalune.id/template/internal/platform/tenant"
	"altalune.id/template/schema"
)

func newSQLiteStoreForTest(t *testing.T) (invite.Store, *sql.DB, tenant.Context) {
	t.Helper()
	dsn := "file:" + t.Name() + "?mode=memory&cache=shared&_pragma=foreign_keys(1)"
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("sqlite open: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	cfg := config.Defaults()
	if err := schema.MigrateUp(context.Background(), sqlDB, cfg); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	uid, oid := seedTenant(t, sqlDB, cfg.DB.TablePrefix)
	tc := tenant.Context{OrgID: oid, ProjectID: uuid.New(), UserID: uid}

	store := invite.NewStore(db.DBConfig{Driver: db.DriverSQLite, TablePrefix: cfg.DB.TablePrefix}, db.Pool{W: sqlDB, R: sqlDB}, nil)
	return store, sqlDB, tc
}

func seedTenant(t *testing.T, sqlDB *sql.DB, prefix string) (userID, orgID uuid.UUID) {
	t.Helper()
	userID = uuid.New()
	orgID = uuid.New()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := sqlDB.Exec(
		"INSERT INTO "+prefix+"users (id, email, name, avatar_url, is_admin, created_at, updated_at) "+
			"VALUES (?, ?, '', '', 0, ?, ?)",
		userID.String(), userID.String()+"@x.com", now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(
		"INSERT INTO "+prefix+"orgs (id, slug, name, created_by, created_at, updated_at) "+
			"VALUES (?, ?, 'Org', ?, ?, ?)",
		orgID.String(), orgID.String()[:8], userID.String(), now, now); err != nil {
		t.Fatal(err)
	}
	return
}

func newInviteForTest(t *testing.T, orgID uuid.UUID, email string, ttl time.Duration) *invite.Invite {
	t.Helper()
	inv, err := invite.New(invite.NewParams{
		OrgID: orgID,
		Email: email,
		Role:  invite.RoleMember,
		TTL:   ttl,
		Token: "raw-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return inv
}

func TestSQLiteStore_SaveAndByID(t *testing.T) {
	store, _, tc := newSQLiteStoreForTest(t)
	ctx := tenant.Into(context.Background(), tc)
	inv := newInviteForTest(t, tc.OrgID, "alice@example.com", time.Hour)
	if err := store.Save(ctx, inv); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := store.ByID(ctx, inv.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if got.Email != inv.Email || got.TokenHash != inv.TokenHash {
		t.Errorf("mismatch: %+v", got)
	}
}

func TestSQLiteStore_ByID_NotFound(t *testing.T) {
	store, _, tc := newSQLiteStoreForTest(t)
	ctx := tenant.Into(context.Background(), tc)
	if _, err := store.ByID(ctx, uuid.New()); !invite.IsNotFoundError(err) {
		t.Errorf("want IsNotFoundError, got %v", err)
	}
}

func TestSQLiteStore_ByID_CrossTenantHidden(t *testing.T) {
	store, sqlDB, tcA := newSQLiteStoreForTest(t)
	foreignOrg := uuid.New()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := sqlDB.Exec(
		"INSERT INTO altempl_orgs (id, slug, name, created_by, created_at, updated_at) "+
			"VALUES (?, 'other', 'Other', ?, ?, ?)",
		foreignOrg.String(), tcA.UserID.String(), now, now); err != nil {
		t.Fatal(err)
	}

	inv := newInviteForTest(t, foreignOrg, "eve@example.com", time.Hour)
	ctxForeign := tenant.Into(context.Background(), tenant.Context{OrgID: foreignOrg, UserID: tcA.UserID})
	if err := store.Save(ctxForeign, inv); err != nil {
		t.Fatalf("foreign Save: %v", err)
	}
	ctxA := tenant.Into(context.Background(), tcA)
	if _, err := store.ByID(ctxA, inv.ID); !invite.IsNotFoundError(err) {
		t.Errorf("want IsNotFoundError, got %v", err)
	}
}

func TestSQLiteStore_ByTokenHash(t *testing.T) {
	store, _, tc := newSQLiteStoreForTest(t)
	ctx := tenant.Into(context.Background(), tc)
	inv := newInviteForTest(t, tc.OrgID, "alice@example.com", time.Hour)
	if err := store.Save(ctx, inv); err != nil {
		t.Fatal(err)
	}

	got, err := store.ByTokenHash(context.Background(), inv.TokenHash)
	if err != nil {
		t.Fatalf("ByTokenHash: %v", err)
	}
	if got.ID != inv.ID {
		t.Errorf("ID mismatch")
	}
	if _, err := store.ByTokenHash(context.Background(), "no-such-hash"); !invite.IsNotFoundError(err) {
		t.Errorf("want IsNotFoundError, got %v", err)
	}
}

func TestSQLiteStore_ListPending_ExcludesAccepted(t *testing.T) {
	store, _, tc := newSQLiteStoreForTest(t)
	ctx := tenant.Into(context.Background(), tc)

	pending := newInviteForTest(t, tc.OrgID, "pending@example.com", time.Hour)
	accepted := newInviteForTest(t, tc.OrgID, "accepted@example.com", time.Hour)
	now := time.Now().UTC()
	accepted.UsedAt = &now
	if err := store.Save(ctx, pending); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, accepted); err != nil {
		t.Fatal(err)
	}

	got, err := store.ListPending(ctx, tc.OrgID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Email != "pending@example.com" {
		t.Errorf("ListPending: %+v", got)
	}
}

func TestSQLiteStore_FindPendingForEmail(t *testing.T) {
	store, _, tc := newSQLiteStoreForTest(t)
	ctx := tenant.Into(context.Background(), tc)

	target := newInviteForTest(t, tc.OrgID, "target@example.com", time.Hour)
	other := newInviteForTest(t, tc.OrgID, "other@example.com", time.Hour)
	if err := store.Save(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, other); err != nil {
		t.Fatal(err)
	}

	got, err := store.FindPendingForEmail(context.Background(), "target@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Email != "target@example.com" {
		t.Errorf("FindPendingForEmail: %+v", got)
	}

	empty, err := store.FindPendingForEmail(context.Background(), "nobody@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Errorf("expected empty; got %+v", empty)
	}
}

func TestSQLiteStore_SaveIsUpsert_MarksUsed(t *testing.T) {
	store, _, tc := newSQLiteStoreForTest(t)
	ctx := tenant.Into(context.Background(), tc)
	inv := newInviteForTest(t, tc.OrgID, "alice@example.com", time.Hour)
	if err := store.Save(ctx, inv); err != nil {
		t.Fatal(err)
	}
	if err := inv.Consume(time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, inv); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	got, err := store.ByID(ctx, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsUsed() {
		t.Errorf("expected UsedAt round-trip")
	}
}

func TestSQLiteStore_Delete(t *testing.T) {
	store, _, tc := newSQLiteStoreForTest(t)
	ctx := tenant.Into(context.Background(), tc)
	inv := newInviteForTest(t, tc.OrgID, "alice@example.com", time.Hour)
	if err := store.Save(ctx, inv); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, inv.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.ByID(ctx, inv.ID); !invite.IsNotFoundError(err) {
		t.Errorf("post-delete want IsNotFoundError, got %v", err)
	}
	if err := store.Delete(ctx, inv.ID); !invite.IsNotFoundError(err) {
		t.Errorf("double delete want IsNotFoundError, got %v", err)
	}
}

func TestSQLiteStore_TenantMissing(t *testing.T) {
	store, _, tc := newSQLiteStoreForTest(t)
	inv := newInviteForTest(t, tc.OrgID, "alice@example.com", time.Hour)
	if err := store.Save(context.Background(), inv); !tenant.IsMissingError(err) {
		t.Errorf("Save without tenant: want MissingError, got %v", err)
	}
}
