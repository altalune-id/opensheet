package authn_test

import (
	"net/http"
	"testing"

	"altalune.id/opensheet/internal/platform/authn"
)

func TestSchemeLooks(t *testing.T) {
	s := authn.Scheme{Prefix: "key_"}
	tests := []struct {
		name string
		raw  string
		want authn.Shape
	}{
		{"empty", "", authn.ShapeUnknown},
		{"api key", "key_abc123", authn.ShapeAPIKey},
		{"jwt", "aaa.bbb.ccc", authn.ShapeJWT},
		{"jwt with space is not a jwt", "aaa.bbb ccc", authn.ShapeUnknown},
		{"other prefix", "osk_abc", authn.ShapeUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := s.Looks(tt.raw); got != tt.want {
				t.Fatalf("Looks(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestZeroSchemeClassifiesJWTOnly(t *testing.T) {
	var s authn.Scheme
	if got := s.Looks("key_abc"); got != authn.ShapeUnknown {
		t.Fatalf("zero Scheme matched a prefix: %v", got)
	}
	if got := s.Looks("aaa.bbb.ccc"); got != authn.ShapeJWT {
		t.Fatalf("zero Scheme did not classify a JWT: %v", got)
	}
}

func TestCredentialFromHeader(t *testing.T) {
	tests := []struct{ name, header, value, want string }{
		{"bearer", "Authorization", "Bearer key_abc", "key_abc"},
		{"lowercase bearer", "Authorization", "bearer key_abc", "key_abc"},
		{"api key header", "X-API-Key", "key_abc", "key_abc"},
		{"basic is not taken", "Authorization", "Basic abc", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := http.Header{}
			h.Set(tt.header, tt.value)
			if got := authn.CredentialFromHeader(h); got != tt.want {
				t.Fatalf("CredentialFromHeader = %q, want %q", got, tt.want)
			}
		})
	}
}
