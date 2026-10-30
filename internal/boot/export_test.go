package boot

import (
	"log/slog"

	"altalune.id/opensheet/internal/platform/config"
)

// ResolveStateSecret exposes resolveStateSecret to the external tests.
func ResolveStateSecret(cfg *config.Config, log *slog.Logger) ([]byte, error) {
	return resolveStateSecret(cfg, log)
}
