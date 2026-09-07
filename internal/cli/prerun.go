package cli

import (
	"errors"

	"github.com/spf13/cobra"

	"altalune.id/template/internal/api"
	"altalune.id/template/internal/platform/config"
)

func withCfg(cmd *cobra.Command) (*config.Config, error) {
	cfg := configFromCtx(cmd.Context())
	if cfg == nil {
		return nil, errors.New("config not loaded — PersistentPreRunE missed")
	}
	return cfg, nil
}

//nolint:unparam // requireAuth is part of the documented CLI plumbing surface.
func withPrincipal(cmd *cobra.Command, bootClient ClientBootFn, requireAuth bool) (Principal, error) {
	cfg, err := withCfg(cmd)
	if err != nil {
		return Principal{}, err
	}
	p, err := Resolve(cmd.Context(), cmd, cfg, bootClient)
	if err != nil {
		if !requireAuth && errors.Is(err, ErrNotSignedIn) {
			return Principal{}, nil
		}
		return Principal{}, err
	}
	cmd.SetContext(ctxWithPrincipal(cmd.Context(), p))
	return p, nil
}

// TODO(future-tokens): fall back to a token minted from the session file once tokens.Issuer.Mint lands.
func connFromCmd(cmd *cobra.Command, bootClient ClientBootFn) (*api.Client, error) {
	cfg, err := withCfg(cmd)
	if err != nil {
		return nil, err
	}
	tok, _, err := readToken(cmd)
	if err != nil {
		return nil, err
	}
	client, err := bootClient(cmd.Context(), cfg, tok)
	if err != nil {
		return nil, err
	}
	if client == nil || client.Conn == nil {
		return nil, errors.New("cli: boot client returned nil connection — bootClient must populate Conn")
	}
	return client.Conn, nil
}
