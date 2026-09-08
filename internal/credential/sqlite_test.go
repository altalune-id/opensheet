package credential_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"altalune.id/opensheet/internal/credential"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/schema"
)

type sqliteFixture struct {
	store  credential.Store
	sqlDB  *sql.DB
	prefix string
	tc     tenant.Context
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
	userID, orgID, projID := seedProject(t, sqlDB, prefix)
	return &sqliteFixture{
		store: credential.NewStore(
			db.DBConfig{Driver: db.DriverSQLite, TablePrefix: prefix},
			db.Pool{W: sqlDB, R: sqlDB},
			nil,
		),
		sqlDB:  sqlDB,
		prefix: prefix,
		tc:     tenant.Context{OrgID: orgID, ProjectID: projID, UserID: userID},
	}
}

func (f *sqliteFixture) ctx() context.Context {
	return tenant.Into(context.Background(), f.tc)
}

func (f *sqliteFixture) newCredential(t *testing.T, name string) *credential.Credential {
	t.Helper()
	c, err := credential.New(uuid.Nil, f.tc.OrgID, f.tc.ProjectID, f.tc.UserID,
		name, credential.KindServiceAccount, "sa@x.iam.gserviceaccount.com", []byte{0xde, 0xad, 0xbe, 0xef})
	if err != nil {
		t.Fatalf("credential.New: %v", err)
	}
	return c
}

func seedProject(t *testing.T, sqlDB *sql.DB, prefix string) (userID, orgID, projID uuid.UUID) { //nolint:nonamedreturns // triple
	t.Helper()
	userID, orgID, projID = uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	exec := func(query string, args ...any) {
		if _, err := sqlDB.Exec(query, args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	exec("INSERT INTO "+prefix+"users (id, email, name, avatar_url, is_admin, created_at, updated_at) "+
		"VALUES (?, ?, '', '', 0, ?, ?)", userID.String(), userID.String()+"@x.com", now, now)
	exec("INSERT INTO "+prefix+"orgs (id, slug, name, created_by, created_at, updated_at) "+
		"VALUES (?, ?, 'Org', ?, ?, ?)", orgID.String(), orgID.String()[:8], userID.String(), now, now)
	exec("INSERT INTO "+prefix+"projects (id, org_id, slug, name, created_by, created_at, updated_at) "+
		"VALUES (?, ?, 'web', 'Web', ?, ?, ?)", projID.String(), orgID.String(), userID.String(), now, now)
	return userID, orgID, projID
}

func (f *sqliteFixture) seedSpreadsheet(t *testing.T, credentialID uuid.UUID) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := f.sqlDB.Exec(
		"INSERT INTO "+f.prefix+"spreadsheets (id, org_id, project_id, credential_id, google_file_id, title, created_at, updated_at) "+
			"VALUES (?, ?, ?, ?, 'FILE', 'Prices', ?, ?)",
		uuid.New().String(), f.tc.OrgID.String(), f.tc.ProjectID.String(), credentialID.String(), now, now)
	if err != nil {
		t.Fatalf("seed spreadsheet: %v", err)
	}
}

func TestSQLiteStore_SaveAndByID(t *testing.T) {
	f := newSQLiteFixture(t)
	ctx := f.ctx()
	c := f.newCredential(t, "prod reader")
	if err := f.store.Save(ctx, c); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := f.store.ByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if got.Name != "prod reader" || got.Kind != credential.KindServiceAccount || got.Status != credential.StatusActive {
		t.Errorf("got %+v", got)
	}
	if got.OrgID != c.OrgID || got.ProjectID != c.ProjectID || got.AuthorizedByUserID != c.AuthorizedByUserID {
		t.Errorf("ids did not round trip: %+v", got)
	}
	if string(got.Sealed) != string(c.Sealed) {
		t.Errorf("Sealed=%v want %v", got.Sealed, c.Sealed)
	}
	if !got.CreatedAt.Equal(c.CreatedAt) || !got.UpdatedAt.Equal(c.UpdatedAt) {
		t.Errorf("timestamps did not round trip: %+v", got)
	}
}

func TestSQLiteStore_ByID_NotFound(t *testing.T) {
	f := newSQLiteFixture(t)
	if _, err := f.store.ByID(f.ctx(), uuid.New()); !credential.IsNotFoundError(err) {
		t.Fatalf("error = %T %v, want *NotFoundError", err, err)
	}
}

func TestSQLiteStore_SaveIsUpsert(t *testing.T) {
	f := newSQLiteFixture(t)
	ctx := f.ctx()
	c := f.newCredential(t, "prod")
	if err := f.store.Save(ctx, c); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := c.Rotate([]byte{0x01, 0x02}, "next@x.com"); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	c.MarkReauthNeeded()
	if err := f.store.Save(ctx, c); err != nil {
		t.Fatalf("second Save: %v", err)
	}

	got, err := f.store.ByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if got.Status != credential.StatusReauthNeeded {
		t.Errorf("Status=%q", got.Status)
	}
	if string(got.Sealed) != string([]byte{0x01, 0x02}) {
		t.Errorf("Sealed=%v want the rotated ciphertext", got.Sealed)
	}
	if got.GoogleAccountEmail != "next@x.com" {
		t.Errorf("GoogleAccountEmail=%q", got.GoogleAccountEmail)
	}
}

func TestSQLiteStore_DuplicateNameInProject(t *testing.T) {
	f := newSQLiteFixture(t)
	ctx := f.ctx()
	if err := f.store.Save(ctx, f.newCredential(t, "prod")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	err := f.store.Save(ctx, f.newCredential(t, "prod"))
	if !credential.IsAlreadyExistsError(err) {
		t.Fatalf("error = %T %v, want *AlreadyExistsError", err, err)
	}
}

func TestSQLiteStore_List(t *testing.T) {
	f := newSQLiteFixture(t)
	ctx := f.ctx()
	for i, name := range []string{"a", "b", "c"} {
		c := f.newCredential(t, name)
		c.CreatedAt = c.CreatedAt.Add(time.Duration(i) * time.Millisecond)
		if err := f.store.Save(ctx, c); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	got, err := f.store.List(ctx, f.tc.OrgID, f.tc.ProjectID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d credentials, want 3", len(got))
	}
	if got[0].Name != "c" {
		t.Errorf("List is not newest first: %q", got[0].Name)
	}

	empty, err := f.store.List(ctx, f.tc.OrgID, uuid.New())
	if err != nil {
		t.Fatalf("List other project: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("another project returned %d rows", len(empty))
	}
}

func TestSQLiteStore_ListRefusesAnotherOrg(t *testing.T) {
	f := newSQLiteFixture(t)
	ctx := f.ctx()
	if err := f.store.Save(ctx, f.newCredential(t, "prod")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := f.store.List(ctx, uuid.New(), f.tc.ProjectID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d rows for an org the scope does not name", len(got))
	}
}

func TestSQLiteStore_Delete(t *testing.T) {
	f := newSQLiteFixture(t)
	ctx := f.ctx()
	c := f.newCredential(t, "prod")
	if err := f.store.Save(ctx, c); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := f.store.Delete(ctx, c.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := f.store.ByID(ctx, c.ID); !credential.IsNotFoundError(err) {
		t.Fatalf("after Delete error = %T %v, want *NotFoundError", err, err)
	}
	if err := f.store.Delete(ctx, c.ID); !credential.IsNotFoundError(err) {
		t.Fatalf("double Delete error = %T %v, want *NotFoundError", err, err)
	}
}

func TestSQLiteStore_DeleteReferencedIsInUse(t *testing.T) {
	f := newSQLiteFixture(t)
	ctx := f.ctx()
	c := f.newCredential(t, "prod")
	if err := f.store.Save(ctx, c); err != nil {
		t.Fatalf("Save: %v", err)
	}
	f.seedSpreadsheet(t, c.ID)

	err := f.store.Delete(ctx, c.ID)
	if !credential.IsInUseError(err) {
		t.Fatalf("error = %T %v, want *InUseError from the ON DELETE RESTRICT foreign key", err, err)
	}
	if _, byIDErr := f.store.ByID(ctx, c.ID); byIDErr != nil {
		t.Fatalf("a refused Delete must leave the row in place: %v", byIDErr)
	}
}

func TestSQLiteStore_RequiresTenantScope(t *testing.T) {
	f := newSQLiteFixture(t)
	c := f.newCredential(t, "prod")
	bare := context.Background()

	if err := f.store.Save(bare, c); !tenant.IsMissingError(err) {
		t.Errorf("Save error = %T %v, want tenant.MissingError", err, err)
	}
	if _, err := f.store.ByID(bare, c.ID); !tenant.IsMissingError(err) {
		t.Errorf("ByID error = %T %v, want tenant.MissingError", err, err)
	}
	if _, err := f.store.List(bare, f.tc.OrgID, f.tc.ProjectID); !tenant.IsMissingError(err) {
		t.Errorf("List error = %T %v, want tenant.MissingError", err, err)
	}
	if err := f.store.Delete(bare, c.ID); !tenant.IsMissingError(err) {
		t.Errorf("Delete error = %T %v, want tenant.MissingError", err, err)
	}
}
