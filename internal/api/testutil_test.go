package api_test

import (
	"context"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"golang.org/x/oauth2"

	apikeyv1connect "altalune.id/opensheet/gen/go/apikey/v1/apikeyv1connect"
	authv1connect "altalune.id/opensheet/gen/go/auth/v1/authv1connect"
	credentialv1connect "altalune.id/opensheet/gen/go/credential/v1/credentialv1connect"
	sheetv1connect "altalune.id/opensheet/gen/go/sheet/v1/sheetv1connect"
	spreadsheetv1connect "altalune.id/opensheet/gen/go/spreadsheet/v1/spreadsheetv1connect"
	todov1connect "altalune.id/opensheet/gen/go/todo/v1/todov1connect"
	"altalune.id/opensheet/gworkspace"
	"altalune.id/opensheet/gworkspace/gsheet"
	"altalune.id/opensheet/internal/api"
	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/credential"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/capabilities"
	"altalune.id/opensheet/internal/platform/sealer"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/sheet"
	"altalune.id/opensheet/internal/spreadsheet"
	"altalune.id/opensheet/internal/testutil/fakes"
	"altalune.id/opensheet/internal/todo"
)

const tabsBody = `{"properties":{"title":"Prices"},"sheets":[{"properties":{"title":"Q1"}},{"properties":{"title":"Q2"}}]}`

type stubAuthenticator struct {
	principal session.Principal
	err       error
}

func (s stubAuthenticator) Authenticate(_ context.Context, _ string) (session.Principal, error) {
	return s.principal, s.err
}

var _ authn.Authenticator = stubAuthenticator{}

type stubTokenSources struct{}

func (stubTokenSources) TokenSourceFor(_ context.Context, _ uuid.UUID) (oauth2.TokenSource, error) {
	return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "dummy", TokenType: "Bearer"}), nil
}

type publicSheetsOn struct{}

func (publicSheetsOn) PublicSheetsEnabled() bool { return true }

type harness struct {
	t         *testing.T
	server    *httptest.Server
	orgs      *fakes.Org
	projs     *fakes.Project
	todos     *fakes.Todo
	creds     *fakes.Credential
	sprds     *fakes.Spreadsheet
	sheets    *fakes.Sheet
	keys      *fakes.APIKey
	keySheets *fakes.APIKeySheets
	snaps     *fakes.SheetSnapshots
}

func humanPrincipal(orgID uuid.UUID) session.Principal {
	return session.Principal{UserID: uuid.New(), Email: "a@b", Source: session.SourceToken, ActiveOrgID: orgID}
}

func keyPrincipal(orgID uuid.UUID) session.Principal {
	return session.Principal{
		Name:        "ci-key",
		Source:      session.SourceAPIKey,
		ActiveOrgID: orgID,
		Scopes:      []string{"credentials:write", "spreadsheets:write", "sheets:admin", "apikeys:write", "cache:purge"},
	}
}

func newHarness(t *testing.T, p session.Principal) *harness {
	t.Helper()
	return newHarnessOpts(t, p, nil)
}

func newHarnessOpts(t *testing.T, p session.Principal, aerr error) *harness {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	reporter := apperror.NewReporter(log, false)

	orgs := fakes.NewOrg()
	projs := fakes.NewProject()
	tds := fakes.NewTodo()
	creds := fakes.NewCredential()
	sprds := fakes.NewSpreadsheet()
	shts := fakes.NewSheet()
	keys := fakes.NewAPIKey()
	snaps := fakes.NewSheetSnapshots()

	sl := testSealer(t)
	orgSvc := org.NewService(orgs, capabilities.Capabilities{OrgCreation: true}, log, reporter.Unexpected)
	projectSvc := project.NewService(projs, log, reporter.Unexpected)
	todoSvc := todo.NewService(tds, log, reporter.Unexpected)
	credSvc := credential.NewService(creds, log, reporter.Unexpected, sl)
	sprdSvc := spreadsheet.NewService(sprds, log, reporter.Unexpected, stubTokenSources{}, googleFactory(t))
	sheetSvc := sheet.NewService(shts, log, reporter.Unexpected, publicSheetsOn{}, snaps)
	keySheets := fakes.NewAPIKeySheets()
	keySvc := apikey.NewService(keys, log, reporter.Unexpected, keySheets)

	kernel := &platform.Kernel{Log: log, Reporter: reporter}

	// NOTE: Cfg is left nil so OpenAPI stays off in these tests.
	srv := api.New(api.Deps{
		Kernel:            kernel,
		Authn:             authn.Chain{stubAuthenticator{principal: p, err: aerr}},
		Orgs:              orgSvc,
		Projects:          projectSvc,
		Todos:             todoSvc,
		TodoStore:         tds,
		Credentials:       credSvc,
		CredentialConnect: connectWorkflow(t, creds, sl, log, reporter),
		Spreadsheets:      sprdSvc,
		Sheets:            sheetSvc,
		APIKeys:           keySvc,
	})
	ts := httptest.NewServer(srv.Handler(""))
	t.Cleanup(ts.Close)
	return &harness{
		t: t, server: ts, orgs: orgs, projs: projs, todos: tds,
		creds: creds, sprds: sprds, sheets: shts, keys: keys, keySheets: keySheets, snaps: snaps,
	}
}

func (h *harness) seedProject(orgID uuid.UUID) *project.Project {
	h.t.Helper()
	proj, err := project.New(orgID, "p1", "Project 1")
	if err != nil {
		h.t.Fatalf("project.New: %v", err)
	}
	if err := h.projs.Save(h.t.Context(), proj); err != nil {
		h.t.Fatalf("save project: %v", err)
	}
	return proj
}

func testSealer(t *testing.T) sealer.Sealer {
	t.Helper()
	key, err := hex.DecodeString(strings.Repeat("ab", sealer.KeyLen))
	if err != nil {
		t.Fatalf("decode key: %v", err)
	}
	s, err := sealer.New(key)
	if err != nil {
		t.Fatalf("sealer.New: %v", err)
	}
	return s
}

func googleFactory(t *testing.T) gsheet.Factory {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, tabsBody)
	}))
	t.Cleanup(srv.Close)
	return func(ctx context.Context, ts oauth2.TokenSource) (*gsheet.Client, error) {
		return gsheet.New(ctx, ts, gworkspace.WithBaseURL(srv.URL))
	}
}

func connectWorkflow(
	t *testing.T,
	store credential.Store,
	sl sealer.Sealer,
	log *slog.Logger,
	reporter *apperror.Reporter,
) *credential.ConnectWorkflow {
	t.Helper()
	connector, err := gworkspace.NewConnector(&oauth2.Config{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		RedirectURL:  "https://opensheet.test/credentials/google/callback",
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://accounts.google.com/o/oauth2/auth",
			TokenURL: "https://oauth2.googleapis.com/token",
		},
	}, gworkspace.Scopes())
	if err != nil {
		t.Fatalf("NewConnector: %v", err)
	}
	now := func() time.Time { return time.Now().UTC() }
	return credential.NewConnectWorkflow(store, sl, connector, []byte("state-secret"), now, log, reporter.Unexpected)
}

func (h *harness) authClient() todov1connect.TodoServiceClient {
	return todov1connect.NewTodoServiceClient(http.DefaultClient, h.server.URL+"/api")
}

func (h *harness) whoamiClient() authv1connect.AuthServiceClient {
	return authv1connect.NewAuthServiceClient(http.DefaultClient, h.server.URL+"/api")
}

func (h *harness) credentialClient() credentialv1connect.CredentialServiceClient {
	return credentialv1connect.NewCredentialServiceClient(http.DefaultClient, h.server.URL+"/api")
}

func (h *harness) spreadsheetClient() spreadsheetv1connect.SpreadsheetServiceClient {
	return spreadsheetv1connect.NewSpreadsheetServiceClient(http.DefaultClient, h.server.URL+"/api")
}

func (h *harness) sheetClient() sheetv1connect.SheetServiceClient {
	return sheetv1connect.NewSheetServiceClient(http.DefaultClient, h.server.URL+"/api")
}

func (h *harness) apiKeyClient() apikeyv1connect.APIKeyServiceClient {
	return apikeyv1connect.NewAPIKeyServiceClient(http.DefaultClient, h.server.URL+"/api")
}

func withBearer(hdr http.Header) {
	hdr.Set("Authorization", "Bearer stub-token")
}

func connectCode(err error) connect.Code {
	if err == nil {
		return connect.CodeUnknown
	}
	return connect.CodeOf(err)
}
