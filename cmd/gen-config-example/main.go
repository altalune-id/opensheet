// Command gen-config-example regenerates .env.example and config.example.yaml from internal/platform/config struct tags.
package main

import (
	"flag"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"

	"altalune.id/template/internal/platform/config"
)

const (
	formatEnv  = "env"
	formatYAML = "yaml"
)

func main() {
	var (
		format = flag.String("format", formatEnv, "output format: env | yaml")
		out    = flag.String("output", "", "output file path (default: .env.example for env, config.example.yaml for yaml; use '-' for stdout)")
		check  = flag.Bool("check", false, "exit non-zero if the on-disk file differs from what would be generated")
	)
	flag.Parse()

	if *format != formatEnv && *format != formatYAML {
		fmt.Fprintf(os.Stderr, "gen-config-example: unknown format %q (want %q or %q)\n", *format, formatEnv, formatYAML)
		os.Exit(2)
	}

	target := *out
	if target == "" {
		if *format == formatYAML {
			target = "config.example.yaml"
		} else {
			target = ".env.example"
		}
	}

	body := generate(*format)

	if target == "-" {
		if _, err := os.Stdout.WriteString(body); err != nil {
			fmt.Fprintln(os.Stderr, "gen-config-example:", err)
			os.Exit(1)
		}
		return
	}

	if *check {
		existing, err := os.ReadFile(target) //nolint:gosec // G304: target is a maintainer-supplied flag, not tainted input.
		if err != nil {
			fmt.Fprintf(os.Stderr, "gen-config-example: reading %s: %v\n", target, err)
			os.Exit(2)
		}
		if string(existing) == body {
			fmt.Fprintf(os.Stderr, "gen-config-example: %s is up-to-date\n", target)
			return
		}
		fmt.Fprintf(os.Stderr, "gen-config-example: %s is stale — run `go tool gen-config-example --format=%s` to update.\n", target, *format)
		os.Exit(1)
	}

	if err := os.WriteFile(target, []byte(body), 0o644); err != nil { //nolint:gosec // G306: example files are committed source, not secrets.
		fmt.Fprintln(os.Stderr, "gen-config-example:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "gen-config-example: wrote %s\n", target)
}

func generate(format string) string {
	keys := config.WalkEnvKeys("ALT")

	var bootstrap, runtime []config.EnvKey
	for _, k := range keys {
		if hasMarker(k.Awareness, "bootstrap") {
			bootstrap = append(bootstrap, k)
		} else {
			runtime = append(runtime, k)
		}
	}

	if format == formatYAML {
		return generateYAML(bootstrap, runtime)
	}
	return generateEnv(bootstrap, runtime)
}

func generateEnv(bootstrap, runtime []config.EnvKey) string {
	var buf strings.Builder
	buf.WriteString(envHeader())
	buf.WriteString(envBootstrapPreamble())
	writeEnvSection(&buf, bootstrap)
	buf.WriteString(envRuntimePreamble())
	writeEnvSection(&buf, runtime)
	return buf.String()
}

func writeEnvSection(buf *strings.Builder, keys []config.EnvKey) {
	groups, order := groupByTopSegment(keys)
	for _, top := range order {
		fmt.Fprintf(buf, "\n# ─── %s ───\n", top)
		universal, cloudOnly, selfhostedOnly := splitByMode(groups[top])
		for _, k := range universal {
			writeEnvField(buf, k)
		}
		if len(cloudOnly) > 0 {
			buf.WriteString("\n# ── cloud only ──\n")
			for _, k := range cloudOnly {
				writeEnvField(buf, k)
			}
		}
		if len(selfhostedOnly) > 0 {
			buf.WriteString("\n# ── selfhosted only ──\n")
			for _, k := range selfhostedOnly {
				writeEnvField(buf, k)
			}
		}
	}
}

func writeEnvField(buf *strings.Builder, k config.EnvKey) {
	if len(k.Awareness) > 0 {
		fmt.Fprintf(buf, "# [%s] %s\n", strings.Join(k.Awareness, ", "), k.YAML)
	} else {
		fmt.Fprintf(buf, "# %s\n", k.YAML)
	}
	value := ""
	if !hasMarker(k.Awareness, "secret") {
		value = normalizeDefault(k.FormatDefault())
	}
	fmt.Fprintf(buf, "%s=%s\n", k.Key, value)
}

func generateYAML(bootstrap, runtime []config.EnvKey) string {
	var buf strings.Builder
	buf.WriteString(yamlHeader())
	writeYAMLTree(&buf, append(append([]config.EnvKey{}, bootstrap...), runtime...))
	return buf.String()
}

type yamlNode struct {
	name     string
	key      *config.EnvKey
	children map[string]*yamlNode
	order    []string
}

func newYAMLNode(name string) *yamlNode {
	return &yamlNode{name: name, children: map[string]*yamlNode{}}
}

func (n *yamlNode) insert(path []string, k config.EnvKey) {
	if len(path) == 0 {
		copyK := k
		n.key = &copyK
		return
	}
	head := path[0]
	child, ok := n.children[head]
	if !ok {
		child = newYAMLNode(head)
		n.children[head] = child
		n.order = append(n.order, head)
	}
	child.insert(path[1:], k)
}

func writeYAMLTree(buf *strings.Builder, keys []config.EnvKey) {
	root := newYAMLNode("")
	for _, k := range keys {
		root.insert(strings.Split(k.YAML, "."), k)
	}
	sort.Strings(root.order)
	for _, name := range root.order {
		child := root.children[name]
		buf.WriteByte('\n')
		writeYAMLNode(buf, child, 0)
	}
}

func writeYAMLNode(buf *strings.Builder, n *yamlNode, indent int) {
	pad := strings.Repeat("  ", indent)
	if n.key != nil && len(n.children) == 0 {
		writeYAMLLeaf(buf, n, indent)
		return
	}
	fmt.Fprintf(buf, "%s%s:\n", pad, n.name)
	if n.key != nil {
		writeYAMLLeaf(buf, &yamlNode{name: "value", key: n.key}, indent+1)
	}
	sort.Strings(n.order)
	for _, name := range n.order {
		writeYAMLNode(buf, n.children[name], indent+1)
	}
}

func writeYAMLLeaf(buf *strings.Builder, n *yamlNode, indent int) {
	pad := strings.Repeat("  ", indent)
	if len(n.key.Awareness) > 0 {
		fmt.Fprintf(buf, "%s# [%s] env: %s\n", pad, strings.Join(n.key.Awareness, ", "), n.key.Key)
	} else {
		fmt.Fprintf(buf, "%s# env: %s\n", pad, n.key.Key)
	}
	value := ""
	if !hasMarker(n.key.Awareness, "secret") {
		value = normalizeDefault(n.key.FormatDefault())
	}
	fmt.Fprintf(buf, "%s%s: %s\n", pad, n.name, yamlScalar(value))
}

func yamlScalar(v string) string {
	if v == "" {
		return `""`
	}
	if strings.ContainsAny(v, ":#\n") || strings.HasPrefix(v, "-") || strings.HasPrefix(v, "&") || strings.HasPrefix(v, "*") {
		return fmt.Sprintf("%q", v)
	}
	return v
}

func groupByTopSegment(keys []config.EnvKey) (groups map[string][]config.EnvKey, order []string) {
	groups = map[string][]config.EnvKey{}
	for _, k := range keys {
		top := topSegment(k.YAML)
		if _, ok := groups[top]; !ok {
			order = append(order, top)
		}
		groups[top] = append(groups[top], k)
	}
	sort.Strings(order)
	return groups, order
}

func splitByMode(keys []config.EnvKey) (universal, cloudOnly, selfhostedOnly []config.EnvKey) {
	for _, k := range keys {
		switch {
		case hasMarker(k.Awareness, "mode:cloud"):
			cloudOnly = append(cloudOnly, k)
		case hasMarker(k.Awareness, "mode:selfhosted"):
			selfhostedOnly = append(selfhostedOnly, k)
		default:
			universal = append(universal, k)
		}
	}
	return
}

func normalizeDefault(s string) string {
	if s == "" {
		return s
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return s
	}
	if strings.HasPrefix(s, home) {
		return "~" + s[len(home):]
	}
	return s
}

func hasMarker(awareness []string, want string) bool {
	return slices.Contains(awareness, want)
}

func topSegment(yaml string) string {
	if top, _, ok := strings.Cut(yaml, "."); ok {
		return top
	}
	return yaml
}

func envHeader() string {
	return `# altempl — auto-generated from internal/platform/config struct tags.
# DO NOT edit by hand. Run ` + "`go tool gen-config-example`" + ` to regenerate.
#
# Awareness markers on each field (in [brackets] above the env var):
#   required   — must be non-empty for the app to boot (in the applicable mode)
#   bootstrap  — set once at first-run; changing later has no effect or breaks things
#   secret     — never commit; use env vars or a secret store
#   mode:X     — only applies in the given mode (selfhosted | cloud)
#
# Fields split into two top-level sections:
#   BOOTSTRAP — locked at first boot, changing later is NOT SAFE
#   RUNTIME   — safe to change via env + restart
#
# RHS shows the built-in default when present. Secret fields intentionally
# emit no RHS value, even if a default exists internally.

`
}

func envBootstrapPreamble() string {
	return `
# ════════════════════════════════════════════════════════════════════
#   BOOTSTRAP — set once at first boot; changing later is NOT SAFE
# ════════════════════════════════════════════════════════════════════
#
# Fields in this section lock in behavior for the lifetime of the deployment:
#   - Changing them via env vars after first boot has NO effect on already-
#     persisted data (e.g., db.driver is set at first migration; switching
#     from sqlite→postgres requires a full data migration, not an env flip).
#   - Some fields are secrets used to sign existing tokens/cookies
#     (http.stateSecret) — rotating invalidates in-flight sessions.
#   - Some fields are database-layer decisions (schema, tablePrefix) that
#     require SQL-level intervention to change.
#
# Read every line here BEFORE first ` + "`altempl serve`" + `. Set them deliberately.
#
# ────────────────────────────────────────────────────────────────────
`
}

func envRuntimePreamble() string {
	return `

# ════════════════════════════════════════════════════════════════════
#   RUNTIME — safe to change via env + restart
# ════════════════════════════════════════════════════════════════════
#
# Fields in this section can be adjusted between deployments without
# affecting persisted data. Restarting the app picks up the new values.
#
# ────────────────────────────────────────────────────────────────────
`
}

func yamlHeader() string {
	return `# altempl — auto-generated from internal/platform/config struct tags.
# DO NOT edit by hand. Run ` + "`go tool gen-config-example --format=yaml`" + ` to regenerate.
#
# Copy to config.yaml, uncomment what you need to set, and pass ` + "`-c config.yaml`" + `.
# Every field maps to an env var (shown as ` + "`env: ALT_*`" + ` in the per-field comment).
# Env vars override YAML.
#
# Awareness markers (in [brackets] on the per-field comment):
#   required   — must be non-empty for the app to boot (in the applicable mode)
#   bootstrap  — set once at first-run; changing later has no effect or breaks things
#   secret     — never commit; use env vars or a secret store
#   mode:X     — only applies in the given mode (selfhosted | cloud)
`
}
