package boot

import (
	"context"
	"fmt"
	"log/slog"

	"altalune.id/template/internal/onboard"
	"altalune.id/template/internal/platform/config"
	"altalune.id/template/internal/user"
)

func bootstrap(
	ctx context.Context,
	cfg *config.Config,
	users *user.Service,
	onboards *onboard.Service,
	log *slog.Logger,
) (bool, error) {
	required, err := onboards.Required(ctx)
	if err != nil {
		return false, fmt.Errorf("onboard: required: %w", err)
	}

	outcome, rErr := users.ReconcileGenesisAdmin(ctx)
	if rErr != nil {
		return false, fmt.Errorf("genesis: %w", rErr)
	}
	if log != nil {
		switch outcome {
		case user.OutcomeClaimed:
			log.Info("bootstrap: genesis admin promoted", slog.String("email", cfg.Genesis.Email))
		case user.OutcomeUnclaimed:
			log.Warn("bootstrap: genesis admin not yet claimed — the address has never signed in",
				slog.String("email", cfg.Genesis.Email))
		case user.OutcomeSatisfied, user.OutcomeUnconfigured:
		}
	}
	return !required, nil
}
