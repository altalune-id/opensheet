package api_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	authv1 "altalune.id/template/gen/go/auth/v1"
	"altalune.id/template/internal/org"
	"altalune.id/template/internal/platform/session"
)

func TestWhoami_MissingAuth_ReturnsUnauthenticated(t *testing.T) {
	h := newHarness(t, session.Principal{UserID: uuid.New(), Email: "a@b"})

	client := h.whoamiClient()
	req := connect.NewRequest(&authv1.WhoamiRequest{})
	if _, err := client.Whoami(context.Background(), req); err == nil {
		t.Fatal("expected error, got nil")
	} else if got := connectCode(err); got != connect.CodeUnauthenticated {
		t.Errorf("code = %v, want Unauthenticated", got)
	}
}

func TestWhoami_ValidPrincipal_ReturnsFields(t *testing.T) {
	userID := uuid.New()
	orgID := uuid.New()
	p := session.Principal{
		UserID:      userID,
		Email:       "alice@example.com",
		Name:        "Alice",
		ActiveOrgID: orgID,
		Scopes:      []string{"todo.read", "todo.write"},
	}
	h := newHarness(t, p)

	o, err := org.NewOrg("acme", "Acme", userID)
	if err != nil {
		t.Fatal(err)
	}
	o.ID = orgID
	if err := h.orgs.Save(context.Background(), o); err != nil {
		t.Fatal(err)
	}

	client := h.whoamiClient()
	req := connect.NewRequest(&authv1.WhoamiRequest{})
	withBearer(req.Header())

	resp, err := client.Whoami(context.Background(), req)
	if err != nil {
		t.Fatalf("Whoami: %v", err)
	}
	if resp.Msg.UserId != userID.String() {
		t.Errorf("user_id = %q, want %q", resp.Msg.UserId, userID.String())
	}
	if resp.Msg.Email != "alice@example.com" {
		t.Errorf("email = %q", resp.Msg.Email)
	}
	if resp.Msg.Name != "Alice" {
		t.Errorf("name = %q", resp.Msg.Name)
	}
	if resp.Msg.ActiveOrgId != orgID.String() {
		t.Errorf("active_org_id = %q", resp.Msg.ActiveOrgId)
	}
	if resp.Msg.ActiveOrgSlug != "acme" {
		t.Errorf("active_org_slug = %q, want acme", resp.Msg.ActiveOrgSlug)
	}
	if len(resp.Msg.Scopes) != 2 {
		t.Errorf("scopes len = %d, want 2", len(resp.Msg.Scopes))
	}
}

func TestWhoami_BadTokenScheme_ReturnsUnauthenticated(t *testing.T) {
	h := newHarness(t, session.Principal{UserID: uuid.New(), Email: "a@b"})

	client := h.whoamiClient()
	req := connect.NewRequest(&authv1.WhoamiRequest{})
	req.Header().Set("Authorization", "Basic abc")
	if _, err := client.Whoami(context.Background(), req); err == nil {
		t.Fatal("expected error")
	} else if got := connectCode(err); got != connect.CodeUnauthenticated {
		t.Errorf("code = %v, want Unauthenticated", got)
	}
}
