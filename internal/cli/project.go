package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"altalune.id/template/internal/cli/render"
	"altalune.id/template/internal/platform/tenant"
	"altalune.id/template/internal/project"
)

// TODO(future-proto): switch to bootClient.Conn once api/project/v1 lands.
func newProjectCmd(bootServer ServerBootFn, bootClient ClientBootFn) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "project",
		Short:   "Manage projects within the active org",
		GroupID: "tenancy",
	}
	cmd.AddCommand(
		newProjectListCmd(bootServer, bootClient),
		newProjectCreateCmd(bootServer, bootClient),
	)
	return cmd
}

func newProjectListCmd(bootServer ServerBootFn, bootClient ClientBootFn) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List projects in the active org",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := withPrincipal(cmd, bootClient, true)
			if err != nil {
				return err
			}
			cfg, _ := withCfg(cmd)
			s, err := bootServer(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			defer func() { _ = s.Close() }()

			tc := tenant.Context{OrgID: p.OrgID, ProjectID: p.ProjectID, UserID: p.UserID}
			ctx := tenant.Into(cmd.Context(), tc)
			items, err := s.Projects.List(ctx, p.OrgID)
			if err != nil {
				return err
			}
			format := render.Detect(cmd)
			switch format {
			case render.FormatJSON, render.FormatNDJSON:
				out := make([]map[string]any, 0, len(items))
				for _, it := range items {
					out = append(out, projectToMap(it))
				}
				return render.JSON(cmd.OutOrStdout(), out)
			default:
				rows := make([][]string, 0, len(items))
				for _, it := range items {
					rows = append(rows, []string{it.Slug, it.Name, it.CreatedAt.Format("2006-01-02")})
				}
				return render.Table(cmd.OutOrStdout(), []string{"SLUG", "NAME", "CREATED"}, rows)
			}
		},
	}
}

func newProjectCreateCmd(bootServer ServerBootFn, bootClient ClientBootFn) *cobra.Command {
	var slug, name string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new project in the active org",
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := withPrincipal(cmd, bootClient, true)
			if err != nil {
				return err
			}
			cfg, _ := withCfg(cmd)
			s, err := bootServer(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			defer func() { _ = s.Close() }()

			tc := tenant.Context{OrgID: p.OrgID, ProjectID: p.ProjectID, UserID: p.UserID}
			ctx := tenant.Into(cmd.Context(), tc)
			pr, err := s.Projects.Create(ctx, p.OrgID, slug, name)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Created project %s (%s)\n", pr.Slug, pr.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&slug, "slug", "", "URL-safe project slug (required)")
	cmd.Flags().StringVar(&name, "name", "", "Human-readable project name (required)")
	_ = cmd.MarkFlagRequired("slug")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func projectToMap(p *project.Project) map[string]any {
	return map[string]any{
		"id":         p.ID.String(),
		"org_id":     p.OrgID.String(),
		"slug":       p.Slug,
		"name":       p.Name,
		"created_at": p.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}
