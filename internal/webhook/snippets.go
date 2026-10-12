package webhook

import (
	"embed"
	"fmt"
	"strings"
	"sync"
	"text/template"
)

// Snippet language ids, in the order VerifySnippets returns them.
const (
	SnippetGo     = "go"
	SnippetNode   = "node"
	SnippetPython = "python"
)

//go:embed snippets/*.tmpl
var snippetFS embed.FS

var snippets = sync.OnceValues(renderSnippets) //nolint:gochecknoglobals // sync.OnceValues memoized render of embedded files.

// Snippet is one receiver example that verifies a delivery's signature.
type Snippet struct {
	Lang  string
	Label string
	Code  string
}

type snippetSource struct {
	lang, label, file string
}

type snippetHeaders struct {
	EventType, DeliveryID, Timestamp, Signature, Scheme string
}

// VerifySnippets returns the signature verifiers shown in the console, rendered with the live header names.
func VerifySnippets() ([]Snippet, error) {
	out, err := snippets()
	if err != nil {
		return nil, err
	}
	return append([]Snippet(nil), out...), nil
}

func renderSnippets() ([]Snippet, error) {
	sources := []snippetSource{
		{lang: SnippetGo, label: "Go", file: "verify.go.tmpl"},
		{lang: SnippetNode, label: "Node.js", file: "verify.mjs.tmpl"},
		{lang: SnippetPython, label: "Python", file: "verify.py.tmpl"},
	}
	data := snippetHeaders{
		EventType:  HeaderEventType,
		DeliveryID: HeaderDeliveryID,
		Timestamp:  HeaderTimestamp,
		Signature:  HeaderSignature,
		Scheme:     signatureScheme,
	}
	out := make([]Snippet, 0, len(sources))
	for _, src := range sources {
		tmpl, err := template.New(src.file).
			Funcs(template.FuncMap{"lower": strings.ToLower}).
			Option("missingkey=error").
			ParseFS(snippetFS, "snippets/"+src.file)
		if err != nil {
			return nil, fmt.Errorf("webhook: parse snippet %s: %w", src.file, err)
		}
		var b strings.Builder
		if err := tmpl.Execute(&b, data); err != nil {
			return nil, fmt.Errorf("webhook: render snippet %s: %w", src.file, err)
		}
		out = append(out, Snippet{Lang: src.lang, Label: src.label, Code: b.String()})
	}
	return out, nil
}
