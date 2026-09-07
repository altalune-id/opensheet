package icons

import "testing"

func TestHas_ChevronRightIsVendored(t *testing.T) {
	if !Has("chevron-right") {
		t.Fatal("chevron-right must be vendored — the projects list row affordance uses it")
	}
}
