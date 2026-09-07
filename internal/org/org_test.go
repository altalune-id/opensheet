package org_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/org"
)

func TestRole_IsValid(t *testing.T) {
	valid := []org.Role{org.RoleOwner, org.RoleAdmin, org.RoleMember}
	for _, r := range valid {
		if !r.IsValid() {
			t.Errorf("Role(%q).IsValid() = false, want true", r)
		}
	}
	invalid := []org.Role{"", "root", "guest", org.Role("OWNER")}
	for _, r := range invalid {
		if r.IsValid() {
			t.Errorf("Role(%q).IsValid() = true, want false", r)
		}
	}
}

func TestNewOrg(t *testing.T) {
	owner := uuid.New()
	cases := []struct {
		name      string
		slug      string
		orgName   string
		ownerID   uuid.UUID
		wantErrIs func(error) bool
	}{
		{"ok simple", "acme", "Acme", owner, nil},
		{"ok dashed", "acme-corp", "Acme Corp", owner, nil},
		{"ok numeric", "ac9-42", "Acme", owner, nil},
		{"trims slug", "  acme  ", "Acme", owner, nil},
		{"slug too short", "a", "Acme", owner, org.IsInvalidSlugError},
		{"slug too long", strings.Repeat("a", 65), "Acme", owner, org.IsInvalidSlugError},
		{"slug uppercase", "Acme", "Acme", owner, org.IsInvalidSlugError},
		{"slug space", "a b", "Acme", owner, org.IsInvalidSlugError},
		{"slug leading dash", "-acme", "Acme", owner, org.IsInvalidSlugError},
		{"slug trailing dash", "acme-", "Acme", owner, org.IsInvalidSlugError},
		{"name empty", "acme", "", owner, org.IsInvalidNameError},
		{"name whitespace", "acme", "   ", owner, org.IsInvalidNameError},
		{"name too long", "acme", strings.Repeat("x", 201), owner, org.IsInvalidNameError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o, err := org.NewOrg(tc.slug, tc.orgName, tc.ownerID)
			if tc.wantErrIs != nil {
				if err == nil {
					t.Fatalf("want error, got org=%+v", o)
				}
				if !tc.wantErrIs(err) {
					t.Fatalf("wrong error type: %T: %v", err, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if o.ID == uuid.Nil {
				t.Error("expected non-nil ID")
			}
			if o.OwnerID != tc.ownerID {
				t.Errorf("OwnerID = %v want %v", o.OwnerID, tc.ownerID)
			}
			if o.CreatedAt.IsZero() {
				t.Error("expected non-zero CreatedAt")
			}
		})
	}
}

func TestOrg_Rename(t *testing.T) {
	o, err := org.NewOrg("acme", "Acme", uuid.New())
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := o.Rename("Acme Corp"); err != nil {
		t.Fatalf("Rename returned %v", err)
	}
	if o.Name != "Acme Corp" {
		t.Errorf("Name = %q want %q", o.Name, "Acme Corp")
	}
	if err := o.Rename("   "); !org.IsInvalidNameError(err) {
		t.Errorf("Rename empty: want InvalidNameError got %v", err)
	}
	if err := o.Rename(strings.Repeat("x", 201)); !org.IsInvalidNameError(err) {
		t.Errorf("Rename too long: want InvalidNameError got %v", err)
	}
}

func TestNewMembership(t *testing.T) {
	orgID, userID := uuid.New(), uuid.New()
	cases := []struct {
		name      string
		role      org.Role
		wantErrIs func(error) bool
	}{
		{"owner", org.RoleOwner, nil},
		{"admin", org.RoleAdmin, nil},
		{"member", org.RoleMember, nil},
		{"empty", "", org.IsInvalidRoleError},
		{"unknown", "root", org.IsInvalidRoleError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := org.NewMembership(orgID, userID, tc.role)
			if tc.wantErrIs != nil {
				if !tc.wantErrIs(err) {
					t.Fatalf("want error type, got %T: %v", err, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected: %v", err)
			}
			if m.OrgID != orgID || m.UserID != userID {
				t.Errorf("ids mismatch")
			}
			if m.Role != tc.role {
				t.Errorf("role = %q want %q", m.Role, tc.role)
			}
			if m.CreatedAt.IsZero() {
				t.Error("expected non-zero CreatedAt")
			}
		})
	}
}

func TestErrors_ToAppError(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		wantCod string
	}{
		{"NotFound", &org.NotFoundError{ID: "x"}, apperror.CodeOrgNotFound},
		{"AlreadyExists", &org.AlreadyExistsError{Slug: "acme"}, apperror.CodeOrgAlreadyExists},
		{"InvalidSlug", &org.InvalidSlugError{Slug: "X"}, apperror.CodeOrgInvalidSlug},
		{"InvalidName", &org.InvalidNameError{}, apperror.CodeOrgInvalidName},
		{"InvalidRole", &org.InvalidRoleError{Role: "x"}, apperror.CodeValidation},
		{"MembershipExists", &org.MembershipExistsError{}, apperror.CodeOrgMembershipExists},
		{"MembershipMissing", &org.MembershipMissingError{}, apperror.CodeOrgMembershipMissing},
		{"CreationDisabled", &org.CreationDisabledError{}, apperror.CodeOrgCreationDisabled},
		{"SystemProtected", &org.SystemProtectedError{Op: "rename", Resource: "org"}, apperror.CodeOrgSystemProtected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ae, ok := apperror.AsAppError(tc.err)
			if !ok {
				t.Fatalf("AsAppError returned false")
			}
			if ae.Code() != tc.wantCod {
				t.Errorf("code = %q want %q", ae.Code(), tc.wantCod)
			}
			if tc.err.Error() == "" {
				t.Error("Error() empty")
			}
		})
	}
}

// TestValidateSlug_RejectsRouteShadowingSlugs guards the router: /orgs/new is a literal pattern, so an
// org slugged "new" would never reach its own page.
func TestValidateSlug_RejectsRouteShadowingSlugs(t *testing.T) {
	t.Parallel()
	if _, err := org.NewOrg("new", "New", uuid.New()); !org.IsInvalidSlugError(err) {
		t.Fatalf("slug %q must be refused, got %T: %v", "new", err, err)
	}
	if _, err := org.NewOrg("newsroom", "Newsroom", uuid.New()); err != nil {
		t.Fatalf("only the exact reserved word is refused, got %v", err)
	}
}
