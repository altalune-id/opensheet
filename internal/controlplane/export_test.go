package controlplane

import (
	"maps"
	"net/http"

	todov1connect "altalune.id/opensheet/gen/go/todo/v1/todov1connect"
	"altalune.id/opensheet/internal/platform/authn"
)

// The template's posts scopes stand in for opensheet's sheet scopes on the reference handler, since opensheet's catalog carries no posts:*.
const (
	ScopePostsRead  = authn.ScopeSheetsRead
	ScopePostsWrite = authn.ScopeSheetsWrite
	ScopePostsAdmin = authn.ScopeSheetsAdmin
)

// ReferenceHandler serves the todo handler, which opensheet does not mount, behind the production interceptor chain.
func (s *Server) ReferenceHandler(todos *TodoService) http.Handler {
	table := ScopeTable()
	maps.Copy(table, authn.ScopeTable{
		todov1connect.TodoServiceListProcedure:   ScopePostsRead,
		todov1connect.TodoServiceCreateProcedure: ScopePostsWrite,
		todov1connect.TodoServiceToggleProcedure: ScopePostsWrite,
		todov1connect.TodoServiceDeleteProcedure: ScopePostsAdmin,
	})
	opts := s.handlerOptionsFor(table)
	inner := http.NewServeMux()
	todoPath, todoHandler := todov1connect.NewTodoServiceHandler(todos, opts...)
	inner.Handle(todoPath, todoHandler)
	return http.StripPrefix("/api", inner)
}
