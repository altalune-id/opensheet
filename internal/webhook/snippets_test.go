package webhook_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/webhook"
)

const (
	vectorSecret    = "whsec_test"
	vectorTimestamp = int64(1758153600)
	vectorBody      = `{"a":1}`
	vectorSignature = "v1=5d7a59cb9a5399806655f45f56c0139fbffdf766435435b0d7004e9d9ae115b1"
)

type verifyCase struct {
	Name    string            `json:"name"`
	Secret  string            `json:"secret"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
	Now     int64             `json:"now"`
	NoRaw   bool              `json:"no_raw"`
	Want    bool              `json:"-"`
}

func signedHeaders(ts int64, sig string) map[string]string {
	return map[string]string{
		webhook.HeaderTimestamp: formatInt(ts),
		webhook.HeaderSignature: sig,
	}
}

func formatInt(n int64) string {
	return strconv.FormatInt(n, 10)
}

func verifyCases() []verifyCase {
	return []verifyCase{
		{Name: "docs vector", Secret: vectorSecret, Headers: signedHeaders(vectorTimestamp, vectorSignature), Body: vectorBody, Now: vectorTimestamp, Want: true},
		{Name: "rotation pair, match second", Secret: vectorSecret, Headers: signedHeaders(vectorTimestamp, "v1=00ff "+vectorSignature), Body: vectorBody, Now: vectorTimestamp, Want: true},
		{Name: "inside the skew window", Secret: vectorSecret, Headers: signedHeaders(vectorTimestamp, vectorSignature), Body: vectorBody, Now: vectorTimestamp + 299, Want: true},
		{Name: "tampered body", Secret: vectorSecret, Headers: signedHeaders(vectorTimestamp, vectorSignature), Body: `{"a":2}`, Now: vectorTimestamp, Want: false},
		{Name: "stale timestamp", Secret: vectorSecret, Headers: signedHeaders(vectorTimestamp, vectorSignature), Body: vectorBody, Now: vectorTimestamp + 301, Want: false},
		{Name: "future timestamp", Secret: vectorSecret, Headers: signedHeaders(vectorTimestamp, vectorSignature), Body: vectorBody, Now: vectorTimestamp - 301, Want: false},
		{Name: "wrong secret", Secret: "whsec_other", Headers: signedHeaders(vectorTimestamp, vectorSignature), Body: vectorBody, Now: vectorTimestamp, Want: false},
		{Name: "uppercase hex", Secret: vectorSecret, Headers: signedHeaders(vectorTimestamp, strings.ToUpper(vectorSignature)), Body: vectorBody, Now: vectorTimestamp, Want: false},
		{Name: "missing signature", Secret: vectorSecret, Headers: map[string]string{webhook.HeaderTimestamp: formatInt(vectorTimestamp)}, Body: vectorBody, Now: vectorTimestamp, Want: false},
		{Name: "framework handed over no raw body", Secret: vectorSecret, Headers: signedHeaders(vectorTimestamp, vectorSignature), Body: vectorBody, Now: vectorTimestamp, NoRaw: true, Want: false},
		{Name: "non-numeric timestamp", Secret: vectorSecret, Headers: map[string]string{webhook.HeaderTimestamp: "17581536OO", webhook.HeaderSignature: vectorSignature}, Body: vectorBody, Now: vectorTimestamp, Want: false},
	}
}

const goHarness = `package main

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestHarness(t *testing.T) {
	var cases []struct {
		Secret  string            ` + "`json:\"secret\"`" + `
		Headers map[string]string ` + "`json:\"headers\"`" + `
		Body    string            ` + "`json:\"body\"`" + `
		Now     int64             ` + "`json:\"now\"`" + `
		NoRaw   bool              ` + "`json:\"no_raw\"`" + `
	}
	raw, err := os.ReadFile("cases.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	out := make([]bool, 0, len(cases))
	for _, c := range cases {
		h := http.Header{}
		for k, v := range c.Headers {
			h.Set(k, v)
		}
		body := []byte(c.Body)
		if c.NoRaw {
			body = nil
		}
		out = append(out, verifyWebhook(c.Secret, h, body, time.Unix(c.Now, 0)))
	}
	buf, _ := json.Marshal(out)
	if err := os.WriteFile("out.json", buf, 0o600); err != nil {
		t.Fatal(err)
	}
}
`

const nodeHarness = `import fs from "node:fs";
import { verifyWebhook } from "./webhook.mjs";

const cases = JSON.parse(fs.readFileSync("cases.json", "utf8"));
const out = cases.map((c) => {
  const headers = Object.fromEntries(Object.entries(c.headers).map(([k, v]) => [k.toLowerCase(), v]));
  const body = c.no_raw ? {} : Buffer.from(c.body, "utf8");
  return verifyWebhook(c.secret, headers, body, c.now * 1000);
});
fs.writeFileSync("out.json", JSON.stringify(out));
`

const expressStub = `function express() {
  return { post() {}, listen() {} };
}
express.raw = () => () => {};
module.exports = express;
`

const pythonHarness = `import json

from webhook import verify_webhook

with open("cases.json", encoding="utf-8") as f:
    cases = json.load(f)
out = [
    verify_webhook(c["secret"], c["headers"], b"" if c["no_raw"] else c["body"].encode("utf-8"), c["now"])
    for c in cases
]
with open("out.json", "w", encoding="utf-8") as f:
    json.dump(out, f)
`

const flaskStub = `class Flask:
    def __init__(self, name):
        self.name = name

    def post(self, path):
        return lambda fn: fn


def abort(code):
    raise RuntimeError(code)


request = None
`

type snippetRun struct {
	bin   string
	args  []string
	env   []string
	files map[string]string
}

func snippetRunFor(t *testing.T, s webhook.Snippet) snippetRun {
	t.Helper()
	switch s.Lang {
	case webhook.SnippetGo:
		return snippetRun{
			bin:  "go",
			args: []string{"test", "-count=1", "-run", "TestHarness", "."},
			env:  []string{"GOWORK=off", "GOFLAGS=", "GOTOOLCHAIN=local", "CGO_ENABLED=0"},
			files: map[string]string{
				"go.mod":          "module receiver\n\ngo 1.22\n",
				"main.go":         s.Code,
				"harness_test.go": goHarness,
			},
		}
	case webhook.SnippetNode:
		return snippetRun{
			bin:  "node",
			args: []string{"harness.mjs"},
			files: map[string]string{
				"webhook.mjs":                       s.Code,
				"harness.mjs":                       nodeHarness,
				"node_modules/express/package.json": `{"name":"express","main":"index.js"}`,
				"node_modules/express/index.js":     expressStub,
			},
		}
	case webhook.SnippetPython:
		return snippetRun{
			bin:  "python3",
			args: []string{"harness.py"},
			env:  []string{"PYTHONDONTWRITEBYTECODE=1"},
			files: map[string]string{
				"webhook.py": s.Code,
				"harness.py": pythonHarness,
				"flask.py":   flaskStub,
			},
		}
	}
	t.Fatalf("no runner for snippet %q", s.Lang)
	return snippetRun{}
}

func runSnippet(t *testing.T, s webhook.Snippet, cases []verifyCase) []bool {
	t.Helper()
	run := snippetRunFor(t, s)
	bin, err := exec.LookPath(run.bin)
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("%s not installed on CI: %v", run.bin, err)
		}
		t.Skipf("%s not installed: %v", run.bin, err)
	}
	dir := t.TempDir()
	for name, body := range run.files {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	}
	raw, err := json.Marshal(cases)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "cases.json"), raw, 0o600))

	cmd := exec.CommandContext(t.Context(), bin, run.args...) //nolint:gosec // G204: bin is one of three fixed interpreters
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), run.env...)
	combined, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s snippet failed:\n%s", s.Lang, combined)

	out, err := os.ReadFile(filepath.Join(dir, "out.json"))
	require.NoError(t, err)
	var got []bool
	require.NoError(t, json.Unmarshal(out, &got))
	require.Len(t, got, len(cases))
	return got
}

func TestSign_MatchesTheDocsVector(t *testing.T) {
	t.Parallel()
	assert.Equal(t, vectorSignature, webhook.Sign(vectorSecret, formatInt(vectorTimestamp), []byte(vectorBody)))
}

func TestVerifySnippets_OnePerLanguageWithTheLiveHeaderNames(t *testing.T) {
	t.Parallel()
	snippets, err := webhook.VerifySnippets()
	require.NoError(t, err)
	langs := make([]string, 0, len(snippets))
	for _, s := range snippets {
		langs = append(langs, s.Lang)
		assert.NotEmpty(t, s.Label, s.Lang)
		assert.NotContains(t, s.Code, "{{", "%s left a template action unrendered", s.Lang)
		code := strings.ToLower(s.Code)
		for _, h := range []string{webhook.HeaderTimestamp, webhook.HeaderSignature, webhook.HeaderDeliveryID} {
			assert.Contains(t, code, strings.ToLower(h), "%s must read %s", s.Lang, h)
		}
	}
	assert.Equal(t, []string{webhook.SnippetGo, webhook.SnippetNode, webhook.SnippetPython}, langs)
}

func TestVerifySnippets_AcceptTheDocsVectorAndRejectTampering(t *testing.T) {
	t.Parallel()
	snippets, err := webhook.VerifySnippets()
	require.NoError(t, err)
	cases := verifyCases()
	for _, s := range snippets {
		t.Run(s.Lang, func(t *testing.T) {
			t.Parallel()
			got := runSnippet(t, s, cases)
			for i, c := range cases {
				assert.Equal(t, c.Want, got[i], "%s: %s", s.Lang, c.Name)
			}
		})
	}
}
