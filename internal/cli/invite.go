package cli

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"altalune.id/template/internal/cli/render"
	"altalune.id/template/internal/invite"
	"altalune.id/template/internal/platform/tenant"
)

// TODO(future-proto): switch to bootClient.Conn once api/invite/v1 lands.
func newInviteCmd(bootServer ServerBootFn, bootClient ClientBootFn) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "invite",
		Short:   "Manage invitations to the active org",
		GroupID: "tenancy",
	}
	cmd.AddCommand(
		newInviteListCmd(bootServer, bootClient),
		newInviteSendCmd(bootServer, bootClient),
		newInviteRevokeCmd(bootServer, bootClient),
	)
	return cmd
}

func newInviteListCmd(bootServer ServerBootFn, bootClient ClientBootFn) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List pending invites for the active org",
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
			items, err := s.Invites.ListPending(ctx)
			if err != nil {
				return err
			}
			format := render.Detect(cmd)
			switch format {
			case render.FormatJSON, render.FormatNDJSON:
				out := make([]map[string]any, 0, len(items))
				for _, it := range items {
					out = append(out, inviteToMap(it))
				}
				return render.JSON(cmd.OutOrStdout(), out)
			default:
				rows := make([][]string, 0, len(items))
				for _, inv := range items {
					status := "pending"
					if inv.IsUsed() {
						status = "accepted"
					}
					rows = append(rows, []string{
						inv.ID.String(), inv.Email, string(inv.Role), status,
						inv.ExpiresAt.Format("2006-01-02"),
					})
				}
				return render.Table(cmd.OutOrStdout(), []string{"ID", "EMAIL", "ROLE", "STATUS", "EXPIRES"}, rows)
			}
		},
	}
}

func newInviteSendCmd(bootServer ServerBootFn, bootClient ClientBootFn) *cobra.Command {
	var email, role string
	cmd := &cobra.Command{
		Use:   "send",
		Short: "Send an invite to a new member of the active org",
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
			inv, err := s.Invites.Send(ctx, invite.SendRequest{Email: email, Role: invite.Role(role)})
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Invite queued: id=%s email=%s role=%s\n", inv.ID, inv.Email, inv.Role)
			return nil
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "Invitee email (required)")
	cmd.Flags().StringVar(&role, "role", "member", "Role granted on accept: owner|admin|member")
	_ = cmd.MarkFlagRequired("email")
	return cmd
}

func newInviteRevokeCmd(bootServer ServerBootFn, bootClient ClientBootFn) *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <id>",
		Short: "Revoke a pending invite",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := uuid.Parse(args[0])
			if err != nil {
				return fmt.Errorf("invite revoke: %q is not a uuid: %w", args[0], err)
			}
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
			if err := s.Invites.Revoke(ctx, id); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Revoked invite %s\n", id)
			return nil
		},
	}
}

func inviteToMap(inv *invite.Invite) map[string]any {
	status := "pending"
	if inv.IsUsed() {
		status = "accepted"
	}
	return map[string]any{
		"id":         inv.ID.String(),
		"org_id":     inv.OrgID.String(),
		"email":      inv.Email,
		"role":       string(inv.Role),
		"status":     status,
		"expires_at": inv.ExpiresAt.Format("2006-01-02T15:04:05Z07:00"),
		"created_at": inv.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}
