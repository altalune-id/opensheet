package mcp_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"altalune.id/opensheet/mcp"
)

func TestRegistryHandlerForReturnsTheRegisteredHandler(t *testing.T) {
	reg := mcp.NewRegistry()
	sentinel := &struct{ n int }{n: 1}
	reg.Register(mcp.ToolSpec{Name: "todo_create", Scope: "posts:write", Handler: nilHandler}, sentinel)

	got := reg.HandlerFor("todo_create")
	if got != any(sentinel) {
		t.Fatalf("HandlerFor returned %v, want the exact registered handler", got)
	}
}

func TestRegistryHandlerForUnknownToolReturnsNil(t *testing.T) {
	reg := mcp.NewRegistry()
	if got := reg.HandlerFor("nonexistent"); got != nil {
		t.Fatalf("HandlerFor(unknown) = %v, want nil", got)
	}
}

func TestRegistryNamesAreSorted(t *testing.T) {
	reg := mcp.NewRegistry()
	for _, name := range []string{"todo_create", "blog_publish", "blog_list"} {
		reg.Register(mcp.ToolSpec{Name: name, Scope: "posts:read", Handler: nilHandler}, nil)
	}

	want := []string{"blog_list", "blog_publish", "todo_create"}
	if got := reg.Names(); !slices.Equal(got, want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
}

func TestRegistrySpecReturnsTheRegisteredSpec(t *testing.T) {
	reg := mcp.NewRegistry()
	reg.Register(mcp.ToolSpec{Name: "blog_publish", Scope: "posts:write", Mutation: true, Handler: nilHandler}, nil)

	spec, ok := reg.Spec("blog_publish")
	if !ok {
		t.Fatal("Spec(blog_publish) not found")
	}
	if spec.Scope != "posts:write" || !spec.Mutation {
		t.Fatalf("Spec = %+v, want scope posts:write and Mutation true", spec)
	}

	if _, ok := reg.Spec("nonexistent"); ok {
		t.Fatal("Spec(unknown) reported found")
	}
}

func TestRegistryRejectsAnInvalidRegistration(t *testing.T) {
	tests := []struct {
		name     string
		register func(*mcp.Registry)
		want     string
	}{
		{
			name: "duplicate name",
			register: func(reg *mcp.Registry) {
				reg.Register(mcp.ToolSpec{Name: "blog_list", Scope: "posts:read", Handler: nilHandler}, nil)
				reg.Register(mcp.ToolSpec{Name: "blog_list", Scope: "posts:write", Handler: nilHandler}, nil)
			},
			want: "mcp: duplicate tool name blog_list",
		},
		{
			name:     "empty name",
			register: func(reg *mcp.Registry) { reg.Register(mcp.ToolSpec{Scope: "posts:read", Handler: nilHandler}, nil) },
			want:     "mcp: ToolSpec.Name must not be empty",
		},
		{
			name:     "nil handler",
			register: func(reg *mcp.Registry) { reg.Register(mcp.ToolSpec{Name: "blog_list", Scope: "posts:read"}, nil) },
			want:     "mcp: ToolSpec.Handler must not be nil for tool blog_list",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				got, ok := recover().(string)
				if !ok {
					t.Fatalf("Register did not panic; the registration was accepted")
				}
				if got != tc.want {
					t.Fatalf("panic = %q, want %q", got, tc.want)
				}
			}()
			tc.register(mcp.NewRegistry())
		})
	}
}

// TestRegistryKeepsTheFirstOfTwoToolsOfTheSameName pins that a rejected duplicate leaves the first registration intact.
func TestRegistryKeepsTheFirstOfTwoToolsOfTheSameName(t *testing.T) {
	reg := mcp.NewRegistry()
	reg.Register(mcp.ToolSpec{Name: "blog_list", Scope: "posts:read", Handler: nilHandler}, "first")

	func() {
		defer func() { _ = recover() }()
		reg.Register(mcp.ToolSpec{Name: "blog_list", Scope: "posts:write", Handler: nilHandler}, "second")
	}()

	if got := reg.HandlerFor("blog_list"); got != "first" {
		t.Fatalf("HandlerFor(blog_list) = %v, want the first registration", got)
	}
	spec, _ := reg.Spec("blog_list")
	if spec.Scope != "posts:read" {
		t.Fatalf("Spec(blog_list).Scope = %q, want posts:read", spec.Scope)
	}
}

func nilHandler(context.Context, json.RawMessage) (json.RawMessage, error) { return nil, nil }
