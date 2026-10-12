package gen

import (
	"strings"
	"testing"
)

func TestOptionsSet(t *testing.T) {
	tests := []struct {
		name, key, value, wantPrefix, wantRuntime, wantErr string
	}{
		{name: "valid", key: "ui_prefix", value: "ui://blog", wantPrefix: "ui://blog"},
		{name: "unknown key", key: "nope", value: "x", wantErr: `unknown parameter "nope"`},
		{name: "bad scheme", key: "ui_prefix", value: "https://blog", wantErr: "scheme must be ui"},
		{name: "not a uri", key: "ui_prefix", value: "::::", wantErr: "ui_prefix"},
		{name: "runtime package", key: "runtime_package", value: "example.com/fork/mcp", wantRuntime: "example.com/fork/mcp"},
		{name: "empty runtime package", key: "runtime_package", value: "", wantErr: "runtime_package"},
		{name: "runtime package with a space", key: "runtime_package", value: "example.com/a b", wantErr: "runtime_package"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var o Options
			err := o.Set(tc.key, tc.value)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Set() = %v, want nil", err)
				}
				if o.UIPrefix != tc.wantPrefix {
					t.Errorf("UIPrefix = %q, want %q", o.UIPrefix, tc.wantPrefix)
				}
				if o.RuntimePackage != tc.wantRuntime {
					t.Errorf("RuntimePackage = %q, want %q", o.RuntimePackage, tc.wantRuntime)
				}
				return
			}
			if err == nil {
				t.Fatalf("Set() = nil, want error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Set() = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestRuntimeImportPathDefaults(t *testing.T) {
	tests := []struct{ name, set, want string }{
		{"absent", "", DefaultRuntimePackage},
		{"overridden", "example.com/fork/mcp", "example.com/fork/mcp"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := string(Options{RuntimePackage: tc.set}.runtimeImportPath())
			if got != tc.want {
				t.Errorf("runtimeImportPath() = %q, want %q", got, tc.want)
			}
		})
	}
}
