// Command protoc-gen-mcp generates MCP tool bindings for every RPC annotated with mcp.v1.tool.
package main

import (
	"google.golang.org/protobuf/compiler/protogen"

	"altalune.id/opensheet/cmd/protoc-gen-mcp/internal/gen"
)

func main() {
	var opts gen.Options
	protogen.Options{ParamFunc: opts.Set}.Run(func(p *protogen.Plugin) error {
		return gen.Generate(p, opts)
	})
}
