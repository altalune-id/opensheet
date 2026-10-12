package reqid_test

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"altalune.id/opensheet/reqid"
)

var uuidV7Re = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestNew_ProducesUUIDv7(t *testing.T) {
	id := reqid.New()
	if !uuidV7Re.MatchString(id) {
		t.Fatalf("New() = %q, want UUIDv7", id)
	}
}

func TestNew_UniquePerCall(t *testing.T) {
	a, b := reqid.New(), reqid.New()
	if a == b {
		t.Fatal("two consecutive New() returned the same id")
	}
}

func TestWithContext_Roundtrip(t *testing.T) {
	ctx := reqid.WithContext(context.Background(), "test-id")
	if got := reqid.FromContext(ctx); got != "test-id" {
		t.Errorf("FromContext = %q, want test-id", got)
	}
}

func TestFromContext_EmptyOnBareContext(t *testing.T) {
	if got := reqid.FromContext(context.Background()); got != "" {
		t.Errorf("FromContext(bare) = %q, want empty", got)
	}
}

func TestEnsure_PreservesExisting(t *testing.T) {
	ctx := reqid.WithContext(context.Background(), "existing")
	got, id := reqid.Ensure(ctx)
	if id != "existing" {
		t.Errorf("Ensure preserved id = %q, want existing", id)
	}
	if reqid.FromContext(got) != "existing" {
		t.Error("Ensure returned ctx must still carry existing id")
	}
}

func TestEnsure_GeneratesWhenAbsent(t *testing.T) {
	got, id := reqid.Ensure(context.Background())
	if !uuidV7Re.MatchString(id) {
		t.Fatalf("Ensure generated id = %q, want UUIDv7", id)
	}
	if reqid.FromContext(got) != id {
		t.Error("Ensure returned ctx must carry the generated id")
	}
}

func TestFromHTTPHeader(t *testing.T) {
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set(reqid.Header, "hdr-id")
	if got := reqid.FromHTTPHeader(r); got != "hdr-id" {
		t.Errorf("FromHTTPHeader = %q, want hdr-id", got)
	}
}

func TestSanitize(t *testing.T) {
	long := strings.Repeat("a", reqid.MaxLength+1)
	atCap := strings.Repeat("a", reqid.MaxLength)
	for _, tt := range []struct {
		name string
		in   string
		want string
	}{
		{"uuid v7", "0192a3f1-c7c1-7c1d-b1d1-abcdef012345", "0192a3f1-c7c1-7c1d-b1d1-abcdef012345"},
		{"w3c traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"},
		{"underscore dot colon", "svc_a.edge:1", "svc_a.edge:1"},
		{"at the length cap", atCap, atCap},
		{"empty", "", ""},
		{"oversized", long, ""},
		{"space", "abc def", ""},
		{"tab", "abc\tdef", ""},
		{"nul byte", "abc\x00def", ""},
		{"escape byte", "abc\x1b[31mdef", ""},
		{"newline injection", "abc\n{\"level\":\"ERROR\",\"msg\":\"forged\"}", ""},
		{"carriage return injection", "abc\r\nSet-Cookie: x=y", ""},
		{"json breakout", `abc","forged":"yes`, ""},
		{"backslash", `abc\def`, ""},
		{"non ascii", "abcédef", ""},
		{"del byte", "abc\x7f", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := reqid.Sanitize(tt.in); got != tt.want {
				t.Errorf("Sanitize(%q) = %q, want %q", tt.in, got, tt.want)
			}
			h := http.Header{}
			h.Set(reqid.Header, tt.in)
			if got := reqid.FromHeader(h); got != tt.want {
				t.Errorf("FromHeader(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestFromHTTPHeader_RejectsUnsafeInbound(t *testing.T) {
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set(reqid.Header, "bad id with spaces")
	if got := reqid.FromHTTPHeader(r); got != "" {
		t.Errorf("FromHTTPHeader = %q, want empty", got)
	}
}
