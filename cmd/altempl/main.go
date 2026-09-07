// Command altempl is the altalune multitenant SSR + Connect-RPC starter binary. See `altempl --help`.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"altalune.id/template/internal/boot"
	"altalune.id/template/internal/cli"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	root := cli.NewRootCmd(boot.BootServer, boot.BootClient)
	if err := root.ExecuteContext(ctx); err != nil {
		slog.ErrorContext(ctx, "altempl", "error", err)
		return cli.ExitCodeFor(err)
	}
	return cli.ExitOK
}
