package cli

import (
	"github.com/spf13/cobra"

	"altalune.id/opensheet/internal/published"
)

func publishedDomainCmds(bootClient ClientBootFn) []*cobra.Command {
	var cmds []*cobra.Command
	if published.Todo {
		cmds = append(cmds, newTodoCmd(bootClient))
	}
	return cmds
}
