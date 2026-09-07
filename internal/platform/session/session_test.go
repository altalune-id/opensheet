package session

import (
	"context"
	"testing"
)

func TestPrincipalInto_And_From(t *testing.T) {
	ctx := PrincipalInto(context.Background(), Principal{Email: "a@b"})
	got := PrincipalFrom(ctx)
	if got.Email != "a@b" {
		t.Errorf("got=%+v", got)
	}
}

func TestPrincipalFrom_Missing(t *testing.T) {
	got := PrincipalFrom(context.Background())
	if got.Email != "" {
		t.Errorf("expected zero Principal, got %+v", got)
	}
}
