package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"altalune.id/template/authl"
	"altalune.id/template/internal/auth"
	"altalune.id/template/internal/boot"
	"altalune.id/template/internal/cli/render"
	"altalune.id/template/internal/platform/session"
)

func newAuthCmd(bootServer ServerBootFn, bootClient ClientBootFn) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "auth",
		Short:   "Manage authentication (login, logout, whoami, token)",
		GroupID: "auth",
	}
	cmd.AddCommand(
		newAuthLoginCmd(bootServer),
		newAuthLogoutCmd(bootServer),
		newAuthWhoamiCmd(bootServer, bootClient),
		newAuthTokenCmd(bootServer),
	)
	return cmd
}

func newAuthLoginCmd(bootServer ServerBootFn) *cobra.Command {
	var admin bool
	var printToken bool
	var email string
	var passwordStdin bool
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Sign in with OIDC (default) or the local genesis admin (--admin)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := withCfg(cmd)
			if err != nil {
				return err
			}
			s, err := bootServer(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			defer func() { _ = s.Close() }()
			return runLogin(cmd, s, loginOpts{admin: admin, printToken: printToken, email: email, passwordStdin: passwordStdin})
		},
	}
	cmd.Flags().BoolVar(&admin, "admin", false, "Break-glass: force local genesis login even when OIDC is configured")
	cmd.Flags().BoolVar(&printToken, "print-token", false, "Print the bearer token on success (for scripts)")
	cmd.Flags().StringVar(&email, "email", "", "Email for local login (skips prompt)")
	cmd.Flags().BoolVar(&passwordStdin, "password-stdin", false, "Read password from stdin (unmasked)")
	return cmd
}

type loginOpts struct {
	admin         bool
	printToken    bool
	email         string
	passwordStdin bool
}

func runLogin(cmd *cobra.Command, s *boot.Server, o loginOpts) error {
	if o.admin || !s.Caps.ExternalIdentity {
		if !s.Auth.LocalConfigured() {
			return errors.New("login: local admin login not configured; set ALT_GENESIS_EMAIL / ALT_GENESIS_PASSWORD")
		}
		if o.admin && s.Caps.ExternalIdentity {
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "warn: --admin break-glass login (OIDC bypassed for this session)")
		}
		return runLocalPrompt(cmd, s, o)
	}
	return runLoopback(cmd.Context(), cmd, s, o)
}

func runLocalPrompt(cmd *cobra.Command, s *boot.Server, o loginOpts) error {
	reader := bufio.NewReader(os.Stdin)
	emailAddr := strings.TrimSpace(o.email)
	if emailAddr == "" {
		_, _ = fmt.Fprint(cmd.OutOrStdout(), "Email: ")
		line, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("login: read email: %w", err)
		}
		emailAddr = strings.TrimSpace(line)
	}
	pw, err := readPassword(cmd, reader, o.passwordStdin)
	if err != nil {
		return fmt.Errorf("login: read password: %w", err)
	}
	p, err := s.Auth.LoginLocal(cmd.Context(), auth.Credentials{Email: emailAddr, Password: pw})
	if err != nil {
		if auth.IsInvalidCredentialsError(err) {
			return errors.New("login: invalid credentials")
		}
		return err
	}
	if err := saveSessionFile(s.Cfg.Session.Path, &sessionFile{Principal: p, IssuedAt: time.Now().UTC()}); err != nil {
		return fmt.Errorf("login: persist session: %w", err)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Signed in as %s (%s)\n", p.Email, p.Source)
	return nil
}

func runLoopback(ctx context.Context, cmd *cobra.Command, s *boot.Server, _ loginOpts) error {
	if s.Platform == nil || s.Platform.AltAuth == nil {
		return errors.New("login: OIDC not configured (oidc.issuer / oidc.clientID)")
	}
	opts := []authl.LoopbackOption{}
	if p := s.Cfg.OIDC.RedirectPort; p > 0 {
		opts = append(opts, authl.WithLoopbackPort(p))
	}
	ident, err := s.Platform.AltAuth.RunLoopback(ctx, opts...)
	if err != nil {
		return fmt.Errorf("login: loopback: %w", err)
	}
	p, err := s.Auth.LoginOIDC(ctx, auth.OIDCClaims{
		Issuer:  s.Cfg.OIDC.Issuer,
		Subject: ident.Subject,
		Email:   ident.Email,
		Name:    ident.Name,
	})
	if err != nil {
		if auth.IsNotInvitedError(err) {
			return errors.New("login: not invited to this deployment; ask an admin to send an invite")
		}
		return err
	}
	if err := saveSessionFile(s.Cfg.Session.Path, &sessionFile{Principal: p, IssuedAt: time.Now().UTC()}); err != nil {
		return fmt.Errorf("login: persist session: %w", err)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Signed in as %s (%s)\n", p.Email, p.Source)
	return nil
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

func newAuthLogoutCmd(bootServer ServerBootFn) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Clear the local session file",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := withCfg(cmd)
			if err != nil {
				return err
			}
			s, err := bootServer(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			defer func() { _ = s.Close() }()
			if err := clearSessionFile(s.Cfg.Session.Path); err != nil {
				return err
			}
			cmd.Println("Signed out.")
			return nil
		},
	}
}

func newAuthWhoamiCmd(_ ServerBootFn, bootClient ClientBootFn) *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the current signed-in user",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := withCfg(cmd)
			if err != nil {
				return err
			}
			p, err := Resolve(cmd.Context(), cmd, cfg, bootClient)
			if err != nil {
				return err
			}
			format := render.Detect(cmd)
			switch format {
			case render.FormatJSON, render.FormatNDJSON:
				return render.JSON(cmd.OutOrStdout(), whoamiPayload(p, cfg.Session.Path))
			default:
				_, _ = fmt.Fprintf(cmd.OutOrStdout(),
					"user_id: %s\nemail:   %s\nname:    %s\nsource:  %s\nsession: %s\n",
					p.UserID, p.Email, p.Name, p.Source, cfg.Session.Path)
				return nil
			}
		},
	}
}

func whoamiPayload(p Principal, sessionPath string) map[string]any {
	return map[string]any{
		"user_id":      p.UserID.String(),
		"email":        p.Email,
		"name":         p.Name,
		"source":       string(p.Source),
		"session_path": sessionPath,
	}
}

func newAuthTokenCmd(bootServer ServerBootFn) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Mint or inspect API tokens",
	}
	cmd.AddCommand(newAuthTokenMintCmd(bootServer))
	return cmd
}

func newAuthTokenMintCmd(_ ServerBootFn) *cobra.Command {
	var ttl time.Duration
	cmd := &cobra.Command{
		Use:   "mint",
		Short: "Mint a short-lived API token for the signed-in user",
		RunE: func(cmd *cobra.Command, _ []string) error {
			// TODO(task-33): thread this through boot.Server.Tokens once the
			// issuer implements Mint. For now the command is a stub that
			// reports back the requested TTL and the session identity.
			cfg, err := withCfg(cmd)
			if err != nil {
				return err
			}
			sess, err := loadSessionFile(cfg.Session.Path)
			if err != nil {
				return err
			}
			if sess == nil {
				return ErrNotSignedIn
			}
			_ = session.SourceToken
			return errors.New("auth token mint: not implemented yet (blocked on Task 33 tokens issuer)")
		},
	}
	cmd.Flags().DurationVar(&ttl, "ttl", 15*time.Minute, "Token time-to-live (e.g. 15m, 1h)")
	return cmd
}
