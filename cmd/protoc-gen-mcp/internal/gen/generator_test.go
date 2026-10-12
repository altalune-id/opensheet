package gen

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"

	_ "altalune.id/opensheet/gen/go/mcp/v1"
)

const fixturePrefix = "ui://opensheettest"

//nolint:gochecknoglobals // a -update flag for golden files has to be package level.
var update = flag.Bool("update", false, "rewrite the golden files instead of comparing against them")

// SECURITY: every refusal below is a build-time gate. Deleting one ships a tool whose annotation
// and message have silently drifted apart, so each fixture must name the offending RPC.
func TestGenerateRefusals(t *testing.T) {
	tests := []struct {
		name  string
		file  string
		wants []string
	}{
		{
			name:  "tool name that fails the regex",
			file:  "opensheettest/v1/bad_name.proto",
			wants: []string{"ShoutLoudly", "Shout-Loudly"},
		},
		{
			name:  "duplicate tool name inside one service",
			file:  "opensheettest/v1/bad_dup.proto",
			wants: []string{"SecondTwin", "twin_tool", "FirstTwin"},
		},
		{
			name:  "streaming rpc",
			file:  "opensheettest/v1/bad_stream.proto",
			wants: []string{"WatchForever", "unary"},
		},
		{
			name:  "ui name that fails the regex",
			file:  "opensheettest/v1/bad_ui.proto",
			wants: []string{"BadUI", "App_1"},
		},
		{
			name:  "required naming a field the request message does not have",
			file:  "opensheettest/v1/bad_required.proto",
			wants: []string{"RequireGhost", "ghostField", "projectId"},
		},
		{
			name:  "no description and no leading comment",
			file:  "opensheettest/v1/bad_nodesc.proto",
			wants: []string{"Silent", "silent_tool", "description"},
		},
		{
			name:  "two tool names that generate one Go constant",
			file:  "opensheettest/v1/bad_constclash.proto",
			wants: []string{"SecondClash", "_twin", "TwinToolName", "FirstClash"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newPlugin(t, tt.file)
			err := Generate(p, Options{UIPrefix: fixturePrefix})
			if err == nil {
				t.Fatalf("Generate() error = nil, want a refusal")
			}
			for _, want := range tt.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

func TestGenerateRefusesDuplicateToolNamesAcrossFiles(t *testing.T) {
	p := newPlugin(t, "opensheettest/v1/fixture.proto", "opensheettest/v1/bad_echo.proto")
	err := Generate(p, Options{UIPrefix: fixturePrefix})
	if err == nil {
		t.Fatal("Generate() error = nil, want a refusal on the cross-file duplicate")
	}
	for _, want := range []string{"poke_target", "EchoTwice", "Poke"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

func TestGenerateRefusesUISetWithoutUIPrefix(t *testing.T) {
	p := newPlugin(t, "opensheettest/v1/fixture.proto")
	err := Generate(p, Options{})
	if err == nil {
		t.Fatal("Generate() = nil, want a refusal when ui is set but ui_prefix is absent")
	}
	if !strings.Contains(err.Error(), "ui_prefix") {
		t.Errorf("error %q must name ui_prefix", err)
	}
}

func TestGenerateWritesOneFilePerAnnotatedService(t *testing.T) {
	p := newPlugin(t, "opensheettest/v1/fixture.proto")
	if err := Generate(p, Options{UIPrefix: fixturePrefix}); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	resp := p.Response()
	if resp.Error != nil {
		t.Fatalf("response carries error %q", resp.GetError())
	}
	if len(resp.File) != 1 {
		t.Fatalf("got %d generated files, want 1", len(resp.File))
	}
	if got, want := resp.File[0].GetName(), "opensheettest/v1/opensheettestv1mcp/fixture_service.mcp.go"; got != want {
		t.Fatalf("generated file name = %q, want %q", got, want)
	}
	checkGolden(t, "fixture.mcp.go.golden", resp.File[0].GetContent())
}

func TestGenerateSkipsServicesWithoutAnnotatedMethods(t *testing.T) {
	p := newPlugin(t, "opensheettest/v1/unannotated.proto")
	if err := Generate(p, Options{UIPrefix: fixturePrefix}); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if got := len(p.Response().File); got != 0 {
		t.Fatalf("got %d generated files, want 0", got)
	}
}

func TestInputSchemas(t *testing.T) {
	tests := []struct{ name, file, golden string }{
		{"every scalar, nested and repeated shape", "opensheettest/v1/fixture.proto", "schemas.json.golden"},
		{"every well-known type", "opensheettest/v1/wellknown.proto", "wellknown_schemas.json.golden"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newPlugin(t, tt.file)

			schemas := map[string]json.RawMessage{}
			for _, file := range p.Files {
				if !file.Generate {
					continue
				}
				for _, svc := range file.Services {
					tools, err := serviceTools(svc, generatedPackage(file), newClaimed(), Options{UIPrefix: fixturePrefix})
					if err != nil {
						t.Fatalf("serviceTools(%s) error = %v", svc.Desc.FullName(), err)
					}
					for _, tl := range tools {
						schemas[tl.name] = tl.schema
					}
				}
			}

			got, err := json.MarshalIndent(schemas, "", "  ")
			if err != nil {
				t.Fatalf("MarshalIndent() error = %v", err)
			}
			checkGolden(t, tt.golden, string(got)+"\n")
		})
	}
}

func TestRequiredAcceptsEitherProtoJSONSpelling(t *testing.T) {
	p := newPlugin(t, "opensheettest/v1/wellknown.proto")

	for _, file := range p.Files {
		for _, svc := range file.Services {
			tools, err := serviceTools(svc, generatedPackage(file), newClaimed(), Options{UIPrefix: fixturePrefix})
			if err != nil {
				t.Fatalf("serviceTools(%s) error = %v", svc.Desc.FullName(), err)
			}
			for _, tl := range tools {
				if !strings.Contains(string(tl.schema), `"required":["at","bigUnsigned"]`) {
					t.Errorf("tool %q: the snake_case `big_unsigned` was not normalized to the property key:\n%s", tl.name, tl.schema)
				}
			}
		}
	}
}

func TestPascalCase(t *testing.T) {
	tests := []struct{ in, want string }{
		{"ping", "Ping"},
		{"todo_create", "TodoCreate"},
		{"poke_target", "PokeTarget"},
		{"_twin", "Twin"},
		{"blog2", "Blog2"},
	}
	for _, tt := range tests {
		if got := pascalCase(tt.in); got != tt.want {
			t.Errorf("pascalCase(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSnakeCase(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Ping", "ping"},
		{"CreatePost", "create_post"},
		{"TodoService", "todo_service"},
		{"ListV2Items", "list_v2_items"},
		{"HTTPServer", "http_server"},
		{"PublishPost", "publish_post"},
	}
	for _, tt := range tests {
		if got := snakeCase(tt.in); got != tt.want {
			t.Errorf("snakeCase(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestToolNamePattern(t *testing.T) {
	valid := []string{"ping", "_ping", "todo_create", "a", "blog2"}
	invalid := []string{"", "Ping", "2ping", "todo-create", "todo create", strings.Repeat("a", 65)}
	for _, s := range valid {
		if !toolNamePattern.MatchString(s) {
			t.Errorf("toolNamePattern rejected valid name %q", s)
		}
	}
	for _, s := range invalid {
		if toolNamePattern.MatchString(s) {
			t.Errorf("toolNamePattern accepted invalid name %q", s)
		}
	}
}

// TestFixtureAnnotationsMatchTheRealOne keeps the fixture module's copy of the annotation from
// drifting away from the one the repo's own protos import.
func TestFixtureAnnotationsMatchTheRealOne(t *testing.T) {
	copied, err := os.ReadFile(filepath.Join("testdata", "proto", "mcp", "v1", "annotations.proto"))
	if err != nil {
		t.Fatalf("reading the fixture copy: %v", err)
	}
	source, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "api", "mcp", "v1", "annotations.proto"))
	if err != nil {
		t.Fatalf("reading api/mcp/v1/annotations.proto: %v", err)
	}
	if !bytes.Equal(copied, source) {
		t.Error("testdata/proto/mcp/v1/annotations.proto has drifted from api/mcp/v1/annotations.proto; copy it over, rebuild the descriptor set, then rerun with -update")
	}
}

func TestFixtureDescriptorSetIsFresh(t *testing.T) {
	buf := bufCommand(t)

	out := filepath.Join(t.TempDir(), "fixture.binpb")
	args := append(append([]string{}, buf[1:]...), "build", filepath.Join("testdata", "proto"), "-o", out)
	cmd := exec.CommandContext(t.Context(), buf[0], args...)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("rebuilding the fixture descriptor set: %v\n%s", err, combined)
	}

	rebuilt, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading the rebuilt descriptor set: %v", err)
	}
	committed, err := os.ReadFile(filepath.Join("testdata", "fixture.binpb"))
	if err != nil {
		t.Fatalf("reading the committed descriptor set: %v", err)
	}
	if !bytes.Equal(rebuilt, committed) {
		t.Error("testdata/fixture.binpb is stale: it no longer matches testdata/proto. Rebuild it with `pnpm exec buf build cmd/protoc-gen-mcp/internal/gen/testdata/proto -o cmd/protoc-gen-mcp/internal/gen/testdata/fixture.binpb`, then rerun this package's tests with -update.")
	}
}

func bufCommand(t *testing.T) []string {
	t.Helper()

	if path, err := exec.LookPath("buf"); err == nil {
		return []string{path}
	}
	if path, err := exec.LookPath("pnpm"); err == nil {
		return []string{path, "exec", "buf"}
	}
	t.Skip("neither buf nor pnpm is on PATH — cannot verify that testdata/fixture.binpb is fresh")
	return nil
}

func newPlugin(t *testing.T, generate ...string) *protogen.Plugin {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("testdata", "fixture.binpb"))
	if err != nil {
		t.Fatalf("reading the fixture descriptor set: %v", err)
	}
	set := &descriptorpb.FileDescriptorSet{}
	if err := proto.Unmarshal(raw, set); err != nil {
		t.Fatalf("unmarshalling the fixture descriptor set: %v", err)
	}

	p, err := protogen.Options{}.New(&pluginpb.CodeGeneratorRequest{
		FileToGenerate: generate,
		Parameter:      proto.String("paths=source_relative"),
		ProtoFile:      set.File,
	})
	if err != nil {
		t.Fatalf("building the plugin: %v", err)
	}
	return p
}

func checkGolden(t *testing.T, name, got string) {
	t.Helper()

	path := filepath.Join("testdata", "golden", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatalf("writing golden %s: %v", path, err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden %s (run `go test ./cmd/protoc-gen-mcp/... -update` to create it): %v", path, err)
	}
	if !bytes.Equal([]byte(got), want) {
		t.Errorf("generated output differs from %s\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}

func TestGenerateHonoursRuntimePackage(t *testing.T) {
	tests := []struct {
		name           string
		runtime        string
		wantImport     string
		unwantedImport string
	}{
		{
			name:       "absent falls back to the template's own runtime",
			wantImport: DefaultRuntimePackage,
		},
		{
			name:           "a fork's runtime replaces it",
			runtime:        "example.com/fork/internal/mcpruntime",
			wantImport:     "example.com/fork/internal/mcpruntime",
			unwantedImport: DefaultRuntimePackage,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newPlugin(t, "opensheettest/v1/fixture.proto")
			if err := Generate(p, Options{UIPrefix: fixturePrefix, RuntimePackage: tt.runtime}); err != nil {
				t.Fatalf("Generate() error = %v", err)
			}
			resp := p.Response()
			if len(resp.File) != 1 {
				t.Fatalf("got %d generated files, want 1", len(resp.File))
			}
			got := resp.File[0].GetContent()
			if !strings.Contains(got, strconv.Quote(tt.wantImport)) {
				t.Errorf("generated file does not import %q:\n%s", tt.wantImport, got)
			}
			if tt.unwantedImport != "" && strings.Contains(got, strconv.Quote(tt.unwantedImport)) {
				t.Errorf("generated file still imports the baked-in %q:\n%s", tt.unwantedImport, got)
			}
		})
	}
}

func TestGenerateWithNoRuntimePackageMatchesTheGolden(t *testing.T) {
	p := newPlugin(t, "opensheettest/v1/fixture.proto")
	if err := Generate(p, Options{UIPrefix: fixturePrefix}); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	checkGolden(t, "fixture.mcp.go.golden", p.Response().File[0].GetContent())
}
