package interceptor_test

import (
	"testing"

	"altalune.id/template/internal/api/interceptor"
)

func TestOTel_ReturnsNonNil(t *testing.T) {
	got, err := interceptor.OTel(nil, nil)
	if err != nil {
		t.Fatalf("OTel: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil interceptor")
	}
}
