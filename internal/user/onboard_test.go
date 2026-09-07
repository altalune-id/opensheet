package user_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"altalune.id/template/internal/testutil/fakes"
	"altalune.id/template/internal/user"
)

type fakeOrgs struct {
	mu          sync.Mutex
	bySlug      map[string]*user.OrgRef
	byID        map[uuid.UUID]*user.OrgRef
	memberships map[[2]uuid.UUID]*user.MembershipRef
	userToOrgs  map[uuid.UUID][]uuid.UUID
}

func newFakeOrgs() *fakeOrgs {
	return &fakeOrgs{
		bySlug:      map[string]*user.OrgRef{},
		byID:        map[uuid.UUID]*user.OrgRef{},
		memberships: map[[2]uuid.UUID]*user.MembershipRef{},
		userToOrgs:  map[uuid.UUID][]uuid.UUID{},
	}
}

func (f *fakeOrgs) BySlug(_ context.Context, slug string) (*user.OrgRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if o, ok := f.bySlug[slug]; ok {
		cp := *o
		return &cp, nil
	}
	return nil, &user.NotFoundError{}
}

func (f *fakeOrgs) Save(_ context.Context, o *user.OrgRef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *o
	f.bySlug[o.Slug] = &cp
	f.byID[o.ID] = &cp
	return nil
}

func (f *fakeOrgs) ListForUser(_ context.Context, userID uuid.UUID) ([]*user.OrgRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ids := f.userToOrgs[userID]
	out := make([]*user.OrgRef, 0, len(ids))
	for _, id := range ids {
		if o, ok := f.byID[id]; ok {
			cp := *o
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (f *fakeOrgs) MembershipOf(_ context.Context, orgID, userID uuid.UUID) (*user.MembershipRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m, ok := f.memberships[[2]uuid.UUID{orgID, userID}]; ok {
		cp := *m
		return &cp, nil
	}
	return nil, &user.NotFoundError{}
}

func (f *fakeOrgs) SaveMembership(_ context.Context, m *user.MembershipRef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *m
	f.memberships[[2]uuid.UUID{m.OrgID, m.UserID}] = &cp
	f.userToOrgs[m.UserID] = append(f.userToOrgs[m.UserID], m.OrgID)
	return nil
}

type fakeProjects struct {
	mu     sync.Mutex
	byOrg  map[uuid.UUID][]*user.ProjectRef
	bySlug map[string]*user.ProjectRef
}

func newFakeProjects() *fakeProjects {
	return &fakeProjects{
		byOrg:  map[uuid.UUID][]*user.ProjectRef{},
		bySlug: map[string]*user.ProjectRef{},
	}
}

func (f *fakeProjects) BySlug(_ context.Context, orgID uuid.UUID, slug string) (*user.ProjectRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.bySlug[orgID.String()+"/"+slug]; ok {
		cp := *p
		return &cp, nil
	}
	return nil, &user.NotFoundError{}
}

func (f *fakeProjects) Save(_ context.Context, p *user.ProjectRef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *p
	f.byOrg[p.OrgID] = append(f.byOrg[p.OrgID], &cp)
	f.bySlug[p.OrgID.String()+"/"+p.Slug] = &cp
	return nil
}

func (f *fakeProjects) ListByOrg(_ context.Context, orgID uuid.UUID) ([]*user.ProjectRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*user.ProjectRef, 0, len(f.byOrg[orgID]))
	for _, p := range f.byOrg[orgID] {
		cp := *p
		out = append(out, &cp)
	}
	return out, nil
}

type fakeInvites struct {
	mu    sync.Mutex
	byOrg map[uuid.UUID][]*user.InviteRef
	byID  map[uuid.UUID]*user.InviteRef
}

func newFakeInvites() *fakeInvites {
	return &fakeInvites{
		byOrg: map[uuid.UUID][]*user.InviteRef{},
		byID:  map[uuid.UUID]*user.InviteRef{},
	}
}

func (f *fakeInvites) ListByOrg(_ context.Context, orgID uuid.UUID) ([]*user.InviteRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*user.InviteRef, 0, len(f.byOrg[orgID]))
	for _, inv := range f.byOrg[orgID] {
		if stored, ok := f.byID[inv.ID]; ok {
			cp := *stored
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (f *fakeInvites) ListPendingForEmail(_ context.Context, email string) ([]*user.InviteRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*user.InviteRef, 0)
	for _, inv := range f.byID {
		if inv.AcceptedAt != nil {
			continue
		}
		if !strings.EqualFold(inv.Email, email) {
			continue
		}
		cp := *inv
		out = append(out, &cp)
	}
	return out, nil
}

func (f *fakeInvites) Save(_ context.Context, inv *user.InviteRef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.byID[inv.ID]; !ok {
		f.byOrg[inv.OrgID] = append(f.byOrg[inv.OrgID], inv)
	}
	cp := *inv
	f.byID[inv.ID] = &cp
	return nil
}

func TestOnboard_Selfhosted_AcceptsInvite(t *testing.T) {
	t.Parallel()
	orgs := newFakeOrgs()
	projects := newFakeProjects()
	invites := newFakeInvites()
	users := fakes.NewUser()

	singleton := &user.OrgRef{ID: uuid.New(), Slug: "primary", Name: "Primary", OwnerID: uuid.New(), CreatedAt: time.Now().UTC()}
	if err := orgs.Save(context.Background(), singleton); err != nil {
		t.Fatal(err)
	}

	inviteID := uuid.New()
	if err := invites.Save(context.Background(), &user.InviteRef{
		ID: inviteID, OrgID: singleton.ID, Email: "alice@example.com", Role: "member",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	policy := user.Policy{Mode: user.PolicyModeSelfhosted, SingletonOrgSlug: "primary"}
	wf := user.NewOnboardWorkflow(users, orgs, projects, invites, policy, newTestLogger(), noopUnexpected())

	userID := uuid.New()
	res, err := wf.Onboard(context.Background(), userID, "alice@example.com")
	if err != nil {
		t.Fatalf("Onboard: %v", err)
	}
	if res.OrgID != singleton.ID {
		t.Errorf("orgID=%v want %v", res.OrgID, singleton.ID)
	}
	m, err := orgs.MembershipOf(context.Background(), singleton.ID, userID)
	if err != nil {
		t.Fatalf("expected membership, got err %v", err)
	}
	if m.Role != "member" {
		t.Errorf("role=%q want member", m.Role)
	}
	stored := invites.byID[inviteID]
	if stored.AcceptedAt == nil {
		t.Error("invite must be marked accepted")
	}
}

func TestOnboard_Selfhosted_NotInvited(t *testing.T) {
	t.Parallel()
	orgs := newFakeOrgs()
	singleton := &user.OrgRef{ID: uuid.New(), Slug: "primary", Name: "Primary", OwnerID: uuid.New(), CreatedAt: time.Now().UTC()}
	if err := orgs.Save(context.Background(), singleton); err != nil {
		t.Fatal(err)
	}

	policy := user.Policy{Mode: user.PolicyModeSelfhosted, SingletonOrgSlug: "primary"}
	wf := user.NewOnboardWorkflow(fakes.NewUser(), orgs, newFakeProjects(), newFakeInvites(), policy, newTestLogger(), noopUnexpected())

	_, err := wf.Onboard(context.Background(), uuid.New(), "alice@example.com")
	if err == nil {
		t.Fatal("expected NotInvitedError")
	}
	if !user.IsNotInvitedError(err) {
		t.Errorf("want IsNotInvitedError, got %T: %v", err, err)
	}
}

func TestOnboard_Cloud_NoMembershipNoInviteReturnsSignupRequired(t *testing.T) {
	t.Parallel()
	orgs := newFakeOrgs()
	projects := newFakeProjects()
	invites := newFakeInvites()
	users := fakes.NewUser()

	policy := user.Policy{Mode: user.PolicyModeCloud}
	wf := user.NewOnboardWorkflow(users, orgs, projects, invites, policy, newTestLogger(), noopUnexpected())

	_, err := wf.Onboard(context.Background(), uuid.New(), "alice@example.com")
	if err == nil {
		t.Fatal("expected SignupRequiredError")
	}
	if !user.IsSignupRequiredError(err) {
		t.Errorf("want IsSignupRequiredError, got %T: %v", err, err)
	}
	if len(orgs.byID) != 0 {
		t.Errorf("no org must be created; got %d", len(orgs.byID))
	}
}

func TestOnboard_Cloud_AcceptsInvite(t *testing.T) {
	t.Parallel()
	orgs := newFakeOrgs()
	projects := newFakeProjects()
	invites := newFakeInvites()
	users := fakes.NewUser()

	orgA := &user.OrgRef{ID: uuid.New(), Slug: "acme", Name: "Acme", OwnerID: uuid.New(), CreatedAt: time.Now().UTC()}
	if err := orgs.Save(context.Background(), orgA); err != nil {
		t.Fatal(err)
	}
	inviteID := uuid.New()
	if err := invites.Save(context.Background(), &user.InviteRef{
		ID: inviteID, OrgID: orgA.ID, Email: "alice@example.com", Role: "admin",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	policy := user.Policy{Mode: user.PolicyModeCloud}
	wf := user.NewOnboardWorkflow(users, orgs, projects, invites, policy, newTestLogger(), noopUnexpected())

	userID := uuid.New()
	res, err := wf.Onboard(context.Background(), userID, "alice@example.com")
	if err != nil {
		t.Fatalf("Onboard: %v", err)
	}
	if res.OrgID != orgA.ID {
		t.Errorf("orgID=%v want %v", res.OrgID, orgA.ID)
	}
	m, err := orgs.MembershipOf(context.Background(), orgA.ID, userID)
	if err != nil {
		t.Fatalf("expected membership, got err %v", err)
	}
	if m.Role != "admin" {
		t.Errorf("role=%q want admin", m.Role)
	}
	if invites.byID[inviteID].AcceptedAt == nil {
		t.Error("invite must be marked accepted")
	}
}

func TestOnboard_UnknownMode(t *testing.T) {
	t.Parallel()
	wf := user.NewOnboardWorkflow(
		fakes.NewUser(),
		newFakeOrgs(),
		newFakeProjects(),
		newFakeInvites(),
		user.Policy{Mode: "invalid"},
		newTestLogger(),
		noopUnexpected(),
	)
	_, err := wf.Onboard(context.Background(), uuid.New(), "a@b.co")
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}
}
