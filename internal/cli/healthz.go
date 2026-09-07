package cli

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"altalune.id/template/httpclient"
	"altalune.id/template/internal/cli/render"
)

func newHealthzCmd() *cobra.Command {
	var (
		url     string
		timeout time.Duration
	)

	cmd := &cobra.Command{
		Use:     "healthz",
		Short:   "Probe /healthz and exit 0 iff the server is healthy",
		GroupID: "meta",
		RunE: func(cmd *cobra.Command, _ []string) error {
			target := url
			if target == "" {
				target = defaultHealthzURL(cmd.Context())
			}
			res, doErr := httpclient.NewProber(httpclient.WithTimeout(timeout)).Probe(cmd.Context(), target)
			elapsed := res.Elapsed.Truncate(time.Millisecond)
			status := res.StatusCode
			probeErr := ""
			if doErr != nil && !httpclient.IsUnhealthyStatusError(doErr) {
				probeErr = doErr.Error()
			}
			ok := doErr == nil

			switch render.Detect(cmd) {
			case render.FormatJSON, render.FormatNDJSON:
				_ = render.JSON(cmd.OutOrStdout(), map[string]any{
					"url":    target,
					"status": status,
					"ok":     ok,
					"took":   elapsed.String(),
					"error":  probeErr,
				})
			default:
				switch {
				case ok:
					cmd.Printf("ok   %s  (%d, %s)\n", target, status, elapsed)
				case probeErr != "":
					cmd.Printf("fail %s  (%s, %s)\n", target, probeErr, elapsed)
				default:
					cmd.Printf("fail %s  (%d, %s)\n", target, status, elapsed)
				}
			}

			if !ok {
				return errHealthzUnhealthy
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&url, "url", "",
		"health URL to probe (default: http://127.0.0.1:<http.addr port>/healthz)")
	cmd.Flags().DurationVar(&timeout, "timeout", 3*time.Second, "request timeout")
	return cmd
}

var errHealthzUnhealthy = errors.New("healthz: unhealthy")

func defaultHealthzURL(ctx context.Context) string {
	addr := ":5150"
	if cfg := configFromCtx(ctx); cfg != nil && cfg.HTTP.Addr != "" {
		addr = cfg.HTTP.Addr
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		port = strings.TrimPrefix(addr, ":")
	}
	return "http://127.0.0.1:" + port + "/healthz"
}
