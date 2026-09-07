package invite_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"altalune.id/template/internal/invite"
	"altalune.id/template/internal/testutil/fakes"
)

type fakeUsers struct {
	mu    sync.Mutex
	byID  map[uuid.UUID]*invite.UserRef
	byEml map[string]uuid.UUID
	// SaveErr, ByEmailErr — inject failures.
	SaveErr    error
	ByEmailErr error
}

func newFakeUsers() *fakeUsers {
	return &fakeUsers{byID: map[uuid.UUID]*invite.UserRef{}, byEml: map[string]uuid.UUID{}}
}

func (f *fakeUsers) ByEmail(_ context.Context, email string) (*invite.UserRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ByEmailErr != nil {
		return nil, f.ByEmailErr
	}
	id, ok := f.byEml[email]
	if !ok {
		return nil, &notFoundErr{msg: "user: not found: email=" + email}
	}
	cp := *f.byID[id]
	return &cp, nil
}

func (f *fakeUsers) Save(_ context.Context, u *invite.UserRef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.SaveErr != nil {
		return f.SaveErr
	}
	cp := *u
	f.byID[u.ID] = &cp
	f.byEml[u.Email] = u.ID
	return nil
}

type fakeOrgs struct {
	mu   sync.Mutex
	byID map[uuid.UUID]*invite.OrgRef
	mems map[[2]uuid.UUID]*invite.MembershipRef
	// ByIDErr, MembershipErr, SaveMembershipErr — inject failures.
	ByIDErr           error
	SaveMembershipErr error
}

func newFakeOrgs() *fakeOrgs {
	return &fakeOrgs{
		byID: map[uuid.UUID]*invite.OrgRef{},
		mems: map[[2]uuid.UUID]*invite.MembershipRef{},
	}
}

func (f *fakeOrgs) ByID(_ context.Context, id uuid.UUID) (*invite.OrgRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ByIDErr != nil {
		return nil, f.ByIDErr
	}
	if o, ok := f.byID[id]; ok {
		cp := *o
		return &cp, nil
	}
	return nil, &notFoundErr{msg: "org: not found"}
}

func (f *fakeOrgs) SaveMembership(_ context.Context, m *invite.MembershipRef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.SaveMembershipErr != nil {
		return f.SaveMembershipErr
	}
	cp := *m
	f.mems[[2]uuid.UUID{m.OrgID, m.UserID}] = &cp
	return nil
}

func (f *fakeOrgs) MembershipOf(_ context.Context, orgID, userID uuid.UUID) (*invite.MembershipRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m, ok := f.mems[[2]uuid.UUID{orgID, userID}]; ok {
		cp := *m
		return &cp, nil
	}
	return nil, &notFoundErr{msg: "membership: not found"}
}

type notFoundErr struct{ msg string }

func (e *notFoundErr) Error() string { return e.msg }

func newAcceptWorkflow(t *testing.T, invites invite.Store, users *fakeUsers, orgs *fakeOrgs, clock func() time.Time) *invite.AcceptWorkflow {
	t.Helper()
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	unex := 0
	return invite.NewAcceptWorkflow(invites, users, orgs, newTestLogger(), noopUnexpected(&unex), invite.WithAcceptClock(clock))
}

func seedAcceptable(t *testing.T, store invite.Store, orgs *fakeOrgs, orgID uuid.UUID, email string, ttl time.Duration) (*invite.Invite, string) {
	t.Helper()
	raw := "raw-" + uuid.NewString()
	inv, err := invite.New(invite.NewParams{
		OrgID: orgID,
		Email: email,
		Role:  invite.RoleMember,
		TTL:   ttl,
		Token: raw,
	})
	if err != nil {
		t.Fatalf("seed New: %v", err)
	}
	ctx := seedCtx(orgID)
	if err := store.Save(ctx, inv); err != nil {
		t.Fatalf("seed Save: %v", err)
	}
	orgs.byID[orgID] = &invite.OrgRef{ID: orgID, Slug: "acme", Name: "Acme"}
	return inv, raw
}

func seedCtx(orgID uuid.UUID) context.Context {
	return context.Background()
}

func TestAccept_HappyPath_NewUser(t *testing.T) {
	t.Parallel()
	store := fakes.NewInvite()
	users := newFakeUsers()
	orgs := newFakeOrgs()

	orgID := uuid.New()
	_, raw := seedAcceptable(t, store, orgs, orgID, "alice@example.com", time.Hour)

	wf := newAcceptWorkflow(t, store, users, orgs, nil)
	res, err := wf.Execute(context.Background(), invite.AcceptRequest{Token: raw, Email: "alice@example.com", Name: "Alice"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.User == nil || res.User.Email != "alice@example.com" {
		t.Errorf("user not created: %+v", res.User)
	}
	if res.Membership == nil || res.Membership.OrgID != orgID {
		t.Errorf("membership missing: %+v", res.Membership)
	}
	if res.Invite == nil || res.Invite.UsedAt == nil {
		t.Errorf("invite must be marked used: %+v", res.Invite)
	}
}

func TestAccept_HappyPath_ExistingUser(t *testing.T) {
	t.Parallel()
	store := fakes.NewInvite()
	users := newFakeUsers()
	orgs := newFakeOrgs()
	orgID := uuid.New()

	existingID := uuid.New()
	if err := users.Save(context.Background(), &invite.UserRef{ID: existingID, Email: "alice@example.com", Name: "Alice", Source: "oidc"}); err != nil {
		t.Fatal(err)
	}
	_, raw := seedAcceptable(t, store, orgs, orgID, "alice@example.com", time.Hour)

	wf := newAcceptWorkflow(t, store, users, orgs, nil)
	res, err := wf.Execute(context.Background(), invite.AcceptRequest{Token: raw, Email: "alice@example.com"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.User.ID != existingID {
		t.Errorf("reused existing user ID: got %v want %v", res.User.ID, existingID)
	}
}

func TestAccept_TokenMismatch(t *testing.T) {
	t.Parallel()
	store := fakes.NewInvite()
	orgs := newFakeOrgs()
	users := newFakeUsers()
	orgID := uuid.New()
	seedAcceptable(t, store, orgs, orgID, "alice@example.com", time.Hour)

	wf := newAcceptWorkflow(t, store, users, orgs, nil)
	_, err := wf.Execute(context.Background(), invite.AcceptRequest{Token: "wrong-token", Email: "alice@example.com"})
	if !invite.IsTokenMismatchError(err) {
		t.Errorf("want IsTokenMismatchError, got %T: %v", err, err)
	}
}

func TestAccept_EmailMismatch(t *testing.T) {
	t.Parallel()
	store := fakes.NewInvite()
	orgs := newFakeOrgs()
	users := newFakeUsers()
	orgID := uuid.New()
	_, raw := seedAcceptable(t, store, orgs, orgID, "alice@example.com", time.Hour)

	wf := newAcceptWorkflow(t, store, users, orgs, nil)
	_, err := wf.Execute(context.Background(), invite.AcceptRequest{Token: raw, Email: "eve@example.com"})
	if !invite.IsTokenMismatchError(err) {
		t.Errorf("want IsTokenMismatchError, got %T: %v", err, err)
	}
}

func TestAccept_Expired(t *testing.T) {
	t.Parallel()
	store := fakes.NewInvite()
	orgs := newFakeOrgs()
	users := newFakeUsers()
	orgID := uuid.New()
	_, raw := seedAcceptable(t, store, orgs, orgID, "alice@example.com", time.Hour)

	wf := newAcceptWorkflow(t, store, users, orgs, func() time.Time {
		return time.Now().UTC().Add(2 * time.Hour)
	})
	_, err := wf.Execute(context.Background(), invite.AcceptRequest{Token: raw, Email: "alice@example.com"})
	if !invite.IsExpiredError(err) {
		t.Errorf("want IsExpiredError, got %T: %v", err, err)
	}
}

func TestAccept_AlreadyUsed(t *testing.T) {
	t.Parallel()
	store := fakes.NewInvite()
	orgs := newFakeOrgs()
	users := newFakeUsers()
	orgID := uuid.New()
	_, raw := seedAcceptable(t, store, orgs, orgID, "alice@example.com", time.Hour)

	wf := newAcceptWorkflow(t, store, users, orgs, nil)
	if _, err := wf.Execute(context.Background(), invite.AcceptRequest{Token: raw, Email: "alice@example.com"}); err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	if _, err := wf.Execute(context.Background(), invite.AcceptRequest{Token: raw, Email: "alice@example.com"}); !invite.IsAlreadyUsedError(err) {
		t.Errorf("second Execute: want IsAlreadyUsedError, got %T: %v", err, err)
	}
}

func TestAccept_EmptyToken(t *testing.T) {
	t.Parallel()
	wf := newAcceptWorkflow(t, fakes.NewInvite(), newFakeUsers(), newFakeOrgs(), nil)
	_, err := wf.Execute(context.Background(), invite.AcceptRequest{Email: "alice@example.com"})
	if !invite.IsTokenMismatchError(err) {
		t.Errorf("want IsTokenMismatchError, got %v", err)
	}
}

func TestAccept_InvalidEmail(t *testing.T) {
	t.Parallel()
	wf := newAcceptWorkflow(t, fakes.NewInvite(), newFakeUsers(), newFakeOrgs(), nil)
	_, err := wf.Execute(context.Background(), invite.AcceptRequest{Token: "x", Email: ""})
	if !invite.IsInvalidEmailError(err) {
		t.Errorf("want IsInvalidEmailError, got %v", err)
	}
}
