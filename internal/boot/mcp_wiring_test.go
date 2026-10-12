package boot

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/controlplane"
	rootmcp "altalune.id/opensheet/mcp"
)

var (
	reToolOption = regexp.MustCompile(`(?s)option\s*\(mcp\.v1\.tool\)\s*=\s*\{(.*?)\n\s*\};`)
	reToolName   = regexp.MustCompile(`name:\s*"([^"]+)"`)
)

// TestEveryAnnotatedProtoToolIsRegistered walks the protos rather than restating them, so a whole domain package nobody wired is visible here.
func TestEveryAnnotatedProtoToolIsRegistered(t *testing.T) {
	declared := annotatedToolNames(t)
	require.NotEmpty(t, declared, "no .proto declares an (mcp.v1.tool); this test would guard nothing")

	reg := rootmcp.NewRegistry()
	require.NoError(t, registerTools(reg, &controlplane.Server{}))
	registered := reg.Names()

	for _, name := range declared {
		t.Run(name, func(t *testing.T) {
			require.Contains(t, registered, name,
				"a .proto declares tool %q, but no Register<Svc>Tools call in mcpToolManifest() registers it", name)
		})
	}
	for _, name := range registered {
		require.Contains(t, declared, name, "tool %q is registered but no .proto declares it", name)
	}
}

func TestAssertMCPWiringFailsOnAMissingOrUndeclaredSlot(t *testing.T) {
	full := mcpToolManifest()
	require.NoError(t, assertMCPWiring(full), "the real manifest must pass, or the cases below prove nothing")

	tests := []struct {
		name     string
		manifest func() map[string]mcpToolRegistrar
		missing  []string
		extra    []string
	}{
		{
			name: "a dropped domain",
			manifest: func() map[string]mcpToolRegistrar {
				m := mcpToolManifest()
				delete(m, "project.v1")
				return m
			},
			missing: []string{"project.v1"},
		},
		{
			name: "a nil registrar",
			manifest: func() map[string]mcpToolRegistrar {
				m := mcpToolManifest()
				m["org.v1"] = nil
				return m
			},
			missing: []string{"org.v1"},
		},
		{
			name: "a registrar nobody declared",
			manifest: func() map[string]mcpToolRegistrar {
				m := mcpToolManifest()
				m["ghost.v1"] = func(*rootmcp.Registry, *controlplane.Server) {}
				return m
			},
			extra: []string{"ghost.v1"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := assertMCPWiring(tc.manifest())
			require.Error(t, err, "the guard passed a manifest it must refuse")
			require.True(t, IsMCPWiringError(err), "want an *MCPWiringError, got %T", err)
			var wiring *MCPWiringError
			require.ErrorAs(t, err, &wiring)
			require.Equal(t, tc.missing, wiring.Missing)
			require.Equal(t, tc.extra, wiring.Extra)
		})
	}
}

// TestAssertMCPToolsFailsOnAnUnregisteredCatalogTool is the second half of the boot guard: a domain listed in the manifest whose Register call never ran.
func TestAssertMCPToolsFailsOnAnUnregisteredCatalogTool(t *testing.T) {
	full := rootmcp.NewRegistry()
	require.NoError(t, registerTools(full, &controlplane.Server{}),
		"the real wiring must pass, or the case below proves nothing")

	err := assertMCPTools(rootmcp.NewRegistry())
	require.Error(t, err, "an empty registry passed the wiring guard")
	require.True(t, IsMCPToolUnregisteredError(err), "want an *MCPToolUnregisteredError, got %T", err)
	var unreg *MCPToolUnregisteredError
	require.ErrorAs(t, err, &unreg)
	require.Contains(t, unreg.Tools, "project_list")
}

func annotatedToolNames(t *testing.T) []string {
	t.Helper()

	root := filepath.Join("..", "..", "api")
	var names []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".proto") {
			return nil
		}
		if rel, _ := filepath.Rel(root, filepath.Dir(path)); !slices.Contains(publishedMCPDomains(), strings.ReplaceAll(filepath.ToSlash(rel), "/", ".")) {
			return nil
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, block := range reToolOption.FindAllStringSubmatch(string(b), -1) {
			m := reToolName.FindStringSubmatch(block[1])
			require.NotNil(t, m, "%s declares an (mcp.v1.tool) with no name", path)
			names = append(names, m[1])
		}
		return nil
	})
	require.NoError(t, err)
	slices.Sort(names)
	return names
}
