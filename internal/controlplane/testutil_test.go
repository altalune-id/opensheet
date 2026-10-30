package controlplane_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"

	authv1connect "altalune.id/opensheet/gen/go/auth/v1/authv1connect"
	projectv1connect "altalune.id/opensheet/gen/go/project/v1/projectv1connect"
	todov1connect "altalune.id/opensheet/gen/go/todo/v1/todov1connect"
	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/controlplane"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/capabilities"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tokens"
	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/testutil/fakes"
	"altalune.id/opensheet/internal/todo"
)

type stubVerifier struct {
	principal session.Principal
	err       error
}

func (s stubVerifier) Verify(_ context.Context, _ string) (session.Principal, error) {
	return s.principal, s.err
}

var _ tokens.Verifier = stubVerifier{}

type harness struct {
	t      *testing.T
	server *httptest.Server
	orgs   *fakes.Org
	projs  *fakes.Project
	todos  *fakes.Todo
	keys   *fakes.APIKey
}

func newHarness(t *testing.T, p session.Principal) *harness {
	t.Helper()
	return newHarnessOpts(t, p, nil)
}

func newHarnessOpts(t *testing.T, p session.Principal, verr error) *harness {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	reporter := apperror.NewReporter(log, false)

	orgs := fakes.NewOrg()
	projs := fakes.NewProject()
	tds := fakes.NewTodo()
	keys := fakes.NewAPIKey()

	orgSvc := org.NewService(orgs, capabilities.Capabilities{OrgCreation: true}, log, reporter.Unexpected)
	projectSvc := project.NewService(projs, log, reporter.Unexpected)
	todoSvc := todo.NewService(tds, log, reporter.Unexpected, &fakes.Queue{})

	kernel := &platform.Kernel{
		Log:      log,
		Reporter: reporter,
		Verifier: stubVerifier{principal: fullTokenGrant(p), err: verr},
	}

	srv := controlplane.New(nil, kernel, controlplane.Deps{Orgs: orgSvc, Projects: projectSvc})
	srv.Authn = authn.Chain{apikey.NewAuthenticator(keys, nil, apikey.Scheme{}, fakes.NewMembers()), tokens.NewAuthenticator(kernel.Verifier, authn.Scheme{Prefix: apikey.DefaultPrefix})}
	srv.APIKeys = apikey.NewService(keys, apikey.Scheme{}, fakes.PermissiveMembers(), fakes.NewOrgProjects(), log, reporter.Unexpected)
	ref := srv.ReferenceHandler(controlplane.NewTodoService(todoSvc, tds, projectSvc))
	mux := http.NewServeMux()
	mux.Handle("/api/", srv.Handler(""))
	mux.Handle("/api/"+todov1connect.TodoServiceName+"/", ref)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return &harness{t: t, server: ts, orgs: orgs, projs: projs, todos: tds, keys: keys}
}

// NOTE: a token principal is held to the scope table, so the harness's person carries every scope unless a test narrows it.
func fullTokenGrant(p session.Principal) session.Principal {
	if p.Source == "" {
		p.Source = session.SourceToken
	}
	if p.Scopes == nil {
		p.Scopes = authn.AllScopes()
	}
	return p
}

func withKey(plaintext string) func(http.Header) {
	return func(hdr http.Header) { hdr.Set("Authorization", "Bearer "+plaintext) }
}

func (h *harness) authClient() todov1connect.TodoServiceClient {
	return todov1connect.NewTodoServiceClient(http.DefaultClient, h.server.URL+"/api")
}

func (h *harness) projectClient() projectv1connect.ProjectServiceClient {
	return projectv1connect.NewProjectServiceClient(http.DefaultClient, h.server.URL+"/api")
}

func (h *harness) whoamiClient() authv1connect.AuthServiceClient {
	return authv1connect.NewAuthServiceClient(http.DefaultClient, h.server.URL+"/api")
}

func withBearer(hdr http.Header) {
	hdr.Set("Authorization", "Bearer stub.token.value")
}

func connectCode(err error) connect.Code {
	if err == nil {
		return connect.CodeUnknown
	}
	return connect.CodeOf(err)
}
