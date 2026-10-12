package invite

import "testing"

func TestNormalizeEmail_RejectsAddressesTheAtCheckAllowed(t *testing.T) {
	rejected := []struct{ name, in string }{
		{"crlf header injection", "a\r\nBcc: attacker@evil.com"},
		{"lf header injection", "a\nBcc: attacker@evil.com"},
		{"display name spoof", `foo" <evil@evil.com>`},
		{"comma separated list", "victim@x.com, attacker@evil.com"},
		{"angle bracket form", "<a@b.com>"},
		{"no at sign", "not-an-email"},
		{"empty", ""},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeEmail(tc.in)
			if err == nil {
				t.Fatalf("normalizeEmail(%q) = %q, want an error", tc.in, got)
			}
			if !IsInvalidEmailError(err) {
				t.Fatalf("got %T, want *InvalidEmailError", err)
			}
		})
	}
}

func TestNormalizeEmail_AcceptsAndLowercasesBareAddresses(t *testing.T) {
	for in, want := range map[string]string{
		"a@b.com":            "a@b.com",
		"  A@B.COM  ":        "a@b.com",
		"first.last@x.co.id": "first.last@x.co.id",
		"a+tag@b.com":        "a+tag@b.com",
	} {
		got, err := normalizeEmail(in)
		if err != nil {
			t.Fatalf("normalizeEmail(%q): %v", in, err)
		}
		if got != want {
			t.Fatalf("normalizeEmail(%q) = %q, want %q", in, got, want)
		}
	}
}
