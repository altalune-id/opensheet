package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/altalune-id/oidcrp"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	"google.golang.org/grpc/codes"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/auth"
	"altalune.id/opensheet/internal/boot"
	"altalune.id/opensheet/internal/cli/render"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/session"
)

const (
	cliRedirectURI  = "http://127.0.0.1/callback"
	loopbackTimeout = 5 * time.Minute
)

func newAuthCmd(bootServer ServerBootFn, bootClient ClientBootFn) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "auth",
		Short:   "Sign in through Othra (login, logout, whoami)",
		GroupID: "auth",
	}
	cmd.AddCommand(
		newAuthLoginCmd(bootServer, bootClient),
		newAuthLogoutCmd(),
		newAuthWhoamiCmd(bootClient),
	)
	return cmd
}

type loginOpts struct {
	admin         bool
	email         string
	passwordStdin bool
}

func newAuthLoginCmd(bootServer ServerBootFn, bootClient ClientBootFn) *cobra.Command {
	var o loginOpts
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Sign in to a remote server (--url) through Othra, or in-process on this server",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := withCfg(cmd)
			if err != nil {
				return err
			}
			if err := refuseLoginToken(cmd); err != nil {
				return err
			}
			store, err := sessionStore(cmd)
			if err != nil {
				return err
			}
			name := profileName(cmd)
			if base := explicitURL(cmd); base != "" {
				if o.admin || o.email != "" || o.passwordStdin {
					return apperror.New(apperror.CodeValidation,
						"login: --admin, --email and --password-stdin sign in on this server; drop --url", codes.InvalidArgument)
				}
				l := remoteLogin{
					cfg: cfg, bootClient: bootClient, httpClient: operatorHTTPClient(), open: oidcrp.OpenBrowser,
					out: cmd.OutOrStdout(), errOut: cmd.ErrOrStderr(), store: store,
				}
				return l.run(cmd.Context(), base, name)
			}
			return localLogin(cmd, bootServer, cfg, store, name, o)
		},
	}
	cmd.Flags().BoolVar(&o.admin, "admin", false, "Break-glass: force local genesis login even when OIDC is configured")
	cmd.Flags().StringVar(&o.email, "email", "", "Email for local login (skips prompt)")
	cmd.Flags().BoolVar(&o.passwordStdin, "password-stdin", false, "Read password from stdin (unmasked)")
	return cmd
}

func refuseLoginToken(cmd *cobra.Command) error {
	for _, name := range []string{"token", "token-file"} {
		if f := cmd.Flags().Lookup(name); f != nil && f.Changed {
			return apperror.New(apperror.CodeValidation,
				"login: --"+name+" is a credential already; pass it to each command instead of signing in", codes.InvalidArgument)
		}
	}
	return nil
}

func localLogin(cmd *cobra.Command, bootServer ServerBootFn, cfg *config.Config, store profileStore, name string, o loginOpts) error {
	s, err := bootServer(cmd.Context(), cfg)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	p, err := runLogin(cmd, s, o)
	if err != nil {
		return err
	}
	if err := store.put(cmd.Context(), name, profile{Principal: viewOfSession(p)}); err != nil {
		return fmt.Errorf("login: persist session: %w", err)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Signed in as %s (%s, profile %s)\n", p.Email, p.Source, name)
	return nil
}

func runLogin(cmd *cobra.Command, s *boot.Server, o loginOpts) (session.Principal, error) {
	if o.admin || !s.Caps.ExternalIdentity {
		if !s.Auth.LocalConfigured() {
			return session.Principal{}, errors.New("login: local admin login not configured; set OPENSHEET_GENESIS_EMAIL / OPENSHEET_GENESIS_PASSWORD")
		}
		if o.admin && s.Caps.ExternalIdentity {
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "warn: --admin break-glass login (OIDC bypassed for this session)")
		}
		return runLocalPrompt(cmd, s, o)
	}
	return runLoopback(cmd.Context(), cmd, s)
}

func runLocalPrompt(cmd *cobra.Command, s *boot.Server, o loginOpts) (session.Principal, error) {
	reader := bufio.NewReader(os.Stdin)
	emailAddr := strings.TrimSpace(o.email)
	if emailAddr == "" {
		_, _ = fmt.Fprint(cmd.OutOrStdout(), "Email: ")
		line, err := reader.ReadString('\n')
		if err != nil {
			return session.Principal{}, fmt.Errorf("login: read email: %w", err)
		}
		emailAddr = strings.TrimSpace(line)
	}
	pw, err := readPassword(cmd, reader, o.passwordStdin)
	if err != nil {
		return session.Principal{}, fmt.Errorf("login: read password: %w", err)
	}
	p, err := s.Auth.LoginLocal(cmd.Context(), auth.Credentials{Email: emailAddr, Password: pw})
	if err != nil {
		if auth.IsInvalidCredentialsError(err) {
			return session.Principal{}, errors.New("login: invalid credentials")
		}
		return session.Principal{}, err
	}
	return p, nil
}

func runLoopback(ctx context.Context, cmd *cobra.Command, s *boot.Server) (session.Principal, error) {
	if s.Platform == nil || s.Platform.AltAuth == nil {
		return session.Principal{}, errors.New("login: OIDC not configured (oidc.issuer / oidc.clientID)")
	}
	if s.Cfg.OIDC.CLIClientID == "" {
		return session.Principal{}, errors.New("login: oidc.cliClientID not set; register a native client for the CLI (set OPENSHEET_OIDC_CLI_CLIENT_ID)")
	}
	rp := cliRelyingParty(s.Cfg.OIDC, s.Platform.AltAuth.Keys().HTTPClient())
	set, err := loopbackSignIn(ctx, rp, s.Cfg.OIDC.RedirectPort, cmd.ErrOrStderr(), oidcrp.OpenBrowser)
	if err != nil {
		return session.Principal{}, fmt.Errorf("login: loopback: %w", err)
	}
	tok := set.IDToken
	p, err := s.Auth.LoginOIDC(ctx, auth.OIDCClaims{
		Issuer:        s.Cfg.OIDC.Issuer,
		Subject:       tok.Subject,
		Email:         tok.Email,
		EmailVerified: tok.EmailVerified,
		Name:          tok.Name,
	})
	if err != nil {
		if auth.IsNotInvitedError(err) {
			return session.Principal{}, errors.New("login: not invited to this deployment; ask an admin to send an invite")
		}
		return session.Principal{}, err
	}
	return p, nil
}

// SECURITY: the CLI is a native public client (RFC 8252), so it never carries the web client's secret.
func cliRelyingParty(cfg config.OIDCConfig, client *http.Client) *oidcrp.Client {
	return oidcrp.New(oidcrp.Config{
		Issuer:      cfg.Issuer,
		ClientID:    cfg.CLIClientID,
		RedirectURI: cliRedirectURI,
		Scopes:      cfg.Scopes,
		HTTPClient:  client,
	})
}

func loopbackSignIn(ctx context.Context, rp *oidcrp.Client, port int, out io.Writer, open func(context.Context, string) error) (*oidcrp.TokenSet, error) {
	ctx, cancel := context.WithTimeout(ctx, loopbackTimeout)
	defer cancel()
	return rp.RunLoopback(ctx, oidcrp.LoopbackConfig{
		Port:   port,
		Prompt: "select_account",
		Open: func(ctx context.Context, authURL string) error {
			_, _ = fmt.Fprintf(out, "Open this URL to sign in:\n  %s\n", authURL)
			if err := open(ctx, authURL); err != nil {
				_, _ = fmt.Fprintln(out, "Could not open a browser; open the URL above.")
			}
			return nil
		},
	})
}

func readPassword(cmd *cobra.Command, reader *bufio.Reader, stdinMode bool) (string, error) {
	if stdinMode {
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	if term.IsTerminal(int(syscall.Stdin)) { //nolint:unconvert // syscall.Stdin is uintptr on Windows.
		_, _ = fmt.Fprint(cmd.OutOrStdout(), "Password: ")
		buf, err := term.ReadPassword(int(syscall.Stdin)) //nolint:unconvert // same reason.
		_, _ = fmt.Fprintln(cmd.OutOrStdout())
		if err != nil {
			return "", err
		}
		return string(buf), nil
	}
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func newAuthLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Revoke the profile's refresh token at Othra and remove the profile",
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := sessionStore(cmd)
			if err != nil {
				return err
			}
			return logout(cmd.Context(), store, profileName(cmd), operatorHTTPClient(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
}

func newAuthWhoamiCmd(bootClient ClientBootFn) *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show who the current credential acts as",
		RunE: func(cmd *cobra.Command, _ []string) error {
			in, err := invocationFrom(cmd)
			if err != nil {
				return err
			}
			return whoami(cmd.Context(), in, bootClient, cmd.OutOrStdout(), render.Detect(cmd))
		},
	}
}
