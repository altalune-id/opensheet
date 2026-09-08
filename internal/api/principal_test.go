package api_test

import (
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	todov1 "altalune.id/opensheet/gen/go/todo/v1"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/project"
)

func TestPrincipal_AcceptsAnAPIKeySource(t *testing.T) {
	orgID := uuid.New()
	p := session.Principal{
		Name:            "ci-key",
		Source:          session.SourceAPIKey,
		ActiveOrgID:     orgID,
		ActiveProjectID: uuid.New(),
		Scopes:          []string{"sheets:read"},
	}
	h := newHarness(t, p)

	proj, err := project.New(orgID, "p1", "Project 1")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.projs.Save(t.Context(), proj); err != nil {
		t.Fatal(err)
	}

	req := connect.NewRequest(&todov1.CreateRequest{ProjectId: proj.ID.String(), Title: "from a machine"})
	withBearer(req.Header())

	resp, err := h.authClient().Create(t.Context(), req)
	if err != nil {
		t.Fatalf("Create with an api-key principal: %v", err)
	}
	if resp.Msg.Todo.ProjectId != proj.ID.String() {
		t.Errorf("project_id = %q, want %q", resp.Msg.Todo.ProjectId, proj.ID.String())
	}
}

func TestPrincipal_RejectsAnAPIKeyWithoutAnOrg(t *testing.T) {
	h := newHarness(t, session.Principal{Source: session.SourceAPIKey, Name: "orphan-key"})

	req := connect.NewRequest(&todov1.ListRequest{ProjectId: uuid.New().String()})
	withBearer(req.Header())

	if _, err := h.authClient().List(t.Context(), req); err == nil {
		t.Fatal("expected error")
	} else if got := connectCode(err); got != connect.CodeUnauthenticated {
		t.Errorf("code = %v, want Unauthenticated", got)
	}
}

func TestPrincipal_RejectsAnEmptyPrincipal(t *testing.T) {
	h := newHarness(t, session.Principal{})

	req := connect.NewRequest(&todov1.ListRequest{ProjectId: uuid.New().String()})
	withBearer(req.Header())

	if _, err := h.authClient().List(t.Context(), req); err == nil {
		t.Fatal("expected error")
	} else if got := connectCode(err); got != connect.CodeUnauthenticated {
		t.Errorf("code = %v, want Unauthenticated", got)
	}
}

func TestPrincipal_RejectsAHumanSourceWithoutAUserID(t *testing.T) {
	h := newHarness(t, session.Principal{Source: session.SourceToken, Email: "a@b", ActiveOrgID: uuid.New()})

	req := connect.NewRequest(&todov1.ListRequest{ProjectId: uuid.New().String()})
	withBearer(req.Header())

	if _, err := h.authClient().List(t.Context(), req); err == nil {
		t.Fatal("expected error")
	} else if got := connectCode(err); got != connect.CodeUnauthenticated {
		t.Errorf("code = %v, want Unauthenticated", got)
	}
}
