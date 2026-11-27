package cli

import (
	"bytes"
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/boot"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/fakes"
	"altalune.id/opensheet/internal/user"
)

type recordedRoles struct {
	orgs  *org.Service
	mu    sync.Mutex
	roles []string
}

func (r *recordedRoles) RoleOf(ctx context.Context, orgID, userID uuid.UUID) (role string, roleID uuid.UUID, orgName string, grant session.Grant, err error) {
	role, roleID, orgName, grant, err = fakes.OrgRoles{Orgs: r.orgs}.RoleOf(ctx, orgID, userID)
	r.mu.Lock()
	r.roles = append(r.roles, role)
	r.mu.Unlock()
	return role, roleID, orgName, grant, err
}

func (r *recordedRoles) resolved() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.roles...)
}

func TestCLI_LocalCommandsBind(t *testing.T) {
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
	_ = srv.Close()

	writeRemoteProfile(t, sessPath, defaultProfile, profile{Principal: principalView{
		Source: "local", UserID: member.ID.String(), Email: member.Email, ActiveOrgID: o.ID.String(),
	}})

	for _, args := range [][]string{
		{"project", "list"},
		{"project", "create", "--name", "Beta", "--slug", "beta"},
	} {
		t.Run(args[1], func(t *testing.T) {
			rec := &recordedRoles{}
			bootFn := func(ctx context.Context, cfg *config.Config, _ ...boot.Option) (*boot.Server, error) {
				s, err := boot.BootServer(ctx, cfg)
				if err != nil {
					return nil, err
				}
				rec.orgs = s.Orgs
				s.Authz = authz.NewResolver(rec)
				return s, nil
			}
			root := NewRootCmd(bootFn, stubClientBoot)
			buf := &bytes.Buffer{}
			root.SetOut(buf)
			root.SetErr(buf)
			root.SetArgs(args)
			if err := root.ExecuteContext(context.Background()); err != nil {
				t.Fatalf("%v: %v (%s)", args, err, buf.String())
			}
			if got := rec.resolved(); len(got) != 1 || got[0] != "member" {
				t.Fatalf("held role resolved = %v, want one bind as member", got)
			}
		})
	}
}

func TestCLI_OrgCreateIsNotGatedByTheProfileOrg(t *testing.T) {
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
	gone, err := srv.Users.Create(ctx, user.CreateRequest{Email: "gone@example.com", Name: "Gone", Source: user.SourceLocal, Password: "secret12"})
	if err != nil {
		t.Fatal(err)
	}
	o, err := srv.Orgs.BootstrapSingleton(ctx, "acme", "Acme", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	_ = srv.Close()

	writeRemoteProfile(t, sessPath, defaultProfile, profile{Principal: principalView{
		Source: "local", UserID: gone.ID.String(), Email: gone.Email, ActiveOrgID: o.ID.String(),
	}})
	root := NewRootCmd(boot.BootServer, stubClientBoot)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"org", "create", "--name", "Fresh", "--slug", "fresh"})
	err = root.ExecuteContext(context.Background())
	if !org.IsCreationDisabledError(err) {
		t.Fatalf("org create from a profile pinned to an org the user is not in must reach org creation (disabled when self-hosted), got %v (%s)", err, buf.String())
	}
}
