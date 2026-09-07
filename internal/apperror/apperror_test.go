package apperror_test

import (
	"errors"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/template/gen/go/apperror/v1"
	"altalune.id/template/internal/apperror"
)

func TestAppError_BasicShape(t *testing.T) {
	e := apperror.New("test.not_found", "not found", codes.NotFound,
		&apperrorv1.ErrorDetail{Code: "test.not_found"})
	if got := e.Error(); got != "not found" {
		t.Errorf("Error() = %q, want %q", got, "not found")
	}
	if got := e.Code(); got != "test.not_found" {
		t.Errorf("Code() = %q, want %q", got, "test.not_found")
	}
	if got := e.GRPCCode(); got != codes.NotFound {
		t.Errorf("GRPCCode() = %v, want %v", got, codes.NotFound)
	}
	if len(e.Details()) != 1 {
		t.Fatalf("Details() length = %d, want 1", len(e.Details()))
	}
}

func TestAppError_WithCauseAndUnwrap(t *testing.T) {
	root := errors.New("driver: connection lost")
	e := apperror.New("app.internal", "internal", codes.Internal).WithCause(root)
	if !errors.Is(e, root) {
		t.Fatal("errors.Is should reach the wrapped cause")
	}
}

func TestAppError_WithUpstream(t *testing.T) {
	e := apperror.New("app.internal", "internal", codes.Internal).
		WithUpstream(&apperrorv1.UpstreamErrorDetail{Service: "altalune-auth", Source: "connect", Code: "token.expired"})
	e2 := e.WithUpstream(nil)
	if e2 != e {
		t.Fatal("WithUpstream(nil) should be no-op returning receiver")
	}
}

func TestAsAppError_FindsDirect(t *testing.T) {
	e := apperror.New("test.code", "test", codes.Internal)
	got, ok := apperror.AsAppError(e)
	if !ok || got != e {
		t.Fatalf("AsAppError(*AppError) should return same pointer")
	}
}

func TestAsAppError_FindsThroughProducer(t *testing.T) {
	inner := &fakeTypedError{msg: "typed"}
	wrapped := fmt.Errorf("layer: %w", inner)
	got, ok := apperror.AsAppError(wrapped)
	if !ok {
		t.Fatal("AsAppError should find inner via ToAppError()")
	}
	if got.Code() != "fake.typed" {
		t.Errorf("Code() = %q, want fake.typed", got.Code())
	}
}

type fakeTypedError struct{ msg string }

func (e *fakeTypedError) Error() string { return "fake: " + e.msg }
func (e *fakeTypedError) ToAppError() *apperror.AppError {
	return apperror.New("fake.typed", e.msg, codes.InvalidArgument)
}
