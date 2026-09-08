package handlers_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"altalune.id/opensheet/gworkspace"
	"altalune.id/opensheet/gworkspace/gsheet"
	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/credential"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/capabilities"
	"altalune.id/opensheet/internal/platform/sealer"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/sheet"
	"altalune.id/opensheet/internal/spreadsheet"
	"altalune.id/opensheet/internal/testutil/fakes"
	"altalune.id/opensheet/internal/web"
	"altalune.id/opensheet/internal/web/handlers"
)

const (
	testFileID      = "1AbCdEfGhIjKlMnOpQrStUvWxYz0123456789"
	testAccessToken = "picker-access-token"
	testPickerKey   = "picker-api-key"
	serviceAccount  = `{"type":"service_account","project_id":"p","private_key_id":"k",` +
		`"private_key":"-----BEGIN PRIVATE KEY-----\nnot-a-real-key\n-----END PRIVATE KEY-----\n",` +
		`"client_email":"sa@p.iam.gserviceaccount.com","client_id":"1","token_uri":"https://oauth2.googleapis.com/token"}`
)

// sheetsFixture adds the four opensheet domain services to handlerFixture, all on in-memory fakes.
type sheetsFixture struct {
	*handlerFixture
	Caps       capabilities.Capabilities
	Sealer     sealer.Sealer
	CredStore  *fakes.Credential
	Creds      *credential.Service
	SprdStore  *fakes.Spreadsheet
	Sprds      *spreadsheet.Service
	SheetStore *fakes.Sheet
	Sheets     *sheet.Service
	Snaps      *fakes.SheetSnapshots
	Sources    *fakes.SheetSources
	Read       *sheet.ReadWorkflow
	KeyStore   *fakes.APIKey
	Keys       *apikey.Service
	Connect    *credential.ConnectWorkflow
	Google     *fakeSheetsAPI
	Org        *org.Org
	Project    *project.Project
	Principal  session.Principal
}

type publicCaps bool

func (c publicCaps) PublicSheetsEnabled() bool { return bool(c) }

func newSheetsFixture(t *testing.T, tweak ...func(*capabilities.Capabilities)) *sheetsFixture {
	t.Helper()
	base := newFixture(t)
	caps := capabilities.Capabilities{
		LocalIdentity: true,
		GoogleConnect: true,
		GooglePicker:  true,
		PublicSheets:  true,
		Encryption:    true,
	}
	for _, fn := range tweak {
		fn(&caps)
	}
	base.Cfg.Google.Picker.APIKey = testPickerKey

	google := newFakeSheetsAPI(t)
	clients := func(ctx context.Context, ts oauth2.TokenSource) (*gsheet.Client, error) {
		return gsheet.New(ctx, ts, gworkspace.WithBaseURL(google.url))
	}

	sl := testSealer(t)
	credStore := fakes.NewCredential()
	creds := credential.NewService(credStore, discardLogger(), passthroughUnexpected(), sl,
		credential.WithGoogleOAuth(testOAuthConfig(google.tokenURL)))

	sprdStore := fakes.NewSpreadsheet()
	sprds := spreadsheet.NewService(sprdStore, discardLogger(), passthroughUnexpected(), creds, clients)

	snaps := fakes.NewSheetSnapshots()
	sheetStore := fakes.NewSheet()
	sheets := sheet.NewService(sheetStore, discardLogger(), passthroughUnexpected(),
		publicCaps(caps.PublicSheets), snaps)

	sources := fakes.NewSheetSources()
	read := sheet.NewReadWorkflow(snaps, sources, creds, fakes.NewSheetReauthers(), clients,
		publicCaps(caps.PublicSheets), 30*time.Second, 1<<20, discardLogger(), passthroughUnexpected())

	keyStore := fakes.NewAPIKey()
	keys := apikey.NewService(keyStore, discardLogger(), passthroughUnexpected(), fakes.NewAPIKeySheets())

	connector, err := gworkspace.NewConnector(testOAuthConfig(google.tokenURL), nil)
	require.NoError(t, err)
	connect := credential.NewConnectWorkflow(credStore, sl, connector,
		[]byte(base.Cfg.HTTP.StateSecret), nil, discardLogger(), passthroughUnexpected())

	base.Deps.Caps = caps
	uid := uuid.New()
	o := base.seedOrg(t, "acme", uid)
	proj, err := base.Projects.Create(setTenant(context.Background(), o.ID, uid), o.ID, "default", "Default")
	require.NoError(t, err)

	return &sheetsFixture{
		handlerFixture: base,
		Caps:           caps,
		Sealer:         sl,
		CredStore:      credStore,
		Creds:          creds,
		SprdStore:      sprdStore,
		Sprds:          sprds,
		SheetStore:     sheetStore,
		Sheets:         sheets,
		Snaps:          snaps,
		Sources:        sources,
		Read:           read,
		KeyStore:       keyStore,
		Keys:           keys,
		Connect:        connect,
		Google:         google,
		Org:            o,
		Project:        proj,
		Principal:      session.Principal{UserID: uid, ActiveOrgID: o.ID, ActiveProjectID: proj.ID},
	}
}

func testSealer(t *testing.T) sealer.Sealer {
	t.Helper()
	key, err := sealer.ParseKey(strings.Repeat("ab", 32))
	require.NoError(t, err)
	sl, err := sealer.New(key)
	require.NoError(t, err)
	return sl
}

func testOAuthConfig(tokenURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     "client-id.apps.googleusercontent.com",
		ClientSecret: "client-secret",
		RedirectURL:  "http://localhost/credentials/google/callback",
		Scopes:       gworkspace.Scopes(),
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
			TokenURL: tokenURL,
		},
	}
}

// ctx returns a request context scoped to the fixture's org and project.
func (f *sheetsFixture) ctx() context.Context {
	return setTenantProject(context.Background(), f.Org.ID, f.Project.ID, f.Principal.UserID)
}

func (f *sheetsFixture) path(suffix string) string {
	return "/orgs/" + f.Org.Slug + "/projects/" + f.Project.Slug + suffix
}

func (f *sheetsFixture) mux(t *testing.T) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	handlers.NewCredentialHandler(f.Deps, f.Projects, f.Creds, f.Connect).Register(mux)
	handlers.NewSpreadsheetHandler(f.Deps, f.Projects, f.Sprds, f.Creds).Register(mux)
	handlers.NewSheetHandler(f.Deps, f.Projects, f.Sheets, f.Sprds, f.Read).Register(mux)
	handlers.NewAPIKeyHandler(f.Deps, f.Projects, f.Keys, f.Sheets).Register(mux)
	handlers.NewGoogleConnectHandler(f.Deps, f.Projects, f.Creds, f.Connect).Register(mux)
	return mux
}

func (f *sheetsFixture) do(t *testing.T, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	f.mux(t).ServeHTTP(rec, f.authedRequest(t, method, target, body, f.Principal))
	return rec
}

func (f *sheetsFixture) seedCredential(t *testing.T, kind credential.Kind, name string) *credential.Credential {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	secret := []byte(serviceAccount)
	if kind == credential.KindGoogleOAuth {
		secret = []byte("1//refresh-token")
	}
	box, err := f.Sealer.Seal(secret, credential.SealAAD(f.Org.ID, f.Project.ID, id))
	require.NoError(t, err)
	c, err := credential.New(id, f.Org.ID, f.Project.ID, f.Principal.UserID, name, kind, "sa@example.com", box)
	require.NoError(t, err)
	f.CredStore.Seed(c)
	return c
}

func (f *sheetsFixture) seedSpreadsheet(t *testing.T, credID uuid.UUID) *spreadsheet.Spreadsheet {
	t.Helper()
	sp, err := f.Sprds.Register(f.ctx(), credID, testFileID, "Q1 Report")
	require.NoError(t, err)
	f.Sources.Set(sp.ID, sheet.Source{GoogleFileID: sp.GoogleFileID, CredentialID: credID})
	return sp
}

func (f *sheetsFixture) seedSheet(t *testing.T, sprdID uuid.UUID, slug string, vis sheet.Visibility) *sheet.Sheet {
	t.Helper()
	sh, err := f.Sheets.Create(f.ctx(), sprdID, "", slug, vis, 0)
	require.NoError(t, err)
	return sh
}

// fakeSheetsAPI serves the Google Sheets endpoints the read path uses plus an OAuth token endpoint.
type fakeSheetsAPI struct {
	mu         sync.Mutex
	rowsBody   string
	rowsStatus int
	metaStatus int
	tabs       []string
	noRefresh  bool

	url      string
	tokenURL string
}

func newFakeSheetsAPI(t *testing.T) *fakeSheetsAPI {
	t.Helper()
	f := &fakeSheetsAPI{
		rowsBody:   `{"values":[["name","qty"],["apple","3"]]}`,
		rowsStatus: http.StatusOK,
		metaStatus: http.StatusOK,
		tabs:       []string{"First", "Second"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v4/spreadsheets/{file}/values/", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		body, status := f.rowsBody, f.rowsStatus
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	})
	mux.HandleFunc("/v4/spreadsheets/{file}", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		status, tabs := f.metaStatus, slices.Clone(f.tabs)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		grids := make([]map[string]any, 0, len(tabs))
		for _, tab := range tabs {
			grids = append(grids, map[string]any{"properties": map[string]any{"title": tab}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"properties": map[string]any{"title": "Doc"},
			"sheets":     grids,
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		noRefresh := f.noRefresh
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		payload := map[string]any{
			"access_token": testAccessToken,
			"token_type":   "Bearer",
			"expires_in":   3600,
			"scope":        strings.Join(gworkspace.Scopes(), " "),
			"id_token":     testIDToken(t, "connected@example.com"),
		}
		if !noRefresh {
			payload["refresh_token"] = "1//refresh-token"
		}
		_ = json.NewEncoder(w).Encode(payload)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	f.url = srv.URL
	f.tokenURL = srv.URL + "/token"
	return f
}

func (f *fakeSheetsAPI) setRows(body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rowsBody = body
}

func (f *fakeSheetsAPI) setTabs(tabs ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tabs = tabs
}

func (f *fakeSheetsAPI) breakGoogle() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rowsStatus, f.metaStatus = http.StatusInternalServerError, http.StatusInternalServerError
}

func (f *fakeSheetsAPI) dropRefreshToken() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.noRefresh = true
}

func testIDToken(t *testing.T, email string) string {
	t.Helper()
	enc := func(v any) string {
		raw, err := json.Marshal(v)
		require.NoError(t, err)
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	return enc(map[string]string{"alg": "RS256"}) + "." +
		enc(map[string]string{"email": email}) + ".not-a-signature"
}

func multipartServiceAccount(t *testing.T, name, payload string) (string, string) {
	t.Helper()
	var buf strings.Builder
	mw := multipart.NewWriter(&buf)
	require.NoError(t, mw.WriteField("name", name))
	part, err := mw.CreateFormFile("key", "sa.json")
	require.NoError(t, err)
	_, err = io.WriteString(part, payload)
	require.NoError(t, err)
	require.NoError(t, mw.Close())
	return buf.String(), mw.FormDataContentType()
}

func (f *sheetsFixture) upload(t *testing.T, name, payload string) *httptest.ResponseRecorder {
	t.Helper()
	body, contentType := multipartServiceAccount(t, name, payload)
	req := httptest.NewRequest(http.MethodPost, f.path("/credentials"), strings.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	req.AddCookie(&http.Cookie{Name: web.SessionCookieName, Value: f.seedSession(t, f.Principal)})
	req = req.WithContext(session.PrincipalInto(req.Context(), f.Principal))
	rec := httptest.NewRecorder()
	f.mux(t).ServeHTTP(rec, req)
	return rec
}

func TestOpensheetHandlers_UnauthRedirectToLogin(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	for _, target := range []string{"/credentials", "/spreadsheets", "/sheets", "/keys"} {
		t.Run(target, func(t *testing.T) {
			rec := httptest.NewRecorder()
			f.mux(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, f.path(target), nil))
			assert.Equal(t, http.StatusSeeOther, rec.Code)
		})
	}
}

func TestOpensheetHandlers_UnknownProjectIs404(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	for _, target := range []string{"/credentials", "/spreadsheets", "/sheets", "/keys"} {
		t.Run(target, func(t *testing.T) {
			rec := f.do(t, http.MethodGet, "/orgs/acme/projects/ghost"+target, "")
			assert.Equal(t, http.StatusNotFound, rec.Code)
		})
	}
}

func TestOpensheetHandlers_EmptyStates(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	cases := []struct{ target, marker string }{
		{"/credentials", `data-empty="credentials"`},
		{"/spreadsheets", `data-empty="spreadsheets"`},
		{"/sheets", `data-empty="sheets"`},
		{"/keys", `data-empty="keys"`},
	}
	for _, tc := range cases {
		t.Run(tc.target, func(t *testing.T) {
			rec := f.do(t, http.MethodGet, f.path(tc.target), "")
			require.Equal(t, http.StatusOK, rec.Code)
			assert.Contains(t, rec.Body.String(), tc.marker)
		})
	}
}

func TestOpensheetHandlers_ProjectSidebarOmitsTodos(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	rec := f.do(t, http.MethodGet, f.path("/sheets"), "")
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	for _, want := range []string{"/credentials", "/spreadsheets", "/sheets", "/keys"} {
		assert.Contains(t, body, f.path(want), "the project sidebar must link every opensheet section")
	}
	assert.NotContains(t, body, f.path("/todos"),
		"todos stays routable but is no longer a navigable section")
}

func TestCredentialHandler_UploadStoresAndReportsUnverified(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)

	rec := f.upload(t, "Prod service account", serviceAccount)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Contains(t, body, `data-notice="credential-stored-unverified"`,
		"a stored key is unverified until the first read; the page must say so")
	assert.Contains(t, body, "Prod service account")

	items, err := f.Creds.List(f.ctx())
	require.NoError(t, err)
	require.Len(t, items, 1)
}

func TestCredentialHandler_UploadRejectsAPayloadThatIsNotAServiceAccountKey(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	rec := f.upload(t, "Broken", `{"type":"external_account"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `data-error="credentials"`)

	items, err := f.Creds.List(f.ctx())
	require.NoError(t, err)
	assert.Empty(t, items)
}

func TestCredentialHandler_ListNeverRendersSealedMaterial(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	f.seedCredential(t, credential.KindServiceAccount, "Prod")

	rec := f.do(t, http.MethodGet, f.path("/credentials"), "")
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.NotContains(t, body, "BEGIN PRIVATE KEY")
	assert.NotContains(t, body, "private_key")
}

func TestCredentialHandler_Delete(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedCredential(t, credential.KindServiceAccount, "Prod")

	rec := f.do(t, http.MethodPost, f.path("/credentials/"+c.ID.String()+"/delete"), "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	items, err := f.Creds.List(f.ctx())
	require.NoError(t, err)
	assert.Empty(t, items)
}

// The Postgres driver refuses a credential a spreadsheet still references; the page must say why
// rather than reporting a generic failure.
func TestCredentialHandler_DeleteRefusedWhileInUse(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedCredential(t, credential.KindServiceAccount, "Prod")
	f.CredStore.DeleteFn = func(_ context.Context, id uuid.UUID) error {
		return &credential.InUseError{ID: id.String()}
	}

	rec := f.do(t, http.MethodPost, f.path("/credentials/"+c.ID.String()+"/delete"), "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `data-error="credentials"`)
	assert.Contains(t, rec.Body.String(), "still uses this credential")
}

func TestCredentialHandler_ConnectGoogleIsGatedOnTheCapability(t *testing.T) {
	t.Parallel()
	on := newSheetsFixture(t)
	rec := on.do(t, http.MethodPost, on.path("/credentials/google/start"), "")
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Header().Get("Location"), "accounts.google.com")

	off := newSheetsFixture(t, func(c *capabilities.Capabilities) { c.GoogleConnect = false })
	rec = off.do(t, http.MethodPost, off.path("/credentials/google/start"), "")
	assert.Equal(t, http.StatusNotFound, rec.Code, "the server must refuse, not merely hide the button")

	page := off.do(t, http.MethodGet, off.path("/credentials"), "")
	require.Equal(t, http.StatusOK, page.Code)
	assert.NotContains(t, page.Body.String(), "/credentials/google/start")
	assert.Contains(t, page.Body.String(), `data-upload="service-account"`,
		"a deployment with no Google client must still be able to upload a service account")
}

func TestSpreadsheetHandler_RegisterAndEdit(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedCredential(t, credential.KindServiceAccount, "Prod")

	form := url.Values{
		"credential_id":  {c.ID.String()},
		"google_file_id": {"https://docs.google.com/spreadsheets/d/" + testFileID + "/edit#gid=0"},
		"title":          {"Q1 Report"},
	}
	rec := f.do(t, http.MethodPost, f.path("/spreadsheets"), form.Encode())
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "Q1 Report", "a pasted Drive URL must register as its file id")

	items, err := f.Sprds.List(f.ctx())
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, testFileID, items[0].GoogleFileID)

	rec = f.do(t, http.MethodGet, f.path("/spreadsheets/"+items[0].ID.String()), "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), testFileID)

	rec = f.do(t, http.MethodPost, f.path("/spreadsheets/"+items[0].ID.String()),
		url.Values{"title": {"Q2 Report"}, "credential_id": {c.ID.String()}}.Encode())
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())

	updated, err := f.Sprds.ByID(f.ctx(), items[0].ID)
	require.NoError(t, err)
	assert.Equal(t, "Q2 Report", updated.Title)
}

// A fragment renders with its own LayoutData, and LayoutData.ProjectPath collapses to "/orgs" when no
// org is pinned — so an HTMX swap would replace the list with rows linking nowhere.
func TestOpensheetHandlers_FragmentLinksStayProjectScoped(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedCredential(t, credential.KindServiceAccount, "Prod")
	sp := f.seedSpreadsheet(t, c.ID)
	f.seedSheet(t, sp.ID, "q1", sheet.VisibilityKey)

	cases := []struct{ name, method, target, body string }{
		{"credentials", http.MethodPost, "/credentials", ""},
		{"spreadsheets", http.MethodPost, "/spreadsheets", url.Values{"credential_id": {c.ID.String()}}.Encode()},
		{"sheets", http.MethodPost, "/sheets", url.Values{"spreadsheet_id": {sp.ID.String()}}.Encode()},
		{"keys", http.MethodPost, "/keys", url.Values{"name": {"CI"}}.Encode()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.do(t, tc.method, f.path(tc.target), tc.body)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), f.path(tc.target),
				"the swapped fragment must keep links under the project")
		})
	}
}

func TestSpreadsheetHandler_RegisterWithABadFileIDRendersInline(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedCredential(t, credential.KindServiceAccount, "Prod")
	form := url.Values{"credential_id": {c.ID.String()}, "google_file_id": {"not a file id!"}}

	rec := f.do(t, http.MethodPost, f.path("/spreadsheets"), form.Encode())
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `data-error="spreadsheets"`)
}

func TestSpreadsheetHandler_TabsPartialListsTheDocumentsTabs(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedCredential(t, credential.KindGoogleOAuth, "Connected")
	sp := f.seedSpreadsheet(t, c.ID)

	rec := f.do(t, http.MethodGet, f.path("/spreadsheets/"+sp.ID.String()+"/tabs"), "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "Second")
}

func TestSpreadsheetHandler_Delete(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedCredential(t, credential.KindServiceAccount, "Prod")
	sp := f.seedSpreadsheet(t, c.ID)

	rec := f.do(t, http.MethodPost, f.path("/spreadsheets/"+sp.ID.String()+"/delete"), "")
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())

	items, err := f.Sprds.List(f.ctx())
	require.NoError(t, err)
	assert.Empty(t, items)
}

func TestSpreadsheetHandler_PickerLinkFollowsTheCapability(t *testing.T) {
	t.Parallel()
	on := newSheetsFixture(t)
	on.seedCredential(t, credential.KindGoogleOAuth, "Connected")
	rec := on.do(t, http.MethodGet, on.path("/spreadsheets"), "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "/credentials/google/picker")

	off := newSheetsFixture(t, func(c *capabilities.Capabilities) { c.GooglePicker = false })
	off.seedCredential(t, credential.KindGoogleOAuth, "Connected")
	rec = off.do(t, http.MethodGet, off.path("/spreadsheets"), "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "/credentials/google/picker")
}

func TestSheetHandler_CreateKeyVisibility(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedCredential(t, credential.KindServiceAccount, "Prod")
	sp := f.seedSpreadsheet(t, c.ID)

	form := url.Values{
		"spreadsheet_id": {sp.ID.String()},
		"slug":           {"q1"},
		"tab":            {"First"},
		"visibility":     {"key"},
		"cache_ttl":      {"60"},
	}
	rec := f.do(t, http.MethodPost, f.path("/sheets"), form.Encode())
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "q1")

	items, err := f.Sheets.List(f.ctx())
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, sheet.VisibilityKey, items[0].Visibility)
	assert.Equal(t, time.Minute, items[0].CacheTTL)
}

func TestSheetHandler_PublicNeedsAnExplicitConfirmation(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedCredential(t, credential.KindServiceAccount, "Prod")
	sp := f.seedSpreadsheet(t, c.ID)
	form := url.Values{
		"spreadsheet_id": {sp.ID.String()},
		"slug":           {"open"},
		"visibility":     {"public"},
	}

	rec := f.do(t, http.MethodPost, f.path("/sheets"), form.Encode())
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `data-error="sheets"`)
	items, err := f.Sheets.List(f.ctx())
	require.NoError(t, err)
	require.Empty(t, items, "a public sheet must not be created without the acknowledgement")

	form.Set("public_ack", "1")
	rec = f.do(t, http.MethodPost, f.path("/sheets"), form.Encode())
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	items, err = f.Sheets.List(f.ctx())
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, sheet.VisibilityPublic, items[0].Visibility)
}

func TestSheetHandler_PublicIsHiddenAndRefusedWhenTheCapabilityIsOff(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t, func(c *capabilities.Capabilities) { c.PublicSheets = false })
	c := f.seedCredential(t, credential.KindServiceAccount, "Prod")
	sp := f.seedSpreadsheet(t, c.ID)

	page := f.do(t, http.MethodGet, f.path("/sheets"), "")
	require.Equal(t, http.StatusOK, page.Code)
	assert.NotContains(t, page.Body.String(), `value="public"`,
		"the public option must be hidden entirely when the capability is off")

	form := url.Values{
		"spreadsheet_id": {sp.ID.String()},
		"slug":           {"open"},
		"visibility":     {"public"},
		"public_ack":     {"1"},
	}
	rec := f.do(t, http.MethodPost, f.path("/sheets"), form.Encode())
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `data-error="sheets"`)
	items, err := f.Sheets.List(f.ctx())
	require.NoError(t, err)
	assert.Empty(t, items)
}

func TestSheetHandler_ShowRendersTheDataPlaneURLAndUpdate(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedCredential(t, credential.KindServiceAccount, "Prod")
	sp := f.seedSpreadsheet(t, c.ID)
	sh := f.seedSheet(t, sp.ID, "q1", sheet.VisibilityKey)

	rec := f.do(t, http.MethodGet, f.path("/sheets/"+sh.ID.String()), "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "/api/v1/orgs/acme/projects/default/sheets/q1")

	rec = f.do(t, http.MethodPost, f.path("/sheets/"+sh.ID.String()),
		url.Values{"tab": {"Second"}, "visibility": {"key"}, "cache_ttl": {"120"}}.Encode())
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())

	updated, err := f.Sheets.ByID(f.ctx(), sh.ID)
	require.NoError(t, err)
	assert.Equal(t, "Second", updated.Tab)
	assert.Equal(t, 2*time.Minute, updated.CacheTTL)
}

func TestSheetHandler_PreviewRendersRowsAndHeaderWarnings(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	f.Google.setRows(`{"values":[["name","","name"],["apple","3","pear"]]}`)
	c := f.seedCredential(t, credential.KindGoogleOAuth, "Connected")
	sp := f.seedSpreadsheet(t, c.ID)
	sh := f.seedSheet(t, sp.ID, "q1", sheet.VisibilityKey)

	rec := f.do(t, http.MethodGet, f.path("/sheets/"+sh.ID.String()+"/preview"), "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Contains(t, body, "apple")
	assert.Contains(t, body, `data-warnings`, "blank and duplicate headers change the JSON keys; the page must show them")
	assert.Contains(t, body, "col_2")
}

func TestSheetHandler_PurgeDropsTheSnapshot(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedCredential(t, credential.KindGoogleOAuth, "Connected")
	sp := f.seedSpreadsheet(t, c.ID)
	sh := f.seedSheet(t, sp.ID, "q1", sheet.VisibilityKey)

	_, err := f.Read.Rows(f.ctx(), sh)
	require.NoError(t, err)
	_, found, err := f.Snaps.Get(f.ctx(), sheet.SnapshotKey{SheetID: sh.ID, Tab: "First"})
	require.NoError(t, err)
	require.True(t, found, "the read must have cached a snapshot for the purge to mean anything")

	rec := f.do(t, http.MethodPost, f.path("/sheets/"+sh.ID.String()+"/purge"), "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `data-purged`)

	_, found, err = f.Snaps.Get(f.ctx(), sheet.SnapshotKey{SheetID: sh.ID, Tab: "First"})
	require.NoError(t, err)
	assert.False(t, found, "the purge button must drop the cached snapshot")
}

func TestSheetHandler_Delete(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedCredential(t, credential.KindServiceAccount, "Prod")
	sp := f.seedSpreadsheet(t, c.ID)
	sh := f.seedSheet(t, sp.ID, "q1", sheet.VisibilityKey)

	rec := f.do(t, http.MethodPost, f.path("/sheets/"+sh.ID.String()+"/delete"), "")
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())

	items, err := f.Sheets.List(f.ctx())
	require.NoError(t, err)
	assert.Empty(t, items)
}

func TestSheetHandler_BadIDIs400(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	rec := f.do(t, http.MethodGet, f.path("/sheets/not-a-uuid"), "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAPIKeyHandler_CreateShowsThePlaintextOnceAndTheListNeverDoes(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	form := url.Values{"name": {"CI reader"}, "scopes": {authn.ScopeSheetsRead, authn.ScopeCachePurge}}

	rec := f.do(t, http.MethodPost, f.path("/keys"), form.Encode())
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	require.Contains(t, body, `data-minted-key`)
	i := strings.Index(body, apikey.Label+"_")
	require.Positive(t, i, "the create response must show the plaintext exactly once")
	plaintext := body[i : i+len(apikey.Label)+2+apikey.PrefixLen+apikey.SecretLen]
	// NOTE: base64url includes '_', so the secret is the remainder after the second separator.
	secret := strings.SplitN(plaintext, "_", 3)[2]
	require.Len(t, secret, apikey.SecretLen)

	list := f.do(t, http.MethodGet, f.path("/keys"), "")
	require.Equal(t, http.StatusOK, list.Code)
	listed := list.Body.String()
	assert.NotContains(t, listed, plaintext, "the list page must never render a plaintext key")
	assert.NotContains(t, listed, secret, "nor the secret half of one")

	items, err := f.Keys.List(f.ctx())
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Empty(t, items[0].SecretHash, "a list read must not even load the hash")
}

func TestAPIKeyHandler_CreateWithNoScopeRendersInline(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	rec := f.do(t, http.MethodPost, f.path("/keys"), url.Values{"name": {"No scopes"}}.Encode())
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `data-error="keys"`)

	items, err := f.Keys.List(f.ctx())
	require.NoError(t, err)
	assert.Empty(t, items)
}

func TestAPIKeyHandler_RevokeAndDelete(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	k, _, err := f.Keys.Create(f.ctx(), apikey.CreateRequest{Name: "CI", Scopes: []string{authn.ScopeSheetsRead}})
	require.NoError(t, err)

	rec := f.do(t, http.MethodPost, f.path("/keys/"+k.ID.String()+"/revoke"), "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	items, err := f.Keys.List(f.ctx())
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.NotNil(t, items[0].RevokedAt)

	rec = f.do(t, http.MethodPost, f.path("/keys/"+k.ID.String()+"/delete"), "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	items, err = f.Keys.List(f.ctx())
	require.NoError(t, err)
	assert.Empty(t, items)
}

func TestGoogleConnectHandler_CallbackRefusesAGrantThatDroppedDriveFile(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	state := f.startState(t)

	rec := f.do(t, http.MethodGet, "/credentials/google/callback?code=abc&scope=email&state="+url.QueryEscape(state), "")
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "Google Drive")

	items, err := f.Creds.List(f.ctx())
	require.NoError(t, err)
	assert.Empty(t, items, "a grant without drive.file must not be stored")
}

func TestGoogleConnectHandler_CallbackStoresTheGrantAndReturnsWhereItStarted(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	state := f.startState(t)

	target := "/credentials/google/callback?code=abc&scope=" +
		url.QueryEscape(strings.Join(gworkspace.Scopes(), " ")) + "&state=" + url.QueryEscape(state)
	rec := f.do(t, http.MethodGet, target, "")
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, f.path("/credentials"), rec.Header().Get("Location"))

	items, err := f.Creds.List(f.ctx())
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, credential.KindGoogleOAuth, items[0].Kind)
}

func TestGoogleConnectHandler_CallbackRejectsATamperedState(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	rec := f.do(t, http.MethodGet, "/credentials/google/callback?code=abc&state=tampered", "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGoogleConnectHandler_RoutesAreGatedOnTheirCapabilities(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t, func(c *capabilities.Capabilities) {
		c.GoogleConnect = false
		c.GooglePicker = false
	})
	for _, target := range []string{"/credentials/google/callback?code=a&state=b", "/credentials/google/picker?state=b"} {
		t.Run(target, func(t *testing.T) {
			rec := f.do(t, http.MethodGet, target, "")
			assert.Equal(t, http.StatusNotFound, rec.Code)
		})
	}
}

func TestGoogleConnectHandler_PickerRendersTheAPIKeyForAConnectedCredential(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedCredential(t, credential.KindGoogleOAuth, "Connected")

	rec := f.do(t, http.MethodGet, f.pickerPath(t, c.ID), "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Contains(t, body, testPickerKey)
	assert.Contains(t, body, testAccessToken)
	assert.Contains(t, body, "apis.google.com")
}

func TestGoogleConnectHandler_PickerRefusesAServiceAccountCredential(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedCredential(t, credential.KindServiceAccount, "Prod")

	rec := f.do(t, http.MethodGet, f.pickerPath(t, c.ID), "")
	assert.Equal(t, http.StatusConflict, rec.Code,
		"a service account token must never be handed to a browser")
}

func TestGoogleConnectHandler_PickerRefusesAStateSignedElsewhere(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedCredential(t, credential.KindGoogleOAuth, "Connected")
	forged := handlers.PickerStateFor([]byte("another-deployment-secret"), f.Org.Slug, f.Project.Slug, c.ID)

	rec := f.do(t, http.MethodGet, "/credentials/google/picker?state="+url.QueryEscape(forged), "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSheetHandler_PreviewReportsAGoogleFailure(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedCredential(t, credential.KindGoogleOAuth, "Connected")
	sp := f.seedSpreadsheet(t, c.ID)
	sh := f.seedSheet(t, sp.ID, "q1", sheet.VisibilityKey)
	f.Google.breakGoogle()

	rec := f.do(t, http.MethodGet, f.path("/sheets/"+sh.ID.String()+"/preview"), "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `data-error="preview"`)
}

func TestSpreadsheetHandler_TabsReportsAGoogleFailure(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedCredential(t, credential.KindGoogleOAuth, "Connected")
	sp := f.seedSpreadsheet(t, c.ID)
	f.Google.breakGoogle()

	rec := f.do(t, http.MethodGet, f.path("/spreadsheets/"+sp.ID.String()+"/tabs"), "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `data-error="tabs"`)
}

func TestSheetHandler_UpdateWithABadTTLRendersTheDetailPage(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedCredential(t, credential.KindServiceAccount, "Prod")
	sp := f.seedSpreadsheet(t, c.ID)
	sh := f.seedSheet(t, sp.ID, "q1", sheet.VisibilityKey)

	rec := f.do(t, http.MethodPost, f.path("/sheets/"+sh.ID.String()),
		url.Values{"tab": {"First"}, "visibility": {"key"}, "cache_ttl": {"soon"}}.Encode())
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `data-error="sheet"`)
}

func TestSpreadsheetHandler_UpdateWithARejectedTitleRendersTheDetailPage(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	c := f.seedCredential(t, credential.KindServiceAccount, "Prod")
	sp := f.seedSpreadsheet(t, c.ID)

	rec := f.do(t, http.MethodPost, f.path("/spreadsheets/"+sp.ID.String()),
		url.Values{"title": {strings.Repeat("x", 300)}}.Encode())
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `data-error="spreadsheet"`)
}

func TestGoogleConnectHandler_CallbackWithoutARefreshTokenIs400(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	f.Google.dropRefreshToken()
	state := f.startState(t)

	target := "/credentials/google/callback?code=abc&scope=" +
		url.QueryEscape(strings.Join(gworkspace.Scopes(), " ")) + "&state=" + url.QueryEscape(state)
	rec := f.do(t, http.MethodGet, target, "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "refresh token")

	items, err := f.Creds.List(f.ctx())
	require.NoError(t, err)
	assert.Empty(t, items)
}

func (f *sheetsFixture) startState(t *testing.T) string {
	t.Helper()
	raw, err := f.Connect.Start(context.Background(), f.Org.ID, f.Project.ID, f.Principal.UserID, f.path("/credentials"))
	require.NoError(t, err)
	u, err := url.Parse(raw)
	require.NoError(t, err)
	state := u.Query().Get("state")
	require.NotEmpty(t, state)
	return state
}

func (f *sheetsFixture) pickerPath(t *testing.T, credID uuid.UUID) string {
	t.Helper()
	state := handlers.PickerStateFor([]byte(f.Cfg.HTTP.StateSecret), f.Org.Slug, f.Project.Slug, credID)
	return "/credentials/google/picker?state=" + url.QueryEscape(state)
}

func TestSheetHandler_BulkPublishCreatesEveryTickedTab(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	cred := f.seedCredential(t, credential.KindGoogleOAuth, "Connected")
	sp := f.seedSpreadsheet(t, cred.ID)

	body := "tab=0&slug.0=rates&tab=1&slug.1=payroll&visibility=key&cache_ttl=300"
	rec := f.do(t, http.MethodPost, f.path("/spreadsheets/"+sp.ID.String()+"/publish"), body)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	got, err := f.Sheets.List(f.ctx())
	require.NoError(t, err)
	slugs := make([]string, 0, len(got))
	tabs := make([]string, 0, len(got))
	for _, sh := range got {
		slugs = append(slugs, sh.Slug)
		tabs = append(tabs, sh.Tab)
	}
	assert.ElementsMatch(t, []string{"rates", "payroll"}, slugs)
	assert.ElementsMatch(t, []string{"First", "Second"}, tabs,
		"each row must take its tab name from the listing, not from the posted form")
}

func TestSheetHandler_BulkPublishKeepsSuccessesWhenOneSlugCollides(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	cred := f.seedCredential(t, credential.KindGoogleOAuth, "Connected")
	sp := f.seedSpreadsheet(t, cred.ID)
	f.Google.setTabs("First", "Second", "Third")
	f.seedSheet(t, sp.ID, "payroll", sheet.VisibilityKey)

	body := "tab=0&slug.0=rates&tab=1&slug.1=payroll&tab=2&slug.2=notes&visibility=key&cache_ttl=300"
	rec := f.do(t, http.MethodPost, f.path("/spreadsheets/"+sp.ID.String()+"/publish"), body)
	require.Equal(t, http.StatusOK, rec.Code)

	got, err := f.Sheets.List(f.ctx())
	require.NoError(t, err)
	assert.Len(t, got, 3, "the two good rows must survive the collision, not be rolled back")
	assert.Contains(t, rec.Body.String(), "payroll", "the failing row must be named in the response")
}

func TestSheetHandler_BulkPublishWithNothingTickedPublishesNothing(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	cred := f.seedCredential(t, credential.KindGoogleOAuth, "Connected")
	sp := f.seedSpreadsheet(t, cred.ID)

	rec := f.do(t, http.MethodPost, f.path("/spreadsheets/"+sp.ID.String()+"/publish"), "visibility=key&cache_ttl=300")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `data-notice="bulk-none-selected"`)

	got, err := f.Sheets.List(f.ctx())
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestSheetHandler_BulkPublishReportsTwoTabsSlugifyingAlike(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	cred := f.seedCredential(t, credential.KindGoogleOAuth, "Connected")
	sp := f.seedSpreadsheet(t, cred.ID)
	f.Google.setTabs("Rates", "rates")

	body := "tab=0&slug.0=rates&tab=1&slug.1=rates&visibility=key&cache_ttl=300"
	rec := f.do(t, http.MethodPost, f.path("/spreadsheets/"+sp.ID.String()+"/publish"), body)
	require.Equal(t, http.StatusOK, rec.Code)

	got, err := f.Sheets.List(f.ctx())
	require.NoError(t, err)
	assert.Len(t, got, 1, "one created, one reported")
	assert.Contains(t, rec.Body.String(), `data-outcome="failed"`)
}

func TestSheetHandler_BulkPublishSkipsATabThatIsAlreadyPublished(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	cred := f.seedCredential(t, credential.KindGoogleOAuth, "Connected")
	sp := f.seedSpreadsheet(t, cred.ID)
	_, err := f.Sheets.Create(f.ctx(), sp.ID, "First", "first", sheet.VisibilityKey, 0)
	require.NoError(t, err)

	body := "tab=0&slug.0=first-again&tab=1&slug.1=second&visibility=key&cache_ttl=300"
	rec := f.do(t, http.MethodPost, f.path("/spreadsheets/"+sp.ID.String()+"/publish"), body)
	require.Equal(t, http.StatusOK, rec.Code)

	got, err := f.Sheets.List(f.ctx())
	require.NoError(t, err)
	slugs := make([]string, 0, len(got))
	for _, sh := range got {
		slugs = append(slugs, sh.Slug)
	}
	assert.ElementsMatch(t, []string{"first", "second"}, slugs,
		"a disabled row must not be republishable by hand-posting its index")
}

func TestSheetHandler_BulkPanelDisablesAnAlreadyPublishedTab(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	cred := f.seedCredential(t, credential.KindGoogleOAuth, "Connected")
	sp := f.seedSpreadsheet(t, cred.ID)
	sh, err := f.Sheets.Create(f.ctx(), sp.ID, "First", "first", sheet.VisibilityKey, 0)
	require.NoError(t, err)

	rec := f.do(t, http.MethodGet, f.path("/spreadsheets/"+sp.ID.String()+"/publish"), "")
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "disabled", "an already-published tab must not be tickable")
	assert.Contains(t, body, "/sheets/"+sh.ID.String(), "the disabled row must link to its sheet")
	assert.Contains(t, body, `value="second"`, "an unpublished tab must arrive with its slug pre-filled")
}

func TestSheetHandler_BulkPanelDegradesWhenTabsCannotBeListed(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	cred := f.seedCredential(t, credential.KindGoogleOAuth, "Connected")
	sp := f.seedSpreadsheet(t, cred.ID)
	f.Google.breakGoogle()

	rec := f.do(t, http.MethodGet, f.path("/spreadsheets/"+sp.ID.String()+"/publish"), "")
	require.Equal(t, http.StatusOK, rec.Code, "a Google failure must not fail the page")
	body := rec.Body.String()
	assert.Contains(t, body, "bulk-tabs-unavailable")
	assert.Contains(t, body, "/publish", "the single publish form must still be usable")
}

func TestSheetHandler_BulkPublishFragmentLinksStayProjectScoped(t *testing.T) {
	t.Parallel()
	f := newSheetsFixture(t)
	cred := f.seedCredential(t, credential.KindGoogleOAuth, "Connected")
	sp := f.seedSpreadsheet(t, cred.ID)

	rec := f.do(t, http.MethodPost, f.path("/spreadsheets/"+sp.ID.String()+"/publish"),
		"tab=0&slug.0=rates&visibility=key&cache_ttl=300")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), `"/orgs"`,
		"a fragment rendered without ActiveOrg collapses ProjectPath to /orgs")
}
