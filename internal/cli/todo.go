package cli

import (
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/spf13/cobra"

	authv1 "altalune.id/opensheet/gen/go/auth/v1"
	todov1 "altalune.id/opensheet/gen/go/todo/v1"
	"altalune.id/opensheet/internal/cli/render"
	"altalune.id/opensheet/internal/controlplane"
)

func newTodoCmd(bootClient ClientBootFn) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "todo",
		Short:   "Manage todos in the active project",
		GroupID: "domain",
	}
	cmd.AddCommand(
		newTodoListCmd(bootClient),
		newTodoAddCmd(bootClient),
		newTodoToggleCmd(bootClient),
		newTodoDeleteCmd(bootClient),
	)
	return cmd
}

func newTodoListCmd(bootClient ClientBootFn) *cobra.Command {
	var doneFlag, openFlag bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List todos in the active project",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if doneFlag && openFlag {
				return errors.New("todo list: --done and --open are mutually exclusive")
			}
			conn, err := connFromCmd(cmd, bootClient)
			if err != nil {
				return err
			}
			projectID, err := activeProjectID(cmd, conn, "todo list")
			if err != nil {
				return err
			}
			resp, err := conn.Todo.List(cmd.Context(), connect.NewRequest(&todov1.ListRequest{
				ProjectId: projectID,
			}))
			if err != nil {
				return withHint(err)
			}
			items := filterTodos(resp.Msg.GetTodos(), doneFlag, openFlag)
			format := render.Detect(cmd)
			switch format {
			case render.FormatJSON, render.FormatNDJSON:
				out := make([]map[string]any, 0, len(items))
				for _, t := range items {
					out = append(out, protoTodoToMap(t))
				}
				return render.JSON(cmd.OutOrStdout(), out)
			default:
				rows := make([][]string, 0, len(items))
				for _, t := range items {
					done := " "
					if t.GetDone() {
						done = "x"
					}
					rows = append(rows, []string{t.GetId(), "[" + done + "]", t.GetTitle()})
				}
				return render.Table(cmd.OutOrStdout(), []string{"ID", "DONE", "TITLE"}, rows)
			}
		},
	}
	cmd.Flags().BoolVar(&doneFlag, "done", false, "Only completed todos")
	cmd.Flags().BoolVar(&openFlag, "open", false, "Only open todos")
	return cmd
}

func newTodoAddCmd(bootClient ClientBootFn) *cobra.Command {
	return &cobra.Command{
		Use:   "add <title>",
		Short: "Create a new todo",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			title := strings.Join(args, " ")
			conn, err := connFromCmd(cmd, bootClient)
			if err != nil {
				return err
			}
			projectID, err := activeProjectID(cmd, conn, "todo add")
			if err != nil {
				return err
			}
			resp, err := conn.Todo.Create(cmd.Context(), connect.NewRequest(&todov1.CreateRequest{
				ProjectId: projectID,
				Title:     title,
			}))
			if err != nil {
				return withHint(err)
			}
			t := resp.Msg.GetTodo()
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Added todo %s: %s\n", t.GetId(), t.GetTitle())
			return nil
		},
	}
}

func newTodoToggleCmd(bootClient ClientBootFn) *cobra.Command {
	return &cobra.Command{
		Use:   "toggle <id>",
		Short: "Flip the done flag on a todo",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := uuid.Parse(args[0]); err != nil {
				return fmt.Errorf("todo toggle: %q is not a uuid: %w", args[0], err)
			}
			conn, err := connFromCmd(cmd, bootClient)
			if err != nil {
				return err
			}
			resp, err := conn.Todo.Toggle(cmd.Context(), connect.NewRequest(&todov1.ToggleRequest{
				TodoId: args[0],
			}))
			if err != nil {
				return withHint(err)
			}
			t := resp.Msg.GetTodo()
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s done=%v\n", t.GetId(), t.GetDone())
			return nil
		},
	}
}

func newTodoDeleteCmd(bootClient ClientBootFn) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a todo",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := uuid.Parse(args[0]); err != nil {
				return fmt.Errorf("todo delete: %q is not a uuid: %w", args[0], err)
			}
			conn, err := connFromCmd(cmd, bootClient)
			if err != nil {
				return err
			}
			if _, err := conn.Todo.Delete(cmd.Context(), connect.NewRequest(&todov1.DeleteRequest{
				TodoId: args[0],
			})); err != nil {
				return withHint(err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Deleted todo %s\n", args[0])
			return nil
		},
	}
}

func activeProjectID(cmd *cobra.Command, conn *controlplane.Client, op string) (string, error) {
	resp, err := conn.Auth.Whoami(cmd.Context(), connect.NewRequest(&authv1.WhoamiRequest{}))
	if err != nil {
		return "", withHint(err)
	}
	if id := resp.Msg.GetActiveProjectId(); id != "" {
		return id, nil
	}
	return "", errors.New(op + ": the server resolved no active project for this credential")
}

func protoTodoToMap(t *todov1.Todo) map[string]any {
	out := map[string]any{
		"id":         t.GetId(),
		"project_id": t.GetProjectId(),
		"title":      t.GetTitle(),
		"done":       t.GetDone(),
	}
	if ts := t.GetCreatedAt(); ts != nil {
		out["created_at"] = rfc3339UTC(ts.AsTime())
	}
	return out
}

func filterTodos(items []*todov1.Todo, doneOnly, openOnly bool) []*todov1.Todo {
	if !doneOnly && !openOnly {
		return items
	}
	out := make([]*todov1.Todo, 0, len(items))
	for _, t := range items {
		if doneOnly && !t.GetDone() {
			continue
		}
		if openOnly && t.GetDone() {
			continue
		}
		out = append(out, t)
	}
	return out
}
