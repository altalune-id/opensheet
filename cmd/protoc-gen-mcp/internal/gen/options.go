package gen

import (
	"cmp"
	"fmt"
	"net/url"
	"strings"

	"google.golang.org/protobuf/compiler/protogen"
)

// DefaultRuntimePackage is the import path of the MCP runtime the generated bindings call into.
const DefaultRuntimePackage = "altalune.id/opensheet/mcp"

// Options are the plugin parameters protoc passes through ParamFunc.
type Options struct {
	UIPrefix       string
	RuntimePackage string
}

// Set records one plugin parameter; it rejects anything the generator does not define.
func (o *Options) Set(name, value string) error {
	switch name {
	case "ui_prefix":
		return o.setUIPrefix(value)
	case "runtime_package":
		return o.setRuntimePackage(value)
	default:
		return fmt.Errorf("unknown parameter %q", name)
	}
}

func (o *Options) setUIPrefix(value string) error {
	u, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("ui_prefix %q: %w", value, err)
	}
	if u.Scheme != "ui" {
		return fmt.Errorf("ui_prefix %q: scheme must be ui", value)
	}
	o.UIPrefix = value
	return nil
}

func (o *Options) setRuntimePackage(value string) error {
	if value == "" {
		return fmt.Errorf("runtime_package %q: must name a Go import path", value)
	}
	if strings.ContainsAny(value, " \t\n\r\"'`\\") {
		return fmt.Errorf("runtime_package %q: must name a Go import path", value)
	}
	o.RuntimePackage = value
	return nil
}

func (o Options) runtimeImportPath() protogen.GoImportPath {
	return protogen.GoImportPath(cmp.Or(o.RuntimePackage, DefaultRuntimePackage))
}
