// Package gen turns MCP-annotated RPCs into mcp.ToolSpec registrations over their Connect handlers.
package gen

import (
	"cmp"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/pluginpb"

	mcpv1 "altalune.id/opensheet/gen/go/mcp/v1"
)

const (
	contextPackage   = protogen.GoImportPath("context")
	jsonPackage      = protogen.GoImportPath("encoding/json")
	fmtPackage       = protogen.GoImportPath("fmt")
	connectPackage   = protogen.GoImportPath("connectrpc.com/connect")
	protojsonPackage = protogen.GoImportPath("google.golang.org/protobuf/encoding/protojson")
)

type tool struct {
	name        string
	constName   string
	title       string
	description string
	mutation    bool
	destructive bool
	method      *protogen.Method
	schema      json.RawMessage
	uiResource  string
}

// Generate writes one MCP binding file per service that carries at least one annotated method.
func Generate(p *protogen.Plugin, opts Options) error {
	p.SupportedFeatures = uint64(pluginpb.CodeGeneratorResponse_FEATURE_PROTO3_OPTIONAL)

	taken := newClaimed()
	for _, file := range p.Files {
		if !file.Generate {
			continue
		}
		for _, svc := range file.Services {
			tools, err := serviceTools(svc, generatedPackage(file), taken, opts)
			if err != nil {
				return err
			}
			if len(tools) == 0 {
				continue
			}
			writeService(p, file, svc, tools, opts.runtimeImportPath())
		}
	}
	return nil
}

type claimed struct {
	tools  map[string]string
	consts map[string]string
}

func newClaimed() *claimed {
	return &claimed{tools: map[string]string{}, consts: map[string]string{}}
}

func (n *claimed) claim(pkg string, t tool, owner string) error {
	if prior, clash := n.tools[t.name]; clash {
		return fmt.Errorf("%s: tool name %q is already used by %s", owner, t.name, prior)
	}
	key := pkg + "." + t.constName
	if prior, clash := n.consts[key]; clash {
		return fmt.Errorf("%s: tool %q generates constant %s, which %s already generates", owner, t.name, t.constName, prior)
	}
	n.tools[t.name] = owner
	n.consts[key] = owner
	return nil
}

func serviceTools(svc *protogen.Service, pkg string, taken *claimed, opts Options) ([]tool, error) {
	var tools []tool
	for _, method := range svc.Methods {
		spec, annotated := toolAnnotation(method)
		if !annotated {
			continue
		}
		built, err := newTool(method, spec, opts)
		if err != nil {
			return nil, err
		}
		if err := taken.claim(pkg, built, string(method.Desc.FullName())); err != nil {
			return nil, err
		}
		tools = append(tools, built)
	}
	return tools, nil
}

func toolAnnotation(method *protogen.Method) (*mcpv1.Tool, bool) {
	opts := method.Desc.Options()
	if opts == nil || !proto.HasExtension(opts, mcpv1.E_Tool) {
		return nil, false
	}
	spec, ok := proto.GetExtension(opts, mcpv1.E_Tool).(*mcpv1.Tool)
	if !ok || spec == nil {
		return nil, false
	}
	return spec, true
}

//nolint:gochecknoglobals // compiled once; a package-level regexp is the idiom.
var uiNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

func uiResourceURI(ui, prefix, method string) (string, error) {
	if ui == "" {
		return "", nil
	}
	if !uiNameRe.MatchString(ui) {
		return "", fmt.Errorf("%s: ui %q must match %s", method, ui, uiNameRe)
	}
	if prefix == "" {
		return "", fmt.Errorf("%s: ui %q set but ui_prefix plugin parameter is absent", method, ui)
	}
	return prefix + "/" + ui, nil
}

func newTool(method *protogen.Method, spec *mcpv1.Tool, opts Options) (tool, error) {
	full := method.Desc.FullName()
	if method.Desc.IsStreamingClient() || method.Desc.IsStreamingServer() {
		return tool{}, fmt.Errorf("%s: an MCP tool must be a unary RPC", full)
	}
	uiURI, err := uiResourceURI(spec.GetUi(), opts.UIPrefix, string(full))
	if err != nil {
		return tool{}, err
	}

	name := cmp.Or(spec.GetName(), snakeCase(method.GoName))
	if !toolNamePattern.MatchString(name) {
		return tool{}, fmt.Errorf("%s: tool name %q must match %s", full, name, toolNamePattern.String())
	}

	required, unknown := requiredProperties(method.Input, spec.GetRequired())
	if len(unknown) > 0 {
		return tool{}, fmt.Errorf("%s: tool %q marks %v required, but %s has no such JSON field; it has %v",
			full, name, unknown, method.Input.Desc.FullName(), jsonFieldNames(method.Input))
	}

	description := cmp.Or(spec.GetDescription(), comment(method.Comments.Leading))
	if description == "" {
		return tool{}, fmt.Errorf("%s: tool %q has no description; set the tool option's description or give the rpc a leading comment", full, name)
	}

	schema, err := inputSchema(method.Input, required)
	if err != nil {
		return tool{}, fmt.Errorf("%s: building the input schema: %w", full, err)
	}

	return tool{
		name:        name,
		constName:   pascalCase(name) + "ToolName",
		title:       spec.GetTitle(),
		description: description,
		mutation:    spec.GetMutation(),
		destructive: spec.GetDestructive(),
		method:      method,
		schema:      schema,
		uiResource:  uiURI,
	}, nil
}

func generatedPackage(file *protogen.File) string {
	return string(file.GoImportPath) + "/" + string(file.GoPackageName) + "mcp"
}

func writeService(p *protogen.Plugin, file *protogen.File, svc *protogen.Service, tools []tool, runtime protogen.GoImportPath) {
	pkg := string(file.GoPackageName) + "mcp"
	importPath := protogen.GoImportPath(generatedPackage(file))
	filename := path.Join(path.Dir(file.GeneratedFilenamePrefix), pkg, snakeCase(svc.GoName)+".mcp.go")
	// NOTE: mirrors how protoc-gen-connect-go derives its own output package, so the two move in
	// lockstep under managed mode; a non-default `package_suffix=` opt would break the pairing.
	connect := protogen.GoImportPath(string(file.GoImportPath) + "/" + string(file.GoPackageName) + "connect")

	g := p.NewGeneratedFile(filename, importPath)
	g.P("// Code generated by protoc-gen-mcp. DO NOT EDIT.")
	g.P("//")
	g.P("// Source: ", file.Desc.Path())
	g.P()
	g.P("package ", pkg)
	g.P()

	for _, t := range tools {
		g.P("// ", t.constName, " is the MCP tool name for ", svc.GoName, ".", t.method.GoName, ".")
		g.P("const ", t.constName, " = ", strconv.Quote(t.name))
		g.P()
	}

	constNames := make([]string, 0, len(tools))
	for _, t := range tools {
		constNames = append(constNames, t.constName)
	}
	g.P("// ", svc.GoName, "ToolNames returns every MCP tool name generated for ", svc.GoName, ".")
	g.P("func ", svc.GoName, "ToolNames() []string {")
	g.P("return []string{", strings.Join(constNames, ", "), "}")
	g.P("}")
	g.P()

	writeRegister(g, svc, tools, connect, runtime)
}

func writeRegister(g *protogen.GeneratedFile, svc *protogen.Service, tools []tool, connect, runtime protogen.GoImportPath) {
	rawMessage := g.QualifiedGoIdent(jsonPackage.Ident("RawMessage"))
	errorf := g.QualifiedGoIdent(fmtPackage.Ident("Errorf"))

	g.P("// Register", svc.GoName, "Tools registers every MCP-annotated ", svc.GoName,
		" method on reg, against h and under the scope scopeFor resolves for each tool name.")
	g.P("// SECURITY: this file declares no scope of its own; scopeFor is the single catalog every tool's scope comes from.")
	g.P("func Register", svc.GoName, "Tools(reg *", g.QualifiedGoIdent(runtime.Ident("Registry")),
		", h ", g.QualifiedGoIdent(connect.Ident(svc.GoName+"Handler")), ", scopeFor func(string) string) {")
	for _, t := range tools {
		g.P("reg.Register(", g.QualifiedGoIdent(runtime.Ident("ToolSpec")), "{")
		g.P("Name: ", t.constName, ",")
		if t.title != "" {
			g.P("Title: ", strconv.Quote(t.title), ",")
		}
		g.P("Description: ", strconv.Quote(t.description), ",")
		g.P("Scope: scopeFor(", t.constName, "),")
		g.P("Mutation: ", t.mutation, ",")
		g.P("Destructive: ", t.destructive, ",")
		if t.uiResource != "" {
			g.P("UI: ", strconv.Quote(t.uiResource), ",")
		}
		g.P("InputSchema: ", rawMessage, "(", goStringLiteral(string(t.schema)), "),")
		g.P("Handler: func(ctx ", g.QualifiedGoIdent(contextPackage.Ident("Context")), ", input ", rawMessage,
			") (", rawMessage, ", error) {")
		g.P("if len(input) == 0 {")
		g.P("input = ", rawMessage, "(`{}`)")
		g.P("}")
		g.P("var req ", g.QualifiedGoIdent(t.method.Input.GoIdent))
		g.P("if err := (", g.QualifiedGoIdent(protojsonPackage.Ident("UnmarshalOptions")),
			"{DiscardUnknown: false}).Unmarshal(input, &req); err != nil {")
		g.P("return nil, ", g.QualifiedGoIdent(runtime.Ident("NewInvalidArgumentsError")), "(", t.constName, ", err)")
		g.P("}")
		g.P("resp, err := h.", t.method.GoName, "(ctx, ",
			g.QualifiedGoIdent(connectPackage.Ident("NewRequest")), "(&req))")
		g.P("if err != nil {")
		g.P("return nil, err")
		g.P("}")
		g.P("body, err := ", g.QualifiedGoIdent(protojsonPackage.Ident("Marshal")), "(resp.Msg)")
		g.P("if err != nil {")
		g.P("return nil, ", errorf, "(\"mcp: %s: encode result: %w\", ", t.constName, ", err)")
		g.P("}")
		g.P("return ", rawMessage, "(body), nil")
		g.P("},")
		g.P("}, h)")
	}
	g.P("}")
}

func goStringLiteral(s string) string {
	if strings.Contains(s, "`") || strings.Contains(s, "\n") {
		return strconv.Quote(s)
	}
	return "`" + s + "`"
}
