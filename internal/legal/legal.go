// Package legal ships the Terms of Service and Privacy Policy as embedded markdown, parsed to HTML once at process start.
package legal

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/renderer/html"
)

//go:embed content/terms.md content/privacy.md
var contentFS embed.FS

// Document is a parsed legal document ready for rendering.
type Document struct {
	Slug      string
	Title     string
	HTML      string
	UpdatedAt time.Time
}

// TermsSlug identifies the Terms of Service document.
const TermsSlug = "terms"

// PrivacySlug identifies the Privacy Policy document.
const PrivacySlug = "privacy"

var docs = sync.OnceValues(loadAll) //nolint:gochecknoglobals // sync.OnceValues memoized cache — package-scoped by design.

// Terms returns the Terms of Service document.
func Terms() (*Document, error) {
	m, err := docs()
	if err != nil {
		return nil, err
	}
	return m[TermsSlug], nil
}

// Privacy returns the Privacy Policy document.
func Privacy() (*Document, error) {
	m, err := docs()
	if err != nil {
		return nil, err
	}
	return m[PrivacySlug], nil
}

// ByslugFn is the shape TemplateData needs; keep a package-level lookup helper for handlers.
func BySlug(slug string) (*Document, error) {
	m, err := docs()
	if err != nil {
		return nil, err
	}
	d, ok := m[slug]
	if !ok {
		return nil, fmt.Errorf("legal: unknown slug %q", slug)
	}
	return d, nil
}

func loadAll() (map[string]*Document, error) {
	md := goldmark.New(goldmark.WithRendererOptions(html.WithUnsafe()))
	out := make(map[string]*Document, 2)
	for slug, path := range map[string]string{
		TermsSlug:   "content/terms.md",
		PrivacySlug: "content/privacy.md",
	} {
		raw, err := contentFS.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("legal: read %s: %w", path, err)
		}
		meta, body, err := splitFrontmatter(raw)
		if err != nil {
			return nil, fmt.Errorf("legal: parse frontmatter %s: %w", path, err)
		}
		var buf bytes.Buffer
		if err := md.Convert(body, &buf); err != nil {
			return nil, fmt.Errorf("legal: render %s: %w", path, err)
		}
		out[slug] = &Document{
			Slug:      slug,
			Title:     meta.Title,
			HTML:      buf.String(),
			UpdatedAt: meta.Updated,
		}
	}
	return out, nil
}

type frontmatter struct {
	Title   string
	Updated time.Time
}

func splitFrontmatter(raw []byte) (frontmatter, []byte, error) {
	const delim = "---"
	trimmed := bytes.TrimLeft(raw, "\ufeff\r\n\t ")
	if !bytes.HasPrefix(trimmed, []byte(delim)) {
		return frontmatter{}, raw, nil
	}
	head, body, ok := bytes.Cut(trimmed[len(delim):], []byte("\n"+delim))
	if !ok {
		return frontmatter{}, nil, errors.New("frontmatter opening --- has no closing ---")
	}
	body = bytes.TrimLeft(body, "\r\n")

	var fm frontmatter
	for line := range strings.SplitSeq(strings.TrimSpace(string(head)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "title":
			fm.Title = strings.Trim(value, `"'`)
		case "updated":
			if t, err := time.Parse("2006-01-02", strings.Trim(value, `"'`)); err == nil {
				fm.Updated = t
			}
		}
	}
	return fm, body, nil
}
