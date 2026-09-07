package onboard_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/onboard"
)

func TestMethod_IsValid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		m    onboard.Method
		want bool
	}{
		{onboard.MethodEnvGenesis, true},
		{onboard.MethodWebOnboard, true},
		{onboard.MethodCLIInit, true},
		{onboard.Method(""), false},
		{onboard.Method("other"), false},
	}
	for _, tc := range cases {
		if got := tc.m.IsValid(); got != tc.want {
			t.Errorf("Method(%q).IsValid()=%v want %v", tc.m, got, tc.want)
		}
	}
}

func TestNew_RejectsZeroUUID(t *testing.T) {
	t.Parallel()
	_, err := onboard.New(uuid.Nil, onboard.MethodEnvGenesis, time.Now())
	if err == nil {
		t.Fatal("expected error")
	}
	if !onboard.IsInvalidMethodError(err) {
		t.Fatalf("want IsInvalidMethodError, got %T", err)
	}
}

func TestNew_RejectsUnknownMethod(t *testing.T) {
	t.Parallel()
	_, err := onboard.New(uuid.New(), onboard.Method("bogus"), time.Now())
	if !onboard.IsInvalidMethodError(err) {
		t.Fatalf("want IsInvalidMethodError, got %T: %v", err, err)
	}
}

func TestNew_RejectsZeroTime(t *testing.T) {
	t.Parallel()
	_, err := onboard.New(uuid.New(), onboard.MethodCLIInit, time.Time{})
	if !onboard.IsInvalidMethodError(err) {
		t.Fatalf("want IsInvalidMethodError, got %T: %v", err, err)
	}
}

func TestNew_Ok(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	now := time.Now()
	b, err := onboard.New(id, onboard.MethodCLIInit, now)
	if err != nil {
		t.Fatal(err)
	}
	if b.OnboardedBy != id {
		t.Errorf("by=%v want %v", b.OnboardedBy, id)
	}
	if b.Method != onboard.MethodCLIInit {
		t.Errorf("method=%q", b.Method)
	}
	if b.OnboardedAt.Location() != time.UTC {
		t.Errorf("timestamp not UTC: %v", b.OnboardedAt.Location())
	}
}

func TestNotOnboardedError_ToAppError(t *testing.T) {
	t.Parallel()
	e := &onboard.NotOnboardedError{}
	ae := e.ToAppError()
	if ae == nil {
		t.Fatal("nil AppError")
	}
	if ae.Code() != apperror.CodeOnboardingRequired {
		t.Errorf("code=%q", ae.Code())
	}
}

func TestAlreadyOnboardedError_ToAppError(t *testing.T) {
	t.Parallel()
	e := &onboard.AlreadyOnboardedError{}
	ae := e.ToAppError()
	if ae.Code() != apperror.CodeOnboardingAlreadyDone {
		t.Errorf("code=%q", ae.Code())
	}
}
