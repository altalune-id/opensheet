package data

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	"altalune.id/opensheet/gworkspace/gsheet"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/sheet"
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
	err   error
	calls []authorizeCall
}

func (f *fakeAuthorizer) Authorize(_ context.Context, raw, scope string, orgID, projectID, sheetID uuid.UUID) (session.Principal, error) {
	f.calls = append(f.calls, authorizeCall{raw: raw, scope: scope, orgID: orgID, projectID: projectID, sheetID: sheetID})
	if f.err != nil {
		return session.Principal{}, f.err
	}
	return session.Principal{Source: session.SourceAPIKey, ActiveOrgID: orgID, ActiveProjectID: projectID}, nil
}

type fakeCaps struct{ public bool }

func (f fakeCaps) PublicSheetsEnabled() bool { return f.public }

type rig struct {
	orgs     *fakeOrgs
	projects *fakeProjects
	sheets   *fakeSheets
	reader   *fakeReader
	purger   *fakePurger
	authz    *fakeAuthorizer
	caps     fakeCaps
	basePath string
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
		purger: &fakePurger{},
		authz:  &fakeAuthorizer{},
		caps:   fakeCaps{public: true},
	}
}

func (g *rig) handler() http.Handler {
	return NewHandler(g.basePath, g.orgs, g.projects, g.sheets, g.reader, g.purger, g.authz, g.caps,
		30*time.Second, slog.New(slog.DiscardHandler))
}

func (g *rig) do(t *testing.T, method, path string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, http.NoBody)
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	rec := httptest.NewRecorder()
	g.handler().ServeHTTP(rec, req)
	return rec
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
