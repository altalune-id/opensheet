package api_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"

	authv1connect "altalune.id/opensheet/gen/go/auth/v1/authv1connect"
	todov1connect "altalune.id/opensheet/gen/go/todo/v1/todov1connect"
	"altalune.id/opensheet/internal/api"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/capabilities"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/testutil/fakes"
	"altalune.id/opensheet/internal/todo"
)

type stubAuthenticator struct {
	principal session.Principal
	err       error
}

func (s stubAuthenticator) Authenticate(_ context.Context, _ string) (session.Principal, error) {
	return s.principal, s.err
}

var _ authn.Authenticator = stubAuthenticator{}

type harness struct {
	t      *testing.T
	server *httptest.Server
	orgs   *fakes.Org
	projs  *fakes.Project
	todos  *fakes.Todo
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

	orgSvc := org.NewService(orgs, capabilities.Capabilities{OrgCreation: true}, log, reporter.Unexpected)
	projectSvc := project.NewService(projs, log, reporter.Unexpected)
	todoSvc := todo.NewService(tds, log, reporter.Unexpected)

	kernel := &platform.Kernel{Log: log, Reporter: reporter}

	// NOTE: Cfg is left nil so OpenAPI stays off in these tests.
	srv := api.New(api.Deps{
		Kernel:    kernel,
		Authn:     authn.Chain{stubAuthenticator{principal: p, err: aerr}},
		Orgs:      orgSvc,
		Projects:  projectSvc,
		Todos:     todoSvc,
		TodoStore: tds,
	})
	ts := httptest.NewServer(srv.Handler(""))
	t.Cleanup(ts.Close)
	return &harness{t: t, server: ts, orgs: orgs, projs: projs, todos: tds}
}

func (h *harness) authClient() todov1connect.TodoServiceClient {
	return todov1connect.NewTodoServiceClient(http.DefaultClient, h.server.URL+"/api")
}

func (h *harness) whoamiClient() authv1connect.AuthServiceClient {
	return authv1connect.NewAuthServiceClient(http.DefaultClient, h.server.URL+"/api")
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
