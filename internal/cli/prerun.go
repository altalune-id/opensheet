package cli

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"altalune.id/opensheet/internal/boot"
	"altalune.id/opensheet/internal/controlplane"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
)

func withCfg(cmd *cobra.Command) (*config.Config, error) {
	cfg := configFromCtx(cmd.Context())
	if cfg == nil {
		return nil, errors.New("config not loaded — PersistentPreRunE missed")
	}
	return cfg, nil
}

func withPrincipal(cmd *cobra.Command) (Principal, error) {
	if _, err := withCfg(cmd); err != nil {
		return Principal{}, err
	}
	return resolveLocal(cmd)
}

// SECURITY: a local command acts on the active org only after Bind admits the profile's person there; with no active org it stays unbound and holds nothing.
func bindLocal(ctx context.Context, s *boot.Server, p Principal) (context.Context, error) {
	tc := tenant.Context{OrgID: p.OrgID, ProjectID: p.ProjectID, UserID: p.UserID}
	if p.OrgID == uuid.Nil {
		return tenant.Into(ctx, tc), nil
	}
	sp := session.Principal{
		UserID: p.UserID, Email: p.Email, Name: p.Name, Source: session.Source(p.Source),
		ActiveOrgID: p.OrgID, ActiveProjectID: p.ProjectID,
	}
	return s.Authz.Bind(ctx, sp, tc)
}

func connFromCmd(cmd *cobra.Command, bootClient ClientBootFn) (*controlplane.Client, error) {
	in, err := invocationFrom(cmd)
	if err != nil {
		return nil, err
	}
	r, err := resolveRemote(cmd.Context(), in, operatorHTTPClient())
	if err != nil {
		return nil, err
	}
	return dial(cmd.Context(), bootClient, in.cfg, r.url, r.token)
}

func dial(ctx context.Context, bootClient ClientBootFn, cfg *config.Config, baseURL, token string) (*controlplane.Client, error) {
	client, err := bootClient(ctx, cfg, baseURL, token)
	if err != nil {
		return nil, err
	}
	if client == nil || client.Conn == nil {
		return nil, errors.New("cli: boot client returned nil connection — bootClient must populate Conn")
	}
	return client.Conn, nil
}
