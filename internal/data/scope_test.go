package data

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/sheet"
)

func newResolverRig() (resolver, *fakeOrgs, *fakeProjects, *fakeSheets) {
	orgs := &fakeOrgs{ref: OrgRef{ID: uuid.Must(uuid.NewV7())}}
	projects := &fakeProjects{ref: ProjectRef{ID: uuid.Must(uuid.NewV7())}}
	sheets := &fakeSheets{ref: SheetRef{ID: uuid.Must(uuid.NewV7()), Visibility: "key", CacheTTL: time.Minute}}
	return resolver{orgs: orgs, projects: projects, sheets: sheets}, orgs, projects, sheets
}

// SECURITY: boot stringifies sheet.Visibility into SheetRef, so a drift here would silently key-gate a public sheet.
func TestVisibilityPublicMatchesTheSheetDomain(t *testing.T) {
	if got, want := VisibilityPublic, string(sheet.VisibilityPublic); got != want {
		t.Errorf("data.VisibilityPublic = %q, want sheet.VisibilityPublic %q", got, want)
	}
}

func TestResolver_ResolvesOrgThenProjectThenSheet(t *testing.T) {
	rs, orgs, projects, sheets := newResolverRig()

	ctx, sc, err := rs.resolve(t.Context(), "acme", "default", "prices")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if sc.orgID != orgs.ref.ID || sc.projectID != projects.ref.ID || sc.sheet.ID != sheets.ref.ID {
		t.Errorf("scope = %+v, want the three resolved ids", sc)
	}
	tc, err := tenant.From(ctx)
	if err != nil {
		t.Fatalf("the returned context must carry a usable tenant scope: %v", err)
	}
	if tc.OrgID != orgs.ref.ID || tc.ProjectID != projects.ref.ID {
		t.Errorf("tenant scope = %+v, want org %s project %s", tc, orgs.ref.ID, projects.ref.ID)
	}
}

func TestResolver_ProjectAndSheetLookupsAreTenanted(t *testing.T) {
	rs, orgs, projects, sheets := newResolverRig()

	if _, _, err := rs.resolve(t.Context(), "acme", "default", "prices"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(projects.ctxs) != 1 || len(sheets.ctxs) != 1 {
		t.Fatalf("lookups = %d project, %d sheet, want 1 each", len(projects.ctxs), len(sheets.ctxs))
	}
	pt, err := tenant.From(projects.ctxs[0])
	if err != nil {
		t.Fatalf("project.BySlug ran unscoped: %v", err)
	}
	if pt.OrgID != orgs.ref.ID {
		t.Errorf("project lookup scope org = %s, want %s", pt.OrgID, orgs.ref.ID)
	}
	st, err := tenant.From(sheets.ctxs[0])
	if err != nil {
		t.Fatalf("sheet.BySlug ran unscoped: %v", err)
	}
	if st.OrgID != orgs.ref.ID || st.ProjectID != projects.ref.ID {
		t.Errorf("sheet lookup scope = %+v, want org %s project %s", st, orgs.ref.ID, projects.ref.ID)
	}
}

// The org lookup is the read that establishes the scope, so the resolver must not add one of its own.
func TestResolver_DoesNotScopeTheOrgLookup(t *testing.T) {
	rs, orgs, _, _ := newResolverRig()

	if _, _, err := rs.resolve(t.Context(), "acme", "default", "prices"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(orgs.ctxs) != 1 {
		t.Fatalf("org lookups = %d, want 1", len(orgs.ctxs))
	}
	if _, err := tenant.From(orgs.ctxs[0]); err == nil {
		t.Error("the resolver must not scope the org lookup — that lookup is what establishes the scope")
	}
}

func TestResolver_StopsAtTheFirstFailure(t *testing.T) {
	rs, orgs, projects, sheets := newResolverRig()
	orgs.err = apperror.New(apperror.CodeOrgNotFound, "Organization not found", codes.NotFound)

	if _, _, err := rs.resolve(t.Context(), "acme", "default", "prices"); !IsNotFoundError(err) {
		t.Fatalf("err = %v, want a masked *NotFoundError", err)
	}
	if len(projects.ctxs) != 0 || len(sheets.ctxs) != 0 {
		t.Errorf("lookups continued after the org miss: %d project, %d sheet", len(projects.ctxs), len(sheets.ctxs))
	}
}

func TestResolver_MasksEveryStageIdentically(t *testing.T) {
	stages := []struct {
		name    string
		arrange func(*fakeOrgs, *fakeProjects, *fakeSheets)
	}{
		{"org", func(o *fakeOrgs, _ *fakeProjects, _ *fakeSheets) {
			o.err = apperror.New(apperror.CodeOrgNotFound, "Organization not found", codes.NotFound)
		}},
		{"project", func(_ *fakeOrgs, p *fakeProjects, _ *fakeSheets) {
			p.err = apperror.New(apperror.CodeProjectNotFound, "Project not found", codes.NotFound)
		}},
		{"sheet", func(_ *fakeOrgs, _ *fakeProjects, s *fakeSheets) {
			s.err = apperror.New(apperror.CodeSheetNotFound, "Sheet not found", codes.NotFound)
		}},
	}
	for _, stage := range stages {
		t.Run(stage.name, func(t *testing.T) {
			rs, orgs, projects, sheets := newResolverRig()
			stage.arrange(orgs, projects, sheets)
			if _, _, err := rs.resolve(t.Context(), "acme", "default", "prices"); !IsNotFoundError(err) {
				t.Errorf("err = %v, want *NotFoundError", err)
			}
		})
	}
}

func TestResolver_PassesAnUnexpectedFailureThrough(t *testing.T) {
	rs, _, projects, _ := newResolverRig()
	cause := errors.New("dial tcp: connection refused")
	projects.err = cause

	if _, _, err := rs.resolve(t.Context(), "acme", "default", "prices"); !errors.Is(err, cause) {
		t.Errorf("err = %v, want the unexpected cause to survive masking", err)
	}
}
