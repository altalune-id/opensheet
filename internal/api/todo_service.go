package api

import (
	"context"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	todov1 "altalune.id/opensheet/gen/go/todo/v1"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/todo"
)

// TodoService implements todo.v1.TodoService.
type TodoService struct {
	todos     *todo.Service
	todoStore todo.Store
	projects  *project.Service
}

// NewTodoService binds the handler to its collaborators.
func NewTodoService(todos *todo.Service, todoStore todo.Store, projects *project.Service) *TodoService {
	return &TodoService{todos: todos, todoStore: todoStore, projects: projects}
}

// Create persists a new todo bound to the request's project.
func (s *TodoService) Create(ctx context.Context, req *connect.Request[todov1.CreateRequest]) (*connect.Response[todov1.CreateResponse], error) {
	tctx, err := scopeToProject(ctx, s.projects, req.Msg.GetProjectId())
	if err != nil {
		return nil, err
	}
	t, err := s.todos.Create(tctx, req.Msg.GetTitle())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&todov1.CreateResponse{Todo: toProto(t)}), nil
}

// List returns todos in the request's project.
func (s *TodoService) List(ctx context.Context, req *connect.Request[todov1.ListRequest]) (*connect.Response[todov1.ListResponse], error) {
	tctx, err := scopeToProject(ctx, s.projects, req.Msg.GetProjectId())
	if err != nil {
		return nil, err
	}
	items, err := s.todos.List(tctx, todo.ListOpts{})
	if err != nil {
		return nil, err
	}
	resp := &todov1.ListResponse{Todos: make([]*todov1.Todo, 0, len(items))}
	for _, t := range items {
		resp.Todos = append(resp.Todos, toProto(t))
	}
	return connect.NewResponse(resp), nil
}

// Toggle flips the referenced todo's done flag.
func (s *TodoService) Toggle(ctx context.Context, req *connect.Request[todov1.ToggleRequest]) (*connect.Response[todov1.ToggleResponse], error) {
	tctx, tid, err := s.scopeToTodo(ctx, req.Msg.GetTodoId())
	if err != nil {
		return nil, err
	}
	t, err := s.todos.Toggle(tctx, tid)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&todov1.ToggleResponse{Todo: toProto(t)}), nil
}

// Delete removes the referenced todo.
func (s *TodoService) Delete(ctx context.Context, req *connect.Request[todov1.DeleteRequest]) (*connect.Response[todov1.DeleteResponse], error) {
	tctx, tid, err := s.scopeToTodo(ctx, req.Msg.GetTodoId())
	if err != nil {
		return nil, err
	}
	if err := s.todos.Delete(tctx, tid); err != nil {
		return nil, err
	}
	return connect.NewResponse(&todov1.DeleteResponse{}), nil
}

func (s *TodoService) scopeToTodo(ctx context.Context, todoIDRaw string) (context.Context, uuid.UUID, error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, uuid.Nil, err
	}
	tid, err := parseUUID("todo_id", todoIDRaw)
	if err != nil {
		return nil, uuid.Nil, err
	}
	t, err := s.todoStore.ByID(ctx, tid)
	if err != nil {
		return nil, uuid.Nil, err
	}
	if t.OrgID != p.ActiveOrgID {
		return nil, uuid.Nil, forbiddenErr("todo belongs to another org", "todo_id", tid.String())
	}
	return tenant.Into(ctx, tenant.Context{
		OrgID:     t.OrgID,
		ProjectID: t.ProjectID,
		UserID:    p.UserID,
	}), tid, nil
}

func toProto(t *todo.Todo) *todov1.Todo {
	created := timestamppb.New(t.CreatedAt)
	return &todov1.Todo{
		Id:        t.ID.String(),
		ProjectId: t.ProjectID.String(),
		Title:     t.Title,
		Done:      t.Done,
		CreatedAt: created,
		UpdatedAt: created,
	}
}
