package schema

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"
	"text/template"
	"time"
)

type templateVars struct {
	Schema      string
	TablePrefix string
	RLSEnforce  bool
}

func (v templateVars) toMap() map[string]any {
	return map[string]any{
		"Schema":      v.Schema,
		"TablePrefix": v.TablePrefix,
		"RLSEnforce":  v.RLSEnforce,
	}
}

type templatedFS struct {
	base fs.FS
	vars templateVars
}

func newTemplatedFS(base fs.FS, vars templateVars) fs.FS {
	return &templatedFS{base: base, vars: vars}
}

func (t *templatedFS) Open(name string) (fs.File, error) {
	f, err := t.base.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if info.IsDir() || !isSQL(name) {
		return f, nil
	}
	raw, err := io.ReadAll(f)
	if closeErr := f.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, fmt.Errorf("templatedFS: read %s: %w", name, err)
	}
	rendered, err := renderTemplate(name, raw, t.vars)
	if err != nil {
		return nil, err
	}
	return &memoryFile{
		Reader: bytes.NewReader(rendered),
		info:   memoryFileInfo{name: info.Name(), size: int64(len(rendered)), mod: info.ModTime()},
	}, nil
}

func (t *templatedFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if rd, ok := t.base.(fs.ReadDirFS); ok {
		return rd.ReadDir(name)
	}
	return fs.ReadDir(t.base, name)
}

func (t *templatedFS) ReadFile(name string) ([]byte, error) {
	raw, err := fs.ReadFile(t.base, name)
	if err != nil {
		return nil, err
	}
	if !isSQL(name) {
		return raw, nil
	}
	return renderTemplate(name, raw, t.vars)
}

func isSQL(name string) bool {
	return strings.HasSuffix(strings.ToLower(path.Base(name)), ".sql")
}

func renderTemplate(name string, body []byte, vars templateVars) ([]byte, error) {
	if !bytes.Contains(body, []byte("{{")) {
		return body, nil
	}
	tpl, err := template.New(name).
		Option("missingkey=error").
		Parse(string(body))
	if err != nil {
		return nil, fmt.Errorf("templatedFS: parse %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, vars.toMap()); err != nil {
		return nil, fmt.Errorf("templatedFS: execute %s: %w", name, err)
	}
	return buf.Bytes(), nil
}

type memoryFile struct {
	*bytes.Reader
	info memoryFileInfo
}

func (m *memoryFile) Stat() (fs.FileInfo, error) { return &m.info, nil }
func (m *memoryFile) Close() error               { return nil }

type memoryFileInfo struct {
	name string
	size int64
	mod  time.Time
}

func (i *memoryFileInfo) Name() string       { return i.name }
func (i *memoryFileInfo) Size() int64        { return i.size }
func (i *memoryFileInfo) Mode() fs.FileMode  { return 0o444 }
func (i *memoryFileInfo) ModTime() time.Time { return i.mod }
func (i *memoryFileInfo) IsDir() bool        { return false }
func (i *memoryFileInfo) Sys() any           { return nil }
