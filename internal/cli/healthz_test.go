package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestWithHealthzPath(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"bare host", "http://h:1", "http://h:1/healthz"},
		{"host with root path", "http://h:1/", "http://h:1/healthz"},
		{"host already naming healthz", "http://h:1/healthz", "http://h:1/healthz"},
		{"host naming another path", "http://h:1/probe", "http://h:1/probe"},
		{"host naming a base path", "http://h:1/app/", "http://h:1/app/"},
		{"https bare host", "https://prod.example", "https://prod.example/healthz"},
		{"unparseable", "://nope", "://nope"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := withHealthzPath(tc.in); got != tc.want {
				t.Fatalf("withHealthzPath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestHealthzHasNoLocalURLFlag(t *testing.T) {
	if f := newHealthzCmd().Flags().Lookup("url"); f != nil {
		t.Fatal("healthz must not declare a local --url; it would shadow the root persistent flag")
	}
}

func runHealthz(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := NewRootCmd(stubServerBoot, stubClientBoot)
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs(append([]string{"healthz", "--timeout", "2s"}, args...))
	err := root.ExecuteContext(context.Background())
	return buf.String(), err
}

func healthzRecorder(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits = append(hits, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), hits...)
	}
}

func writeProfileFor(t *testing.T, path, target string) {
	t.Helper()
	b, err := json.Marshal(sessionFile{Profiles: map[string]profile{target: {URL: target}}})
	if err != nil {
		t.Fatalf("marshal session file: %v", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("write session file: %v", err)
	}
}

func TestHealthzProbeTarget(t *testing.T) {
	tests := []struct {
		name       string
		flagURL    func(other string) string
		envURL     func(other string) string
		baseURL    func(other string) string
		profile    bool
		wantRemote bool
		wantPath   string
	}{
		{
			name:       "--url bare host probes that host",
			flagURL:    func(other string) string { return other },
			wantRemote: true,
			wantPath:   "/healthz",
		},
		{
			name:       "OPENSHEET_URL probes that host",
			envURL:     func(other string) string { return other },
			wantRemote: true,
			wantPath:   "/healthz",
		},
		{
			name:     "http.baseURL naming another host does not move the probe",
			baseURL:  func(other string) string { return other },
			wantPath: "/healthz",
		},
		{
			name:     "a saved profile naming another host does not move the probe",
			profile:  true,
			wantPath: "/healthz",
		},
		{
			name:       "--url naming the same host as http.baseURL still probes it",
			flagURL:    func(other string) string { return other },
			baseURL:    func(other string) string { return other },
			wantRemote: true,
			wantPath:   "/healthz",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			local, localHits := healthzRecorder(t)
			other, otherHits := healthzRecorder(t)

			sessPath := setSelfhostedEnv(t)
			_, port, err := net.SplitHostPort(strings.TrimPrefix(local.URL, "http://"))
			if err != nil {
				t.Fatalf("split local addr: %v", err)
			}
			t.Setenv("OPENSHEET_HTTP_ADDR", "127.0.0.1:"+port)
			t.Setenv("OPENSHEET_OUTPUT", "text")

			base := ""
			if tc.baseURL != nil {
				base = tc.baseURL(other.URL)
			}
			t.Setenv("OPENSHEET_HTTP_BASE_URL", base)
			t.Setenv("OPENSHEET_HTTP_BASEURL", base)

			env := ""
			if tc.envURL != nil {
				env = tc.envURL(other.URL)
			}
			t.Setenv("OPENSHEET_URL", env)

			if tc.profile {
				writeProfileFor(t, sessPath, other.URL)
			}

			var args []string
			if tc.flagURL != nil {
				args = []string{"--url", tc.flagURL(other.URL)}
			}

			out, runErr := runHealthz(t, args...)
			if runErr != nil {
				t.Fatalf("healthz: %v; out=%q", runErr, out)
			}

			probed, quiet := localHits(), otherHits()
			wantURL := local.URL
			if tc.wantRemote {
				probed, quiet = otherHits(), localHits()
				wantURL = other.URL
			}
			if len(quiet) != 0 {
				t.Fatalf("probed the wrong server: %v; out=%q", quiet, out)
			}
			if len(probed) != 1 || probed[0] != tc.wantPath {
				t.Fatalf("probed %v, want [%q]; out=%q", probed, tc.wantPath, out)
			}
			if !strings.Contains(out, wantURL+tc.wantPath) {
				t.Fatalf("output %q must name %q", out, wantURL+tc.wantPath)
			}
		})
	}
}

func TestHealthzRefusesAFalseHealthyRoot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	setSelfhostedEnv(t)
	t.Setenv("OPENSHEET_OUTPUT", "text")

	out, err := runHealthz(t, "--url", srv.URL)
	if !errors.Is(err, errHealthzUnhealthy) {
		t.Fatalf("a 200 at / must not mask a failing /healthz: err = %v, out = %q", err, out)
	}
	if !strings.Contains(out, "/healthz") {
		t.Fatalf("output %q must name the probed /healthz URL", out)
	}
}

func TestHealthzCmd(t *testing.T) {
	tests := []struct {
		name       string
		handler    http.HandlerFunc
		output     string
		wantErr    bool
		wantSubstr string
	}{
		{
			name:       "200 text ok",
			handler:    func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) },
			output:     "text",
			wantErr:    false,
			wantSubstr: "ok   ",
		},
		{
			name:       "500 text fail",
			handler:    func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) },
			output:     "text",
			wantErr:    true,
			wantSubstr: "fail ",
		},
		{
			name:       "200 json ok:true",
			handler:    func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) },
			output:     "json",
			wantErr:    false,
			wantSubstr: `"ok": true`,
		},
		{
			name:       "500 json ok:false",
			handler:    func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) },
			output:     "json",
			wantErr:    true,
			wantSubstr: `"ok": false`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			t.Cleanup(srv.Close)

			setSelfhostedEnv(t)
			t.Setenv("OPENSHEET_OUTPUT", tc.output)

			out, err := runHealthz(t, "--url", srv.URL)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v; out=%q", err, tc.wantErr, out)
			}
			if tc.wantErr && !errors.Is(err, errHealthzUnhealthy) {
				t.Fatalf("want errHealthzUnhealthy, got %v", err)
			}
			if !strings.Contains(out, tc.wantSubstr) {
				t.Fatalf("output = %q, want substring %q", out, tc.wantSubstr)
			}
		})
	}
}

func TestDefaultHealthzURL_DefaultAddr(t *testing.T) {
	got := defaultHealthzURL(t.Context())
	if got != "http://127.0.0.1:5150/healthz" {
		t.Fatalf("default = %q", got)
	}
}
