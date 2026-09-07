package cli

import (
	"bufio"
	"errors"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"altalune.id/template/internal/onboard"
	"altalune.id/template/internal/platform/config"
	"altalune.id/template/internal/user"
)

func newInitCmd(bootServer ServerBootFn) *cobra.Command {
	var (
		email       string
		name        string
		orgSlug     string
		orgName     string
		projectSlug string
		interactive bool
	)
	cmd := &cobra.Command{
		Use:     "init",
		Short:   "Initialize altempl on first run — create the first admin, org, and project.",
		GroupID: "runtime",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := withCfg(cmd)
			if err != nil {
				return err
			}
			srv, err := bootServer(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			defer func() { _ = srv.Close() }()

			required, err := srv.Onboards.Required(cmd.Context())
			if err != nil {
				return err
			}
			if !required {
				return (&onboard.AlreadyOnboardedError{}).ToAppError()
			}

			if interactive {
				email = promptIfEmpty(cmd, "admin email", email)
				name = promptIfEmpty(cmd, "admin display name", name)
				orgSlug = promptIfEmpty(cmd, "org slug", orgSlug)
				orgName = promptIfEmpty(cmd, "org name", orgName)
				projectSlug = promptIfEmpty(cmd, "project slug", projectSlug)
			}
			if strings.TrimSpace(email) == "" {
				return errors.New("--email is required (use --interactive to prompt)")
			}
			if strings.TrimSpace(name) == "" {
				name = email
			}

			u, err := srv.Users.Create(cmd.Context(), user.CreateRequest{
				Email:  email,
				Name:   name,
				Source: user.SourceGenesis,
			})
			if err != nil {
				if user.IsAlreadyExistsError(err) {
					return (&onboard.AlreadyOnboardedError{}).ToAppError()
				}
				return err
			}

			if cfg.Mode == config.ModeSelfhosted && orgSlug != "" {
				if _, err := srv.Orgs.BootstrapSingleton(cmd.Context(), orgSlug, orgName, u.ID); err != nil {
					return err
				}
			}

			if _, err := srv.Onboards.Complete(cmd.Context(), u.ID, onboard.MethodCLIInit); err != nil {
				if onboard.IsAlreadyOnboardedError(err) {
					return (&onboard.AlreadyOnboardedError{}).ToAppError()
				}
				return err
			}

			cmd.Printf("altempl: onboarded admin=%s (org=%s, project=%s)\n", u.Email, orgSlug, projectSlug)
			return nil
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "admin email")
	cmd.Flags().StringVar(&name, "name", "", "admin display name (defaults to email when empty)")
	cmd.Flags().StringVar(&orgSlug, "org-slug", "default", "first org slug")
	cmd.Flags().StringVar(&orgName, "org-name", "Default Organization", "first org name")
	cmd.Flags().StringVar(&projectSlug, "project-slug", "default", "first project slug")
	cmd.Flags().BoolVar(&interactive, "interactive", false, "prompt for missing values on stdin")
	return cmd
}

//nolint:unparam // future callers may want to skip prompts on unmapped labels.
func promptIfEmpty(cmd *cobra.Command, label, current string) string {
	if strings.TrimSpace(current) != "" {
		return current
	}
	in := cmd.InOrStdin()
	if in == nil {
		in = os.Stdin
	}
	cmd.Printf("%s: ", label)
	sc := bufio.NewScanner(in)
	if sc.Scan() {
		return strings.TrimSpace(sc.Text())
	}
	return current
}
