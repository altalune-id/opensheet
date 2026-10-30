package controlplane_test

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

	"github.com/google/uuid"
	"golang.org/x/oauth2"

	apikeyv1connect "altalune.id/opensheet/gen/go/apikey/v1/apikeyv1connect"
	credentialv1connect "altalune.id/opensheet/gen/go/credential/v1/credentialv1connect"
	projectv1connect "altalune.id/opensheet/gen/go/project/v1/projectv1connect"
	sheetv1connect "altalune.id/opensheet/gen/go/sheet/v1/sheetv1connect"
	spreadsheetv1connect "altalune.id/opensheet/gen/go/spreadsheet/v1/spreadsheetv1connect"
	"altalune.id/opensheet/gworkspace"
	"altalune.id/opensheet/gworkspace/gsheet"
	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/controlplane"
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
)

const tabsBody = `{"properties":{"title":"Prices"},"sheets":[{"properties":{"title":"Q1"}},{"properties":{"title":"Q2"}}]}`

// NOTE: publish now reads the tab, so sheet.Create needs a body that satisfies the table contract.
const valuesBody = `{"values":[["id","name"],["a","ada"]]}`

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

type osHarness struct {
	t      *testing.T
	server *httptest.Server
	orgs   *fakes.Org
	projs  *fakes.Project
	creds  *fakes.Credential
	sprds  *fakes.Spreadsheet
	sheets *fakes.Sheet
	keys   *fakes.APIKey
	snaps  *fakes.SheetSnapshots
}

func humanPrincipal(orgID uuid.UUID) session.Principal {
	return session.Principal{
		UserID: uuid.New(), Email: "a@b", Source: session.SourceToken, ActiveOrgID: orgID, Scopes: authn.MintableScopes(),
	}
}

func keyPrincipal(orgID uuid.UUID) session.Principal {
	return session.Principal{
		Name:        "ci-key",
		Source:      session.SourceAPIKey,
		ActiveOrgID: orgID,
		AllProjects: true,
		Scopes: []string{
			"credentials:read", "credentials:write", "spreadsheets:read", "spreadsheets:write",
			"sheets:read", "sheets:admin", "cache:purge",
		},
	}
}

func newOSHarness(t *testing.T, p session.Principal) *osHarness {
	t.Helper()
	return newOSHarnessOpts(t, p, nil)
}

func newOSHarnessOpts(t *testing.T, p session.Principal, aerr error) *osHarness {
	t.Helper()
	return buildOSHarness(t, p, aerr, false)
}

func newOSHarnessWithKeys(t *testing.T, p session.Principal) *osHarness {
	t.Helper()
	return buildOSHarness(t, p, nil, true)
}

func buildOSHarness(t *testing.T, p session.Principal, aerr error, realKeys bool) *osHarness {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	reporter := apperror.NewReporter(log, false)

	orgs := fakes.NewOrg()
	projs := fakes.NewProject()
	creds := fakes.NewCredential()
	sprds := fakes.NewSpreadsheet()
	shts := fakes.NewSheet()
	keys := fakes.NewAPIKey()
	snaps := fakes.NewSheetSnapshots()

	sl := testSealer(t)
	orgSvc := org.NewService(orgs, capabilities.Capabilities{OrgCreation: true}, log, reporter.Unexpected)
	projectSvc := project.NewService(projs, log, reporter.Unexpected)
	credSvc := credential.NewService(creds, log, reporter.Unexpected, sl)
	sprdSvc := spreadsheet.NewService(sprds, log, reporter.Unexpected, stubTokenSources{}, googleFactory(t),
		stubTokenSources{}, googleWriterFactory(t))
	publishWorkflow := sheet.NewPublishWorkflow(anySheetSource{}, stubTokenSources{}, fakes.NewSheetReauthers(),
		googleFactory(t), log, reporter.Unexpected)
	sheetSvc := sheet.NewService(shts, log, reporter.Unexpected, publicSheetsOn{}, snaps,
		sheet.NewMemoryIdempotencyStore(), publishWorkflow)
	keySvc := apikey.NewService(keys, apikey.Scheme{}, fakes.PermissiveMembers(), fakes.NewOrgProjects(), log, reporter.Unexpected)

	kernel := &platform.Kernel{Log: log, Reporter: reporter}

	srv := controlplane.New(nil, kernel, controlplane.Deps{
		Orgs:              orgSvc,
		Projects:          projectSvc,
		Credentials:       credSvc,
		CredentialConnect: connectWorkflow(t, creds, sl, log, reporter),
		Spreadsheets:      sprdSvc,
		Sheets:            sheetSvc,
	})
	srv.Authn = authn.Chain{stubAuthenticator{principal: p, err: aerr}}
	if realKeys {
		srv.Authn = authn.Chain{apikey.NewAuthenticator(keys, nil, apikey.Scheme{}, fakes.NewMembers()), stubAuthenticator{principal: p, err: aerr}}
	}
	srv.APIKeys = keySvc
	ts := httptest.NewServer(srv.Handler(""))
	t.Cleanup(ts.Close)
	return &osHarness{
		t: t, server: ts, orgs: orgs, projs: projs,
		creds: creds, sprds: sprds, sheets: shts, keys: keys, snaps: snaps,
	}
}

func (h *osHarness) seedProject(orgID uuid.UUID) *project.Project {
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

func (h *osHarness) mintKey(orgID, projectID uuid.UUID, scopes []string, resourceIDs []uuid.UUID) string {
	h.t.Helper()
	k, plaintext, err := apikey.Scheme{}.Mint(orgID, projectID, "test-key", scopes, resourceIDs, nil, time.Now().UTC())
	if err != nil {
		h.t.Fatalf("mint key: %v", err)
	}
	h.keys.Seed(k)
	return plaintext
}

func (h *osHarness) projectClient() projectv1connect.ProjectServiceClient {
	return projectv1connect.NewProjectServiceClient(http.DefaultClient, h.server.URL+"/api")
}

func (h *osHarness) apikeyClient() apikeyv1connect.APIKeyServiceClient {
	return apikeyv1connect.NewAPIKeyServiceClient(http.DefaultClient, h.server.URL+"/api")
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

type anySheetSource struct{}

func (anySheetSource) SourceFor(_ context.Context, spreadsheetID uuid.UUID) (sheet.Source, error) {
	return sheet.Source{GoogleFileID: "FILE", CredentialID: spreadsheetID}, nil
}

func googleFactory(t *testing.T) gsheet.Factory {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v4/spreadsheets/{file}/values/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, valuesBody)
	})
	mux.HandleFunc("/v4/spreadsheets/{file}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, tabsBody)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return func(ctx context.Context, ts oauth2.TokenSource) (*gsheet.Client, error) {
		return gsheet.New(ctx, ts, gworkspace.WithBaseURL(srv.URL))
	}
}

func googleWriterFactory(t *testing.T) gsheet.WriterFactory {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{}")
	}))
	t.Cleanup(srv.Close)
	return func(ctx context.Context, ts oauth2.TokenSource) (*gsheet.Writer, error) {
		return gsheet.NewWriter(ctx, ts, gworkspace.WithBaseURL(srv.URL))
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

func (h *osHarness) credentialClient() credentialv1connect.CredentialServiceClient {
	return credentialv1connect.NewCredentialServiceClient(http.DefaultClient, h.server.URL+"/api")
}

func (h *osHarness) spreadsheetClient() spreadsheetv1connect.SpreadsheetServiceClient {
	return spreadsheetv1connect.NewSpreadsheetServiceClient(http.DefaultClient, h.server.URL+"/api")
}

func (h *osHarness) sheetClient() sheetv1connect.SheetServiceClient {
	return sheetv1connect.NewSheetServiceClient(http.DefaultClient, h.server.URL+"/api")
}
