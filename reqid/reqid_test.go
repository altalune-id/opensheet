package reqid_test

import (
	"context"
	"net/http"
	"regexp"
	"testing"

	"altalune.id/template/reqid"
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
