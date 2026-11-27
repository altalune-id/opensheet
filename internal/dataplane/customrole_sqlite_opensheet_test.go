package dataplane

import (
	"context"
	"database/sql"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/db"
	sqliteent "altalune.id/opensheet/internal/platform/db/entity/sqlite"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/role"
	"altalune.id/opensheet/internal/testutil/fakes"
	"altalune.id/opensheet/schema"
)

type notSystem struct{}

func (notSystem) IsStaff(_ context.Context, _ uuid.UUID) (bool, error) { return false, nil }

func TestDataPlane_KeyFollowsEditedCustomRole(t *testing.T) {
	body := []byte(`{"name":"ada"}`)
	g := newTokenRig(authn.ScopeSheetsRead, authn.ScopeSheetsWrite)
	g.sheets.ref.Writable = true
	want := g.missingSlugBody(t)
	orgID, creator, owner := g.orgs.ref.ID, g.creator, uuid.New()

	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	cfg := config.Defaults()
	if err := schema.MigrateUp(t.Context(), sqlDB, cfg); err != nil {
		t.Fatal(err)
	}
	prefix, now := cfg.DB.TablePrefix, sqliteent.SQLiteTime(time.Now())
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := sqlDB.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for _, u := range []uuid.UUID{creator, owner} {
		exec("INSERT INTO "+prefix+"users (id, email, name, avatar_url, is_admin, created_at, updated_at) VALUES (?, ?, '', '', 0, ?, ?)",
			u.String(), u.String()+"@x.co", now, now)
	}
	exec("INSERT INTO "+prefix+"orgs (id, slug, name, created_by, created_at, updated_at) VALUES (?, 'acme', 'Acme', ?, ?, ?)",
		orgID.String(), owner.String(), now, now)

	dbcfg, pool := db.DBConfig{Driver: db.DriverSQLite, TablePrefix: prefix}, db.Pool{W: sqlDB, R: sqlDB}
	orgs, roles := org.NewStore(dbcfg, pool, nil), role.NewStore(dbcfg, pool, nil)
	log := discardLog()
	svc := role.NewService(roles, notSystem{}, log, apperror.NewReporter(log, false).Unexpected, tenant.NewUnitOfWork(dbcfg, pool, nil))
	ownerCtx := fakes.BindRole(t.Context(), tenant.Context{OrgID: orgID, UserID: owner}, "owner")
	r, err := svc.Create(ownerCtx, "Editor", "", []authz.Permission{authz.SheetsRead, authz.SheetsWrite})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	exec("INSERT INTO "+prefix+"memberships (id, org_id, user_id, role, role_id, system, created_at) VALUES (?, ?, ?, 'custom', ?, 0, ?)",
		uuid.NewString(), orgID.String(), creator.String(), r.ID.String(), now)

	a := NewCredentialAuthorizer(g.keys, nil, authz.NewResolver(fakes.OrgRoles{Orgs: orgs, Roles: roles}), authn.Scheme{Prefix: "osk_", TokenPrefix: "othra_pt_"})
	h := g.rig
	call := func(method, path string, body []byte) (int, string) {
		rec := serve(t, NewSheetHandler(SheetHandlerParams{
			Orgs: h.orgs, Projects: h.projects, Sheets: h.sheets, Reader: h.reader, Inspector: h.inspector,
			Purger: h.purger, Writer: h.writer, Tabs: h.tabs, Authz: a, Caps: h.caps, Log: discardLog(),
		}), method, path, body, http.Header{"Authorization": {"Bearer " + testKey}})
		return rec.Code, rec.Body.String()
	}

	if code, got := call(http.MethodPost, rowsPath+"/rows", body); code != http.StatusCreated {
		t.Fatalf("key whose creator's custom role holds sheets:write: %d %s, want 201", code, got)
	}
	if _, err := svc.Update(ownerCtx, r.ID, "Editor", "", []authz.Permission{authz.SheetsRead}, r.Version); err != nil {
		t.Fatalf("edit role: %v", err)
	}
	if code, got := call(http.MethodPost, rowsPath+"/rows", body); code != http.StatusNotFound || got != want {
		t.Errorf("key after its creator's role lost sheets:write: %d %s, want the missing-slug 404", code, got)
	}
	if code, got := call(http.MethodGet, rowsPath, nil); code != http.StatusOK {
		t.Errorf("key after the edit still reads: %d %s, want 200", code, got)
	}
}
