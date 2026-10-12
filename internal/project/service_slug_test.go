package project_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/project"
	"altalune.id/opensheet/internal/testutil/fakes"
	"altalune.id/opensheet/slug"
)

func TestNew_AcceptsGeneratedSlugs(t *testing.T) {
	orgID := uuid.New()
	for range 500 {
		s := slug.Generate()
		if _, err := project.New(orgID, s, "Web"); err != nil {
			t.Fatalf("generated slug %q must satisfy the project slug invariants: %v", s, err)
		}
	}
}

func TestService_Create_GeneratesSlugWhenBlank(t *testing.T) {
	orgID, userID := uuid.New(), uuid.New()
	for _, supplied := range []string{"", "   "} {
		svc, _ := newTestService(t)
		p, err := svc.Create(tenantCtx(orgID, userID), orgID, supplied, "Web")
		if err != nil {
			t.Fatalf("Create(%q): %v", supplied, err)
		}
		if p.Slug == "" {
			t.Fatal("Create left the slug empty")
		}
		if _, err := project.New(orgID, p.Slug, p.Name); err != nil {
			t.Fatalf("generated slug %q is not valid: %v", p.Slug, err)
		}
	}
}

func TestService_Create_RetriesPastTakenGeneratedSlugs(t *testing.T) {
	orgID, userID := uuid.New(), uuid.New()
	svc, store := newTestService(t)
	store.TakeNextSlugs(slug.MaxAttempts - 1)

	p, err := svc.Create(tenantCtx(orgID, userID), orgID, "", "Web")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if p.Slug == "" {
		t.Fatal("Create left the slug empty")
	}
}

func TestService_Create_GivesUpAfterMaxSlugAttempts(t *testing.T) {
	orgID, userID := uuid.New(), uuid.New()
	svc, store := newTestService(t)
	store.TakeNextSlugs(slug.MaxAttempts)

	_, err := svc.Create(tenantCtx(orgID, userID), orgID, "", "Web")
	if !project.IsAlreadyExistsError(err) {
		t.Fatalf("want AlreadyExistsError, got %T: %v", err, err)
	}
}

func TestService_Create_KeepsUserEditedSlug(t *testing.T) {
	orgID, userID := uuid.New(), uuid.New()
	svc, _ := newTestService(t)

	p, err := svc.Create(tenantCtx(orgID, userID), orgID, "web-app", "Web")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if p.Slug != "web-app" {
		t.Fatalf("Slug = %q, want %q", p.Slug, "web-app")
	}
}

func TestService_Create_UserEditedSlugTakenIsNotRetried(t *testing.T) {
	orgID, userID := uuid.New(), uuid.New()
	svc, _ := newTestService(t)
	ctx := tenantCtx(orgID, userID)

	if _, err := svc.Create(ctx, orgID, "web-app", "Web"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err := svc.Create(ctx, orgID, "web-app", "Web Two")
	if !project.IsAlreadyExistsError(err) {
		t.Fatalf("want AlreadyExistsError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "web-app") {
		t.Fatalf("error must name the taken slug: %v", err)
	}
}

func TestService_BootstrapSystem_GeneratesSlugWhenBlank(t *testing.T) {
	orgID, userID := uuid.New(), uuid.New()
	for _, supplied := range []string{"", "   "} {
		svc, _ := newTestService(t)
		p, err := svc.BootstrapSystem(tenantCtx(orgID, userID), orgID, supplied, "Web")
		if err != nil {
			t.Fatalf("BootstrapSystem(%q): %v", supplied, err)
		}
		if _, err := project.New(orgID, p.Slug, p.Name); err != nil {
			t.Fatalf("generated slug %q is not valid: %v", p.Slug, err)
		}
		if !p.System {
			t.Fatal("the bootstrapped project must be a system project")
		}
	}
}

func TestService_BootstrapSystem_BlankRetriesPastTakenGeneratedSlugs(t *testing.T) {
	orgID, userID := uuid.New(), uuid.New()
	svc, store := newTestService(t)
	store.TakeNextSlugs(slug.MaxAttempts - 1)

	p, err := svc.BootstrapSystem(tenantCtx(orgID, userID), orgID, "", "Web")
	if err != nil {
		t.Fatalf("BootstrapSystem: %v", err)
	}
	if p.Slug == "" {
		t.Fatal("BootstrapSystem left the slug empty")
	}
}

func TestService_BootstrapSystem_BlankGivesUpAfterMaxSlugAttempts(t *testing.T) {
	orgID, userID := uuid.New(), uuid.New()
	svc, store := newTestService(t)
	store.TakeNextSlugs(slug.MaxAttempts)

	_, err := svc.BootstrapSystem(tenantCtx(orgID, userID), orgID, "", "Web")
	if !project.IsAlreadyExistsError(err) {
		t.Fatalf("want AlreadyExistsError, got %T: %v", err, err)
	}
}

func TestService_BootstrapSystem_BlankReusesTheSystemProject(t *testing.T) {
	orgID, userID := uuid.New(), uuid.New()
	svc, _ := newTestService(t)
	ctx := tenantCtx(orgID, userID)

	if _, err := svc.Create(ctx, orgID, "plain", "Plain"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	first, err := svc.BootstrapSystem(ctx, orgID, "", "Web")
	if err != nil {
		t.Fatalf("first BootstrapSystem: %v", err)
	}
	again, err := svc.BootstrapSystem(ctx, orgID, "", "Web")
	if err != nil {
		t.Fatalf("second BootstrapSystem: %v", err)
	}
	if again.ID != first.ID {
		t.Fatalf("a blank retry must reuse the system project %s, got %s", first.ID, again.ID)
	}
}

func TestService_BootstrapSystem_KeepsTheChosenSlug(t *testing.T) {
	orgID, userID := uuid.New(), uuid.New()
	svc, _ := newTestService(t)

	p, err := svc.BootstrapSystem(tenantCtx(orgID, userID), orgID, "web-app", "Web")
	if err != nil {
		t.Fatalf("BootstrapSystem: %v", err)
	}
	if p.Slug != "web-app" {
		t.Fatalf("Slug = %q, want %q", p.Slug, "web-app")
	}
}

func TestService_BootstrapSystem_SecondSlugReusesTheSystemProject(t *testing.T) {
	orgID, userID := uuid.New(), uuid.New()
	svc, store := newTestService(t)
	ctx := tenantCtx(orgID, userID)

	first, err := svc.BootstrapSystem(ctx, orgID, "web-app", "Web")
	if err != nil {
		t.Fatalf("first BootstrapSystem: %v", err)
	}
	again, err := svc.BootstrapSystem(ctx, orgID, "api-app", "API")
	if err != nil {
		t.Fatalf("second BootstrapSystem: %v", err)
	}
	if again.ID != first.ID {
		t.Fatalf("a second bootstrap must reuse the system project %s, got %s", first.ID, again.ID)
	}
	if _, err := store.BySlug(ctx, orgID, "api-app"); !project.IsNotFoundError(err) {
		t.Fatalf("no second project may be created, got %v", err)
	}
}

type staleListStore struct {
	*fakes.Project
	stale int
}

func (s *staleListStore) List(ctx context.Context, orgID uuid.UUID) ([]*project.Project, error) {
	if s.stale > 0 {
		s.stale--
		return nil, nil
	}
	return s.Project.List(ctx, orgID)
}

func TestService_BootstrapSystem_RaceLoserReturnsTheWinner(t *testing.T) {
	for _, slug := range []string{"", "loser-app"} {
		t.Run("slug="+slug, func(t *testing.T) {
			orgID, userID := uuid.New(), uuid.New()
			svc, store := newTestService(t)
			ctx := tenantCtx(orgID, userID)
			winner, err := svc.BootstrapSystem(ctx, orgID, "winner-app", "Web")
			if err != nil {
				t.Fatalf("winner: %v", err)
			}
			loser := project.NewService(&staleListStore{Project: store, stale: 1}, discardLog(), failOnUnexpected(t))
			got, err := loser.BootstrapSystem(ctx, orgID, slug, "Other")
			if err != nil {
				t.Fatalf("the unique index refusal must resolve to the winner: %v", err)
			}
			if got.ID != winner.ID {
				t.Fatalf("got %s, want the winner %s", got.ID, winner.ID)
			}
		})
	}
}

type racingProjectStore struct {
	*fakes.Project
	staleList int
}

func (s *racingProjectStore) List(ctx context.Context, orgID uuid.UUID) ([]*project.Project, error) {
	if s.staleList > 0 {
		s.staleList--
		return nil, nil
	}
	return s.Project.List(ctx, orgID)
}

func (s *racingProjectStore) BySlug(_ context.Context, _ uuid.UUID, slug string) (*project.Project, error) {
	return nil, &project.NotFoundError{Slug: slug}
}

func TestService_BootstrapSystem_SameSlugRaceLoserReturnsTheWinner(t *testing.T) {
	orgID, userID := uuid.New(), uuid.New()
	svc, store := newTestService(t)
	ctx := tenantCtx(orgID, userID)
	winner, err := svc.BootstrapSystem(ctx, orgID, "web-app", "Web")
	if err != nil {
		t.Fatalf("winner: %v", err)
	}
	loser := project.NewService(&racingProjectStore{Project: store, staleList: 1}, discardLog(), failOnUnexpected(t))
	got, err := loser.BootstrapSystem(ctx, orgID, "web-app", "Web")
	if err != nil {
		t.Fatalf("a same-slug race must return the winner: %v", err)
	}
	if got.ID != winner.ID {
		t.Fatalf("got %s, want the winner %s", got.ID, winner.ID)
	}
}

func TestService_BootstrapSystem_SlugTakenByAPlainProjectIsAlreadyExists(t *testing.T) {
	orgID, userID := uuid.New(), uuid.New()
	svc, store := newTestService(t)
	ctx := tenantCtx(orgID, userID)
	if _, err := svc.Create(ctx, orgID, "web-app", "Web"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	racing := project.NewService(&racingProjectStore{Project: store}, discardLog(), failOnUnexpected(t))
	_, err := racing.BootstrapSystem(ctx, orgID, "web-app", "Web")
	if !project.IsAlreadyExistsError(err) {
		t.Fatalf("want AlreadyExistsError, not an unexpected error, got %T: %v", err, err)
	}
}
