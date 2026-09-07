package cli

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
			t.Setenv("ALT_OUTPUT", tc.output)
			srv := httptest.NewServer(tc.handler)
			t.Cleanup(srv.Close)

			cmd := newHealthzCmd()
			cmd.SetArgs([]string{"--url", srv.URL, "--timeout", "2s"})
			buf := new(bytes.Buffer)
			cmd.SetOut(buf)
			cmd.SetErr(buf)

			err := cmd.ExecuteContext(t.Context())
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v; out=%q", err, tc.wantErr, buf.String())
			}
			if tc.wantErr && !errors.Is(err, errHealthzUnhealthy) {
				t.Fatalf("want errHealthzUnhealthy, got %v", err)
			}
			if !strings.Contains(buf.String(), tc.wantSubstr) {
				t.Fatalf("output = %q, want substring %q", buf.String(), tc.wantSubstr)
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
