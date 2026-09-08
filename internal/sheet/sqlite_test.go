package sheet_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/sheet"
	"altalune.id/opensheet/schema"
)

type fixture struct {
	store         sheet.Store
	orgID         uuid.UUID
	projectID     uuid.UUID
	spreadsheetID uuid.UUID
	userID        uuid.UUID
	db            *sql.DB
	prefix        string
}

func (f *fixture) ctx() context.Context {
	return tenant.Into(context.Background(), tenant.Context{
		OrgID: f.orgID, ProjectID: f.projectID, UserID: f.userID,
	})
}

func newSQLiteFixture(t *testing.T) *fixture {
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
	prefix := cfg.DB.TablePrefix

	f := &fixture{db: sqlDB, prefix: prefix}
	f.userID, f.orgID = seedUserAndOrg(t, sqlDB, prefix)
	f.projectID = seedProject(t, sqlDB, prefix, f.orgID, f.userID, "web")
	credentialID := seedCredential(t, sqlDB, prefix, f.orgID, f.projectID, f.userID, "primary")
	f.spreadsheetID = seedSpreadsheet(t, sqlDB, prefix, f.orgID, f.projectID, credentialID, "gfile-1")
	f.store = sheet.NewStore(
		db.DBConfig{Driver: db.DriverSQLite, TablePrefix: prefix},
		db.Pool{W: sqlDB, R: sqlDB},
		nil,
	)
	return f
}

func seedUserAndOrg(t *testing.T, sqlDB *sql.DB, prefix string) (userID, orgID uuid.UUID) {
	t.Helper()
	userID, orgID = uuid.New(), uuid.New()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := sqlDB.Exec(
		"INSERT INTO "+prefix+"users (id, email, name, avatar_url, is_admin, created_at, updated_at) "+
			"VALUES (?, ?, '', '', 0, ?, ?)",
		userID.String(), userID.String()+"@x.com", now, now); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := sqlDB.Exec(
		"INSERT INTO "+prefix+"orgs (id, slug, name, created_by, created_at, updated_at) "+
			"VALUES (?, ?, 'Org', ?, ?, ?)",
		orgID.String(), orgID.String()[:8], userID.String(), now, now); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	return userID, orgID
}

func seedProject(t *testing.T, sqlDB *sql.DB, prefix string, orgID, userID uuid.UUID, slug string) uuid.UUID {
	t.Helper()
	projectID := uuid.New()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := sqlDB.Exec(
		"INSERT INTO "+prefix+"projects (id, org_id, slug, name, created_by, created_at, updated_at) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?)",
		projectID.String(), orgID.String(), slug, slug, userID.String(), now, now); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return projectID
}

func seedCredential(t *testing.T, sqlDB *sql.DB, prefix string, orgID, projectID, userID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	credentialID := uuid.New()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := sqlDB.Exec(
		"INSERT INTO "+prefix+"credentials (id, org_id, project_id, name, kind, status, "+
			"authorized_by_user_id, google_account_email, sealed, created_at, updated_at) "+
			"VALUES (?, ?, ?, ?, 'service_account', 'active', ?, '', ?, ?, ?)",
		credentialID.String(), orgID.String(), projectID.String(), name,
		userID.String(), []byte("sealed"), now, now); err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	return credentialID
}

func seedSpreadsheet(t *testing.T, sqlDB *sql.DB, prefix string, orgID, projectID, credentialID uuid.UUID, fileID string) uuid.UUID {
	t.Helper()
	spreadsheetID := uuid.New()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := sqlDB.Exec(
		"INSERT INTO "+prefix+"spreadsheets (id, org_id, project_id, credential_id, google_file_id, title, created_at, updated_at) "+
			"VALUES (?, ?, ?, ?, ?, 'Rates', ?, ?)",
		spreadsheetID.String(), orgID.String(), projectID.String(), credentialID.String(), fileID, now, now); err != nil {
		t.Fatalf("seed spreadsheet: %v", err)
	}
	return spreadsheetID
}

func TestSQLiteStore_SaveAndByID(t *testing.T) {
	f := newSQLiteFixture(t)
	ctx := f.ctx()

	want, err := sheet.New(f.orgID, f.projectID, f.spreadsheetID, "Kamar", "prices", sheet.VisibilityPublic, 90*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.Save(ctx, want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := f.store.ByID(ctx, want.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if got.ID != want.ID || got.OrgID != f.orgID || got.ProjectID != f.projectID || got.SpreadsheetID != f.spreadsheetID {
		t.Errorf("identity mismatch: %+v", got)
	}
	if got.Tab != "Kamar" || got.Slug != "prices" || got.Visibility != sheet.VisibilityPublic {
		t.Errorf("fields mismatch: %+v", got)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) || !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Errorf("timestamps mismatch: got %v/%v want %v/%v", got.CreatedAt, got.UpdatedAt, want.CreatedAt, want.UpdatedAt)
	}
}

func TestSQLiteStore_CacheTTLRoundTripsInSeconds(t *testing.T) {
	f := newSQLiteFixture(t)
	ctx := f.ctx()

	for _, tc := range []struct {
		name string
		ttl  time.Duration
	}{
		{name: "90s", ttl: 90 * time.Second},
		{name: "zero means the configured default", ttl: 0},
		{name: "1s lower bound", ttl: time.Second},
		{name: "24h upper bound", ttl: 24 * time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sh, err := sheet.New(f.orgID, f.projectID, f.spreadsheetID, "", "ttl-"+tc.name[:2], sheet.VisibilityKey, tc.ttl)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.store.Save(ctx, sh); err != nil {
				t.Fatalf("Save: %v", err)
			}
			got, err := f.store.ByID(ctx, sh.ID)
			if err != nil {
				t.Fatalf("ByID: %v", err)
			}
			if got.CacheTTL != tc.ttl {
				t.Fatalf("CacheTTL = %v (%d ns), want %v", got.CacheTTL, int64(got.CacheTTL), tc.ttl)
			}
		})
	}
}

func TestSQLiteStore_ByID_NotFound(t *testing.T) {
	f := newSQLiteFixture(t)
	if _, err := f.store.ByID(f.ctx(), uuid.New()); !sheet.IsNotFoundError(err) {
		t.Errorf("err = %v, want *NotFoundError", err)
	}
}

func TestSQLiteStore_BySlug(t *testing.T) {
	f := newSQLiteFixture(t)
	ctx := f.ctx()
	sh, err := sheet.New(f.orgID, f.projectID, f.spreadsheetID, "", "prices", sheet.VisibilityKey, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.Save(ctx, sh); err != nil {
		t.Fatal(err)
	}

	got, err := f.store.BySlug(ctx, f.orgID, f.projectID, "prices")
	if err != nil {
		t.Fatalf("BySlug: %v", err)
	}
	if got.ID != sh.ID {
		t.Errorf("got %v, want %v", got.ID, sh.ID)
	}

	if _, err := f.store.BySlug(ctx, f.orgID, f.projectID, "absent"); !sheet.IsNotFoundError(err) {
		t.Errorf("unknown slug err = %v, want *NotFoundError", err)
	}
}

func TestSQLiteStore_SameSlugInTwoProjectsResolvesToDifferentSheets(t *testing.T) {
	f := newSQLiteFixture(t)
	otherProject := seedProject(t, f.db, f.prefix, f.orgID, f.userID, "mobile")
	otherCredential := seedCredential(t, f.db, f.prefix, f.orgID, otherProject, f.userID, "mobile-primary")
	otherSpreadsheet := seedSpreadsheet(t, f.db, f.prefix, f.orgID, otherProject, otherCredential, "gfile-2")

	ctxA := f.ctx()
	ctxB := tenant.Into(context.Background(), tenant.Context{
		OrgID: f.orgID, ProjectID: otherProject, UserID: f.userID,
	})

	a, err := sheet.New(f.orgID, f.projectID, f.spreadsheetID, "Kamar A", "prices", sheet.VisibilityKey, 0)
	if err != nil {
		t.Fatal(err)
	}
	b, err := sheet.New(f.orgID, otherProject, otherSpreadsheet, "Kamar B", "prices", sheet.VisibilityKey, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.Save(ctxA, a); err != nil {
		t.Fatalf("Save in project A: %v", err)
	}
	if err := f.store.Save(ctxB, b); err != nil {
		t.Fatalf("Save in project B: %v: the same slug must be free in another project", err)
	}

	gotA, err := f.store.BySlug(ctxA, f.orgID, f.projectID, "prices")
	if err != nil {
		t.Fatalf("BySlug in project A: %v", err)
	}
	gotB, err := f.store.BySlug(ctxB, f.orgID, otherProject, "prices")
	if err != nil {
		t.Fatalf("BySlug in project B: %v", err)
	}
	if gotA.ID != a.ID {
		t.Errorf("project A resolved %v, want %v", gotA.ID, a.ID)
	}
	if gotB.ID != b.ID {
		t.Errorf("project B resolved %v, want %v", gotB.ID, b.ID)
	}
	if gotA.Tab != "Kamar A" || gotB.Tab != "Kamar B" {
		t.Errorf("tabs crossed over: A=%q B=%q", gotA.Tab, gotB.Tab)
	}
}

func TestSQLiteStore_SaveRejectsDuplicateSlugInOneProject(t *testing.T) {
	f := newSQLiteFixture(t)
	ctx := f.ctx()
	first, err := sheet.New(f.orgID, f.projectID, f.spreadsheetID, "", "prices", sheet.VisibilityKey, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.Save(ctx, first); err != nil {
		t.Fatal(err)
	}
	second, err := sheet.New(f.orgID, f.projectID, f.spreadsheetID, "", "prices", sheet.VisibilityKey, 0)
	if err != nil {
		t.Fatal(err)
	}

	if err := f.store.Save(ctx, second); !sheet.IsAlreadyExistsError(err) {
		t.Fatalf("err = %v (%T), want *AlreadyExistsError", err, err)
	}
}

func TestSQLiteStore_SaveIsUpsert(t *testing.T) {
	f := newSQLiteFixture(t)
	ctx := f.ctx()
	sh, err := sheet.New(f.orgID, f.projectID, f.spreadsheetID, "Kamar", "prices", sheet.VisibilityKey, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.Save(ctx, sh); err != nil {
		t.Fatal(err)
	}

	sh.Retab("Harga")
	if err := sh.Publish(sheet.VisibilityPublic); err != nil {
		t.Fatal(err)
	}
	if err := sh.SetTTL(2 * time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Save(ctx, sh); err != nil {
		t.Fatalf("second Save: %v", err)
	}

	got, err := f.store.ByID(ctx, sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Tab != "Harga" || got.Visibility != sheet.VisibilityPublic || got.CacheTTL != 2*time.Hour {
		t.Errorf("upsert did not apply: %+v", got)
	}
	all, err := f.store.List(ctx, f.orgID, f.projectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Errorf("len = %d, want 1: an upsert must not insert a second row", len(all))
	}
}

func TestSQLiteStore_List(t *testing.T) {
	f := newSQLiteFixture(t)
	ctx := f.ctx()
	for _, slug := range []string{"rooms", "prices"} {
		sh, err := sheet.New(f.orgID, f.projectID, f.spreadsheetID, "", slug, sheet.VisibilityKey, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.store.Save(ctx, sh); err != nil {
			t.Fatal(err)
		}
	}

	got, err := f.store.List(ctx, f.orgID, f.projectID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Slug != "prices" || got[1].Slug != "rooms" {
		t.Errorf("order = %q, %q, want slug ascending", got[0].Slug, got[1].Slug)
	}
}

func TestSQLiteStore_Delete(t *testing.T) {
	f := newSQLiteFixture(t)
	ctx := f.ctx()
	sh, err := sheet.New(f.orgID, f.projectID, f.spreadsheetID, "", "prices", sheet.VisibilityKey, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.Save(ctx, sh); err != nil {
		t.Fatal(err)
	}

	if err := f.store.Delete(ctx, sh.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := f.store.ByID(ctx, sh.ID); !sheet.IsNotFoundError(err) {
		t.Errorf("after Delete err = %v, want *NotFoundError", err)
	}
	if err := f.store.Delete(ctx, sh.ID); !sheet.IsNotFoundError(err) {
		t.Errorf("double Delete err = %v, want *NotFoundError", err)
	}
}

func TestSQLiteStore_AnotherOrgSeesNothing(t *testing.T) {
	f := newSQLiteFixture(t)
	ctx := f.ctx()
	sh, err := sheet.New(f.orgID, f.projectID, f.spreadsheetID, "", "prices", sheet.VisibilityKey, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.Save(ctx, sh); err != nil {
		t.Fatal(err)
	}

	otherUser, otherOrg := seedUserAndOrg(t, f.db, f.prefix)
	foreign := tenant.Into(context.Background(), tenant.Context{
		OrgID: otherOrg, ProjectID: f.projectID, UserID: otherUser,
	})

	if _, err := f.store.ByID(foreign, sh.ID); !sheet.IsNotFoundError(err) {
		t.Errorf("cross-org ByID err = %v, want *NotFoundError", err)
	}
	if _, err := f.store.BySlug(foreign, otherOrg, f.projectID, "prices"); !sheet.IsNotFoundError(err) {
		t.Errorf("cross-org BySlug err = %v, want *NotFoundError", err)
	}
	got, err := f.store.List(foreign, otherOrg, f.projectID)
	if err != nil {
		t.Fatalf("cross-org List: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("cross-org List returned %d rows, want 0", len(got))
	}
	if err := f.store.Delete(foreign, sh.ID); !sheet.IsNotFoundError(err) {
		t.Errorf("cross-org Delete err = %v, want *NotFoundError", err)
	}
	if _, err := f.store.ByID(ctx, sh.ID); err != nil {
		t.Errorf("the owning org lost its sheet to a cross-org Delete: %v", err)
	}
}

func TestSQLiteStore_RequiresTenantScope(t *testing.T) {
	f := newSQLiteFixture(t)
	sh, err := sheet.New(f.orgID, f.projectID, f.spreadsheetID, "", "prices", sheet.VisibilityKey, 0)
	if err != nil {
		t.Fatal(err)
	}
	bare := context.Background()

	if err := f.store.Save(bare, sh); !tenant.IsMissingError(err) {
		t.Errorf("Save err = %v, want tenant.MissingError", err)
	}
	if _, err := f.store.ByID(bare, sh.ID); !tenant.IsMissingError(err) {
		t.Errorf("ByID err = %v, want tenant.MissingError", err)
	}
	if _, err := f.store.BySlug(bare, f.orgID, f.projectID, "prices"); !tenant.IsMissingError(err) {
		t.Errorf("BySlug err = %v, want tenant.MissingError", err)
	}
	if _, err := f.store.List(bare, f.orgID, f.projectID); !tenant.IsMissingError(err) {
		t.Errorf("List err = %v, want tenant.MissingError", err)
	}
	if err := f.store.Delete(bare, sh.ID); !tenant.IsMissingError(err) {
		t.Errorf("Delete err = %v, want tenant.MissingError", err)
	}
}
