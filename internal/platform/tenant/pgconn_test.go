package tenant

import (
	"strings"
	"testing"
)

func TestBeginTenanted_UsesParameterizedSetConfig(t *testing.T) {
	const want = "SELECT set_config('app.current_org_id', $1, true)"
	if sqlSetTenant != want {
		t.Fatalf("sqlSetTenant=%q want %q", sqlSetTenant, want)
	}
	if strings.Contains(sqlSetTenant, "SET LOCAL") {
		t.Error("must not use SET LOCAL (no placeholders)")
	}
	if !strings.Contains(sqlSetTenant, "$1") {
		t.Error("must use $1 placeholder — never fmt.Sprintf interpolation")
	}
}
