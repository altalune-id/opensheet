// Package comments enforces the repository comment discipline over Go sources.
package comments

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Rule names the comment-discipline rule a violation broke.
type Rule string

// The rules enforced by Run.
const (
	RuleUnexportedDoc Rule = "unexported-doc"
	RuleMultilineDoc  Rule = "multiline-doc"
	RuleBlankDocLine  Rule = "blank-doc-line"
	RuleSectionHeader Rule = "section-header"
	RuleProseBlock    Rule = "prose-block"
)

// Violation is one comment that breaks one rule.
type Violation struct {
	File    string
	Line    int
	Ident   string
	Rule    Rule
	Message string
}

// String renders the violation as a file:line diagnostic.
func (v Violation) String() string {
	return fmt.Sprintf("%s:%d: [%s] %s: %s", v.File, v.Line, v.Rule, v.Ident, v.Message)
}

// Options configures a Run.
type Options struct {
	Roots []string
}

// Report is the outcome of a Run.
type Report struct {
	Violations []Violation
	Files      int
}

// Print writes every violation followed by a per-rule tally.
func (r Report) Print(w io.Writer) {
	for _, v := range r.Violations {
		_, _ = fmt.Fprintln(w, v)
	}
	if len(r.Violations) == 0 {
		_, _ = fmt.Fprintf(w, "comment-lint: %d files, no violations\n", r.Files)
		return
	}
	counts := map[Rule]int{}
	for _, v := range r.Violations {
		counts[v.Rule]++
	}
	rules := make([]string, 0, len(counts))
	for rule := range counts {
		rules = append(rules, string(rule))
	}
	sort.Strings(rules)
	_, _ = fmt.Fprintf(w, "\ncomment-lint: %d violations in %d files\n", len(r.Violations), r.Files)
	for _, rule := range rules {
		_, _ = fmt.Fprintf(w, "  %-16s %d\n", rule, counts[Rule(rule)])
	}
}

//nolint:gochecknoglobals // Immutable manifest; not runtime state.
var skipDirs = map[string]bool{
	".git":         true,
	".github":      true,
	"bin":          true,
	"dist":         true,
	"gen":          true,
	"node_modules": true,
	"testdata":     true,
	"vendor":       true,
}

//nolint:gochecknoglobals // Immutable manifest; not runtime state.
var skipSuffixes = []string{
	"_templ.go",
	".pb.go",
	".connect.go",
	".mcp.go",
	"_gen.go",
	".gen.go",
}

//nolint:gochecknoglobals // Compiled once; read-only.
var (
	directiveRe = regexp.MustCompile(`^//(go:|line |export |extern |cgo_|nolint|lint:|i18n:use|revive:|noinspection|sys |systemstack|uintptrescapes|\+build)`)
	markerRe    = regexp.MustCompile(`\b(TODO|FIXME|SECURITY|NOTE|XXX|HACK|BUG|Deprecated)\b`)
	urlRe       = regexp.MustCompile(`(https?://|\bRFC\s?[0-9]{3,}|\bGH-[0-9]+|[\w.-]+/[\w.-]+#[0-9]+)`)
	sectionRe   = regexp.MustCompile(`^//\s*[-=*~#_+]{3,}|[-=*~#_+]{4,}\s*$`)
	generatedRe = regexp.MustCompile(`^// Code generated .* DO NOT EDIT\.$`)
)

// Run scans every Go file under the configured roots and reports violations.
func Run(opts Options) (Report, error) {
	roots := opts.Roots
	if len(roots) == 0 {
		roots = []string{"."}
	}
	var report Report
	seen := map[string]bool{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if skipDirs[d.Name()] {
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || skipFile(path) || seen[path] {
				return nil
			}
			seen[path] = true
			report.Files++
			vs, ferr := checkFile(path)
			if ferr != nil {
				return ferr
			}
			report.Violations = append(report.Violations, vs...)
			return nil
		})
		if err != nil {
			return Report{}, err
		}
	}
	sort.Slice(report.Violations, func(i, j int) bool {
		a, b := report.Violations[i], report.Violations[j]
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})
	return report, nil
}

func skipFile(path string) bool {
	base := filepath.Base(path)
	for _, suffix := range skipSuffixes {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return false
}

func checkFile(path string) ([]Violation, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if isGenerated(file) {
		return nil, nil
	}

	var out []Violation
	add := func(vs ...Violation) { out = append(out, vs...) }

	docGroups := map[*ast.CommentGroup]bool{}
	if file.Doc != nil {
		docGroups[file.Doc] = true
		add(checkDoc(fset, path, file.Doc, "package "+file.Name.Name, true)...)
	}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Doc != nil {
				docGroups[d.Doc] = true
				add(checkDoc(fset, path, d.Doc, funcName(d), ast.IsExported(d.Name.Name))...)
			}
		case *ast.GenDecl:
			if d.Doc != nil {
				docGroups[d.Doc] = true
				add(checkDoc(fset, path, d.Doc, genDeclName(d), genDeclExported(d))...)
			}
			for _, spec := range d.Specs {
				doc, name, exported := specDoc(spec)
				if doc == nil {
					continue
				}
				docGroups[doc] = true
				add(checkDoc(fset, path, doc, name, exported)...)
			}
		}
	}

	for _, group := range file.Comments {
		add(checkFree(fset, path, group, docGroups[group])...)
	}
	return out, nil
}

func isGenerated(file *ast.File) bool {
	for _, group := range file.Comments {
		for _, c := range group.List {
			if generatedRe.MatchString(c.Text) {
				return true
			}
		}
	}
	return false
}

func funcName(d *ast.FuncDecl) string {
	if d.Recv == nil || len(d.Recv.List) == 0 {
		return d.Name.Name
	}
	return recvTypeName(d.Recv.List[0].Type) + "." + d.Name.Name
}

func recvTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return recvTypeName(t.X)
	case *ast.IndexExpr:
		return recvTypeName(t.X)
	case *ast.IndexListExpr:
		return recvTypeName(t.X)
	case *ast.Ident:
		return t.Name
	}
	return "?"
}

func genDeclName(d *ast.GenDecl) string {
	names := specNames(d.Specs)
	if len(names) == 0 {
		return d.Tok.String()
	}
	return d.Tok.String() + " " + strings.Join(names, ", ")
}

func genDeclExported(d *ast.GenDecl) bool {
	for _, name := range specNames(d.Specs) {
		if ast.IsExported(name) {
			return true
		}
	}
	return false
}

func specNames(specs []ast.Spec) []string {
	var names []string
	for _, spec := range specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			names = append(names, s.Name.Name)
		case *ast.ValueSpec:
			for _, n := range s.Names {
				names = append(names, n.Name)
			}
		case *ast.ImportSpec:
			names = append(names, strings.Trim(s.Path.Value, `"`))
		}
	}
	return names
}

func specDoc(spec ast.Spec) (*ast.CommentGroup, string, bool) {
	switch s := spec.(type) {
	case *ast.TypeSpec:
		return s.Doc, s.Name.Name, ast.IsExported(s.Name.Name)
	case *ast.ValueSpec:
		names := make([]string, 0, len(s.Names))
		exported := false
		for _, n := range s.Names {
			names = append(names, n.Name)
			if ast.IsExported(n.Name) {
				exported = true
			}
		}
		return s.Doc, strings.Join(names, ", "), exported
	case *ast.ImportSpec:
		return s.Doc, strings.Trim(s.Path.Value, `"`), false
	}
	return nil, "", false
}

type line struct {
	text  string
	pos   token.Pos
	off   int
	blank bool
	kind  lineKind
}

type lineKind int

const (
	kindProse lineKind = iota
	kindDirective
	kindKeep
	kindSection
)

func classify(group *ast.CommentGroup) []line {
	lines := make([]line, 0, len(group.List))
	for _, c := range group.List {
		for i, raw := range strings.Split(c.Text, "\n") {
			text := strings.TrimSpace(raw)
			if strings.HasPrefix(c.Text, "/*") {
				text = "// " + strings.TrimSpace(strings.Trim(strings.Trim(text, "/*"), "*/"))
			}
			lines = append(lines, line{
				text:  text,
				pos:   c.Slash,
				off:   i,
				blank: isBlankComment(text),
				kind:  kindOf(text),
			})
		}
	}
	return lines
}

func isBlankComment(text string) bool {
	return strings.TrimSpace(strings.TrimPrefix(text, "//")) == ""
}

func kindOf(text string) lineKind {
	switch {
	case directiveRe.MatchString(text):
		return kindDirective
	case sectionRe.MatchString(text):
		return kindSection
	case markerRe.MatchString(text) || urlRe.MatchString(text):
		return kindKeep
	}
	return kindProse
}

func checkDoc(fset *token.FileSet, path string, group *ast.CommentGroup, ident string, exported bool) []Violation {
	lines := classify(group)
	var out []Violation

	for i, l := range lines {
		if l.blank && !nextIsDirective(lines, i) {
			out = append(out, violation(fset, path, l, ident, RuleBlankDocLine,
				"blank comment line inside a doc block"))
			break
		}
	}
	for _, l := range lines {
		if l.kind == kindSection {
			out = append(out, violation(fset, path, l, ident, RuleSectionHeader,
				"decorative section header"))
			break
		}
	}

	content := 0
	var firstProse *line
	for i, l := range lines {
		if l.blank || l.kind == kindDirective || l.kind == kindSection {
			continue
		}
		content++
		if firstProse == nil && l.kind == kindProse {
			firstProse = &lines[i]
		}
	}
	if content == 0 {
		return out
	}

	if !exported {
		if firstProse != nil {
			out = append(out, violation(fset, path, *firstProse, ident, RuleUnexportedDoc,
				"comment on an unexported identifier carries no TODO/FIXME/SECURITY/NOTE marker or URL"))
		}
		return out
	}
	if content > 1 {
		out = append(out, violation(fset, path, lines[0], ident, RuleMultilineDoc,
			fmt.Sprintf("godoc on an exported identifier spans %d lines; keep one sentence", content)))
	}
	return out
}

func checkFree(fset *token.FileSet, path string, group *ast.CommentGroup, isDoc bool) []Violation {
	lines := classify(group)
	var out []Violation
	for _, l := range lines {
		if l.kind == kindSection {
			if isDoc {
				break
			}
			out = append(out, violation(fset, path, l, "(free comment)", RuleSectionHeader,
				"decorative section header"))
			break
		}
	}
	if isDoc {
		return out
	}
	prose := 0
	var first *line
	for i, l := range lines {
		if l.kind != kindProse || l.blank {
			continue
		}
		prose++
		if first == nil {
			first = &lines[i]
		}
	}
	if prose >= 3 {
		out = append(out, violation(fset, path, *first, "(free comment)", RuleProseBlock,
			fmt.Sprintf("%d-line prose block with no marker or URL", prose)))
	}
	return out
}

func nextIsDirective(lines []line, i int) bool {
	for j := i + 1; j < len(lines); j++ {
		if lines[j].blank {
			continue
		}
		return lines[j].kind == kindDirective
	}
	return false
}

func violation(fset *token.FileSet, path string, l line, ident string, rule Rule, msg string) Violation {
	return Violation{
		File:    path,
		Line:    fset.Position(l.pos).Line + l.off,
		Ident:   ident,
		Rule:    rule,
		Message: msg,
	}
}
