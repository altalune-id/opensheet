package apikey_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/db"
	sqliteent "altalune.id/opensheet/internal/platform/db/entity/sqlite"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/schema"
)

type sqliteFixture struct {
	store  apikey.Store
	db     *sql.DB
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
	userID, orgID, projID := seedSQLiteProject(t, sqlDB, prefix, "acme", "web")
	return &sqliteFixture{
		store:  apikey.NewStore(db.DBConfig{Driver: db.DriverSQLite, TablePrefix: prefix}, db.Pool{W: sqlDB, R: sqlDB}, nil),
		db:     sqlDB,
		prefix: prefix,
		tc:     tenant.Context{OrgID: orgID, ProjectID: projID, UserID: userID},
	}
}

func seedSQLiteProject(t *testing.T, sqlDB *sql.DB, prefix, orgSlug, projSlug string) (uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	userID, orgID, projID := uuid.New(), uuid.New(), uuid.New()
	now := sqliteent.SQLiteTime(time.Now())
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := sqlDB.Exec(q, args...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	exec("INSERT INTO "+prefix+"users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES (?, ?, '', '', 0, ?, ?)",
		userID.String(), userID.String()+"@x.co", now, now)
	exec("INSERT INTO "+prefix+"orgs (id, slug, name, created_by, created_at, updated_at) VALUES (?, ?, 'Org', ?, ?, ?)",
		orgID.String(), orgSlug, userID.String(), now, now)
	exec("INSERT INTO "+prefix+"projects (id, org_id, slug, name, created_by, created_at, updated_at) VALUES (?, ?, ?, 'Proj', ?, ?, ?)",
		projID.String(), orgID.String(), projSlug, userID.String(), now, now)
	return userID, orgID, projID
}

func (f *sqliteFixture) seedSheet(t *testing.T) uuid.UUID {
	t.Helper()
	credID, docID, sheetID := uuid.New(), uuid.New(), uuid.New()
	now := sqliteent.SQLiteTime(time.Now())
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := f.db.Exec(q, args...); err != nil {
			t.Fatalf("seed sheet: %v", err)
		}
	}
	exec("INSERT INTO "+f.prefix+"credentials (id, org_id, project_id, name, kind, authorized_by_user_id, sealed, created_at, updated_at) "+
		"VALUES (?, ?, ?, ?, 'service_account', ?, X'00', ?, ?)",
		credID.String(), f.tc.OrgID.String(), f.tc.ProjectID.String(), "cred-"+credID.String()[:8], f.tc.UserID.String(), now, now)
	exec("INSERT INTO "+f.prefix+"spreadsheets (id, org_id, project_id, credential_id, google_file_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		docID.String(), f.tc.OrgID.String(), f.tc.ProjectID.String(), credID.String(), "gfile-"+docID.String()[:8], now, now)
	exec("INSERT INTO "+f.prefix+"sheets (id, org_id, project_id, spreadsheet_id, slug, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		sheetID.String(), f.tc.OrgID.String(), f.tc.ProjectID.String(), docID.String(), "sheet-"+sheetID.String()[:8], now, now)
	return sheetID
}

func mintFor(t *testing.T, tc tenant.Context, scopes []string, sheetIDs []uuid.UUID, expiresAt *time.Time) (*apikey.APIKey, string) {
	t.Helper()
	k, plaintext, err := apikey.Mint(tc.OrgID, tc.ProjectID, "ci", scopes, sheetIDs, expiresAt, time.Now().UTC())
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	return k, plaintext
}

func TestSQLiteStore_SaveAndByID(t *testing.T) {
	f := newSQLiteFixture(t)
	ctx := tenant.Into(context.Background(), f.tc)
	expires := time.Now().UTC().Add(time.Hour).Truncate(time.Millisecond)

	k, _ := mintFor(t, f.tc, []string{authn.ScopeSheetsRead, authn.ScopeCachePurge}, nil, &expires)
	if err := f.store.Save(ctx, k); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := f.store.ByID(ctx, k.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if got.Name != k.Name || got.KeyPrefix != k.KeyPrefix {
		t.Errorf("identity round trip mismatch: %+v", got)
	}
	if len(got.Scopes) != 2 || got.Scopes[0] != authn.ScopeSheetsRead || got.Scopes[1] != authn.ScopeCachePurge {
		t.Errorf("Scopes = %v, want the minted order", got.Scopes)
	}
	if got.ExpiresAt == nil || !got.ExpiresAt.Equal(expires) {
		t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, expires)
	}
	if !got.CreatedAt.Equal(k.CreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, k.CreatedAt)
	}
	if got.LastUsedAt != nil || got.RevokedAt != nil {
		t.Error("nullable timestamps did not come back nil")
	}
	if len(got.SecretHash) != 0 {
		t.Error("ByID returned secret material")
	}
}

func TestSQLiteStore_ByIDNotFound(t *testing.T) {
	f := newSQLiteFixture(t)
	ctx := tenant.Into(context.Background(), f.tc)
	if _, err := f.store.ByID(ctx, uuid.New()); !apikey.IsNotFoundError(err) {
		t.Fatalf("err = %v, want *NotFoundError", err)
	}
}

func TestSQLiteStore_SheetGrantRoundTrip(t *testing.T) {
	f := newSQLiteFixture(t)
	ctx := tenant.Into(context.Background(), f.tc)
	a, b := f.seedSheet(t), f.seedSheet(t)

	k, _ := mintFor(t, f.tc, []string{authn.ScopeSheetsRead}, []uuid.UUID{a, b}, nil)
	if err := f.store.Save(ctx, k); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := f.store.ByID(ctx, k.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if len(got.SheetIDs) != 2 {
		t.Fatalf("SheetIDs = %v, want 2 entries", got.SheetIDs)
	}
	if !got.Allows(authn.ScopeSheetsRead, a) || !got.Allows(authn.ScopeSheetsRead, b) {
		t.Error("a granted sheet is not allowed after the round trip")
	}
	if got.Allows(authn.ScopeSheetsRead, uuid.New()) {
		t.Error("an ungranted sheet is allowed after the round trip")
	}

	k.SheetIDs = []uuid.UUID{b}
	if err := f.store.Save(ctx, k); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	got, err = f.store.ByID(ctx, k.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if len(got.SheetIDs) != 1 || got.SheetIDs[0] != b {
		t.Fatalf("SheetIDs = %v, want [%v] after narrowing the grant", got.SheetIDs, b)
	}
}

func TestSQLiteStore_ByPrefixNeedsNoTenantScope(t *testing.T) {
	f := newSQLiteFixture(t)
	ctx := tenant.Into(context.Background(), f.tc)
	sheetID := f.seedSheet(t)

	k, plaintext := mintFor(t, f.tc, []string{authn.ScopeSheetsRead}, []uuid.UUID{sheetID}, nil)
	if err := f.store.Save(ctx, k); err != nil {
		t.Fatalf("Save: %v", err)
	}
	prefix, secret, err := apikey.Parse(plaintext)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	got, err := f.store.ByPrefix(context.Background(), prefix)
	if err != nil {
		t.Fatalf("ByPrefix without a tenant scope: %v", err)
	}
	if got.OrgID != f.tc.OrgID || got.ProjectID != f.tc.ProjectID {
		t.Error("ByPrefix did not resolve the key's own tenant")
	}
	if !got.Verify(secret) {
		t.Error("ByPrefix returned no usable secret hash")
	}
	if len(got.SheetIDs) != 1 || got.SheetIDs[0] != sheetID {
		t.Fatalf("SheetIDs = %v, want [%v] — a lost grant silently widens the key", got.SheetIDs, sheetID)
	}
}

func TestSQLiteStore_ByPrefixNotFound(t *testing.T) {
	f := newSQLiteFixture(t)
	if _, err := f.store.ByPrefix(context.Background(), "0000000000000000"); !apikey.IsNotFoundError(err) {
		t.Fatalf("err = %v, want *NotFoundError", err)
	}
}

func TestSQLiteStore_ListCarriesNoSecretMaterial(t *testing.T) {
	f := newSQLiteFixture(t)
	ctx := tenant.Into(context.Background(), f.tc)
	for range 3 {
		k, _ := mintFor(t, f.tc, []string{authn.ScopeAPIKeysRead}, nil, nil)
		if err := f.store.Save(ctx, k); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	got, err := f.store.List(ctx, f.tc.OrgID, f.tc.ProjectID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("List len = %d, want 3", len(got))
	}
	for _, k := range got {
		if len(k.SecretHash) != 0 {
			t.Errorf("key %s carries secret material in a List result", k.ID)
		}
	}
}

func TestSQLiteStore_ListIsScopedToTheProject(t *testing.T) {
	f := newSQLiteFixture(t)
	ctx := tenant.Into(context.Background(), f.tc)
	k, _ := mintFor(t, f.tc, []string{authn.ScopeAPIKeysRead}, nil, nil)
	if err := f.store.Save(ctx, k); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := f.store.List(ctx, f.tc.OrgID, uuid.New())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("another project's List returned %d keys, want 0", len(got))
	}

	foreign := tenant.Into(context.Background(), tenant.Context{OrgID: uuid.New(), ProjectID: f.tc.ProjectID})
	got, err = f.store.List(foreign, f.tc.OrgID, f.tc.ProjectID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("another org's scope returned %d keys, want 0", len(got))
	}
}

func TestSQLiteStore_SaveWithoutSecretHashPreservesTheStoredOne(t *testing.T) {
	f := newSQLiteFixture(t)
	ctx := tenant.Into(context.Background(), f.tc)
	k, plaintext := mintFor(t, f.tc, []string{authn.ScopeSheetsRead}, nil, nil)
	if err := f.store.Save(ctx, k); err != nil {
		t.Fatalf("Save: %v", err)
	}
	prefix, secret, err := apikey.Parse(plaintext)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	public, err := f.store.ByID(ctx, k.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	public.Revoke(time.Now().UTC())
	if err := f.store.Save(ctx, public); err != nil {
		t.Fatalf("Save of a public read: %v", err)
	}

	got, err := f.store.ByPrefix(context.Background(), prefix)
	if err != nil {
		t.Fatalf("ByPrefix: %v", err)
	}
	if !got.Verify(secret) {
		t.Fatal("saving a key read without secret material blanked the stored hash")
	}
	if got.RevokedAt == nil {
		t.Error("the revocation did not persist")
	}
	if got.Active(time.Now().UTC()) {
		t.Error("a revoked key is still active")
	}
}

func TestSQLiteStore_SaveWithoutSecretHashRejectsAnUnknownKey(t *testing.T) {
	f := newSQLiteFixture(t)
	ctx := tenant.Into(context.Background(), f.tc)
	k, _ := mintFor(t, f.tc, []string{authn.ScopeSheetsRead}, nil, nil)
	k.SecretHash = nil
	if err := f.store.Save(ctx, k); !apikey.IsNotFoundError(err) {
		t.Fatalf("err = %v, want *NotFoundError", err)
	}
}

func TestSQLiteStore_DuplicateKeyPrefixIsRejected(t *testing.T) {
	f := newSQLiteFixture(t)
	ctx := tenant.Into(context.Background(), f.tc)
	first, _ := mintFor(t, f.tc, []string{authn.ScopeSheetsRead}, nil, nil)
	if err := f.store.Save(ctx, first); err != nil {
		t.Fatalf("Save: %v", err)
	}
	second, _ := mintFor(t, f.tc, []string{authn.ScopeSheetsRead}, nil, nil)
	second.KeyPrefix = first.KeyPrefix
	if err := f.store.Save(ctx, second); !apikey.IsAlreadyExistsError(err) {
		t.Fatalf("err = %v, want *AlreadyExistsError", err)
	}
}

func TestSQLiteStore_Delete(t *testing.T) {
	f := newSQLiteFixture(t)
	ctx := tenant.Into(context.Background(), f.tc)
	sheetID := f.seedSheet(t)
	k, _ := mintFor(t, f.tc, []string{authn.ScopeSheetsRead}, []uuid.UUID{sheetID}, nil)
	if err := f.store.Save(ctx, k); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := f.store.Delete(ctx, k.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := f.store.ByID(ctx, k.ID); !apikey.IsNotFoundError(err) {
		t.Fatalf("err = %v, want *NotFoundError after Delete", err)
	}
	if err := f.store.Delete(ctx, k.ID); !apikey.IsNotFoundError(err) {
		t.Fatalf("double Delete err = %v, want *NotFoundError", err)
	}

	var grants int
	if err := f.db.QueryRow("SELECT count(*) FROM "+f.prefix+"api_key_sheets WHERE api_key_id = ?", k.ID.String()).Scan(&grants); err != nil {
		t.Fatalf("count grants: %v", err)
	}
	if grants != 0 {
		t.Errorf("grant rows left behind = %d, want 0", grants)
	}
}

func TestSQLiteStore_DeleteIsScopedToTheOrg(t *testing.T) {
	f := newSQLiteFixture(t)
	ctx := tenant.Into(context.Background(), f.tc)
	k, _ := mintFor(t, f.tc, []string{authn.ScopeSheetsRead}, nil, nil)
	if err := f.store.Save(ctx, k); err != nil {
		t.Fatalf("Save: %v", err)
	}
	foreign := tenant.Into(context.Background(), tenant.Context{OrgID: uuid.New(), ProjectID: f.tc.ProjectID})
	if err := f.store.Delete(foreign, k.ID); !apikey.IsNotFoundError(err) {
		t.Fatalf("err = %v, want *NotFoundError", err)
	}
	if _, err := f.store.ByID(ctx, k.ID); err != nil {
		t.Errorf("a cross-org Delete removed the key: %v", err)
	}
}

func TestSQLiteStore_TenantScopeIsRequired(t *testing.T) {
	f := newSQLiteFixture(t)
	k, _ := mintFor(t, f.tc, []string{authn.ScopeSheetsRead}, nil, nil)
	bare := context.Background()

	if err := f.store.Save(bare, k); !tenant.IsMissingError(err) {
		t.Errorf("Save err = %v, want tenant.MissingError", err)
	}
	if _, err := f.store.ByID(bare, k.ID); !tenant.IsMissingError(err) {
		t.Errorf("ByID err = %v, want tenant.MissingError", err)
	}
	if _, err := f.store.List(bare, f.tc.OrgID, f.tc.ProjectID); !tenant.IsMissingError(err) {
		t.Errorf("List err = %v, want tenant.MissingError", err)
	}
	if err := f.store.Delete(bare, k.ID); !tenant.IsMissingError(err) {
		t.Errorf("Delete err = %v, want tenant.MissingError", err)
	}
}

func TestSQLiteStore_TouchLastUsed(t *testing.T) {
	f := newSQLiteFixture(t)
	ctx := tenant.Into(context.Background(), f.tc)
	k, _ := mintFor(t, f.tc, []string{authn.ScopeSheetsRead}, nil, nil)
	if err := f.store.Save(ctx, k); err != nil {
		t.Fatalf("Save: %v", err)
	}
	at := time.Now().UTC().Truncate(time.Millisecond)

	if err := f.store.TouchLastUsed(ctx, f.tc.OrgID, f.tc.ProjectID, k.ID, at); err != nil {
		t.Fatalf("TouchLastUsed: %v", err)
	}
	got, err := f.store.ByID(ctx, k.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if got.LastUsedAt == nil || !got.LastUsedAt.Equal(at) {
		t.Errorf("LastUsedAt = %v, want %v", got.LastUsedAt, at)
	}
	if got.Name != k.Name || len(got.Scopes) != 1 || got.RevokedAt != nil {
		t.Errorf("TouchLastUsed disturbed another column: %+v", got)
	}
}

// TestSQLiteStore_TouchLastUsedMissingRowIsNotAnError pins the worker's contract: a key may be deleted
// between the request that used it and the flush that records the use.
func TestSQLiteStore_TouchLastUsedMissingRowIsNotAnError(t *testing.T) {
	f := newSQLiteFixture(t)
	ctx := tenant.Into(context.Background(), f.tc)
	if err := f.store.TouchLastUsed(ctx, f.tc.OrgID, f.tc.ProjectID, uuid.New(), time.Now().UTC()); err != nil {
		t.Fatalf("TouchLastUsed on an absent key: %v", err)
	}
}

func TestSQLiteStore_TouchLastUsedIsScopedToTheOrgAndProject(t *testing.T) {
	f := newSQLiteFixture(t)
	ctx := tenant.Into(context.Background(), f.tc)
	k, _ := mintFor(t, f.tc, []string{authn.ScopeSheetsRead}, nil, nil)
	if err := f.store.Save(ctx, k); err != nil {
		t.Fatalf("Save: %v", err)
	}
	at := time.Now().UTC().Truncate(time.Millisecond)

	foreignOrg := uuid.New()
	cases := map[string]struct{ orgID, projectID uuid.UUID }{
		"another org":     {orgID: foreignOrg, projectID: f.tc.ProjectID},
		"another project": {orgID: f.tc.OrgID, projectID: uuid.New()},
	}
	for name, c := range cases {
		scoped := tenant.Into(context.Background(), tenant.Context{OrgID: c.orgID, ProjectID: c.projectID})
		if err := f.store.TouchLastUsed(scoped, c.orgID, c.projectID, k.ID, at); err != nil {
			t.Fatalf("TouchLastUsed under %s: %v", name, err)
		}
	}
	got, err := f.store.ByID(ctx, k.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if got.LastUsedAt != nil {
		t.Errorf("LastUsedAt = %v, want nil — an out-of-tenant touch wrote the row", got.LastUsedAt)
	}
}

func TestSQLiteStore_TouchLastUsedRequiresTenantScope(t *testing.T) {
	f := newSQLiteFixture(t)
	err := f.store.TouchLastUsed(context.Background(), f.tc.OrgID, f.tc.ProjectID, uuid.New(), time.Now().UTC())
	if !tenant.IsMissingError(err) {
		t.Fatalf("err = %v, want tenant.MissingError", err)
	}
}
