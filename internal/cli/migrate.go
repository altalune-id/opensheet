package cli

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"

	"altalune.id/template/internal/boot"
	"altalune.id/template/internal/platform/config"
	"altalune.id/template/internal/platform/db"
	"altalune.id/template/schema"
)

func openMigratorDB(ctx context.Context, cfg *config.Config) (*sql.DB, *config.Config, error) {
	dbCfg := boot.MigratorDBConfig(cfg)
	sqldb, err := db.Open(ctx, dbCfg, slog.Default())
	if err != nil {
		return nil, nil, fmt.Errorf("open migrator: %w", err)
	}
	migCfg := *cfg
	migCfg.DB = dbCfg
	return sqldb, &migCfg, nil
}

func runCLIMigrateUp(ctx context.Context, cfg *config.Config, _ ServerBootFn, cmd *cobra.Command) error {
	sqldb, migCfg, err := openMigratorDB(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = sqldb.Close() }()
	if err := schema.MigrateUp(ctx, sqldb, migCfg); err != nil {
		return err
	}
	cmd.Println("migrations: up-to-date")
	return nil
}

func newMigrateCmd(bootServer ServerBootFn) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "migrate",
		Short:   "Manage database schema migrations",
		GroupID: "runtime",
	}
	cmd.AddCommand(
		newMigrateUpCmd(bootServer),
		newMigrateStatusCmd(bootServer),
		newMigrateDownToCmd(bootServer),
	)
	return cmd
}

func newMigrateUpCmd(bootServer ServerBootFn) *cobra.Command {
	return &cobra.Command{
		Use:   "up",
		Short: "Apply all pending migrations",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := withCfg(cmd)
			if err != nil {
				return err
			}
			return runCLIMigrateUp(cmd.Context(), cfg, bootServer, cmd)
		},
	}
}

func newMigrateStatusCmd(_ ServerBootFn) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Print the current migration status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := withCfg(cmd)
			if err != nil {
				return err
			}
			sqldb, migCfg, err := openMigratorDB(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			defer func() { _ = sqldb.Close() }()
			rows, err := schema.MigrateStatus(cmd.Context(), sqldb, migCfg)
			if err != nil {
				return err
			}
			for _, r := range rows {
				applied := "pending"
				if r.Applied {
					applied = r.AppliedAt.Format("2006-01-02 15:04:05")
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%-12d %s  %s\n", r.Version, applied, r.Source)
			}
			return nil
		},
	}
}

func newMigrateDownToCmd(_ ServerBootFn) *cobra.Command {
	return &cobra.Command{
		Use:   "down-to <version>",
		Short: "Roll back to the given goose version",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var version int64
			if _, err := fmt.Sscanf(args[0], "%d", &version); err != nil {
				return fmt.Errorf("migrate down-to: %q is not a valid version: %w", args[0], err)
			}
			cfg, err := withCfg(cmd)
			if err != nil {
				return err
			}
			sqldb, migCfg, err := openMigratorDB(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			defer func() { _ = sqldb.Close() }()
			if err := schema.MigrateDownTo(cmd.Context(), sqldb, migCfg, version); err != nil {
				return err
			}
			cmd.Printf("migrations: rolled back to %d\n", version)
			return nil
		},
	}
}
