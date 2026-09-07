package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"altalune.id/template/internal/cli/render"
	"altalune.id/template/internal/org"
	"altalune.id/template/internal/platform/tenant"
)

// TODO(future-proto): switch to bootClient.Conn once api/org/v1 lands.
func newOrgCmd(bootServer ServerBootFn, bootClient ClientBootFn) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "org",
		Short:   "Manage organizations",
		GroupID: "tenancy",
	}
	cmd.AddCommand(
		newOrgListCmd(bootServer, bootClient),
		newOrgCreateCmd(bootServer, bootClient),
	)
	return cmd
}

func newOrgListCmd(bootServer ServerBootFn, bootClient ClientBootFn) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List orgs the caller is a member of",
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

			orgs, err := s.Orgs.List(cmd.Context(), p.UserID)
			if err != nil {
				return err
			}
			format := render.Detect(cmd)
			switch format {
			case render.FormatJSON, render.FormatNDJSON:
				out := make([]map[string]any, 0, len(orgs))
				for _, o := range orgs {
					out = append(out, orgToMap(o))
				}
				return render.JSON(cmd.OutOrStdout(), out)
			default:
				rows := make([][]string, 0, len(orgs))
				for _, o := range orgs {
					rows = append(rows, []string{o.Slug, o.Name, o.CreatedAt.Format("2006-01-02")})
				}
				return render.Table(cmd.OutOrStdout(), []string{"SLUG", "NAME", "CREATED"}, rows)
			}
		},
	}
}

func newOrgCreateCmd(bootServer ServerBootFn, bootClient ClientBootFn) *cobra.Command {
	var slug, name string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new organization",
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
			o, err := s.Orgs.Create(ctx, org.CreateRequest{Slug: slug, Name: name, OwnerID: p.UserID})
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Created org %s (%s)\n", o.Slug, o.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&slug, "slug", "", "URL-safe org slug (required)")
	cmd.Flags().StringVar(&name, "name", "", "Human-readable org name (required)")
	_ = cmd.MarkFlagRequired("slug")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func orgToMap(o *org.Org) map[string]any {
	return map[string]any{
		"id":         o.ID.String(),
		"slug":       o.Slug,
		"name":       o.Name,
		"owner_id":   o.OwnerID.String(),
		"created_at": o.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}
