package controlplane_test

import (
	"strings"
	"testing"

	"altalune.id/opensheet/internal/controlplane"
)

// TestEveryRPCHasAScope guards the fail-closed rule: a mounted procedure with no scope entry is callable unchecked.
func TestEveryRPCHasAScope(t *testing.T) {
	srv := controlplane.New(nil, nil, controlplane.Deps{})
	_ = srv.Handler("")

	mounted := srv.MountedProcedures()
	if len(mounted) == 0 {
		t.Fatal("no procedures discovered; the test cannot guard anything")
	}

	table := controlplane.ScopeTable()
	for _, procedure := range mounted {
		t.Run(procedure, func(t *testing.T) {
			if _, ok := table[procedure]; !ok {
				t.Errorf("procedure %s has no scope entry in controlplane.ScopeTable()", procedure)
			}
		})
	}

	for procedure := range table {
		if !strings.HasPrefix(procedure, "/") {
			t.Errorf("scope table key %q must be a full procedure path starting with /", procedure)
		}
	}
}
