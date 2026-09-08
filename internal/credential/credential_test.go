package credential

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/apperror"
)

func sealedFixture() []byte { return []byte{0x01, 0x02, 0x03} }

func TestNew_Invariants(t *testing.T) {
	tests := []struct {
		name      string
		credName  string
		kind      Kind
		sealed    []byte
		wantName  string
		wantErrIs func(error) bool
	}{
		{name: "trims name", credName: "  prod key  ", kind: KindServiceAccount, sealed: sealedFixture(), wantName: "prod key"},
		{
			name: "accepts 100 rune name", credName: strings.Repeat("é", MaxNameRunes),
			kind: KindServiceAccount, sealed: sealedFixture(), wantName: strings.Repeat("é", MaxNameRunes),
		},
		{name: "accepts google_oauth kind", credName: "oauth", kind: KindGoogleOAuth, sealed: sealedFixture(), wantName: "oauth"},
		{name: "rejects empty name", credName: "", kind: KindServiceAccount, sealed: sealedFixture(), wantErrIs: IsInvalidNameError},
		{name: "rejects whitespace name", credName: "   ", kind: KindServiceAccount, sealed: sealedFixture(), wantErrIs: IsInvalidNameError},
		{
			name: "rejects name over 100 runes", credName: strings.Repeat("a", MaxNameRunes+1),
			kind: KindServiceAccount, sealed: sealedFixture(), wantErrIs: IsInvalidNameError,
		},
		{name: "rejects unknown kind", credName: "prod", kind: Kind("external_account"), sealed: sealedFixture(), wantErrIs: IsInvalidKindError},
		{name: "rejects empty kind", credName: "prod", kind: Kind(""), sealed: sealedFixture(), wantErrIs: IsInvalidKindError},
		{name: "rejects nil sealed", credName: "prod", kind: KindServiceAccount, sealed: nil, wantErrIs: IsNotSealedError},
		{name: "rejects empty sealed", credName: "prod", kind: KindServiceAccount, sealed: []byte{}, wantErrIs: IsNotSealedError},
	}
	orgID, projectID, actorID := uuid.New(), uuid.New(), uuid.New()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, err := New(uuid.Nil, orgID, projectID, actorID, tc.credName, tc.kind, " sa@x.iam.gserviceaccount.com ", tc.sealed)
			if tc.wantErrIs != nil {
				if err == nil {
					t.Fatalf("want error, got credential=%+v", c)
				}
				if !tc.wantErrIs(err) {
					t.Fatalf("wrong error type: %T %v", err, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if c.Name != tc.wantName {
				t.Errorf("Name=%q want %q", c.Name, tc.wantName)
			}
			if c.Kind != tc.kind {
				t.Errorf("Kind=%q want %q", c.Kind, tc.kind)
			}
			if c.Status != StatusActive {
				t.Errorf("Status=%q want %q", c.Status, StatusActive)
			}
			if c.OrgID != orgID || c.ProjectID != projectID || c.AuthorizedByUserID != actorID {
				t.Errorf("ids not carried through: %+v", c)
			}
			if c.GoogleAccountEmail != "sa@x.iam.gserviceaccount.com" {
				t.Errorf("GoogleAccountEmail not trimmed: %q", c.GoogleAccountEmail)
			}
			if c.ID == uuid.Nil {
				t.Error("ID unset")
			}
			if c.CreatedAt.IsZero() || !c.CreatedAt.Equal(c.UpdatedAt) {
				t.Errorf("timestamps: created=%v updated=%v", c.CreatedAt, c.UpdatedAt)
			}
			if c.CreatedAt.Location() != nil && c.CreatedAt.Location().String() != "UTC" {
				t.Errorf("CreatedAt not UTC: %v", c.CreatedAt.Location())
			}
		})
	}
}

func TestNew_KeepsTheSuppliedID(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	c, err := New(id, uuid.New(), uuid.New(), uuid.New(), "prod", KindServiceAccount, "", sealedFixture())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.ID != id {
		t.Fatalf("ID=%v want %v; the ciphertext is sealed against the id the caller chose", c.ID, id)
	}
}

func TestRotate(t *testing.T) {
	t.Run("replaces sealed and reactivates", func(t *testing.T) {
		c, err := New(uuid.Nil, uuid.New(), uuid.New(), uuid.New(), "prod", KindServiceAccount, "old@x.com", sealedFixture())
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		c.MarkReauthNeeded()
		before := c.UpdatedAt

		next := []byte{0x09, 0x08}
		if rErr := c.Rotate(next, "  new@x.com  "); rErr != nil {
			t.Fatalf("Rotate: %v", rErr)
		}
		if !bytes.Equal(c.Sealed, next) {
			t.Errorf("Sealed=%v want %v", c.Sealed, next)
		}
		if c.GoogleAccountEmail != "new@x.com" {
			t.Errorf("GoogleAccountEmail=%q want trimmed new@x.com", c.GoogleAccountEmail)
		}
		if c.Status != StatusActive {
			t.Errorf("Status=%q want %q", c.Status, StatusActive)
		}
		if c.UpdatedAt.Before(before) {
			t.Error("UpdatedAt went backwards")
		}
	})

	t.Run("enforces the same sealed invariant New does", func(t *testing.T) {
		c, err := New(uuid.Nil, uuid.New(), uuid.New(), uuid.New(), "prod", KindServiceAccount, "", sealedFixture())
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		for _, empty := range [][]byte{nil, {}} {
			if rErr := c.Rotate(empty, "x@y.com"); !IsNotSealedError(rErr) {
				t.Fatalf("Rotate(%v) error = %T %v, want *NotSealedError", empty, rErr, rErr)
			}
		}
		if !bytes.Equal(c.Sealed, sealedFixture()) {
			t.Error("a rejected Rotate must not touch the stored ciphertext")
		}
	})
}

func TestMarkReauthNeededAndMarkActive(t *testing.T) {
	c, err := New(uuid.Nil, uuid.New(), uuid.New(), uuid.New(), "prod", KindServiceAccount, "", sealedFixture())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	created := c.UpdatedAt

	c.MarkReauthNeeded()
	if c.Status != StatusReauthNeeded {
		t.Errorf("Status=%q want %q", c.Status, StatusReauthNeeded)
	}
	if c.UpdatedAt.Before(created) {
		t.Error("MarkReauthNeeded moved UpdatedAt backwards")
	}

	c.MarkActive()
	if c.Status != StatusActive {
		t.Errorf("Status=%q want %q", c.Status, StatusActive)
	}
}

func TestKindAndStatus_Valid(t *testing.T) {
	for _, k := range []Kind{KindServiceAccount, KindGoogleOAuth} {
		if !k.Valid() {
			t.Errorf("Kind(%q).Valid() = false", k)
		}
	}
	for _, k := range []Kind{"", "external_account", "authorized_user"} {
		if k.Valid() {
			t.Errorf("Kind(%q).Valid() = true", k)
		}
	}
	for _, s := range []Status{StatusActive, StatusReauthNeeded} {
		if !s.Valid() {
			t.Errorf("Status(%q).Valid() = false", s)
		}
	}
	for _, s := range []Status{"", "revoked"} {
		if s.Valid() {
			t.Errorf("Status(%q).Valid() = true", s)
		}
	}
}

func TestSealAAD_BindsTenantAndRow(t *testing.T) {
	orgA, orgB := uuid.New(), uuid.New()
	projA, projB := uuid.New(), uuid.New()
	credA, credB := uuid.New(), uuid.New()

	base := SealAAD(orgA, projA, credA)
	if len(base) == 0 {
		t.Fatal("SealAAD returned nothing")
	}
	if !bytes.Equal(base, SealAAD(orgA, projA, credA)) {
		t.Fatal("SealAAD is not deterministic")
	}
	for name, other := range map[string][]byte{
		"other org":        SealAAD(orgB, projA, credA),
		"other project":    SealAAD(orgA, projB, credA),
		"other credential": SealAAD(orgA, projA, credB),
	} {
		if bytes.Equal(base, other) {
			t.Errorf("SealAAD collides with %s; a stolen ciphertext would replay", name)
		}
	}
	for _, id := range []uuid.UUID{orgA, projA, credA} {
		if !strings.Contains(string(base), id.String()) {
			t.Errorf("SealAAD omits %v", id)
		}
	}
}

func TestErrors_ToAppErrorAndPredicates(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		toApp     func() *apperror.AppError
		wantCode  string
		wantIn    string
		predicate func(error) bool
	}{
		{
			name: "not found", err: &NotFoundError{ID: "cred-1"},
			toApp:    (&NotFoundError{ID: "cred-1"}).ToAppError,
			wantCode: apperror.CodeCredentialNotFound, wantIn: "cred-1", predicate: IsNotFoundError,
		},
		{
			name: "already exists", err: &AlreadyExistsError{Name: "prod"},
			toApp:    (&AlreadyExistsError{Name: "prod"}).ToAppError,
			wantCode: apperror.CodeCredentialAlreadyExists, wantIn: "prod", predicate: IsAlreadyExistsError,
		},
		{
			name: "invalid name", err: &InvalidNameError{Reason: "empty"},
			toApp:    (&InvalidNameError{Reason: "empty"}).ToAppError,
			wantCode: apperror.CodeCredentialInvalidName, wantIn: "empty", predicate: IsInvalidNameError,
		},
		{
			name: "invalid kind", err: &InvalidKindError{Kind: "external_account"},
			toApp:    (&InvalidKindError{Kind: "external_account"}).ToAppError,
			wantCode: apperror.CodeCredentialInvalidKind, wantIn: "external_account", predicate: IsInvalidKindError,
		},
		{
			name: "invalid service account", err: &InvalidServiceAccountError{Reason: "not valid JSON"},
			toApp:    (&InvalidServiceAccountError{Reason: "not valid JSON"}).ToAppError,
			wantCode: apperror.CodeCredentialInvalidKind, wantIn: "not valid JSON", predicate: IsInvalidServiceAccountError,
		},
		{
			name: "in use", err: &InUseError{ID: "cred-2"},
			toApp:    (&InUseError{ID: "cred-2"}).ToAppError,
			wantCode: apperror.CodeCredentialInUse, wantIn: "cred-2", predicate: IsInUseError,
		},
		{
			name: "reauth needed", err: &ReauthNeededError{ID: "cred-3"},
			toApp:    (&ReauthNeededError{ID: "cred-3"}).ToAppError,
			wantCode: apperror.CodeCredentialReauthNeeded, wantIn: "cred-3", predicate: IsReauthNeededError,
		},
		{
			name: "not sealed", err: &NotSealedError{Situation: "rotate"},
			toApp:    (&NotSealedError{Situation: "rotate"}).ToAppError,
			wantCode: apperror.CodeCredentialNotSealed, wantIn: "rotate", predicate: IsNotSealedError,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg := tc.err.Error()
			if !strings.HasPrefix(msg, "credential: ") {
				t.Errorf("Error()=%q must start with %q", msg, "credential: ")
			}
			if !strings.Contains(msg, tc.wantIn) {
				t.Errorf("Error()=%q missing %q", msg, tc.wantIn)
			}
			ae := tc.toApp()
			if ae == nil {
				t.Fatal("ToAppError returned nil")
			}
			if ae.Code() != tc.wantCode {
				t.Errorf("Code()=%q want %q", ae.Code(), tc.wantCode)
			}
			if !tc.predicate(fmt.Errorf("wrapped: %w", tc.err)) {
				t.Error("predicate does not match through a wrap")
			}
			if tc.predicate(errors.New("unrelated")) {
				t.Error("predicate matched an unrelated error")
			}
		})
	}
}
