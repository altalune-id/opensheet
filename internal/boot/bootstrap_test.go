package boot_test

import (
	"context"
	"testing"

	"altalune.id/template/internal/boot"
	"altalune.id/template/internal/org"
	"altalune.id/template/internal/user"
)

func TestBoot_Bootstrap_UnclaimedGenesisSeedsNothing(t *testing.T) {
	cfg := newSmokeCfg(t)
	srv, err := boot.BootServer(context.Background(), cfg)
	if err != nil {
		t.Fatalf("BootServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	if srv.Onboarded {
		t.Error("an unclaimed genesis address must leave the deployment un-onboarded")
	}
	if !srv.Caps.OnboardingRequired {
		t.Error("caps.OnboardingRequired must stay true until someone onboards")
	}
	assertNoSeededOrg(t, srv, cfg.Tenant.SingletonOrg.Slug)
	assertNoSeededUser(t, srv)
	if srv.SetupToken == "" {
		t.Error("a deployment that still needs onboarding must hold a setup token")
	}
}

func TestBoot_Bootstrap_Idempotent(t *testing.T) {
	cfg := newSmokeCfg(t)

	srv1, err := boot.BootServer(context.Background(), cfg)
	if err != nil {
		t.Fatalf("first boot: %v", err)
	}
	firstToken := srv1.SetupToken
	assertNoSeededUser(t, srv1)
	_ = srv1.Close()

	srv2, err := boot.BootServer(context.Background(), cfg)
	if err != nil {
		t.Fatalf("second boot must be idempotent: %v", err)
	}
	t.Cleanup(func() { _ = srv2.Close() })

	if srv2.Onboarded {
		t.Error("a second boot must not onboard the deployment either")
	}
	assertNoSeededOrg(t, srv2, cfg.Tenant.SingletonOrg.Slug)
	assertNoSeededUser(t, srv2)
	if srv2.SetupToken == firstToken {
		t.Error("each boot must mint a fresh setup token")
	}
}

func assertNoSeededOrg(t *testing.T, srv *boot.Server, slug string) {
	t.Helper()
	o, err := srv.Orgs.BySlug(context.Background(), slug)
	if err == nil {
		t.Fatalf("boot must not create an org, got %q", o.Slug)
	}
	if !org.IsNotFoundError(err) {
		t.Fatalf("orgs.BySlug: %v", err)
	}
}

func assertNoSeededUser(t *testing.T, srv *boot.Server) {
	t.Helper()
	has, err := srv.Users.HasLocalUsers(context.Background())
	if err != nil {
		t.Fatalf("users.HasLocalUsers: %v", err)
	}
	if has {
		t.Error("boot must not create a user row for the genesis address")
	}
	outcome, err := srv.Users.ReconcileGenesisAdmin(context.Background())
	if err != nil {
		t.Fatalf("ReconcileGenesisAdmin: %v", err)
	}
	if outcome != user.OutcomeUnclaimed {
		t.Errorf("outcome=%q, want %q", outcome, user.OutcomeUnclaimed)
	}
}
