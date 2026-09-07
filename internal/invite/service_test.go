package invite_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/invite"
	"altalune.id/template/internal/platform/tenant"
	"altalune.id/template/internal/testutil/fakes"
	"altalune.id/template/mailer"
)

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func noopUnexpected(counter *int) apperror.UnexpectedFunc {
	return func(_ context.Context, message string, cause error, _ ...any) *apperror.AppError {
		if counter != nil {
			*counter++
		}
		return apperror.New(apperror.CodeUnexpectedError, message, 0).WithCause(cause)
	}
}

func newService(t *testing.T, store invite.Store) (*invite.Service, *int) {
	t.Helper()
	unex := 0
	unf := noopUnexpected(&unex)
	send := invite.NewSendWorkflow(store, nopMailer{}, "https://example.test", newTestLogger(), unf)
	accept := invite.NewAcceptWorkflow(store, newFakeUsers(), newFakeOrgs(), newTestLogger(), unf)
	return invite.NewService(store, send, accept, true, newTestLogger(), unf), &unex
}

func tenantCtx() (context.Context, tenant.Context) {
	tc := tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New(), UserID: uuid.New()}
	return tenant.Into(context.Background(), tc), tc
}

func seedInvite(t *testing.T, store invite.Store, orgID uuid.UUID) *invite.Invite {
	t.Helper()
	inv, err := invite.New(invite.NewParams{
		OrgID: orgID,
		Email: "alice@example.com",
		Role:  invite.RoleMember,
		TTL:   invite.DefaultTTL,
		Token: "raw-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("seed New: %v", err)
	}
	ctx := tenant.Into(context.Background(), tenant.Context{OrgID: orgID, UserID: uuid.New()})
	if err := store.Save(ctx, inv); err != nil {
		t.Fatalf("seed Save: %v", err)
	}
	return inv
}

func TestService_ListPending(t *testing.T) {
	t.Parallel()

	t.Run("returns tenant-scoped pending invites", func(t *testing.T) {
		store := fakes.NewInvite()
		svc, unex := newService(t, store)
		ctx, tc := tenantCtx()

		seedInvite(t, store, tc.OrgID)
		seedInvite(t, store, uuid.New())

		out, err := svc.ListPending(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != 1 {
			t.Errorf("len=%d want 1", len(out))
		}
		if *unex != 0 {
			t.Errorf("unexpected() called %d times", *unex)
		}
	})

	t.Run("missing tenant surfaces MissingError", func(t *testing.T) {
		svc, _ := newService(t, fakes.NewInvite())
		_, err := svc.ListPending(context.Background())
		if !tenant.IsMissingError(err) {
			t.Errorf("want MissingError, got %v", err)
		}
	})

	t.Run("store error routes through unexpected", func(t *testing.T) {
		store := fakes.NewInvite()
		store.ListErr = errors.New("boom")
		store.StickyError = true
		svc, unex := newService(t, store)
		ctx, _ := tenantCtx()
		if _, err := svc.ListPending(ctx); err == nil {
			t.Fatal("want err")
		}
		if *unex != 1 {
			t.Errorf("unexpected() calls=%d want 1", *unex)
		}
	})
}

func TestService_Revoke(t *testing.T) {
	t.Parallel()

	t.Run("happy path", func(t *testing.T) {
		store := fakes.NewInvite()
		svc, unex := newService(t, store)
		ctx, tc := tenantCtx()
		inv := seedInvite(t, store, tc.OrgID)

		if err := svc.Revoke(ctx, inv.ID); err != nil {
			t.Fatal(err)
		}
		if store.Len() != 0 {
			t.Errorf("invite not deleted")
		}
		if *unex != 0 {
			t.Errorf("unexpected() called")
		}
	})

	t.Run("cross-tenant returns NotFoundError", func(t *testing.T) {
		store := fakes.NewInvite()
		svc, _ := newService(t, store)
		other := uuid.New()
		inv := seedInvite(t, store, other)

		ctx, _ := tenantCtx()
		if err := svc.Revoke(ctx, inv.ID); !invite.IsNotFoundError(err) {
			t.Errorf("want IsNotFoundError, got %v", err)
		}
	})

	t.Run("missing id returns NotFoundError", func(t *testing.T) {
		svc, _ := newService(t, fakes.NewInvite())
		ctx, _ := tenantCtx()
		if err := svc.Revoke(ctx, uuid.New()); !invite.IsNotFoundError(err) {
			t.Errorf("want IsNotFoundError, got %v", err)
		}
	})
}

func TestService_Send_DelegatesToWorkflow(t *testing.T) {
	t.Parallel()
	store := fakes.NewInvite()
	svc, unex := newService(t, store)
	tc := tenant.Context{OrgID: uuid.New(), UserID: uuid.New()}
	ctx := tenant.Into(context.Background(), tc)

	inv, err := svc.Send(ctx, invite.SendRequest{Email: "alice@example.com", Role: invite.RoleMember, TTL: 0})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if inv.OrgID != tc.OrgID {
		t.Errorf("OrgID mismatch")
	}
	if *unex != 0 {
		t.Errorf("unexpected() called")
	}
}

func TestService_Accept_DelegatesToWorkflow(t *testing.T) {
	t.Parallel()
	store := fakes.NewInvite()
	svc, _ := newService(t, store)
	_, err := svc.Accept(context.Background(), invite.AcceptRequest{Token: "", Email: "a@b.co"})
	if !invite.IsTokenMismatchError(err) {
		t.Errorf("want IsTokenMismatchError, got %v", err)
	}
}

func TestService_Send_Disabled(t *testing.T) {
	t.Parallel()
	store := fakes.NewInvite()
	unex := 0
	unf := noopUnexpected(&unex)
	send := invite.NewSendWorkflow(store, nopMailer{}, "https://example.test", newTestLogger(), unf)
	accept := invite.NewAcceptWorkflow(store, newFakeUsers(), newFakeOrgs(), newTestLogger(), unf)
	svc := invite.NewService(store, send, accept, false, newTestLogger(), unf)
	if svc.Enabled() {
		t.Fatal("Enabled must be false when disabled")
	}
	ctx, _ := tenantCtx()
	_, err := svc.Send(ctx, invite.SendRequest{Email: "alice@example.com", Role: invite.RoleMember})
	if !invite.IsInvitesDisabledError(err) {
		t.Errorf("want IsInvitesDisabledError, got %T: %v", err, err)
	}
	if store.Len() != 0 {
		t.Errorf("no invite must be stored; got %d", store.Len())
	}
}

func TestService_Send_EnabledDoesNotBlock(t *testing.T) {
	t.Parallel()
	store := fakes.NewInvite()
	unex := 0
	unf := noopUnexpected(&unex)
	send := invite.NewSendWorkflow(store, nopMailer{}, "https://example.test", newTestLogger(), unf)
	accept := invite.NewAcceptWorkflow(store, newFakeUsers(), newFakeOrgs(), newTestLogger(), unf)
	svc := invite.NewService(store, send, accept, true, newTestLogger(), unf)
	if !svc.Enabled() {
		t.Fatal("Enabled must be true when enabled")
	}
	ctx, _ := tenantCtx()
	_, err := svc.Send(ctx, invite.SendRequest{Email: "alice@example.com", Role: invite.RoleMember})
	if err != nil {
		t.Errorf("unexpected err: %v", err)
	}
}

func TestService_HasPendingForEmail(t *testing.T) {
	t.Parallel()
	store := fakes.NewInvite()
	svc, _ := newService(t, store)

	has, err := svc.HasPendingForEmail(context.Background(), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Errorf("expected false on empty store")
	}

	seedInvite(t, store, uuid.New())
	has, err = svc.HasPendingForEmail(context.Background(), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Errorf("expected true after seeding invite for alice")
	}
}

type nopMailer struct{}

func (nopMailer) Send(_ context.Context, _ mailer.Message) error { return nil }
