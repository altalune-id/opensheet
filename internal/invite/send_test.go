package invite_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"altalune.id/template/internal/invite"
	"altalune.id/template/internal/platform/tenant"
	"altalune.id/template/internal/testutil/fakes"
	"altalune.id/template/mailer"
)

type recordingMailer struct {
	mu  sync.Mutex
	msg mailer.Message
	err error
}

func (m *recordingMailer) Send(_ context.Context, msg mailer.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.msg = msg
	return nil
}

func fixedTokenGen(raw string) invite.TokenGenFunc {
	return func() (string, string, error) {
		return raw, invite.HashToken(raw), nil
	}
}

func TestSendWorkflow_Execute_HappyPath(t *testing.T) {
	t.Parallel()
	store := fakes.NewInvite()
	mail := &recordingMailer{}
	unex := 0
	frozen := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	wf := invite.NewSendWorkflow(
		store, mail, "https://altempl.example.test/",
		newTestLogger(), noopUnexpected(&unex),
		invite.WithTokenGen(fixedTokenGen("raw-token-XYZ")),
		invite.WithClock(func() time.Time { return frozen }),
	)

	tc := tenant.Context{OrgID: uuid.New(), UserID: uuid.New()}
	ctx := tenant.Into(context.Background(), tc)
	inv, err := wf.Execute(ctx, invite.SendRequest{Email: "alice@example.com", Role: invite.RoleMember, TTL: time.Hour})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if inv.OrgID != tc.OrgID {
		t.Errorf("OrgID not carried")
	}
	if inv.TokenHash != invite.HashToken("raw-token-XYZ") {
		t.Errorf("token hash mismatch")
	}
	if !inv.ExpiresAt.Equal(frozen.Add(time.Hour)) {
		t.Errorf("ExpiresAt=%v want %v", inv.ExpiresAt, frozen.Add(time.Hour))
	}
	if store.Len() != 1 {
		t.Errorf("store len=%d want 1", store.Len())
	}
	if mail.msg.To != "alice@example.com" {
		t.Errorf("mail To=%q", mail.msg.To)
	}
	if want := "https://altempl.example.test/invites/accept?token=raw-token-XYZ"; !strings.Contains(mail.msg.TextBody, want) {
		t.Errorf("mail body missing accept URL %q; got %q", want, mail.msg.TextBody)
	}
	if unex != 0 {
		t.Errorf("unexpected() called %d times", unex)
	}
}

func TestSendWorkflow_Execute_DefaultsTTL(t *testing.T) {
	t.Parallel()
	store := fakes.NewInvite()
	unex := 0
	wf := invite.NewSendWorkflow(store, &recordingMailer{}, "", newTestLogger(), noopUnexpected(&unex),
		invite.WithTokenGen(fixedTokenGen("t")))
	tc := tenant.Context{OrgID: uuid.New(), UserID: uuid.New()}
	ctx := tenant.Into(context.Background(), tc)
	inv, err := wf.Execute(ctx, invite.SendRequest{Email: "a@b.co", Role: invite.RoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	if got := inv.ExpiresAt.Sub(inv.CreatedAt); got < 6*24*time.Hour || got > 8*24*time.Hour {
		t.Errorf("expected DefaultTTL (~7d), got %v", got)
	}
}

func TestSendWorkflow_Execute_InvalidRole(t *testing.T) {
	t.Parallel()
	wf := invite.NewSendWorkflow(fakes.NewInvite(), &recordingMailer{}, "", newTestLogger(), noopUnexpected(nil),
		invite.WithTokenGen(fixedTokenGen("t")))
	tc := tenant.Context{OrgID: uuid.New(), UserID: uuid.New()}
	ctx := tenant.Into(context.Background(), tc)
	_, err := wf.Execute(ctx, invite.SendRequest{Email: "a@b.co", Role: "guest"})
	if !invite.IsInvalidRoleError(err) {
		t.Errorf("want IsInvalidRoleError, got %v", err)
	}
}

func TestSendWorkflow_Execute_InvalidEmail(t *testing.T) {
	t.Parallel()
	wf := invite.NewSendWorkflow(fakes.NewInvite(), &recordingMailer{}, "", newTestLogger(), noopUnexpected(nil),
		invite.WithTokenGen(fixedTokenGen("t")))
	tc := tenant.Context{OrgID: uuid.New(), UserID: uuid.New()}
	ctx := tenant.Into(context.Background(), tc)
	_, err := wf.Execute(ctx, invite.SendRequest{Email: "not-an-email", Role: invite.RoleMember})
	if !invite.IsInvalidEmailError(err) {
		t.Errorf("want IsInvalidEmailError, got %v", err)
	}
}

func TestSendWorkflow_Execute_MissingTenant(t *testing.T) {
	t.Parallel()
	wf := invite.NewSendWorkflow(fakes.NewInvite(), &recordingMailer{}, "", newTestLogger(), noopUnexpected(nil),
		invite.WithTokenGen(fixedTokenGen("t")))
	_, err := wf.Execute(context.Background(), invite.SendRequest{Email: "a@b.co", Role: invite.RoleMember})
	if !tenant.IsMissingError(err) {
		t.Errorf("want MissingError, got %v", err)
	}
}

func TestSendWorkflow_Execute_MailerFailure(t *testing.T) {
	t.Parallel()
	store := fakes.NewInvite()
	mail := &recordingMailer{err: errors.New("smtp down")}
	unex := 0
	wf := invite.NewSendWorkflow(store, mail, "", newTestLogger(), noopUnexpected(&unex),
		invite.WithTokenGen(fixedTokenGen("t")))
	tc := tenant.Context{OrgID: uuid.New(), UserID: uuid.New()}
	ctx := tenant.Into(context.Background(), tc)
	_, err := wf.Execute(ctx, invite.SendRequest{Email: "a@b.co", Role: invite.RoleMember, TTL: time.Hour})
	if err == nil {
		t.Fatal("want err")
	}
	if unex != 1 {
		t.Errorf("unexpected() calls=%d want 1", unex)
	}
	if store.Len() != 1 {
		t.Errorf("invite must still be persisted before mail failure; got len=%d", store.Len())
	}
}

func TestSendWorkflow_Execute_StoreFailure(t *testing.T) {
	t.Parallel()
	store := fakes.NewInvite()
	store.SaveErr = errors.New("db down")
	store.StickyError = true
	unex := 0
	wf := invite.NewSendWorkflow(store, &recordingMailer{}, "", newTestLogger(), noopUnexpected(&unex),
		invite.WithTokenGen(fixedTokenGen("t")))
	tc := tenant.Context{OrgID: uuid.New(), UserID: uuid.New()}
	ctx := tenant.Into(context.Background(), tc)
	_, err := wf.Execute(ctx, invite.SendRequest{Email: "a@b.co", Role: invite.RoleMember, TTL: time.Hour})
	if err == nil {
		t.Fatal("want err")
	}
	if unex != 1 {
		t.Errorf("unexpected() calls=%d want 1", unex)
	}
}

func TestSendWorkflow_Execute_TokenGenFailure(t *testing.T) {
	t.Parallel()
	unex := 0
	wf := invite.NewSendWorkflow(fakes.NewInvite(), &recordingMailer{}, "", newTestLogger(), noopUnexpected(&unex),
		invite.WithTokenGen(func() (string, string, error) { return "", "", errors.New("rand down") }))
	tc := tenant.Context{OrgID: uuid.New(), UserID: uuid.New()}
	ctx := tenant.Into(context.Background(), tc)
	_, err := wf.Execute(ctx, invite.SendRequest{Email: "a@b.co", Role: invite.RoleMember, TTL: time.Hour})
	if err == nil {
		t.Fatal("want err")
	}
	if unex != 1 {
		t.Errorf("unexpected() calls=%d want 1", unex)
	}
}
