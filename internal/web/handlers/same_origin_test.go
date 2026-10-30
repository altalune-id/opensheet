package handlers

import "testing"

func TestSameOrigin(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"https://id.example/p/acme/oauth2/end_session?x=1", "https://id.example/p/acme", true},
		{"https://ID.example:443/logout", "https://id.example:443/p/acme", true},
		{"https://other.example/logout", "https://id.example/p/acme", false},
		{"http://id.example/logout", "https://id.example/p/acme", false},
		{"https://id.example:8443/logout", "https://id.example/p/acme", false},
		{"::bad", "https://id.example", false},
	} {
		if got := sameOrigin(tc.a, tc.b); got != tc.want {
			t.Errorf("sameOrigin(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
