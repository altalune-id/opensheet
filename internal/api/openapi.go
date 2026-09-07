package api

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"io/fs"
	"net/http"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	genassets "altalune.id/template/gen"
)

// BasicAuth guards the OpenAPI endpoints when set. Empty = no guard.
type BasicAuth struct {
	User     string
	Password string
}

//nolint:gochecknoglobals // module-scoped memoized bundle; lazy singleton is intentional.
var (
	openAPIOnce sync.Once
	openAPIYAML []byte
	openAPIJSON []byte
)

func openAPI() (yamlBody, jsonBody []byte) {
	openAPIOnce.Do(func() {
		openAPIYAML, openAPIJSON = loadOpenAPI(genassets.OpenAPI, "openapi")
	})
	return openAPIYAML, openAPIJSON
}

func loadOpenAPI(fsys fs.FS, root string) (yamlBody, jsonBody []byte) {
	entries, err := collectOpenAPI(fsys, root)
	if err != nil || len(entries) == 0 {
		return nil, nil
	}

	docs := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		var doc map[string]any
		if uerr := yaml.Unmarshal(e.data, &doc); uerr == nil {
			docs = append(docs, doc)
		}
	}

	merged := mergeOpenAPI(docs)
	if merged != nil {
		if b, jerr := json.MarshalIndent(merged, "", "  "); jerr == nil {
			jsonBody = b
		}
		if b, yerr := marshalOpenAPI(merged); yerr == nil {
			yamlBody = b
		}
	}
	if jsonBody == nil {
		jsonBody = []byte("{}\n")
	}
	if yamlBody == nil {
		yamlBody = fallbackYAML(entries)
	}
	return yamlBody, jsonBody
}

// marshalOpenAPI emits the merged spec as YAML with top-level keys in canonical OpenAPI order (openapi first) so strict parsers and human readers see the version field on line 1.
func marshalOpenAPI(spec map[string]any) ([]byte, error) {
	order := []string{"openapi", "info", "jsonSchemaDialect", "servers", "security", "tags", "externalDocs", "paths", "webhooks", "components"}
	seen := map[string]bool{}
	content := make([]*yaml.Node, 0, 2*len(spec))
	add := func(k string, v any) error {
		kn := &yaml.Node{Kind: yaml.ScalarNode, Value: k}
		vn := &yaml.Node{}
		if err := vn.Encode(v); err != nil {
			return err
		}
		content = append(content, kn, vn)
		return nil
	}
	for _, k := range order {
		if v, ok := spec[k]; ok {
			if err := add(k, v); err != nil {
				return nil, err
			}
			seen[k] = true
		}
	}
	extras := make([]string, 0)
	for k := range spec {
		if !seen[k] {
			extras = append(extras, k)
		}
	}
	sort.Strings(extras)
	for _, k := range extras {
		if err := add(k, spec[k]); err != nil {
			return nil, err
		}
	}
	root := &yaml.Node{Kind: yaml.MappingNode, Content: content}
	return yaml.Marshal(root)
}

func fallbackYAML(entries []openAPIFile) []byte {
	var buf bytes.Buffer
	for i, e := range entries {
		if i > 0 {
			buf.WriteString("\n---\n")
		}
		buf.WriteString("# ")
		buf.WriteString(e.path)
		buf.WriteByte('\n')
		buf.Write(e.data)
		if !bytes.HasSuffix(e.data, []byte("\n")) {
			buf.WriteByte('\n')
		}
	}
	return buf.Bytes()
}

type openAPIFile struct {
	path string
	data []byte
}

func collectOpenAPI(fsys fs.FS, root string) ([]openAPIFile, error) {
	var out []openAPIFile
	err := fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(p, ".openapi.yaml") {
			return nil
		}
		data, rerr := fs.ReadFile(fsys, p)
		if rerr != nil {
			return rerr
		}
		out = append(out, openAPIFile{path: p, data: data})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out, nil
}

func mergeOpenAPI(docs []map[string]any) map[string]any {
	if len(docs) == 0 {
		return nil
	}
	out := map[string]any{}
	paths := map[string]any{}
	components := map[string]any{}
	tags := []any{}
	for _, d := range docs {
		for k, v := range d {
			switch k {
			case "paths":
				if m, ok := v.(map[string]any); ok {
					for pk, pv := range m {
						paths[pk] = pv
					}
				}
			case "components":
				if m, ok := v.(map[string]any); ok {
					for ck, cv := range m {
						sub, sok := components[ck].(map[string]any)
						if !sok {
							sub = map[string]any{}
							components[ck] = sub
						}
						if cvm, cok := cv.(map[string]any); cok {
							for sk, sv := range cvm {
								sub[sk] = sv
							}
						}
					}
				}
			case "tags":
				if arr, ok := v.([]any); ok {
					tags = append(tags, arr...)
				}
			default:
				if _, exists := out[k]; !exists {
					out[k] = v
				}
			}
		}
	}
	if len(paths) > 0 {
		out["paths"] = paths
	}
	if len(components) > 0 {
		out["components"] = components
	}
	if len(tags) > 0 {
		out["tags"] = tags
	}
	return out
}

func openAPIHandler(body []byte, contentType string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(body)
	})
}

func openAPIGuard(auth *BasicAuth) func(http.Handler) http.Handler {
	if auth == nil {
		return func(h http.Handler) http.Handler { return h }
	}
	want := *auth
	return func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u, p, ok := r.BasicAuth()
			userOK := subtle.ConstantTimeCompare([]byte(u), []byte(want.User)) == 1
			passOK := subtle.ConstantTimeCompare([]byte(p), []byte(want.Password)) == 1
			if !ok || !userOK || !passOK {
				w.Header().Set("WWW-Authenticate", `Basic realm="altempl openapi"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			h.ServeHTTP(w, r)
		})
	}
}
