package project

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNew_OK(t *testing.T) {
	orgID := uuid.New()
	p, err := New(orgID, "proj-1", "  Alpha  ")
	if err != nil {
		t.Fatal(err)
	}
	if p.Slug != "proj-1" {
		t.Errorf("slug=%q want %q", p.Slug, "proj-1")
	}
	if p.Name != "Alpha" {
		t.Errorf("name not trimmed: %q", p.Name)
	}
	if p.OrgID != orgID {
		t.Error("orgID mismatch")
	}
	if p.ID == uuid.Nil {
		t.Error("ID must be assigned")
	}
	if p.CreatedAt.Location() != time.UTC {
		t.Error("CreatedAt must be UTC")
	}
	if p.CreatedAt.IsZero() {
		t.Error("CreatedAt must be set")
	}
}

func TestNew_ValidatesSlug(t *testing.T) {
	tests := []struct {
		name string
		slug string
	}{
		{"empty", ""},
		{"leading dash", "-abc"},
		{"uppercase", "ABC"},
		{"whitespace", "with space"},
		{"underscore", "a_b"},
		{"punctuation", "a!"},
		{"too long", strings.Repeat("a", slugMaxLen+1)},
	}
	orgID := uuid.New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(orgID, tt.slug, "Name")
			if err == nil {
				t.Fatalf("slug %q must fail", tt.slug)
			}
			if !IsInvalidSlugError(err) {
				t.Errorf("want *InvalidSlugError, got %T", err)
			}
		})
	}
}

func TestNew_ValidatesName(t *testing.T) {
	tests := []struct {
		name string
		val  string
	}{
		{"empty", ""},
		{"whitespace", "   "},
		{"too long", strings.Repeat("x", nameMaxLen+1)},
	}
	orgID := uuid.New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(orgID, "abc", tt.val)
			if err == nil {
				t.Fatalf("name %q must fail", tt.val)
			}
			if !IsInvalidNameError(err) {
				t.Errorf("want *InvalidNameError, got %T", err)
			}
		})
	}
}

func TestProject_RenameOK_SlugImmutable(t *testing.T) {
	p, err := New(uuid.New(), "abc", "Old")
	if err != nil {
		t.Fatal(err)
	}
	origSlug := p.Slug
	if err := p.Rename("  New  "); err != nil {
		t.Fatal(err)
	}
	if p.Name != "New" {
		t.Errorf("name=%q want %q", p.Name, "New")
	}
	if p.Slug != origSlug {
		t.Error("slug must be immutable")
	}
}

func TestProject_RenameRejectsEmpty(t *testing.T) {
	p, _ := New(uuid.New(), "abc", "Old")
	if err := p.Rename(""); !IsInvalidNameError(err) {
		t.Errorf("want *InvalidNameError, got %T", err)
	}
	if err := p.Rename("   "); !IsInvalidNameError(err) {
		t.Errorf("want *InvalidNameError, got %T", err)
	}
}

func TestNotFoundError(t *testing.T) {
	tests := []struct {
		name string
		err  *NotFoundError
		want string
	}{
		{"by id", &NotFoundError{ID: "abc"}, "id=abc"},
		{"by slug", &NotFoundError{OrgID: "o", Slug: "s"}, `slug="s"`},
		{"blank", &NotFoundError{}, "not found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(tt.err.Error(), tt.want) {
				t.Errorf("Error()=%q want contains %q", tt.err.Error(), tt.want)
			}
			if ae := tt.err.ToAppError(); ae == nil {
				t.Error("ToAppError returned nil")
			}
			var target *NotFoundError
			if !errors.As(tt.err, &target) {
				t.Error("errors.As failed for *NotFoundError")
			}
			if !IsNotFoundError(tt.err) {
				t.Error("IsNotFoundError false")
			}
		})
	}
}

func TestAlreadyExistsError(t *testing.T) {
	tests := []struct {
		name string
		err  *AlreadyExistsError
		want string
	}{
		{"with field", &AlreadyExistsError{Field: "slug", Value: "foo"}, `slug="foo"`},
		{"blank", &AlreadyExistsError{}, "already exists"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(tt.err.Error(), tt.want) {
				t.Errorf("Error()=%q want contains %q", tt.err.Error(), tt.want)
			}
			if ae := tt.err.ToAppError(); ae == nil {
				t.Error("ToAppError returned nil")
			}
			if !IsAlreadyExistsError(tt.err) {
				t.Error("IsAlreadyExistsError false")
			}
		})
	}
}

func TestInvalidSlugError(t *testing.T) {
	tests := []struct {
		name string
		err  *InvalidSlugError
		want string
	}{
		{"with reason", &InvalidSlugError{Slug: "bad!", Reason: "punctuation"}, "punctuation"},
		{"blank slug", &InvalidSlugError{Reason: "empty"}, "empty"},
		{"blank reason", &InvalidSlugError{Slug: "abc"}, "invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(tt.err.Error(), tt.want) {
				t.Errorf("Error()=%q want contains %q", tt.err.Error(), tt.want)
			}
			if ae := tt.err.ToAppError(); ae == nil {
				t.Error("ToAppError returned nil")
			}
			if !IsInvalidSlugError(tt.err) {
				t.Error("IsInvalidSlugError false")
			}
		})
	}
}

func TestInvalidNameError(t *testing.T) {
	tests := []struct {
		name string
		err  *InvalidNameError
		want string
	}{
		{"with reason", &InvalidNameError{Reason: "empty"}, "empty"},
		{"blank", &InvalidNameError{}, "invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(tt.err.Error(), tt.want) {
				t.Errorf("Error()=%q want contains %q", tt.err.Error(), tt.want)
			}
			if ae := tt.err.ToAppError(); ae == nil {
				t.Error("ToAppError returned nil")
			}
			if !IsInvalidNameError(tt.err) {
				t.Error("IsInvalidNameError false")
			}
		})
	}
}

// TestValidateSlug_RejectsRouteShadowingSlugs guards the router: /orgs/{org}/projects/new is a literal
// pattern, so a project slugged "new" would never reach its own page.
func TestValidateSlug_RejectsRouteShadowingSlugs(t *testing.T) {
	t.Parallel()
	if _, err := New(uuid.New(), "new", "New"); !IsInvalidSlugError(err) {
		t.Fatalf("slug %q must be refused, got %T: %v", "new", err, err)
	}
	if _, err := New(uuid.New(), "newsroom", "Newsroom"); err != nil {
		t.Fatalf("only the exact reserved word is refused, got %v", err)
	}
}
