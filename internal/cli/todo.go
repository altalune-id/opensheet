package cli

import (
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/spf13/cobra"

	todov1 "altalune.id/template/gen/go/todo/v1"
	"altalune.id/template/internal/cli/render"
)

func newTodoCmd(_ ServerBootFn, bootClient ClientBootFn) *cobra.Command {
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
			p, err := withPrincipal(cmd, bootClient, true)
			if err != nil {
				return err
			}
			if p.ProjectID == uuid.Nil {
				return errors.New("todo list: no active project — pass --project or run `altempl auth login`")
			}
			conn, err := connFromCmd(cmd, bootClient)
			if err != nil {
				return err
			}
			resp, err := conn.Todo.List(cmd.Context(), connect.NewRequest(&todov1.ListRequest{
				ProjectId: p.ProjectID.String(),
			}))
			if err != nil {
				return err
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
			p, err := withPrincipal(cmd, bootClient, true)
			if err != nil {
				return err
			}
			if p.ProjectID == uuid.Nil {
				return errors.New("todo add: no active project — pass --project or run `altempl auth login`")
			}
			conn, err := connFromCmd(cmd, bootClient)
			if err != nil {
				return err
			}
			resp, err := conn.Todo.Create(cmd.Context(), connect.NewRequest(&todov1.CreateRequest{
				ProjectId: p.ProjectID.String(),
				Title:     title,
			}))
			if err != nil {
				return err
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
			if _, err := withPrincipal(cmd, bootClient, true); err != nil {
				return err
			}
			conn, err := connFromCmd(cmd, bootClient)
			if err != nil {
				return err
			}
			resp, err := conn.Todo.Toggle(cmd.Context(), connect.NewRequest(&todov1.ToggleRequest{
				TodoId: args[0],
			}))
			if err != nil {
				return err
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
			if _, err := withPrincipal(cmd, bootClient, true); err != nil {
				return err
			}
			conn, err := connFromCmd(cmd, bootClient)
			if err != nil {
				return err
			}
			if _, err := conn.Todo.Delete(cmd.Context(), connect.NewRequest(&todov1.DeleteRequest{
				TodoId: args[0],
			})); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Deleted todo %s\n", args[0])
			return nil
		},
	}
}

func protoTodoToMap(t *todov1.Todo) map[string]any {
	out := map[string]any{
		"id":         t.GetId(),
		"project_id": t.GetProjectId(),
		"title":      t.GetTitle(),
		"done":       t.GetDone(),
	}
	if ts := t.GetCreatedAt(); ts != nil {
		out["created_at"] = ts.AsTime().Format("2006-01-02T15:04:05Z07:00")
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
