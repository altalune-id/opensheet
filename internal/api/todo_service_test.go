package api_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	todov1 "altalune.id/template/gen/go/todo/v1"
	"altalune.id/template/internal/platform/session"
	"altalune.id/template/internal/project"
	"altalune.id/template/internal/todo"
)

func TestTodo_List_MissingAuth_ReturnsUnauthenticated(t *testing.T) {
	h := newHarness(t, session.Principal{UserID: uuid.New(), Email: "a@b", ActiveOrgID: uuid.New()})

	client := h.authClient()
	req := connect.NewRequest(&todov1.ListRequest{ProjectId: uuid.New().String()})
	if _, err := client.List(context.Background(), req); err == nil {
		t.Fatal("expected error")
	} else if got := connectCode(err); got != connect.CodeUnauthenticated {
		t.Errorf("code = %v, want Unauthenticated", got)
	}
}

func TestTodo_Create_And_List_HappyPath(t *testing.T) {
	userID := uuid.New()
	orgID := uuid.New()
	p := session.Principal{UserID: userID, Email: "a@b", ActiveOrgID: orgID}
	h := newHarness(t, p)

	proj, err := project.New(orgID, "p1", "Project 1")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.projs.Save(context.Background(), proj); err != nil {
		t.Fatal(err)
	}

	client := h.authClient()

	createReq := connect.NewRequest(&todov1.CreateRequest{ProjectId: proj.ID.String(), Title: "buy milk"})
	withBearer(createReq.Header())
	createResp, err := client.Create(context.Background(), createReq)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if createResp.Msg.Todo.Title != "buy milk" {
		t.Errorf("title = %q", createResp.Msg.Todo.Title)
	}
	if createResp.Msg.Todo.ProjectId != proj.ID.String() {
		t.Errorf("project_id mismatch")
	}
	if createResp.Msg.Todo.Done {
		t.Error("new todo must not be done")
	}

	listReq := connect.NewRequest(&todov1.ListRequest{ProjectId: proj.ID.String()})
	withBearer(listReq.Header())
	listResp, err := client.List(context.Background(), listReq)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got := len(listResp.Msg.Todos); got != 1 {
		t.Errorf("todos len = %d, want 1", got)
	}
}

func TestTodo_Create_CrossTenantProject_ReturnsPermissionDenied(t *testing.T) {
	userID := uuid.New()
	orgA := uuid.New()
	orgB := uuid.New()
	p := session.Principal{UserID: userID, Email: "a@b", ActiveOrgID: orgA}
	h := newHarness(t, p)

	proj, err := project.New(orgB, "p1", "Foreign")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.projs.Save(context.Background(), proj); err != nil {
		t.Fatal(err)
	}

	client := h.authClient()
	req := connect.NewRequest(&todov1.CreateRequest{ProjectId: proj.ID.String(), Title: "sneak"})
	withBearer(req.Header())

	_, err = client.Create(context.Background(), req)
	if err == nil {
		t.Fatal("expected permission denied, got nil")
	}
	if got := connectCode(err); got != connect.CodePermissionDenied {
		t.Errorf("code = %v, want PermissionDenied", got)
	}
}

func TestTodo_Create_InvalidProjectUUID_ReturnsInvalidArgument(t *testing.T) {
	h := newHarness(t, session.Principal{UserID: uuid.New(), Email: "a@b", ActiveOrgID: uuid.New()})

	client := h.authClient()
	req := connect.NewRequest(&todov1.CreateRequest{ProjectId: "not-a-uuid", Title: "x"})
	withBearer(req.Header())

	_, err := client.Create(context.Background(), req)
	if err == nil {
		t.Fatal("expected error")
	}
	if got := connectCode(err); got != connect.CodeInvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", got)
	}
}

func TestTodo_Create_ProjectNotFound_ReturnsNotFound(t *testing.T) {
	h := newHarness(t, session.Principal{UserID: uuid.New(), Email: "a@b", ActiveOrgID: uuid.New()})

	client := h.authClient()
	req := connect.NewRequest(&todov1.CreateRequest{ProjectId: uuid.New().String(), Title: "x"})
	withBearer(req.Header())

	_, err := client.Create(context.Background(), req)
	if err == nil {
		t.Fatal("expected error")
	}
	if got := connectCode(err); got != connect.CodeNotFound {
		t.Errorf("code = %v, want NotFound", got)
	}
}

func TestTodo_Toggle_HappyPath(t *testing.T) {
	userID := uuid.New()
	orgID := uuid.New()
	p := session.Principal{UserID: userID, Email: "a@b", ActiveOrgID: orgID}
	h := newHarness(t, p)

	td, err := todo.New(orgID, uuid.New(), "flip me")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.todos.Save(context.Background(), td); err != nil {
		t.Fatal(err)
	}

	client := h.authClient()
	req := connect.NewRequest(&todov1.ToggleRequest{TodoId: td.ID.String()})
	withBearer(req.Header())

	resp, err := client.Toggle(context.Background(), req)
	if err != nil {
		t.Fatalf("Toggle: %v", err)
	}
	if !resp.Msg.Todo.Done {
		t.Error("Toggle should have set Done=true")
	}
}

func TestTodo_Toggle_CrossTenant_ReturnsPermissionDenied(t *testing.T) {
	userID := uuid.New()
	p := session.Principal{UserID: userID, Email: "a@b", ActiveOrgID: uuid.New()}
	h := newHarness(t, p)

	td, err := todo.New(uuid.New(), uuid.New(), "not yours")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.todos.Save(context.Background(), td); err != nil {
		t.Fatal(err)
	}

	client := h.authClient()
	req := connect.NewRequest(&todov1.ToggleRequest{TodoId: td.ID.String()})
	withBearer(req.Header())

	_, err = client.Toggle(context.Background(), req)
	if err == nil {
		t.Fatal("expected error")
	}
	if got := connectCode(err); got != connect.CodePermissionDenied {
		t.Errorf("code = %v, want PermissionDenied", got)
	}
}

func TestTodo_Delete_HappyPath(t *testing.T) {
	userID := uuid.New()
	orgID := uuid.New()
	p := session.Principal{UserID: userID, Email: "a@b", ActiveOrgID: orgID}
	h := newHarness(t, p)

	td, err := todo.New(orgID, uuid.New(), "gone")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.todos.Save(context.Background(), td); err != nil {
		t.Fatal(err)
	}

	client := h.authClient()
	req := connect.NewRequest(&todov1.DeleteRequest{TodoId: td.ID.String()})
	withBearer(req.Header())

	if _, err := client.Delete(context.Background(), req); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := h.todos.ByID(context.Background(), td.ID); err == nil {
		t.Error("todo should be gone")
	}
}

func TestTodo_Delete_TodoNotFound_ReturnsNotFound(t *testing.T) {
	h := newHarness(t, session.Principal{UserID: uuid.New(), Email: "a@b", ActiveOrgID: uuid.New()})

	client := h.authClient()
	req := connect.NewRequest(&todov1.DeleteRequest{TodoId: uuid.New().String()})
	withBearer(req.Header())

	_, err := client.Delete(context.Background(), req)
	if err == nil {
		t.Fatal("expected error")
	}
	if got := connectCode(err); got != connect.CodeNotFound {
		t.Errorf("code = %v, want NotFound", got)
	}
}
