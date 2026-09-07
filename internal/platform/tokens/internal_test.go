package tokens

import "testing"

func TestSplitScope(t *testing.T) {
	got := splitScope("openid profile email")
	if len(got) != 3 || got[0] != "openid" || got[2] != "email" {
		t.Errorf("got=%v", got)
	}
	if len(splitScope("")) != 0 {
		t.Errorf("empty string should yield empty slice")
	}
}
