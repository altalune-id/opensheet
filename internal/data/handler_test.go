package data

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	"altalune.id/opensheet/gworkspace"
	"altalune.id/opensheet/gworkspace/gsheet"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/gwerr"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/sheet"
	"altalune.id/opensheet/internal/spreadsheet"
)

type fakeOrgs struct {
	ref  OrgRef
	err  error
	ctxs []context.Context
}

func (f *fakeOrgs) BySlug(ctx context.Context, _ string) (OrgRef, error) {
	f.ctxs = append(f.ctxs, ctx)
	return f.ref, f.err
}

type fakeProjects struct {
	ref  ProjectRef
	err  error
	ctxs []context.Context
}

func (f *fakeProjects) BySlug(ctx context.Context, _ uuid.UUID, _ string) (ProjectRef, error) {
	f.ctxs = append(f.ctxs, ctx)
	return f.ref, f.err
}

type fakeSheets struct {
	ref  *sheet.Sheet
	err  error
	ctxs []context.Context
}

func (f *fakeSheets) BySlug(ctx context.Context, _, _ uuid.UUID, _ string) (*sheet.Sheet, error) {
	f.ctxs = append(f.ctxs, ctx)
	return f.ref, f.err
}

type fakeReader struct {
	rows  sheet.Rows
	err   error
	calls []uuid.UUID
}

func (f *fakeReader) Rows(_ context.Context, sh *sheet.Sheet) (sheet.Rows, error) {
	f.calls = append(f.calls, sh.ID)
	return f.rows, f.err
}

type fakeInspector struct {
	info  sheet.TableInfo
	err   error
	calls []uuid.UUID
}

func (f *fakeInspector) TableInfo(_ context.Context, sh *sheet.Sheet) (sheet.TableInfo, error) {
	f.calls = append(f.calls, sh.ID)
	if f.err != nil {
		return sheet.TableInfo{}, f.err
	}
	return f.info, nil
}

type appendCall struct {
	sheetID  uuid.UUID
	cells    []any
	idemKey  string
	bodyHash string
}

type patchCall struct {
	sheetID uuid.UUID
	rowID   string
	patch   map[string]any
}

type fakeWriter struct {
	appended  int
	appendErr error
	row       gsheet.Row
	patchErr  error
	appends   []appendCall
	patches   []patchCall
}

func (f *fakeWriter) Append(_ context.Context, sh *sheet.Sheet, cells []any, idemKey, bodyHash string) (int, error) {
	f.appends = append(f.appends, appendCall{sheetID: sh.ID, cells: cells, idemKey: idemKey, bodyHash: bodyHash})
	if f.appendErr != nil {
		return 0, f.appendErr
	}
	return f.appended, nil
}

func (f *fakeWriter) PatchRow(_ context.Context, sh *sheet.Sheet, id string, patch map[string]any) (gsheet.Row, error) {
	f.patches = append(f.patches, patchCall{sheetID: sh.ID, rowID: id, patch: patch})
	if f.patchErr != nil {
		return nil, f.patchErr
	}
	return f.row, nil
}

type addTabCall struct {
	spreadsheetID uuid.UUID
	title         string
}

type fakeTabber struct {
	tabs    []string
	listErr error
	addErr  error
	lists   []uuid.UUID
	adds    []addTabCall
}

func (f *fakeTabber) ListTabs(_ context.Context, spreadsheetID uuid.UUID) ([]string, error) {
	f.lists = append(f.lists, spreadsheetID)
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.tabs, nil
}

func (f *fakeTabber) AddTab(_ context.Context, spreadsheetID uuid.UUID, title string) error {
	f.adds = append(f.adds, addTabCall{spreadsheetID: spreadsheetID, title: title})
	return f.addErr
}

type fakePurger struct {
	err   error
	calls []uuid.UUID
}

func (f *fakePurger) PurgeCache(_ context.Context, sheetID uuid.UUID) error {
	f.calls = append(f.calls, sheetID)
	return f.err
}

type authorizeCall struct {
	raw       string
	scope     string
	orgID     uuid.UUID
	projectID uuid.UUID
	sheetID   uuid.UUID
}

type fakeAuthorizer struct {
	err          error
	calls        []authorizeCall
	projectCalls []authorizeCall
}

func (f *fakeAuthorizer) Authorize(_ context.Context, raw, scope string, orgID, projectID, sheetID uuid.UUID) (session.Principal, error) {
	f.calls = append(f.calls, authorizeCall{raw: raw, scope: scope, orgID: orgID, projectID: projectID, sheetID: sheetID})
	if f.err != nil {
		return session.Principal{}, f.err
	}
	return session.Principal{Source: session.SourceAPIKey, ActiveOrgID: orgID, ActiveProjectID: projectID}, nil
}

func (f *fakeAuthorizer) AuthorizeProject(_ context.Context, raw, scope string, orgID, projectID uuid.UUID) (session.Principal, error) {
	f.projectCalls = append(f.projectCalls, authorizeCall{raw: raw, scope: scope, orgID: orgID, projectID: projectID})
	if f.err != nil {
		return session.Principal{}, f.err
	}
	return session.Principal{Source: session.SourceAPIKey, ActiveOrgID: orgID, ActiveProjectID: projectID}, nil
}

type fakeCaps struct{ public bool }

func (f fakeCaps) PublicSheetsEnabled() bool { return f.public }

type rig struct {
	orgs          *fakeOrgs
	projects      *fakeProjects
	sheets        *fakeSheets
	reader        *fakeReader
	inspector     *fakeInspector
	purger        *fakePurger
	writer        *fakeWriter
	tabs          *fakeTabber
	authz         *fakeAuthorizer
	caps          fakeCaps
	basePath      string
	spreadsheetID uuid.UUID
}

func newRig() *rig {
	return &rig{
		orgs:     &fakeOrgs{ref: OrgRef{ID: uuid.Must(uuid.NewV7())}},
		projects: &fakeProjects{ref: ProjectRef{ID: uuid.Must(uuid.NewV7())}},
		sheets: &fakeSheets{ref: &sheet.Sheet{
			ID:         uuid.Must(uuid.NewV7()),
			Visibility: sheet.VisibilityKey,
			CacheTTL:   time.Minute,
		}},
		reader: &fakeReader{rows: sheet.Rows{
			Values:    []gsheet.Row{{"name": "ada", "role": "eng"}},
			ETag:      "deadbeef",
			FetchedAt: time.Now().UTC(),
		}},
		inspector: &fakeInspector{info: sheet.TableInfo{
			Columns:           []string{"id", "name"},
			IDColumn:          true,
			SatisfiesContract: true,
			RowCount:          2,
			Generation:        7,
		}},
		purger:        &fakePurger{},
		writer:        &fakeWriter{appended: 1, row: gsheet.Row{"id": "42", "name": "ada"}},
		tabs:          &fakeTabber{tabs: []string{"Rates", "Payroll"}},
		authz:         &fakeAuthorizer{},
		caps:          fakeCaps{public: true},
		spreadsheetID: uuid.Must(uuid.NewV7()),
	}
}

func (g *rig) handler() http.Handler {
	return NewHandler(HandlerParams{
		BasePath:   g.basePath,
		Orgs:       g.orgs,
		Projects:   g.projects,
		Sheets:     g.sheets,
		Reader:     g.reader,
		Inspector:  g.inspector,
		Purger:     g.purger,
		Writer:     g.writer,
		Tabs:       g.tabs,
		Authz:      g.authz,
		Caps:       g.caps,
		DefaultTTL: 30 * time.Second,
		Log:        slog.New(slog.DiscardHandler),
	})
}

func (g *rig) do(t *testing.T, method, path string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	return g.send(t, method, path, nil, header)
}

func (g *rig) send(t *testing.T, method, path string, body []byte, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader = http.NoBody
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req := httptest.NewRequestWithContext(t.Context(), method, path, reader)
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	rec := httptest.NewRecorder()
	g.handler().ServeHTTP(rec, req)
	return rec
}

func (g *rig) tabsPath() string {
	return "/api/v1/orgs/acme/projects/default/spreadsheets/" + g.spreadsheetID.String() + "/tabs"
}

const rowsPath = "/api/v1/orgs/acme/projects/default/sheets/prices"

func TestHandler_GetServesRowsAsAJSONArrayKeyedByTheHeaderRow(t *testing.T) {
	g := newRig()
	g.sheets.ref.Visibility = sheet.VisibilityPublic

	rec := g.do(t, http.MethodGet, rowsPath, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got, want := rec.Body.String(), `[{"name":"ada","role":"eng"}]`; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
	if got, want := rec.Header().Get("Content-Type"), "application/json; charset=utf-8"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}
	if got, want := rec.Header().Get("ETag"), `"deadbeef"`; got != want {
		t.Errorf("ETag = %q, want %q", got, want)
	}
	if got, want := rec.Header().Get("Cache-Control"), "public, max-age=60"; got != want {
		t.Errorf("Cache-Control = %q, want %q", got, want)
	}
}

func TestHandler_GetServesTheSnapshotBytesVerbatimSoTheETagStillDescribesTheBody(t *testing.T) {
	g := newRig()
	g.sheets.ref.Visibility = sheet.VisibilityPublic
	// Key order and spacing a re-marshal of Values would never reproduce: if the handler
	// re-serialized, ETag would describe bytes the client never saw.
	stored := []byte(`[{"role":"eng","name":"ada"}]`)
	g.reader.rows.Payload = stored

	rec := g.do(t, http.MethodGet, rowsPath, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != string(stored) {
		t.Errorf("body = %s, want the stored payload %s", got, stored)
	}
}

func TestHandler_GetNeedsNoCredentialForAPublicSheet(t *testing.T) {
	g := newRig()
	g.sheets.ref.Visibility = sheet.VisibilityPublic

	rec := g.do(t, http.MethodGet, rowsPath, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(g.authz.calls) != 0 {
		t.Errorf("Authorize was called %d times for a public sheet, want 0", len(g.authz.calls))
	}
}

func TestHandler_GetEmptySheetServesAnEmptyArray(t *testing.T) {
	g := newRig()
	g.sheets.ref.Visibility = sheet.VisibilityPublic
	g.reader.rows.Values = nil

	rec := g.do(t, http.MethodGet, rowsPath, nil)

	if got := rec.Body.String(); got != "[]" {
		t.Errorf("body = %s, want []", got)
	}
}

func TestHandler_GetKeySheetAuthorizesTheResolvedSheetAfterResolvingThePath(t *testing.T) {
	g := newRig()
	hdr := http.Header{"Authorization": {"Bearer osk_secret"}}

	rec := g.do(t, http.MethodGet, rowsPath, hdr)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(g.authz.calls) != 1 {
		t.Fatalf("Authorize calls = %d, want 1", len(g.authz.calls))
	}
	call := g.authz.calls[0]
	if call.raw != "osk_secret" {
		t.Errorf("raw = %q, want the bearer credential", call.raw)
	}
	if call.scope != authn.ScopeSheetsRead {
		t.Errorf("scope = %q, want %q", call.scope, authn.ScopeSheetsRead)
	}
	if call.orgID != g.orgs.ref.ID || call.projectID != g.projects.ref.ID || call.sheetID != g.sheets.ref.ID {
		t.Errorf("Authorize got ids (%s,%s,%s), want the resolved (%s,%s,%s)",
			call.orgID, call.projectID, call.sheetID, g.orgs.ref.ID, g.projects.ref.ID, g.sheets.ref.ID)
	}
	if got, want := rec.Header().Get("Cache-Control"), "private, max-age=60"; got != want {
		t.Errorf("Cache-Control = %q, want %q", got, want)
	}
}

// SECURITY: a missing sheet, a key for another project and a public sheet the capability forbids must be
// one answer, byte for byte, or the data plane becomes a slug and grant oracle.
func TestHandler_GetMissingForbiddenAndCapabilityOffAreIndistinguishable(t *testing.T) {
	cases := map[string]func(*rig){
		"no such sheet": func(g *rig) {
			g.sheets.err = apperror.New(apperror.CodeSheetNotFound, "Sheet not found", codes.NotFound)
		},
		"key scoped to another project": func(g *rig) {
			g.authz.err = apperror.New(apperror.CodeAPIKeyUnauthorized, "Unauthorized", codes.Unauthenticated)
		},
		"public sheet while the capability is off": func(g *rig) {
			g.sheets.ref.Visibility = sheet.VisibilityPublic
			g.caps = fakeCaps{public: false}
		},
	}

	type answer struct {
		code        int
		body        string
		contentType string
	}
	want := answer{
		code:        http.StatusNotFound,
		body:        `{"error":{"code":"SHT001","message":"Sheet not found"}}`,
		contentType: "application/json; charset=utf-8",
	}
	for name, arrange := range cases {
		t.Run(name, func(t *testing.T) {
			g := newRig()
			arrange(g)
			rec := g.do(t, http.MethodGet, rowsPath, http.Header{"Authorization": {"Bearer osk_secret"}})
			got := answer{code: rec.Code, body: rec.Body.String(), contentType: rec.Header().Get("Content-Type")}
			if got != want {
				t.Errorf("answered %+v, want %+v", got, want)
			}
		})
	}
}

// SECURITY: sheet.ReadWorkflow re-checks the capability and its own SHT006 names the slug, so the handler
// must collapse it to the same 404 rather than let it become a distinguishable 403.
func TestHandler_GetPublicDisabledFromTheReadPathIsTheSame404(t *testing.T) {
	g := newRig()
	g.sheets.ref.Visibility = sheet.VisibilityPublic
	g.reader.err = apperror.New(apperror.CodeSheetPublicDisabled,
		"Public sheets are disabled on this deployment", codes.FailedPrecondition)

	rec := g.do(t, http.MethodGet, rowsPath, nil)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if got, want := rec.Body.String(), `{"error":{"code":"SHT001","message":"Sheet not found"}}`; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestHandler_GetMaskedProjectMissStillReportsTheSheetNotFoundCode(t *testing.T) {
	g := newRig()
	g.projects.err = apperror.New(apperror.CodeProjectNotFound, "Project not found", codes.NotFound)

	rec := g.do(t, http.MethodGet, rowsPath, nil)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if got, want := rec.Body.String(), `{"error":{"code":"SHT001","message":"Sheet not found"}}`; got != want {
		t.Errorf("body = %s, want the masked envelope %s", got, want)
	}
}

func TestHandler_GetNotModifiedOnAMatchingIfNoneMatch(t *testing.T) {
	g := newRig()
	g.sheets.ref.Visibility = sheet.VisibilityPublic

	for _, header := range []string{`"deadbeef"`, `W/"deadbeef"`, `"other", "deadbeef"`, "*"} {
		t.Run(header, func(t *testing.T) {
			rec := g.do(t, http.MethodGet, rowsPath, http.Header{"If-None-Match": {header}})
			if rec.Code != http.StatusNotModified {
				t.Errorf("status = %d, want 304", rec.Code)
			}
			if b := rec.Body.String(); b != "" {
				t.Errorf("body = %q, want empty", b)
			}
			if got, want := rec.Header().Get("ETag"), `"deadbeef"`; got != want {
				t.Errorf("ETag = %q, want %q", got, want)
			}
		})
	}
}

func TestHandler_GetStaleETagStillServesTheBody(t *testing.T) {
	g := newRig()
	g.sheets.ref.Visibility = sheet.VisibilityPublic

	rec := g.do(t, http.MethodGet, rowsPath, http.Header{"If-None-Match": {`"stale"`}})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() == "" {
		t.Error("a non-matching If-None-Match must still serve the rows")
	}
}

func TestHandler_GetStaleSnapshotIsFlaggedWithAnAge(t *testing.T) {
	g := newRig()
	g.sheets.ref.Visibility = sheet.VisibilityPublic
	g.reader.rows.Cached = true
	g.reader.rows.Stale = true
	g.reader.rows.FetchedAt = time.Now().UTC().Add(-90 * time.Second)

	rec := g.do(t, http.MethodGet, rowsPath, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got, want := rec.Header().Get(StaleHeader), "true"; got != want {
		t.Errorf("%s = %q, want %q", StaleHeader, got, want)
	}
	age, err := strconv.Atoi(rec.Header().Get("Age"))
	if err != nil {
		t.Fatalf("Age = %q, want an integer", rec.Header().Get("Age"))
	}
	if age < 90 {
		t.Errorf("Age = %d, want at least 90", age)
	}
	// NOTE: RFC 9111 obsoleted Warning; most clients ignore it, so it must not be sent.
	if got := rec.Header().Get("Warning"); got != "" {
		t.Errorf("Warning = %q, want none", got)
	}
}

func TestHandler_GetReauthNeededIs424AndNeverServesStale(t *testing.T) {
	g := newRig()
	g.sheets.ref.Visibility = sheet.VisibilityPublic
	g.reader.err = apperror.New(apperror.CodeCredentialReauthNeeded,
		"The Google credential needs to be reconnected", codes.FailedPrecondition)

	rec := g.do(t, http.MethodGet, rowsPath, nil)

	if rec.Code != http.StatusFailedDependency {
		t.Fatalf("status = %d, want 424; body=%s", rec.Code, rec.Body.String())
	}
	if got, want := rec.Body.String(), `"code":"`+apperror.CodeCredentialReauthNeeded+`"`; !strings.Contains(got, want) {
		t.Errorf("body = %s, want it to carry %s", got, want)
	}
	if got := rec.Header().Get(StaleHeader); got != "" {
		t.Errorf("%s = %q, want none — a revoked grant must not keep serving rows", StaleHeader, got)
	}
}

func TestHandler_GetGoogleOutageWithNoSnapshotMapsThroughTheEnvelope(t *testing.T) {
	g := newRig()
	g.sheets.ref.Visibility = sheet.VisibilityPublic
	g.reader.err = apperror.New(apperror.CodeGoogleUnavailable, "Google Sheets is unavailable", codes.Unavailable)

	rec := g.do(t, http.MethodGet, rowsPath, nil)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	if got, want := rec.Body.String(), `"code":"`+apperror.CodeGoogleUnavailable+`"`; !strings.Contains(got, want) {
		t.Errorf("body = %s, want it to carry %s", got, want)
	}
}

func TestHandler_UnexpectedFailureIs500AndNotMaskedAsNotFound(t *testing.T) {
	g := newRig()
	g.orgs.err = errors.New("connection refused")

	rec := g.do(t, http.MethodGet, rowsPath, nil)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 — a database outage must not read as a missing sheet", rec.Code)
	}
	if got, want := rec.Body.String(), `"code":"`+apperror.CodeUnexpectedError+`"`; !strings.Contains(got, want) {
		t.Errorf("body = %s, want it to carry %s", got, want)
	}
}

func TestHandler_ErrorRepliesAreNotStored(t *testing.T) {
	g := newRig()
	g.sheets.err = apperror.New(apperror.CodeSheetNotFound, "Sheet not found", codes.NotFound)

	rec := g.do(t, http.MethodGet, rowsPath, nil)

	if got, want := rec.Header().Get("Cache-Control"), "no-store"; got != want {
		t.Errorf("Cache-Control = %q, want %q", got, want)
	}
}

func TestHandler_DeleteCachePurgesThroughTheCachePurgeScope(t *testing.T) {
	g := newRig()

	rec := g.do(t, http.MethodDelete, rowsPath+"/cache", http.Header{"X-API-Key": {"osk_secret"}})

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if len(g.authz.calls) != 1 || g.authz.calls[0].scope != authn.ScopeCachePurge {
		t.Fatalf("Authorize calls = %+v, want one call with %q", g.authz.calls, authn.ScopeCachePurge)
	}
	if len(g.purger.calls) != 1 || g.purger.calls[0] != g.sheets.ref.ID {
		t.Errorf("PurgeCache calls = %v, want [%s]", g.purger.calls, g.sheets.ref.ID)
	}
}

func TestHandler_DeleteCacheOnAPublicSheetStillNeedsTheScope(t *testing.T) {
	g := newRig()
	g.sheets.ref.Visibility = sheet.VisibilityPublic

	rec := g.do(t, http.MethodDelete, rowsPath+"/cache", nil)

	if len(g.authz.calls) != 1 {
		t.Fatalf("Authorize calls = %d, want 1 — a public sheet does not make its cache purgeable", len(g.authz.calls))
	}
	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
}

func TestHandler_DeleteCacheRefusalIsTheSame404(t *testing.T) {
	g := newRig()
	g.authz.err = apperror.New(apperror.CodeAPIKeyInsufficientScope, "Insufficient scope", codes.PermissionDenied)

	rec := g.do(t, http.MethodDelete, rowsPath+"/cache", nil)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if got, want := rec.Body.String(), `{"error":{"code":"SHT001","message":"Sheet not found"}}`; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
	if len(g.purger.calls) != 0 {
		t.Errorf("PurgeCache ran %d times after a refused authorization, want 0", len(g.purger.calls))
	}
}

func TestHandler_DeleteCachePurgeFailurePropagates(t *testing.T) {
	g := newRig()
	g.purger.err = errors.New("snapshot store down")

	rec := g.do(t, http.MethodDelete, rowsPath+"/cache", nil)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestHandler_UnknownPathUnderTheMountAnswersJSON(t *testing.T) {
	g := newRig()

	for _, path := range []string{"/api/v1/", "/api/v1/orgs/acme", "/api/v1/orgs/acme/projects/default/sheets/prices/rows"} {
		t.Run(path, func(t *testing.T) {
			rec := g.do(t, http.MethodGet, path, nil)
			if rec.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404", rec.Code)
			}
			if got, want := rec.Header().Get("Content-Type"), "application/json; charset=utf-8"; got != want {
				t.Errorf("Content-Type = %q, want %q", got, want)
			}
		})
	}
}

func TestHandler_MountsUnderTheConfiguredBasePath(t *testing.T) {
	g := newRig()
	g.basePath = "/app"
	g.sheets.ref.Visibility = sheet.VisibilityPublic

	if rec := g.do(t, http.MethodGet, "/app"+rowsPath, nil); rec.Code != http.StatusOK {
		t.Errorf("GET /app%s: status = %d, want 200; body=%s", rowsPath, rec.Code, rec.Body.String())
	}
	if rec := g.do(t, http.MethodGet, rowsPath, nil); rec.Code == http.StatusOK {
		t.Error("the unprefixed path must not serve when a basePath is configured")
	}
}

func TestHandler_ReaderReceivesTheResolvedSheetID(t *testing.T) {
	g := newRig()
	g.sheets.ref.Visibility = sheet.VisibilityPublic

	g.do(t, http.MethodGet, rowsPath, nil)

	if len(g.reader.calls) != 1 || g.reader.calls[0] != g.sheets.ref.ID {
		t.Errorf("Rows calls = %v, want [%s]", g.reader.calls, g.sheets.ref.ID)
	}
}

// A sheet with CacheTTL 0 means "use the configured default", so max-age must advertise
// the lifetime the server actually caches for — not 0, which would tell clients not to
// cache something the server does cache.
func TestHandler_CacheControlFallsBackToTheConfiguredDefault(t *testing.T) {
	g := newRig()
	g.sheets.ref.CacheTTL = 0

	rec := g.do(t, http.MethodGet, rowsPath, nil)

	if got, want := rec.Header().Get("Cache-Control"), "private, max-age=30"; got != want {
		t.Errorf("Cache-Control = %q, want %q", got, want)
	}
}

func TestCacheControl(t *testing.T) {
	cases := []struct {
		name string
		in   *sheet.Sheet
		want string
	}{
		{"public", &sheet.Sheet{Visibility: sheet.VisibilityPublic, CacheTTL: 30 * time.Second}, "public, max-age=30"},
		{"key", &sheet.Sheet{Visibility: sheet.VisibilityKey, CacheTTL: 2 * time.Minute}, "private, max-age=120"},
		{"negative ttl falls back too", &sheet.Sheet{Visibility: sheet.VisibilityKey, CacheTTL: -time.Second}, "private, max-age=45"},
		{"zero ttl falls back to the configured default", &sheet.Sheet{Visibility: sheet.VisibilityKey}, "private, max-age=45"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cacheControl(tc.in, 45*time.Second); got != tc.want {
				t.Errorf("cacheControl = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMatchesETag(t *testing.T) {
	cases := []struct {
		header string
		want   bool
	}{
		{"", false},
		{`"abc"`, true},
		{`W/"abc"`, true},
		{`"x", "abc"`, true},
		{"*", true},
		{`"abcd"`, false},
		{"abc", false},
		{`"x"`, false},
	}
	for _, tc := range cases {
		t.Run(tc.header, func(t *testing.T) {
			if got := matchesETag(tc.header, "abc"); got != tc.want {
				t.Errorf("matchesETag(%q, abc) = %v, want %v", tc.header, got, tc.want)
			}
		})
	}
}

func TestAgeSeconds(t *testing.T) {
	if got := ageSeconds(time.Now().UTC().Add(-time.Minute)); got < 60 {
		t.Errorf("ageSeconds = %d, want at least 60", got)
	}
	if got := ageSeconds(time.Now().UTC().Add(time.Hour)); got != 0 {
		t.Errorf("ageSeconds for a future fetch = %d, want 0", got)
	}
}

const (
	rowPath  = rowsPath + "/rows/42"
	bearer   = "Bearer osk_secret"
	jsonType = "application/json; charset=utf-8"
)

func keyHeader() http.Header { return http.Header{"Authorization": {bearer}} }

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("body %s is not a JSON object: %v", rec.Body.String(), err)
	}
	return out
}

func TestHandler_AppendRequiresTheWriteScope(t *testing.T) {
	g := newRig()

	rec := g.send(t, http.MethodPost, rowsPath, []byte(`{"values":["a"]}`), keyHeader())

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(g.authz.calls) != 1 {
		t.Fatalf("Authorize calls = %d, want 1", len(g.authz.calls))
	}
	call := g.authz.calls[0]
	if call.scope != authn.ScopeSheetsWrite {
		t.Errorf("scope = %q, want %q", call.scope, authn.ScopeSheetsWrite)
	}
	if call.sheetID != g.sheets.ref.ID {
		t.Errorf("sheetID = %s, want the resolved sheet %s — the grant is per sheet", call.sheetID, g.sheets.ref.ID)
	}
}

func TestHandler_AppendWithoutTheScopeIsTheSame404AndNeverWrites(t *testing.T) {
	g := newRig()
	g.authz.err = apperror.New(apperror.CodeAPIKeyInsufficientScope, "Insufficient scope", codes.PermissionDenied)

	rec := g.send(t, http.MethodPost, rowsPath, []byte(`{"values":["a"]}`), keyHeader())

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if got, want := rec.Body.String(), `{"error":{"code":"SHT001","message":"Sheet not found"}}`; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
	if len(g.writer.appends) != 0 {
		t.Errorf("Append ran %d times after a refused authorization, want 0", len(g.writer.appends))
	}
}

// SECURITY: nothing in the handler filters formulas — valueInputOption=RAW does, inside the writer.
// So the cells must reach the workflow verbatim, or the control being tested is not the one in force.
func TestHandler_AppendPassesFormulaTextToTheWriterVerbatim(t *testing.T) {
	for _, cell := range []string{"=Payroll!A1", "+1+1", "-1-1", `=IMPORTXML("http://x/","//a")`} {
		t.Run(cell, func(t *testing.T) {
			g := newRig()
			body, err := json.Marshal(map[string]any{"values": []any{cell}})
			if err != nil {
				t.Fatal(err)
			}

			rec := g.send(t, http.MethodPost, rowsPath, body, keyHeader())

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
			}
			if len(g.writer.appends) != 1 {
				t.Fatalf("Append calls = %d, want 1", len(g.writer.appends))
			}
			if got := g.writer.appends[0].cells; len(got) != 1 || got[0] != cell {
				t.Errorf("cells = %#v, want [%q] unchanged", got, cell)
			}
		})
	}
}

func TestHandler_AppendReturnsTheAppendedCount(t *testing.T) {
	g := newRig()
	g.writer.appended = 1

	rec := g.send(t, http.MethodPost, rowsPath, []byte(`{"values":["a","b"]}`), keyHeader())

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got, want := rec.Body.String(), `{"appended":1}`; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
	if got, want := rec.Header().Get("Content-Type"), jsonType; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}
	if got, want := rec.Header().Get("Cache-Control"), "no-store"; got != want {
		t.Errorf("Cache-Control = %q, want %q — a write reply is never cacheable", got, want)
	}
}

func TestHandler_AppendAcceptsABareArrayForPilotCompatibility(t *testing.T) {
	g := newRig()

	rec := g.send(t, http.MethodPost, rowsPath, []byte(`  ["Aston",1250000]`), keyHeader())

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(g.writer.appends) != 1 {
		t.Fatalf("Append calls = %d, want 1", len(g.writer.appends))
	}
	cells := g.writer.appends[0].cells
	if len(cells) != 2 || cells[0] != "Aston" {
		t.Fatalf("cells = %#v", cells)
	}
	if n, ok := cells[1].(float64); !ok || n != 1250000 {
		t.Errorf("cells[1] = %#v, want the JSON number 1250000 — RAW performs no coercion", cells[1])
	}
}

func TestHandler_AppendRejectsABodyItCannotRead(t *testing.T) {
	cases := map[string]string{
		"empty":               ``,
		"not json":            `nope`,
		"values not an array": `{"values":{"a":1}}`,
		"nested cell":         `{"values":[["a"]]}`,
		"numeric hint":        `{"values":["1"],"numeric_columns":["rate"]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			g := newRig()

			rec := g.send(t, http.MethodPost, rowsPath, []byte(body), keyHeader())

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			if len(g.writer.appends) != 0 {
				t.Errorf("Append ran on a body the handler could not read")
			}
		})
	}
}

func TestHandler_AppendForwardsTheIdempotencyKeyAndABodyHash(t *testing.T) {
	g := newRig()
	body := []byte(`{"values":["a"]}`)

	g.send(t, http.MethodPost, rowsPath, body, http.Header{
		"Authorization":   {bearer},
		"Idempotency-Key": {"idem-1"},
	})
	g.send(t, http.MethodPost, rowsPath, body, http.Header{
		"Authorization":   {bearer},
		"Idempotency-Key": {"idem-1"},
	})
	g.send(t, http.MethodPost, rowsPath, []byte(`{"values":["b"]}`), http.Header{
		"Authorization":   {bearer},
		"Idempotency-Key": {"idem-1"},
	})

	if len(g.writer.appends) != 3 {
		t.Fatalf("Append calls = %d, want 3", len(g.writer.appends))
	}
	for i, call := range g.writer.appends {
		if call.idemKey != "idem-1" {
			t.Errorf("call %d: idemKey = %q, want idem-1", i, call.idemKey)
		}
		if call.bodyHash == "" {
			t.Errorf("call %d: bodyHash is empty, so a replay could never compare bodies", i)
		}
	}
	if g.writer.appends[0].bodyHash != g.writer.appends[1].bodyHash {
		t.Error("the same body must hash the same, or a legitimate retry reads as a mismatch")
	}
	if g.writer.appends[1].bodyHash == g.writer.appends[2].bodyHash {
		t.Error("a different body must hash differently, or a reused key would replay the wrong row")
	}
}

// A retry that re-serializes its body, or switches to the bare-array shape, must replay rather than
// read as a different body and be refused 422.
func TestHandler_AppendHashesTheCellsNotTheRawBytes(t *testing.T) {
	g := newRig()
	header := http.Header{"Authorization": {bearer}, "Idempotency-Key": {"idem-1"}}

	for _, body := range []string{`{"values":["a",1]}`, "{\n  \"values\": [ \"a\", 1 ]\n}", `["a",1]`} {
		if rec := g.send(t, http.MethodPost, rowsPath, []byte(body), header); rec.Code != http.StatusOK {
			t.Fatalf("body %s: status = %d, want 200", body, rec.Code)
		}
	}

	if len(g.writer.appends) != 3 {
		t.Fatalf("Append calls = %d, want 3", len(g.writer.appends))
	}
	first := g.writer.appends[0].bodyHash
	for i, call := range g.writer.appends {
		if call.bodyHash != first {
			t.Errorf("call %d hashed %s, want the same %s — the cells are identical", i, call.bodyHash, first)
		}
	}
}

func TestHandler_AppendWithoutTheHeaderSendsNoIdempotencyKey(t *testing.T) {
	g := newRig()

	g.send(t, http.MethodPost, rowsPath, []byte(`{"values":["a"]}`), keyHeader())

	if len(g.writer.appends) != 1 {
		t.Fatalf("Append calls = %d, want 1", len(g.writer.appends))
	}
	if got := g.writer.appends[0].idemKey; got != "" {
		t.Errorf("idemKey = %q, want empty so the workflow reserves nothing", got)
	}
}

// Spelled out because nothing in this repo could emit 422 until the code and its httpStatusOverrides
// entry landed: a reused key with a different body must be distinguishable by a client.
func TestHandler_AppendRejectsAReusedIdempotencyKeyWithADifferentBody(t *testing.T) {
	g := newRig()
	g.writer.appendErr = &sheet.IdempotencyMismatchError{Key: "idem-1"}

	rec := g.send(t, http.MethodPost, rowsPath, []byte(`{"values":["b"]}`), http.Header{
		"Authorization":   {bearer},
		"Idempotency-Key": {"idem-1"},
	})

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); !strings.Contains(got, apperror.CodeSheetIdempotencyMismatch) {
		t.Errorf("body = %s, want it to name %s so a client can branch on it",
			got, apperror.CodeSheetIdempotencyMismatch)
	}
}

func TestHandler_PatchRowReturnsTheRowAsWritten(t *testing.T) {
	g := newRig()
	g.writer.row = gsheet.Row{"id": "42", "name": "ada2"}

	rec := g.send(t, http.MethodPatch, rowPath, []byte(`{"name":"ada2"}`), keyHeader())

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got, want := rec.Body.String(), `{"id":"42","name":"ada2"}`; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
	if len(g.writer.patches) != 1 {
		t.Fatalf("PatchRow calls = %d, want 1", len(g.writer.patches))
	}
	call := g.writer.patches[0]
	if call.rowID != "42" {
		t.Errorf("row id = %q, want 42 from the path", call.rowID)
	}
	if call.sheetID != g.sheets.ref.ID {
		t.Errorf("sheetID = %s, want the resolved sheet", call.sheetID)
	}
	if got, ok := call.patch["name"].(string); !ok || got != "ada2" {
		t.Errorf("patch = %#v, want name=ada2", call.patch)
	}
	if len(g.authz.calls) != 1 || g.authz.calls[0].scope != authn.ScopeSheetsWrite {
		t.Errorf("Authorize calls = %+v, want one call with %q", g.authz.calls, authn.ScopeSheetsWrite)
	}
}

func TestHandler_PatchRowSendsANamedNumericColumnAsANumber(t *testing.T) {
	g := newRig()

	rec := g.send(t, http.MethodPatch, rowPath,
		[]byte(`{"rate_idr":"1300000","numeric_columns":["rate_idr"]}`), keyHeader())

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	patch := g.writer.patches[0].patch
	if _, named := patch["numeric_columns"]; named {
		t.Error("numeric_columns is a hint, not a column — it must be stripped from the patch")
	}
	got, ok := patch["rate_idr"].(float64)
	if !ok {
		t.Fatalf("patch[rate_idr] = %#v, want a float64", patch["rate_idr"])
	}
	if got != 1300000 {
		t.Errorf("patch[rate_idr] = %v, want 1300000", got)
	}
}

// SECURITY: ParseFloat accepts Inf and NaN, which encoding/json then refuses to marshal — that would
// surface as a 500 from inside the Google client instead of the 400 the caller earned.
func TestHandler_PatchRowRejectsANonFiniteOrUnparseableNumericColumn(t *testing.T) {
	for _, value := range []string{"Inf", "+Inf", "-Inf", "NaN", "inf", "nan", "abc", ""} {
		t.Run(value, func(t *testing.T) {
			g := newRig()
			body, err := json.Marshal(map[string]any{"rate": value, "numeric_columns": []string{"rate"}})
			if err != nil {
				t.Fatal(err)
			}

			rec := g.send(t, http.MethodPatch, rowPath, body, keyHeader())

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			if len(g.writer.patches) != 0 {
				t.Error("a cell that is not a finite number must not reach the workflow")
			}
		})
	}
}

func TestHandler_PatchRowRejectsABodyItCannotRead(t *testing.T) {
	cases := map[string]string{
		"empty":              ``,
		"not an object":      `["a"]`,
		"nested value":       `{"name":{"a":1}}`,
		"hint not an array":  `{"name":"a","numeric_columns":"name"}`,
		"hint names nothing": `{"name":"a","numeric_columns":["rate"]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			g := newRig()

			rec := g.send(t, http.MethodPatch, rowPath, []byte(body), keyHeader())

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			if len(g.writer.patches) != 0 {
				t.Error("PatchRow ran on a body the handler could not read")
			}
		})
	}
}

func TestHandler_ListTabsRequiresTheSpreadsheetsReadScope(t *testing.T) {
	g := newRig()

	rec := g.do(t, http.MethodGet, g.tabsPath(), keyHeader())

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got, want := rec.Body.String(), `{"tabs":["Rates","Payroll"]}`; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
	if len(g.authz.projectCalls) != 1 {
		t.Fatalf("AuthorizeProject calls = %d, want 1", len(g.authz.projectCalls))
	}
	call := g.authz.projectCalls[0]
	if call.scope != authn.ScopeSpreadsheetsRead {
		t.Errorf("scope = %q, want %q", call.scope, authn.ScopeSpreadsheetsRead)
	}
	if call.orgID != g.orgs.ref.ID || call.projectID != g.projects.ref.ID {
		t.Errorf("AuthorizeProject got (%s,%s), want the resolved (%s,%s)",
			call.orgID, call.projectID, g.orgs.ref.ID, g.projects.ref.ID)
	}
	if len(g.tabs.lists) != 1 || g.tabs.lists[0] != g.spreadsheetID {
		t.Errorf("ListTabs calls = %v, want [%s] from the path", g.tabs.lists, g.spreadsheetID)
	}
}

func TestHandler_ListTabsOfASpreadsheetWithNoneServesAnEmptyArray(t *testing.T) {
	g := newRig()
	g.tabs.tabs = nil

	rec := g.do(t, http.MethodGet, g.tabsPath(), keyHeader())

	if got, want := rec.Body.String(), `{"tabs":[]}`; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

// The documented rule from the spec: the tabs routes name no sheet, so they authorize project-wide.
// A key restricted to specific sheets is refused by APIKey.Allows and cannot use them at all.
func TestHandler_TabsRoutesAuthorizeWithoutASheet(t *testing.T) {
	for _, tc := range []struct {
		method string
		body   []byte
	}{
		{http.MethodGet, nil},
		{http.MethodPost, []byte(`{"title":"Q2"}`)},
	} {
		t.Run(tc.method, func(t *testing.T) {
			g := newRig()

			g.send(t, tc.method, g.tabsPath(), tc.body, keyHeader())

			if len(g.authz.calls) != 0 {
				t.Errorf("Authorize (per sheet) was called %d times, want 0 — uuid.Nil would refuse every restricted key",
					len(g.authz.calls))
			}
			if len(g.authz.projectCalls) != 1 {
				t.Errorf("AuthorizeProject calls = %d, want 1", len(g.authz.projectCalls))
			}
		})
	}
}

func TestHandler_TabsRoutesRefusalIsTheSame404(t *testing.T) {
	g := newRig()
	g.authz.err = apperror.New(apperror.CodeAPIKeyUnauthorized, "Unauthorized", codes.Unauthenticated)

	rec := g.do(t, http.MethodGet, g.tabsPath(), keyHeader())

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if got, want := rec.Body.String(), `{"error":{"code":"SHT001","message":"Sheet not found"}}`; got != want {
		t.Errorf("body = %s, want the shared masked envelope %s", got, want)
	}
	if len(g.tabs.lists) != 0 {
		t.Error("ListTabs ran after a refused authorization")
	}
}

func TestHandler_CreateTabReturns201(t *testing.T) {
	g := newRig()

	rec := g.send(t, http.MethodPost, g.tabsPath(), []byte(`{"title":"  Q2  "}`), keyHeader())

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if got, want := rec.Body.String(), `{"created":"Q2"}`; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
	if len(g.authz.projectCalls) != 1 || g.authz.projectCalls[0].scope != authn.ScopeSpreadsheetsWrite {
		t.Errorf("AuthorizeProject calls = %+v, want one call with %q",
			g.authz.projectCalls, authn.ScopeSpreadsheetsWrite)
	}
	if len(g.tabs.adds) != 1 || g.tabs.adds[0].title != "Q2" || g.tabs.adds[0].spreadsheetID != g.spreadsheetID {
		t.Errorf("AddTab calls = %+v, want one call for %s titled Q2", g.tabs.adds, g.spreadsheetID)
	}
}

func TestHandler_CreateTabRejectsABodyWithNoTitle(t *testing.T) {
	for _, body := range []string{``, `{}`, `{"title":"   "}`, `{"title":5}`} {
		t.Run(body, func(t *testing.T) {
			g := newRig()

			rec := g.send(t, http.MethodPost, g.tabsPath(), []byte(body), keyHeader())

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			if len(g.tabs.adds) != 0 {
				t.Error("AddTab ran without a title")
			}
		})
	}
}

func TestHandler_TabsRoutesRejectAMalformedSpreadsheetID(t *testing.T) {
	g := newRig()

	rec := g.do(t, http.MethodGet, "/api/v1/orgs/acme/projects/default/spreadsheets/not-a-uuid/tabs", keyHeader())

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if len(g.tabs.lists) != 0 {
		t.Error("ListTabs ran for an id that is not a UUID")
	}
}

// SECURITY: maskNotFound covers resolution only. A resolution failure stays an indistinguishable 404,
// while a write refused after authorization reports its own status — the caller already proved its grant.
func TestHandler_MaskingBoundaryHoldsBothWays(t *testing.T) {
	t.Run("resolution failure masks to 404", func(t *testing.T) {
		g := newRig()
		g.sheets.err = apperror.New(apperror.CodeSheetNotFound, "Sheet not found", codes.NotFound)

		rec := g.send(t, http.MethodPost, rowsPath, []byte(`{"values":["a"]}`), keyHeader())

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
		if got, want := rec.Body.String(), `{"error":{"code":"SHT001","message":"Sheet not found"}}`; got != want {
			t.Errorf("body = %s, want %s", got, want)
		}
		if len(g.writer.appends) != 0 {
			t.Error("an unresolved path must never reach the workflow")
		}
	})
	t.Run("a non-writable sheet reports its own 403", func(t *testing.T) {
		g := newRig()
		g.writer.appendErr = &sheet.NotWritableError{SheetID: g.sheets.ref.ID.String(), Slug: "prices"}

		rec := g.send(t, http.MethodPost, rowsPath, []byte(`{"values":["a"]}`), keyHeader())

		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
		}
		if got := rec.Body.String(); !strings.Contains(got, apperror.CodeSheetNotWritable) {
			t.Errorf("body = %s, want it to name %s", got, apperror.CodeSheetNotWritable)
		}
	})
	t.Run("a non-writable spreadsheet reports its own 403", func(t *testing.T) {
		g := newRig()
		g.tabs.addErr = &spreadsheet.NotWritableError{
			ID: g.spreadsheetID.String(), GoogleFileID: "1SecretFileID",
		}

		rec := g.send(t, http.MethodPost, g.tabsPath(), []byte(`{"title":"Q2"}`), keyHeader())

		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, apperror.CodeSpreadsheetNotWritable) {
			t.Errorf("body = %s, want it to name %s", body, apperror.CodeSpreadsheetNotWritable)
		}
		if strings.Contains(body, "1SecretFileID") {
			t.Errorf("body = %s, want no Google file id: the caller supplied our UUID", body)
		}
	})
}

// SECURITY: the caller holds sheets:write on one sheet, so it must not learn the Google file id behind it.
func TestHandler_GooglePermissionRefusalNamesNoGoogleFileID(t *testing.T) {
	g := newRig()
	g.writer.appendErr = gwerr.AppError(&gworkspace.PermissionDeniedError{FileID: "1SecretFileID"})

	rec := g.send(t, http.MethodPost, rowsPath, []byte(`{"values":["a"]}`), keyHeader())

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, apperror.CodeGooglePermissionDenied) {
		t.Errorf("body = %s, want it to name %s", body, apperror.CodeGooglePermissionDenied)
	}
	if strings.Contains(body, "1SecretFileID") {
		t.Errorf("body = %s, want no Google file id", body)
	}
}

func TestHandler_BlankTabTitleFromTheWriterIs400NotAnInternalError(t *testing.T) {
	g := newRig()
	g.tabs.addErr = gwerr.AppError(&gsheet.InvalidTabTitleError{Title: "x"})

	rec := g.send(t, http.MethodPost, g.tabsPath(), []byte(`{"title":"x"}`), keyHeader())

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); !strings.Contains(got, apperror.CodeSheetInvalidTabTitle) {
		t.Errorf("body = %s, want it to name %s", got, apperror.CodeSheetInvalidTabTitle)
	}
}

func TestHandler_BodyOverTheCapIs413(t *testing.T) {
	g := newRig()
	oversize := append([]byte(`{"values":["`), bytes.Repeat([]byte("x"), maxWriteBodyBytes)...)
	oversize = append(oversize, []byte(`"]}`)...)

	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, rowsPath},
		{http.MethodPatch, rowPath},
	} {
		t.Run(tc.method, func(t *testing.T) {
			rec := g.send(t, tc.method, tc.path, oversize, keyHeader())

			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("status = %d, want 413; body=%s", rec.Code, rec.Body.String())
			}
			if got := rec.Body.String(); !strings.Contains(got, apperror.CodeSheetPayloadTooLarge) {
				t.Errorf("body = %s, want it to name %s", got, apperror.CodeSheetPayloadTooLarge)
			}
		})
	}
	if len(g.writer.appends) != 0 || len(g.writer.patches) != 0 {
		t.Error("an oversize body must not reach the workflow")
	}
}

func TestHandler_WriteFailureFromAnUntypedErrorIs500(t *testing.T) {
	g := newRig()
	g.writer.appendErr = errors.New("google client exploded")

	rec := g.send(t, http.MethodPost, rowsPath, []byte(`{"values":["a"]}`), keyHeader())

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if got := decodeBody(t, rec)["error"]; got == nil {
		t.Errorf("body = %s, want the shared error envelope", rec.Body.String())
	}
}

// TestHandler_Capabilities_ReportsDriftWithoutCallingGoogle is the point of the route: a client learns a
// sheet has no working deleted_at column before trying to delete a row, and pays no Google read for it.
func TestHandler_Capabilities_ReportsDriftWithoutCallingGoogle(t *testing.T) {
	g := newRig()
	g.inspector.info = sheet.TableInfo{
		Columns:           []string{"id", "name"},
		IDColumn:          true,
		SatisfiesContract: false,
		ContractReason:    `sheet: tab "Rates" has two columns named deleted_at`,
		RowCount:          1284,
		Generation:        42,
	}

	rec := g.do(t, http.MethodGet, rowsPath+"/capabilities", keyHeader())

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		SatisfiesContract bool   `json:"satisfiesContract"`
		ContractReason    string `json:"contractReason"`
		RowCount          int64  `json:"rowCount"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal err = %v; body=%s", err, rec.Body.String())
	}
	if got.SatisfiesContract {
		t.Error("satisfiesContract = true, want false")
	}
	if want := `sheet: tab "Rates" has two columns named deleted_at`; got.ContractReason != want {
		t.Errorf("contractReason = %q, want %q", got.ContractReason, want)
	}
	if got.RowCount != 1284 {
		t.Errorf("rowCount = %d, want 1284", got.RowCount)
	}
	if len(g.reader.calls) != 0 {
		t.Errorf("Rows ran %d times, want 0 — the reader is the only path to Google", len(g.reader.calls))
	}
}

func TestHandler_CapabilitiesServesTheTableInfoOfAHealthySheet(t *testing.T) {
	g := newRig()
	validatedAt := time.Date(2026, 9, 10, 4, 11, 9, 0, time.UTC)
	g.sheets.ref.Writable = true
	g.inspector.info = sheet.TableInfo{
		Columns:           []string{"id", "name", "email"},
		IDColumn:          true,
		Writable:          true,
		SatisfiesContract: true,
		RowCount:          1284,
		Generation:        42,
		ValidatedAt:       &validatedAt,
	}

	rec := g.do(t, http.MethodGet, rowsPath+"/capabilities", keyHeader())

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	want := `{"columns":["id","name","email"],"idColumn":true,"softDelete":false,"writable":true,` +
		`"satisfiesContract":true,"contractReason":"","rowCount":1284,"generation":42,` +
		`"validatedAt":"2026-09-10T04:11:09Z"}`
	if got := rec.Body.String(); got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
	if got, want := rec.Header().Get("Content-Type"), "application/json; charset=utf-8"; got != want {
		t.Errorf("Content-Type = %q, want %q", got, want)
	}
	if len(g.inspector.calls) != 1 || g.inspector.calls[0] != g.sheets.ref.ID {
		t.Errorf("TableInfo calls = %v, want one call for %s", g.inspector.calls, g.sheets.ref.ID)
	}
}

// SECURITY: capabilities names a sheet's shape, so it must refuse exactly as the whole-tab GET refuses.
func TestHandler_CapabilitiesRefusalIsTheSame404(t *testing.T) {
	g := newRig()
	g.authz.err = apperror.New(apperror.CodeAPIKeyInsufficientScope, "Insufficient scope", codes.PermissionDenied)

	rec := g.do(t, http.MethodGet, rowsPath+"/capabilities", keyHeader())

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if got, want := rec.Body.String(), `{"error":{"code":"SHT001","message":"Sheet not found"}}`; got != want {
		t.Errorf("body = %s, want the shared masked envelope %s", got, want)
	}
	if len(g.inspector.calls) != 0 {
		t.Errorf("TableInfo ran %d times after a refused authorization, want 0", len(g.inspector.calls))
	}
	if len(g.authz.calls) != 1 || g.authz.calls[0].scope != authn.ScopeSheetsRead {
		t.Errorf("Authorize calls = %+v, want one call with %q", g.authz.calls, authn.ScopeSheetsRead)
	}
}

func TestHandler_CapabilitiesNeedsNoCredentialForAPublicSheet(t *testing.T) {
	g := newRig()
	g.sheets.ref.Visibility = sheet.VisibilityPublic

	rec := g.do(t, http.MethodGet, rowsPath+"/capabilities", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(g.authz.calls) != 0 {
		t.Errorf("Authorize was called %d times for a public sheet, want 0", len(g.authz.calls))
	}
}

func TestHandler_CapabilitiesPublicDisabledFromTheReadPathIsTheSame404(t *testing.T) {
	g := newRig()
	g.sheets.ref.Visibility = sheet.VisibilityPublic
	g.inspector.err = apperror.New(apperror.CodeSheetPublicDisabled,
		"Public sheets are disabled on this deployment", codes.FailedPrecondition)

	rec := g.do(t, http.MethodGet, rowsPath+"/capabilities", nil)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if got, want := rec.Body.String(), `{"error":{"code":"SHT001","message":"Sheet not found"}}`; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

// The literal segment must win over {slug}, or capabilities would resolve as a sheet named "capabilities".
func TestHandler_CapabilitiesBeatsTheSlugPattern(t *testing.T) {
	g := newRig()

	g.do(t, http.MethodGet, rowsPath+"/capabilities", keyHeader())

	if len(g.reader.calls) != 0 {
		t.Errorf("Rows ran %d times, want 0 — GET /sheets/{slug} matched the capabilities path", len(g.reader.calls))
	}
	if len(g.inspector.calls) != 1 {
		t.Errorf("TableInfo calls = %d, want 1", len(g.inspector.calls))
	}
}
