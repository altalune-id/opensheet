package onboard_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/onboard"
	"altalune.id/template/internal/testutil/fakes"
)

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func noopUnexpected() apperror.UnexpectedFunc {
	return func(_ context.Context, message string, cause error, _ ...any) *apperror.AppError {
		return apperror.New(apperror.CodeUnexpectedError, message, 0).WithCause(cause)
	}
}

func TestService_Required_TrueWhenMissing(t *testing.T) {
	t.Parallel()
	store := fakes.NewOnboard()
	s := onboard.NewService(store, newTestLogger(), noopUnexpected())
	req, err := s.Required(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !req {
		t.Fatal("want Required=true")
	}
}

func TestService_Required_FalseWhenPresent(t *testing.T) {
	t.Parallel()
	store := fakes.NewOnboard()
	fixed := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	s := onboard.NewService(store, newTestLogger(), noopUnexpected(), onboard.WithClock(func() time.Time { return fixed }))
	if _, err := s.Complete(context.Background(), uuid.New(), onboard.MethodCLIInit); err != nil {
		t.Fatal(err)
	}
	req, err := s.Required(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if req {
		t.Fatal("want Required=false after Complete")
	}
}

func TestService_Complete_ReturnsAlreadyOnboarded(t *testing.T) {
	t.Parallel()
	store := fakes.NewOnboard()
	s := onboard.NewService(store, newTestLogger(), noopUnexpected())
	if _, err := s.Complete(context.Background(), uuid.New(), onboard.MethodEnvGenesis); err != nil {
		t.Fatal(err)
	}
	_, err := s.Complete(context.Background(), uuid.New(), onboard.MethodEnvGenesis)
	if err == nil {
		t.Fatal("expected error on second Complete")
	}
	if !onboard.IsAlreadyOnboardedError(err) {
		t.Fatalf("want IsAlreadyOnboardedError, got %T: %v", err, err)
	}
}

func TestService_Status_NotOnboarded(t *testing.T) {
	t.Parallel()
	store := fakes.NewOnboard()
	s := onboard.NewService(store, newTestLogger(), noopUnexpected())
	_, err := s.Status(context.Background())
	if !onboard.IsNotOnboardedError(err) {
		t.Fatalf("want IsNotOnboardedError, got %T: %v", err, err)
	}
}

func TestService_Status_ReturnsRow(t *testing.T) {
	t.Parallel()
	store := fakes.NewOnboard()
	fixed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := onboard.NewService(store, newTestLogger(), noopUnexpected(), onboard.WithClock(func() time.Time { return fixed }))
	id := uuid.New()
	if _, err := s.Complete(context.Background(), id, onboard.MethodWebOnboard); err != nil {
		t.Fatal(err)
	}
	b, err := s.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if b.OnboardedBy != id {
		t.Errorf("by mismatch")
	}
	if !b.OnboardedAt.Equal(fixed) {
		t.Errorf("timestamp mismatch: %v vs %v", b.OnboardedAt, fixed)
	}
	if b.Method != onboard.MethodWebOnboard {
		t.Errorf("method=%q", b.Method)
	}
}
