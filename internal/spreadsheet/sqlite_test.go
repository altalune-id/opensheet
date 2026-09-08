package spreadsheet_test

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
	"altalune.id/opensheet/internal/spreadsheet"
	"altalune.id/opensheet/schema"
)

type sqliteFixture struct {
	store        spreadsheet.Store
	db           *sql.DB
	prefix       string
	tc           tenant.Context
	credentialID uuid.UUID
}

func newSQLiteFixture(t *testing.T) *sqliteFixture {
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
	tc := tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New(), UserID: uuid.New()}
	seedSQLiteTenant(t, sqlDB, prefix, tc)
	credentialID := seedSQLiteCredential(t, sqlDB, prefix, tc, "primary")

	store := spreadsheet.NewStore(
		db.DBConfig{Driver: db.DriverSQLite, TablePrefix: prefix},
		db.Pool{W: sqlDB, R: sqlDB},
		nil,
	)
	return &sqliteFixture{store: store, db: sqlDB, prefix: prefix, tc: tc, credentialID: credentialID}
}

func (f *sqliteFixture) ctx() context.Context {
	return tenant.Into(context.Background(), f.tc)
}

func (f *sqliteFixture) save(t *testing.T, fileID, title string) *spreadsheet.Spreadsheet {
	t.Helper()
	sp, err := spreadsheet.New(f.tc.OrgID, f.tc.ProjectID, f.credentialID, fileID, title)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := f.store.Save(f.ctx(), sp); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return sp
}

func seedSQLiteTenant(t *testing.T, sqlDB *sql.DB, prefix string, tc tenant.Context) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	exec(t, sqlDB,
		"INSERT INTO "+prefix+"users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES (?, ?, '', '', 0, ?, ?)",
		tc.UserID.String(), tc.UserID.String()+"@x.co", now, now)
	exec(t, sqlDB,
		"INSERT INTO "+prefix+"orgs (id, slug, name, created_by, created_at, updated_at) VALUES (?, ?, 'Org', ?, ?, ?)",
		tc.OrgID.String(), tc.OrgID.String()[:8], tc.UserID.String(), now, now)
	exec(t, sqlDB,
		"INSERT INTO "+prefix+"projects (id, org_id, slug, name, created_by, created_at, updated_at) VALUES (?, ?, ?, 'Web', ?, ?, ?)",
		tc.ProjectID.String(), tc.OrgID.String(), tc.ProjectID.String()[:8], tc.UserID.String(), now, now)
}

func seedSQLiteCredential(t *testing.T, sqlDB *sql.DB, prefix string, tc tenant.Context, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	exec(t, sqlDB,
		"INSERT INTO "+prefix+"credentials (id, org_id, project_id, name, kind, status, authorized_by_user_id, google_account_email, sealed, created_at, updated_at) "+
			"VALUES (?, ?, ?, ?, 'service_account', 'active', ?, '', X'00', ?, ?)",
		id.String(), tc.OrgID.String(), tc.ProjectID.String(), name, tc.UserID.String(), now, now)
	return id
}

func exec(t *testing.T, sqlDB *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := sqlDB.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func TestSQLiteStore_SaveAndByID(t *testing.T) {
	f := newSQLiteFixture(t)
	sp := f.save(t, goodFileID, "Prices")

	got, err := f.store.ByID(f.ctx(), sp.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if got.GoogleFileID != goodFileID || got.Title != "Prices" {
		t.Errorf("got %+v", got)
	}
	if got.CredentialID != f.credentialID {
		t.Errorf("CredentialID = %v, want %v", got.CredentialID, f.credentialID)
	}
	if got.CreatedAt.Location() != time.UTC || got.UpdatedAt.Location() != time.UTC {
		t.Error("timestamps must come back UTC")
	}
	if !got.CreatedAt.Equal(sp.CreatedAt.UTC().Truncate(time.Nanosecond)) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, sp.CreatedAt)
	}
}

func TestSQLiteStore_WritableRoundTrips(t *testing.T) {
	f := newSQLiteFixture(t)

	for _, tc := range []struct {
		name     string
		fileID   string
		writable bool
	}{
		{name: "writable", fileID: "WRITABLE", writable: true},
		{name: "not-writable", fileID: "READONLY", writable: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sp := f.save(t, tc.fileID, "Prices")
			if tc.writable {
				sp.SetWritable(true)
				if err := f.store.Save(f.ctx(), sp); err != nil {
					t.Fatalf("Save: %v", err)
				}
			}
			got, err := f.store.ByID(f.ctx(), sp.ID)
			if err != nil {
				t.Fatalf("ByID: %v", err)
			}
			if got.Writable != tc.writable {
				t.Fatalf("Writable = %v, want %v", got.Writable, tc.writable)
			}
		})
	}
}

func TestSQLiteStore_WritableSurvivesAnUpsert(t *testing.T) {
	f := newSQLiteFixture(t)
	sp := f.save(t, goodFileID, "Prices")
	if sp.Writable {
		t.Fatal("registration must not imply write permission")
	}

	sp.SetWritable(true)
	if err := f.store.Save(f.ctx(), sp); err != nil {
		t.Fatalf("re-Save: %v", err)
	}
	got, err := f.store.ByID(f.ctx(), sp.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if !got.Writable {
		t.Error("Writable = false after an upsert that set it, want true: the DO_UPDATE branch must carry it")
	}

	got.SetWritable(false)
	if err := f.store.Save(f.ctx(), got); err != nil {
		t.Fatalf("third Save: %v", err)
	}
	back, err := f.store.ByID(f.ctx(), sp.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if back.Writable {
		t.Error("Writable = true after an upsert that cleared it, want false")
	}
}

func TestSQLiteStore_ByID_NotFound(t *testing.T) {
	f := newSQLiteFixture(t)
	_, err := f.store.ByID(f.ctx(), uuid.New())
	if !spreadsheet.IsNotFoundError(err) {
		t.Fatalf("want IsNotFoundError, got %T: %v", err, err)
	}
}

func TestSQLiteStore_ByID_AnotherOrgSeesNothing(t *testing.T) {
	f := newSQLiteFixture(t)
	sp := f.save(t, goodFileID, "Prices")

	other := tenant.Into(context.Background(), tenant.Context{OrgID: uuid.New(), ProjectID: f.tc.ProjectID})
	_, err := f.store.ByID(other, sp.ID)
	if !spreadsheet.IsNotFoundError(err) {
		t.Fatalf("want IsNotFoundError, got %T: %v", err, err)
	}
}

func TestSQLiteStore_ByGoogleFileID(t *testing.T) {
	f := newSQLiteFixture(t)
	sp := f.save(t, goodFileID, "Prices")

	got, err := f.store.ByGoogleFileID(f.ctx(), f.tc.OrgID, f.tc.ProjectID, goodFileID)
	if err != nil {
		t.Fatalf("ByGoogleFileID: %v", err)
	}
	if got.ID != sp.ID {
		t.Errorf("got %v, want %v", got.ID, sp.ID)
	}

	if _, err := f.store.ByGoogleFileID(f.ctx(), f.tc.OrgID, f.tc.ProjectID, "MISSING"); !spreadsheet.IsNotFoundError(err) {
		t.Fatalf("unknown file id: want IsNotFoundError, got %T: %v", err, err)
	}
	if _, err := f.store.ByGoogleFileID(f.ctx(), f.tc.OrgID, uuid.New(), goodFileID); !spreadsheet.IsNotFoundError(err) {
		t.Fatalf("another project: want IsNotFoundError, got %T: %v", err, err)
	}
	other := tenant.Into(context.Background(), tenant.Context{OrgID: uuid.New(), ProjectID: f.tc.ProjectID})
	if _, err := f.store.ByGoogleFileID(other, f.tc.OrgID, f.tc.ProjectID, goodFileID); !spreadsheet.IsNotFoundError(err) {
		t.Fatalf("another org's scope: want IsNotFoundError, got %T: %v", err, err)
	}
}

func TestSQLiteStore_List(t *testing.T) {
	f := newSQLiteFixture(t)
	first := f.save(t, "AAA", "A")
	second := f.save(t, "BBB", "B")

	got, err := f.store.List(f.ctx(), f.tc.OrgID, f.tc.ProjectID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2", len(got))
	}
	if got[0].ID != first.ID || got[1].ID != second.ID {
		t.Error("List must be oldest first")
	}

	empty, err := f.store.List(f.ctx(), f.tc.OrgID, uuid.New())
	if err != nil {
		t.Fatalf("List other project: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("another project returned %d rows", len(empty))
	}

	other := tenant.Into(context.Background(), tenant.Context{OrgID: uuid.New(), ProjectID: f.tc.ProjectID})
	crossOrg, err := f.store.List(other, f.tc.OrgID, f.tc.ProjectID)
	if err != nil {
		t.Fatalf("List cross-org: %v", err)
	}
	if len(crossOrg) != 0 {
		t.Errorf("another org's scope returned %d rows", len(crossOrg))
	}
}

func TestSQLiteStore_SaveIsUpsert(t *testing.T) {
	f := newSQLiteFixture(t)
	sp := f.save(t, goodFileID, "Prices")

	nextCred := seedSQLiteCredential(t, f.db, f.prefix, f.tc, "secondary")
	if err := sp.Retitle("Renamed"); err != nil {
		t.Fatalf("Retitle: %v", err)
	}
	if err := sp.Rebind(nextCred); err != nil {
		t.Fatalf("Rebind: %v", err)
	}
	if err := f.store.Save(f.ctx(), sp); err != nil {
		t.Fatalf("second Save: %v", err)
	}

	rows, err := f.store.List(f.ctx(), f.tc.OrgID, f.tc.ProjectID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].Title != "Renamed" || rows[0].CredentialID != nextCred {
		t.Errorf("upsert did not apply: %+v", rows[0])
	}
}

func TestSQLiteStore_DuplicateGoogleFileIDInOneProject(t *testing.T) {
	f := newSQLiteFixture(t)
	f.save(t, goodFileID, "Prices")

	dup, err := spreadsheet.New(f.tc.OrgID, f.tc.ProjectID, f.credentialID, goodFileID, "Prices again")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = f.store.Save(f.ctx(), dup)
	if !spreadsheet.IsAlreadyExistsError(err) {
		t.Fatalf("want IsAlreadyExistsError, got %T: %v", err, err)
	}

	rows, listErr := f.store.List(f.ctx(), f.tc.OrgID, f.tc.ProjectID)
	if listErr != nil {
		t.Fatalf("List: %v", listErr)
	}
	if len(rows) != 1 {
		t.Errorf("got %d rows, want 1 — the duplicate must not land", len(rows))
	}
}

func TestSQLiteStore_Delete(t *testing.T) {
	f := newSQLiteFixture(t)
	sp := f.save(t, goodFileID, "Prices")

	if err := f.store.Delete(f.ctx(), sp.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := f.store.ByID(f.ctx(), sp.ID); !spreadsheet.IsNotFoundError(err) {
		t.Fatalf("row survived: %v", err)
	}
	if err := f.store.Delete(f.ctx(), sp.ID); !spreadsheet.IsNotFoundError(err) {
		t.Fatalf("second Delete: want IsNotFoundError, got %T: %v", err, err)
	}
}

func TestSQLiteStore_Delete_AnotherOrgCannotReach(t *testing.T) {
	f := newSQLiteFixture(t)
	sp := f.save(t, goodFileID, "Prices")

	other := tenant.Into(context.Background(), tenant.Context{OrgID: uuid.New(), ProjectID: f.tc.ProjectID})
	if err := f.store.Delete(other, sp.ID); !spreadsheet.IsNotFoundError(err) {
		t.Fatalf("want IsNotFoundError, got %T: %v", err, err)
	}
	if _, err := f.store.ByID(f.ctx(), sp.ID); err != nil {
		t.Fatalf("the row must survive another org's delete: %v", err)
	}
}

func TestSQLiteStore_DeleteCascadesSheets(t *testing.T) {
	f := newSQLiteFixture(t)
	sp := f.save(t, goodFileID, "Prices")

	sheetID := uuid.New()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	exec(t, f.db,
		"INSERT INTO "+f.prefix+"sheets (id, org_id, project_id, spreadsheet_id, tab, slug, visibility, cache_ttl_secs, created_at, updated_at) "+
			"VALUES (?, ?, ?, ?, 'Q1', 'prices', 'key', 0, ?, ?)",
		sheetID.String(), f.tc.OrgID.String(), f.tc.ProjectID.String(), sp.ID.String(), now, now)

	if err := f.store.Delete(f.ctx(), sp.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	var n int
	if err := f.db.QueryRow("SELECT count(*) FROM "+f.prefix+"sheets WHERE id = ?", sheetID.String()).Scan(&n); err != nil {
		t.Fatalf("count sheets: %v", err)
	}
	if n != 0 {
		t.Error("the FK must cascade the published sheets away with the spreadsheet")
	}
}

func TestSQLiteStore_TenantMissing(t *testing.T) {
	f := newSQLiteFixture(t)
	sp := f.save(t, goodFileID, "Prices")
	bare := context.Background()

	if err := f.store.Save(bare, sp); !tenant.IsMissingError(err) {
		t.Errorf("Save: want tenant.MissingError, got %T: %v", err, err)
	}
	if _, err := f.store.ByID(bare, sp.ID); !tenant.IsMissingError(err) {
		t.Errorf("ByID: want tenant.MissingError, got %T: %v", err, err)
	}
	if _, err := f.store.ByGoogleFileID(bare, f.tc.OrgID, f.tc.ProjectID, goodFileID); !tenant.IsMissingError(err) {
		t.Errorf("ByGoogleFileID: want tenant.MissingError, got %T: %v", err, err)
	}
	if _, err := f.store.List(bare, f.tc.OrgID, f.tc.ProjectID); !tenant.IsMissingError(err) {
		t.Errorf("List: want tenant.MissingError, got %T: %v", err, err)
	}
	if err := f.store.Delete(bare, sp.ID); !tenant.IsMissingError(err) {
		t.Errorf("Delete: want tenant.MissingError, got %T: %v", err, err)
	}
}

func TestNewStore_DispatchesByDriver(t *testing.T) {
	sqliteStore := spreadsheet.NewStore(
		db.DBConfig{Driver: db.DriverSQLite},
		db.Pool{},
		nil,
	)
	if sqliteStore == nil {
		t.Fatal("sqlite dispatch returned nil")
	}
	pgStore := spreadsheet.NewStore(
		db.DBConfig{Driver: db.DriverPostgres, Schema: "public"},
		db.Pool{},
		tenant.NewPgConn(nil),
	)
	if pgStore == nil {
		t.Fatal("postgres dispatch returned nil")
	}
}
