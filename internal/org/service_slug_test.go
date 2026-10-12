package org_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/slug"
)

func TestNewOrg_AcceptsGeneratedSlugs(t *testing.T) {
	for range 500 {
		s := slug.Generate()
		_, err := org.NewOrg(s, "Acme", uuid.New())
		require.NoErrorf(t, err, "generated slug %q must satisfy the org slug invariants", s)
	}
}

func TestService_Create_GeneratesSlugWhenBlank(t *testing.T) {
	for _, supplied := range []string{"", "   "} {
		svc, _ := newTestService(t, true)
		o, err := svc.Create(context.Background(), org.CreateRequest{Slug: supplied, Name: "Acme", OwnerID: uuid.New()})
		require.NoError(t, err)
		assert.NotEmpty(t, o.Slug)
		_, err = org.NewOrg(o.Slug, o.Name, o.OwnerID)
		assert.NoError(t, err, "generated slug %q must be valid", o.Slug)
	}
}

func TestService_Create_RetriesPastTakenGeneratedSlugs(t *testing.T) {
	svc, store := newTestService(t, true)
	store.TakeNextSlugs(slug.MaxAttempts - 1)

	o, err := svc.Create(context.Background(), org.CreateRequest{Name: "Acme", OwnerID: uuid.New()})
	require.NoError(t, err)
	assert.NotEmpty(t, o.Slug)
}

func TestService_Create_GivesUpAfterMaxSlugAttempts(t *testing.T) {
	svc, store := newTestService(t, true)
	store.TakeNextSlugs(slug.MaxAttempts)

	_, err := svc.Create(context.Background(), org.CreateRequest{Name: "Acme", OwnerID: uuid.New()})
	assert.True(t, org.IsAlreadyExistsError(err), "want AlreadyExistsError, got %T: %v", err, err)
}

func TestService_Create_KeepsUserEditedSlug(t *testing.T) {
	svc, _ := newTestService(t, true)
	o, err := svc.Create(context.Background(), org.CreateRequest{Slug: "acme-hq", Name: "Acme", OwnerID: uuid.New()})
	require.NoError(t, err)
	assert.Equal(t, "acme-hq", o.Slug)
}

func TestService_Create_UserEditedSlugTakenIsNotRetried(t *testing.T) {
	svc, _ := newTestService(t, true)
	_, err := svc.Create(context.Background(), org.CreateRequest{Slug: "acme-hq", Name: "Acme", OwnerID: uuid.New()})
	require.NoError(t, err)

	_, err = svc.Create(context.Background(), org.CreateRequest{Slug: "acme-hq", Name: "Acme Two", OwnerID: uuid.New()})
	require.True(t, org.IsAlreadyExistsError(err), "want AlreadyExistsError, got %T: %v", err, err)
	assert.Contains(t, err.Error(), "acme-hq")
}

func TestService_BootstrapSingleton_GeneratesSlugWhenBlank(t *testing.T) {
	for _, supplied := range []string{"", "   "} {
		svc, store := newTestService(t, false)
		owner := uuid.New()
		o, err := svc.BootstrapSingleton(context.Background(), supplied, "Acme", owner)
		require.NoError(t, err)
		_, err = org.NewOrg(o.Slug, o.Name, o.OwnerID)
		require.NoError(t, err, "generated slug %q must be valid", o.Slug)
		assert.True(t, o.System)

		m, err := store.MembershipOf(context.Background(), o.ID, owner)
		require.NoError(t, err)
		assert.Equal(t, org.RoleOwner, m.Role)
		assert.True(t, m.System)
	}
}

func TestService_BootstrapSingleton_BlankRetriesPastTakenGeneratedSlugs(t *testing.T) {
	svc, store := newTestService(t, false)
	store.TakeNextSlugs(slug.MaxAttempts - 1)

	o, err := svc.BootstrapSingleton(context.Background(), "", "Acme", uuid.New())
	require.NoError(t, err)
	assert.NotEmpty(t, o.Slug)
}

func TestService_BootstrapSingleton_BlankGivesUpAfterMaxSlugAttempts(t *testing.T) {
	svc, store := newTestService(t, false)
	store.TakeNextSlugs(slug.MaxAttempts)

	_, err := svc.BootstrapSingleton(context.Background(), "", "Acme", uuid.New())
	assert.True(t, org.IsAlreadyExistsError(err), "want AlreadyExistsError, got %T: %v", err, err)
}

func TestService_BootstrapSingleton_BlankReusesTheSystemOrg(t *testing.T) {
	svc, _ := newTestService(t, false)
	owner := uuid.New()
	first, err := svc.BootstrapSingleton(context.Background(), "", "Acme", owner)
	require.NoError(t, err)

	again, err := svc.BootstrapSingleton(context.Background(), "", "Acme", owner)
	require.NoError(t, err)
	assert.Equal(t, first.ID, again.ID, "a blank retry must not mint a second singleton org")
	assert.Equal(t, first.Slug, again.Slug)
}

func TestService_BootstrapSingleton_KeepsTheChosenSlug(t *testing.T) {
	svc, _ := newTestService(t, false)
	o, err := svc.BootstrapSingleton(context.Background(), "acme-hq", "Acme", uuid.New())
	require.NoError(t, err)
	assert.Equal(t, "acme-hq", o.Slug)
}

func TestService_SystemOrg(t *testing.T) {
	svc, _ := newTestService(t, false)
	_, err := svc.SystemOrg(context.Background())
	require.True(t, org.IsNotFoundError(err), "want NotFoundError before bootstrap, got %T: %v", err, err)

	o, err := svc.BootstrapSingleton(context.Background(), "custom-slug", "Acme", uuid.New())
	require.NoError(t, err)

	got, err := svc.SystemOrg(context.Background())
	require.NoError(t, err)
	assert.Equal(t, o.ID, got.ID, "the singleton is found by its system flag, whatever its slug")
}

func TestService_BootstrapSingleton_SecondSlugReusesSystemOrg(t *testing.T) {
	svc, store := newTestService(t, false)
	first, second := uuid.New(), uuid.New()
	o, err := svc.BootstrapSingleton(context.Background(), "brave-cove-1234", "Acme", first)
	require.NoError(t, err)

	again, err := svc.BootstrapSingleton(context.Background(), "misty-reef-5678", "Other", second)
	require.NoError(t, err)
	assert.Equal(t, o.ID, again.ID, "a second onboarding must join the one system org, whatever slug it posts")
	assert.Equal(t, "brave-cove-1234", again.Slug)

	_, err = store.BySlug(context.Background(), "misty-reef-5678")
	assert.True(t, org.IsNotFoundError(err), "no second org may be created")
	m, err := store.MembershipOf(context.Background(), o.ID, second)
	require.NoError(t, err, "the second admin must be able to log in to the system org")
	assert.Equal(t, org.RoleOwner, m.Role)
}

type staleSystemOrgStore struct {
	org.Store
	stale int
}

func (s *staleSystemOrgStore) SystemOrg(ctx context.Context) (*org.Org, error) {
	if s.stale > 0 {
		s.stale--
		return nil, &org.NotFoundError{System: true}
	}
	return s.Store.SystemOrg(ctx)
}

func TestService_BootstrapSingleton_RaceLoserAdoptsTheWinner(t *testing.T) {
	for _, slug := range []string{"", "loser-slug"} {
		t.Run("slug="+slug, func(t *testing.T) {
			svc, store := newTestService(t, false)
			winner, err := svc.BootstrapSingleton(context.Background(), "winner-slug", "Acme", uuid.New())
			require.NoError(t, err)

			stale := &staleSystemOrgStore{Store: store, stale: 1}
			loserID := uuid.New()
			got, err := newServiceWithStore(t, stale).BootstrapSingleton(context.Background(), slug, "Other", loserID)
			require.NoError(t, err, "the unique index refusal must resolve to the winner, not an error")
			assert.Equal(t, winner.ID, got.ID)
			_, err = store.MembershipOf(context.Background(), winner.ID, loserID)
			require.NoError(t, err)
		})
	}
}

func TestService_BootstrapSingleton_UnreadableSlugIsTypedNotUnexpected(t *testing.T) {
	_, store := newTestService(t, false)
	require.NoError(t, store.Save(context.Background(), &org.Org{ID: uuid.New(), Slug: "hidden-slug", Name: "Hidden", OwnerID: uuid.New()}))
	hidden := &hiddenSlugStore{Store: store}

	_, err := newServiceWithStore(t, hidden).BootstrapSingleton(context.Background(), "hidden-slug", "Acme", uuid.New())
	require.True(t, org.IsUnreadableExistingOrgError(err), "want UnreadableExistingOrgError, got %T: %v", err, err)
	var appErr *apperror.AppError
	assert.False(t, errors.As(err, &appErr), "a user-fixable slug clash must not go through unexpected")
}

type hiddenSlugStore struct{ org.Store }

func (s *hiddenSlugStore) BySlug(_ context.Context, slug string) (*org.Org, error) {
	return nil, &org.NotFoundError{Slug: slug}
}

type raceSameSlugStore struct {
	*hiddenSlugStore
	stale int
}

func (s *raceSameSlugStore) SystemOrg(ctx context.Context) (*org.Org, error) {
	if s.stale > 0 {
		s.stale--
		return nil, &org.NotFoundError{System: true}
	}
	return s.Store.SystemOrg(ctx)
}

func TestService_BootstrapSingleton_SameSlugRaceLoserAdoptsTheWinner(t *testing.T) {
	svc, store := newTestService(t, false)
	winner, err := svc.BootstrapSingleton(context.Background(), "acme", "Acme", uuid.New())
	require.NoError(t, err)

	racing := &raceSameSlugStore{hiddenSlugStore: &hiddenSlugStore{Store: store}, stale: 1}
	loserID := uuid.New()
	got, err := newServiceWithStore(t, racing).BootstrapSingleton(context.Background(), "acme", "Acme", loserID)
	require.NoError(t, err, "a same-slug race must adopt the winner, not report the slug as taken")
	assert.Equal(t, winner.ID, got.ID)
	_, err = store.MembershipOf(context.Background(), winner.ID, loserID)
	require.NoError(t, err, "the loser's admin must not be orphaned")
}
