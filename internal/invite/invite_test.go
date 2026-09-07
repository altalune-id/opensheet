package invite

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"altalune.id/template/internal/apperror"
)

func TestNew(t *testing.T) {
	t.Parallel()
	orgID := uuid.New()
	base := NewParams{
		OrgID: orgID,
		Email: "  ALICE@example.com ",
		Role:  RoleMember,
		TTL:   time.Hour,
		Token: "raw-token-value",
		Now:   time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
	}

	tests := []struct {
		name      string
		mutate    func(p *NewParams)
		wantErrIs func(error) bool
	}{
		{name: "happy path", mutate: nil},
		{name: "rejects invalid role", mutate: func(p *NewParams) { p.Role = "guest" }, wantErrIs: IsInvalidRoleError},
		{name: "rejects empty email", mutate: func(p *NewParams) { p.Email = "" }, wantErrIs: IsInvalidEmailError},
		{name: "rejects malformed email", mutate: func(p *NewParams) { p.Email = "not-an-email" }, wantErrIs: IsInvalidEmailError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := base
			if tc.mutate != nil {
				tc.mutate(&p)
			}
			inv, err := New(p)
			if tc.wantErrIs != nil {
				if err == nil {
					t.Fatalf("want error, got invite=%+v", inv)
				}
				if !tc.wantErrIs(err) {
					t.Fatalf("wrong error type: %T %v", err, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if inv.OrgID != orgID {
				t.Errorf("OrgID not carried")
			}
			if inv.Email != "alice@example.com" {
				t.Errorf("email not normalized: %q", inv.Email)
			}
			if inv.TokenHash == "" || inv.TokenHash == p.Token {
				t.Errorf("token not hashed: %q", inv.TokenHash)
			}
			if !inv.ExpiresAt.After(inv.CreatedAt) {
				t.Errorf("ExpiresAt should be after CreatedAt: %+v", inv)
			}
			if inv.UsedAt != nil {
				t.Errorf("UsedAt should be nil on fresh invite")
			}
		})
	}
}

func TestHashToken_Deterministic(t *testing.T) {
	t.Parallel()
	a := HashToken("hello")
	b := HashToken("hello")
	if a != b {
		t.Errorf("HashToken not deterministic")
	}
	if a == HashToken("world") {
		t.Errorf("HashToken should differ across inputs")
	}
	if len(a) != 64 {
		t.Errorf("expected 64-char sha256 hex, got len=%d", len(a))
	}
}

func TestIsExpired(t *testing.T) {
	t.Parallel()
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	inv := &Invite{ExpiresAt: created.Add(time.Hour)}
	if inv.IsExpired(created) {
		t.Errorf("should not be expired at creation")
	}
	if !inv.IsExpired(created.Add(2 * time.Hour)) {
		t.Errorf("should be expired 2h after creation")
	}
}

func TestIsUsed_And_Consume(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	inv := &Invite{ID: uuid.New(), ExpiresAt: now.Add(time.Hour)}
	if inv.IsUsed() {
		t.Fatalf("fresh invite should not be used")
	}
	if err := inv.Consume(now); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if !inv.IsUsed() {
		t.Errorf("Consume should set UsedAt")
	}
	if err := inv.Consume(now); !IsAlreadyUsedError(err) {
		t.Errorf("second Consume: want IsAlreadyUsedError, got %v", err)
	}
}

func TestConsume_Expired(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	inv := &Invite{ID: uuid.New(), ExpiresAt: now.Add(-time.Second)}
	if err := inv.Consume(now); !IsExpiredError(err) {
		t.Errorf("want IsExpiredError, got %v", err)
	}
}

func TestErrors_ToAppError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  interface {
			error
			ToAppError() *apperror.AppError
		}
		wantCode string
	}{
		{"NotFound", &NotFoundError{ID: "abc"}, apperror.CodeInviteNotFound},
		{"Expired", &ExpiredError{ID: "abc"}, apperror.CodeInviteExpired},
		{"AlreadyUsed", &AlreadyUsedError{ID: "abc"}, apperror.CodeInviteAlreadyUsed},
		{"InvalidRole", &InvalidRoleError{Role: "guest"}, apperror.CodeInviteInvalidRole},
		{"InvalidEmail", &InvalidEmailError{Reason: "empty"}, apperror.CodeValidation},
		{"TokenMismatch", &TokenMismatchError{}, apperror.CodeInviteNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.err.Error() == "" {
				t.Errorf("empty error string for %T", tc.err)
			}
			if got := tc.err.ToAppError().Code(); got != tc.wantCode {
				t.Errorf("code=%q want %q", got, tc.wantCode)
			}
		})
	}
}
