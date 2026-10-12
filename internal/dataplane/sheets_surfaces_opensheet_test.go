package dataplane_test

import (
	"os"
	"regexp"
	"slices"
	"testing"

	"altalune.id/opensheet/internal/controlplane"
	"altalune.id/opensheet/internal/dataplane"
	"altalune.id/opensheet/internal/platform/surfaces"
)

var sheetRouteRE = regexp.MustCompile(`\w+\.HandleFunc\("([A-Z]+ [^"]+)"`)

func TestSheetVerbTableMatchesRegisteredRoutes(t *testing.T) {
	src, err := os.ReadFile("sheets_opensheet.go")
	if err != nil {
		t.Fatalf("read sheets_opensheet.go: %v", err)
	}
	var registered []string
	for _, m := range sheetRouteRE.FindAllStringSubmatch(string(src), -1) {
		registered = append(registered, m[1])
	}
	if len(registered) < 10 {
		t.Fatalf("found only %d registrations (%v); the registration shape changed and this guard needs updating", len(registered), registered)
	}
	declared := dataplane.VerbTable()
	for _, route := range registered {
		if _, ok := declared[route]; !ok {
			t.Errorf("route %q is registered by NewSheetHandler but names no verb in VerbTable()", route)
		}
	}
	for route := range declared {
		if !slices.Contains(registered, route) {
			t.Errorf("VerbTable() declares %q but NewSheetHandler registers no such route", route)
		}
	}
}

func TestSheetVerbsLiveOnOnePlane(t *testing.T) {
	// NOTE: these verbs predate the seven-surface taxonomy and are served on both machine planes; R1 wants one dropped.
	sheetPlaneOverlaps := map[surfaces.Verb]bool{
		{Module: "sheet", Aggregate: "sheet", Operation: "purge_cache"}:           true,
		{Module: "spreadsheet", Aggregate: "spreadsheet", Operation: "list_tabs"}: true,
	}
	control := map[surfaces.Verb]bool{}
	for _, v := range controlplane.Verbs() {
		control[v] = true
	}
	for _, v := range dataplane.Verbs() {
		if control[v] && !sheetPlaneOverlaps[v] {
			t.Errorf("R1: %+v is registered on both the control plane and the sheet data plane", v)
		}
	}
	for v := range sheetPlaneOverlaps {
		if !control[v] {
			t.Errorf("R1 allowlist names %+v, which the control plane no longer serves — drop it from the allowlist", v)
		}
	}
}

// TestControlPlaneVerbTableMatchesScopeTable is R1's registry half for S2.
func TestControlPlaneVerbTableMatchesScopeTable(t *testing.T) {
	scoped := controlplane.ScopeTable()
	declared := controlplane.VerbTable()

	if len(scoped) == 0 {
		t.Fatal("controlplane.ScopeTable() is empty — this guard needs updating")
	}
	for procedure := range scoped {
		if _, ok := declared[procedure]; !ok {
			t.Errorf("procedure %s has a scope entry but names no verb in controlplane.VerbTable()", procedure)
		}
	}
	for procedure := range declared {
		if _, ok := scoped[procedure]; !ok {
			t.Errorf("controlplane.VerbTable() declares %s but controlplane.ScopeTable() does not", procedure)
		}
	}
}
