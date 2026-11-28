package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/boot"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/user"
)

func TestCLI_ProjectListShowsGrantOnly(t *testing.T) {
	sessPath := setSelfhostedEnv(t)
	clearCLIEnv(t)
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	srv, err := boot.BootServer(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	owner, err := srv.Users.Create(ctx, user.CreateRequest{Email: "owner@example.com", Name: "Owner", Source: user.SourceLocal, Password: "secret12"})
	if err != nil {
		t.Fatal(err)
	}
	member, err := srv.Users.Create(ctx, user.CreateRequest{Email: "member@example.com", Name: "Member", Source: user.SourceLocal, Password: "secret12"})
	if err != nil {
		t.Fatal(err)
	}
	o, err := srv.Orgs.BootstrapSingleton(ctx, "acme", "Acme", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Orgs.AddMember(tenant.Into(ctx, tenant.Context{OrgID: o.ID, UserID: owner.ID}), o.ID, member.ID, org.RoleMember); err != nil {
		t.Fatal(err)
	}
	ownerCtx, err := srv.Authz.Bind(ctx, session.Principal{UserID: owner.ID, Source: session.SourceLocal}, tenant.Context{OrgID: o.ID, UserID: owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	granted, err := srv.Projects.Create(ownerCtx, o.ID, "granted-proj", "Granted")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Projects.Create(ownerCtx, o.ID, "hidden-proj", "Hidden"); err != nil {
		t.Fatal(err)
	}
	if err := srv.Orgs.SetProjectGrant(ownerCtx, o.ID, member.ID, org.ProjectGrant{ProjectIDs: []uuid.UUID{granted.ID}}); err != nil {
		t.Fatal(err)
	}
	_ = srv.Close()

	writeRemoteProfile(t, sessPath, defaultProfile, profile{Principal: principalView{
		Source: "local", UserID: member.ID.String(), Email: member.Email, ActiveOrgID: o.ID.String(),
	}})
	root := NewRootCmd(func(ctx context.Context, cfg *config.Config, _ ...boot.Option) (*boot.Server, error) {
		return boot.BootServer(ctx, cfg)
	}, stubClientBoot)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"project", "list"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("project list: %v (%s)", err, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "granted-proj") || strings.Contains(out, "hidden-proj") {
		t.Fatalf("project list must show the granted project only:\n%s", out)
	}
}
