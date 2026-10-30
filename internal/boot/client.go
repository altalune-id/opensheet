package boot

import (
	"context"
	"log/slog"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/controlplane"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/logger"
)

// Client is the CLI-side wired graph, omitting DB, migrations, and supervisor.
type Client struct {
	Cfg      *config.Config
	Log      *slog.Logger
	Reporter *apperror.Reporter
	Conn     *controlplane.Client
}

// BootClient builds the minimal wired graph for CLI subcommands that call the opensheet server at baseURL with token.
func BootClient(_ context.Context, cfg *config.Config, baseURL, token string) (*Client, error) {
	log := logger.New(cfg.Log)
	reporter := apperror.NewReporter(log, cfg.Mode.IsProduction())
	conn := controlplane.NewClient(baseURL, token)
	return &Client{
		Cfg:      cfg,
		Log:      log,
		Reporter: reporter,
		Conn:     conn,
	}, nil
}
