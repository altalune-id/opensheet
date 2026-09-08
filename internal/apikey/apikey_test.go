package apikey_test

import (
	"crypto/sha256"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/authn"
)

var plaintextPattern = regexp.MustCompile(`^osk_[0-9a-f]{16}_[A-Za-z0-9_-]{43}$`)

func mustMint(t *testing.T) (*apikey.APIKey, string) {
	t.Helper()
	k, plaintext, err := apikey.Mint(uuid.New(), uuid.New(), "ci", []string{authn.ScopeSheetsRead}, nil, nil, time.Now().UTC())
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	return k, plaintext
}

func TestMint_Name(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name    string
		input   string
		want    string
		invalid bool
	}{
		{name: "trims surrounding space", input: "  deploy bot  ", want: "deploy bot"},
		{name: "empty is rejected", input: "", invalid: true},
		{name: "whitespace only is rejected", input: "  \t ", invalid: true},
		{name: "exactly 100 runes is accepted", input: strings.Repeat("é", 100), want: strings.Repeat("é", 100)},
		{name: "101 runes is rejected", input: strings.Repeat("é", 101), invalid: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			k, _, err := apikey.Mint(uuid.New(), uuid.New(), tc.input, []string{authn.ScopeSheetsRead}, nil, nil, now)
			if tc.invalid {
				if !apikey.IsInvalidNameError(err) {
					t.Fatalf("err = %v, want *InvalidNameError", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Mint: %v", err)
			}
			if k.Name != tc.want {
				t.Errorf("Name = %q, want %q", k.Name, tc.want)
			}
		})
	}
}

func TestMint_Scopes(t *testing.T) {
	now := time.Now().UTC()

	t.Run("empty set is rejected", func(t *testing.T) {
		_, _, err := apikey.Mint(uuid.New(), uuid.New(), "ci", nil, nil, nil, now)
		if !apikey.IsInvalidScopeError(err) {
			t.Fatalf("err = %v, want *InvalidScopeError", err)
		}
	})
	t.Run("blank entry is rejected", func(t *testing.T) {
		_, _, err := apikey.Mint(uuid.New(), uuid.New(), "ci", []string{authn.ScopeSheetsRead, "  "}, nil, nil, now)
		if !apikey.IsInvalidScopeError(err) {
			t.Fatalf("err = %v, want *InvalidScopeError", err)
		}
	})
	t.Run("duplicates collapse", func(t *testing.T) {
		k, _, err := apikey.Mint(uuid.New(), uuid.New(), "ci",
			[]string{authn.ScopeSheetsRead, authn.ScopeSheetsRead, " " + authn.ScopeCachePurge + " "}, nil, nil, now)
		if err != nil {
			t.Fatalf("Mint: %v", err)
		}
		want := []string{authn.ScopeSheetsRead, authn.ScopeCachePurge}
		if len(k.Scopes) != len(want) {
			t.Fatalf("Scopes = %v, want %v", k.Scopes, want)
		}
		for i := range want {
			if k.Scopes[i] != want[i] {
				t.Fatalf("Scopes = %v, want %v", k.Scopes, want)
			}
		}
	})
	t.Run("every catalog scope is accepted", func(t *testing.T) {
		k, _, err := apikey.Mint(uuid.New(), uuid.New(), "ci", authn.AllScopes(), nil, nil, now)
		if err != nil {
			t.Fatalf("Mint: %v", err)
		}
		if len(k.Scopes) != len(authn.AllScopes()) {
			t.Errorf("Scopes len = %d, want %d", len(k.Scopes), len(authn.AllScopes()))
		}
	})
}

func TestMint_ExpiresAt(t *testing.T) {
	now := time.Now().UTC()
	past := now.Add(-time.Second)
	future := now.Add(time.Hour)

	tests := []struct {
		name    string
		in      *time.Time
		invalid bool
	}{
		{name: "nil never expires", in: nil},
		{name: "future is accepted", in: &future},
		{name: "equal to now is rejected", in: &now, invalid: true},
		{name: "in the past is rejected", in: &past, invalid: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			k, _, err := apikey.Mint(uuid.New(), uuid.New(), "ci", []string{authn.ScopeSheetsRead}, nil, tc.in, now)
			if tc.invalid {
				if !apikey.IsInvalidExpiryError(err) {
					t.Fatalf("err = %v, want *InvalidExpiryError", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Mint: %v", err)
			}
			if tc.in == nil && k.ExpiresAt != nil {
				t.Errorf("ExpiresAt = %v, want nil", k.ExpiresAt)
			}
			if tc.in != nil && (k.ExpiresAt == nil || !k.ExpiresAt.Equal(*tc.in) || k.ExpiresAt.Location() != time.UTC) {
				t.Errorf("ExpiresAt = %v, want UTC %v", k.ExpiresAt, tc.in)
			}
		})
	}
}

func TestMint_SheetIDsAreDeduplicated(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	k, _, err := apikey.Mint(uuid.New(), uuid.New(), "ci", []string{authn.ScopeSheetsRead}, []uuid.UUID{a, b, a, b, a}, nil, time.Now().UTC())
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if len(k.SheetIDs) != 2 || k.SheetIDs[0] != a || k.SheetIDs[1] != b {
		t.Fatalf("SheetIDs = %v, want [%v %v]", k.SheetIDs, a, b)
	}
}

func TestMint_WireFormat(t *testing.T) {
	orgID, projectID := uuid.New(), uuid.New()
	now := time.Now().UTC()
	k, plaintext, err := apikey.Mint(orgID, projectID, "ci", []string{authn.ScopeSheetsRead}, nil, nil, now)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if !plaintextPattern.MatchString(plaintext) {
		t.Fatalf("plaintext = %q, want osk_<16 hex>_<43 base64url>", plaintext)
	}
	prefix, secret, err := apikey.Parse(plaintext)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if prefix != k.KeyPrefix {
		t.Errorf("prefix = %q, want %q", prefix, k.KeyPrefix)
	}
	sum := sha256.Sum256([]byte(secret))
	if string(k.SecretHash) != string(sum[:]) {
		t.Error("SecretHash is not the SHA-256 of the secret half")
	}
	if strings.Contains(string(k.SecretHash), secret) {
		t.Error("the plaintext secret survives inside SecretHash")
	}
	if k.OrgID != orgID || k.ProjectID != projectID {
		t.Error("tenant identity is not propagated")
	}
	if k.CreatedAt.Location() != time.UTC {
		t.Errorf("CreatedAt is not UTC: %v", k.CreatedAt)
	}
	if k.ID == uuid.Nil {
		t.Error("ID is the nil uuid")
	}
	if k.LastUsedAt != nil || k.RevokedAt != nil {
		t.Error("a fresh key must have no LastUsedAt or RevokedAt")
	}
}

func TestMint_ProducesDistinctKeys(t *testing.T) {
	seen := map[string]bool{}
	for range 32 {
		k, plaintext, err := apikey.Mint(uuid.New(), uuid.New(), "ci", []string{authn.ScopeSheetsRead}, nil, nil, time.Now().UTC())
		if err != nil {
			t.Fatalf("Mint: %v", err)
		}
		if seen[plaintext] || seen[k.KeyPrefix] {
			t.Fatal("crypto/rand produced a repeat; the key material is not random")
		}
		seen[plaintext] = true
		seen[k.KeyPrefix] = true
	}
}

func TestParse(t *testing.T) {
	var goodPrefix, goodSecret string
	for {
		_, plaintext := mustMint(t)
		p, sec, err := apikey.Parse(plaintext)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		// NOTE: the fixtures below slice both halves, so pick a secret with no '_' to keep them readable.
		if !strings.Contains(sec, "_") {
			goodPrefix, goodSecret = p, sec
			break
		}
	}
	good := []string{"osk", goodPrefix, goodSecret}

	tests := []struct {
		name string
		raw  string
	}{
		{name: "empty", raw: ""},
		{name: "no separators", raw: "oskdeadbeef"},
		{name: "wrong label", raw: "sk_" + good[1] + "_" + good[2]},
		{name: "missing secret half", raw: "osk_" + good[1]},
		{name: "short prefix", raw: "osk_" + good[1][:15] + "_" + good[2]},
		{name: "long prefix", raw: "osk_" + good[1] + "a_" + good[2]},
		{name: "non-hex prefix", raw: "osk_" + strings.Repeat("z", 16) + "_" + good[2]},
		{name: "uppercase hex prefix", raw: "osk_" + strings.Repeat("A", 16) + "_" + good[2]},
		{name: "short secret", raw: "osk_" + good[1] + "_" + good[2][:42]},
		{name: "long secret", raw: "osk_" + good[1] + "_" + good[2] + "a"},
		{name: "non-base64url secret", raw: "osk_" + good[1] + "_" + strings.Repeat("*", 43)},
	}
	for _, tc := range tests {
		t.Run(tc.name+" is rejected", func(t *testing.T) {
			_, _, err := apikey.Parse(tc.raw)
			if !apikey.IsUnauthorizedError(err) {
				t.Fatalf("err = %v, want *UnauthorizedError", err)
			}
		})
	}

	t.Run("a secret containing an underscore round-trips", func(t *testing.T) {
		secret := "ab_de" + strings.Repeat("x", 38)
		prefix, got, err := apikey.Parse("osk_" + good[1] + "_" + secret)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if prefix != good[1] || got != secret {
			t.Fatalf("Parse = (%q, %q), want (%q, %q)", prefix, got, good[1], secret)
		}
	})
}

func TestVerify(t *testing.T) {
	k, plaintext := mustMint(t)
	_, secret, err := apikey.Parse(plaintext)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if !k.Verify(secret) {
		t.Error("Verify rejected the minted secret")
	}
	if k.Verify(secret + "x") {
		t.Error("Verify accepted a modified secret")
	}
	if k.Verify("") {
		t.Error("Verify accepted an empty secret")
	}

	public := *k
	public.SecretHash = nil
	if public.Verify(secret) {
		t.Error("Verify accepted a secret against a key carrying no hash")
	}
}

func TestRevoke(t *testing.T) {
	k, _ := mustMint(t)
	now := time.Now().UTC()

	k.Revoke(now)
	if k.RevokedAt == nil || !k.RevokedAt.Equal(now) {
		t.Fatalf("RevokedAt = %v, want %v", k.RevokedAt, now)
	}
	if k.RevokedAt.Location() != time.UTC {
		t.Errorf("RevokedAt is not UTC: %v", k.RevokedAt)
	}

	first := *k.RevokedAt
	k.Revoke(now.Add(time.Hour))
	if !k.RevokedAt.Equal(first) {
		t.Errorf("a second Revoke moved RevokedAt to %v", k.RevokedAt)
	}
}

func TestActive(t *testing.T) {
	now := time.Now().UTC()
	past := now.Add(-time.Second)
	future := now.Add(time.Hour)

	tests := []struct {
		name  string
		setup func(k *apikey.APIKey)
		want  bool
	}{
		{name: "fresh key is active", setup: func(*apikey.APIKey) {}, want: true},
		{name: "future expiry is active", setup: func(k *apikey.APIKey) { k.ExpiresAt = &future }, want: true},
		{name: "revoked is not active", setup: func(k *apikey.APIKey) { k.Revoke(now) }},
		{name: "expired is not active", setup: func(k *apikey.APIKey) { k.ExpiresAt = &past }},
		{name: "expiry exactly now is not active", setup: func(k *apikey.APIKey) { k.ExpiresAt = &now }},
		{name: "revoked and unexpired is not active", setup: func(k *apikey.APIKey) {
			k.ExpiresAt = &future
			k.Revoke(now)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			k, _ := mustMint(t)
			tc.setup(k)
			if got := k.Active(now); got != tc.want {
				t.Errorf("Active = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAllows(t *testing.T) {
	granted, other := uuid.New(), uuid.New()

	t.Run("empty sheet set grants every sheet in this project", func(t *testing.T) {
		k, _ := mustMint(t)
		k.SheetIDs = nil
		if !k.Allows(authn.ScopeSheetsRead, granted) {
			t.Error("Allows = false, want true for an unrestricted key")
		}
		if !k.Allows(authn.ScopeSheetsRead, other) {
			t.Error("Allows = false, want true for any sheet")
		}
		if !k.Allows(authn.ScopeSheetsRead, uuid.Nil) {
			t.Error("Allows = false, want true even for the nil sheet id")
		}
	})
	t.Run("allow-listed sheet is granted", func(t *testing.T) {
		k, _ := mustMint(t)
		k.SheetIDs = []uuid.UUID{granted}
		if !k.Allows(authn.ScopeSheetsRead, granted) {
			t.Error("Allows = false, want true for the allow-listed sheet")
		}
	})
	t.Run("sheet outside the allow list is denied", func(t *testing.T) {
		k, _ := mustMint(t)
		k.SheetIDs = []uuid.UUID{granted}
		if k.Allows(authn.ScopeSheetsRead, other) {
			t.Error("Allows = true, want false for a sheet outside the grant")
		}
	})
	t.Run("ungranted scope is denied whatever the sheet", func(t *testing.T) {
		k, _ := mustMint(t)
		k.SheetIDs = nil
		if k.Allows(authn.ScopeSheetsWrite, granted) {
			t.Error("Allows = true, want false for a scope the key does not carry")
		}
		k.SheetIDs = []uuid.UUID{granted}
		if k.Allows(authn.ScopeSheetsWrite, granted) {
			t.Error("Allows = true, want false for a scope the key does not carry")
		}
	})
}

type appErrorer interface {
	error
	ToAppError() *apperror.AppError
}

func TestErrors_ToAppErrorAndPredicates(t *testing.T) {
	tests := []struct {
		name    string
		err     appErrorer
		message string
		code    string
		is      func(error) bool
		isNot   func(error) bool
	}{
		{
			name:    "NotFoundError",
			err:     &apikey.NotFoundError{ID: "abc"},
			message: `apikey: "abc": not found`,
			code:    apperror.CodeAPIKeyNotFound,
			is:      apikey.IsNotFoundError,
			isNot:   apikey.IsUnauthorizedError,
		},
		{
			name:    "AlreadyExistsError",
			err:     &apikey.AlreadyExistsError{Field: "key_prefix", Value: "dead"},
			message: "apikey: key_prefix: already exists",
			code:    apperror.CodeAlreadyExists,
			is:      apikey.IsAlreadyExistsError,
			isNot:   apikey.IsNotFoundError,
		},
		{
			name:    "InvalidNameError",
			err:     &apikey.InvalidNameError{Reason: "empty"},
			message: "apikey: name: empty",
			code:    apperror.CodeAPIKeyInvalidName,
			is:      apikey.IsInvalidNameError,
			isNot:   apikey.IsInvalidScopeError,
		},
		{
			name:    "InvalidScopeError",
			err:     &apikey.InvalidScopeError{Scope: "bogus", Reason: "not in catalog"},
			message: "apikey: scopes: not in catalog",
			code:    apperror.CodeAPIKeyInvalidScope,
			is:      apikey.IsInvalidScopeError,
			isNot:   apikey.IsInvalidExpiryError,
		},
		{
			name:    "InvalidExpiryError",
			err:     &apikey.InvalidExpiryError{Reason: "not after now"},
			message: "apikey: expires at: not after now",
			code:    apperror.CodeAPIKeyInvalidScope,
			is:      apikey.IsInvalidExpiryError,
			isNot:   apikey.IsInvalidScopeError,
		},
		{
			name:    "UnknownSheetError",
			err:     &apikey.UnknownSheetError{SheetID: "s1"},
			message: `apikey: sheet ids: "s1" is not in this project`,
			code:    apperror.CodeAPIKeyInvalidScope,
			is:      apikey.IsUnknownSheetError,
			isNot:   apikey.IsNotFoundError,
		},
		{
			name:    "UnauthorizedError",
			err:     &apikey.UnauthorizedError{},
			message: "apikey: unauthorized",
			code:    apperror.CodeAPIKeyUnauthorized,
			is:      apikey.IsUnauthorizedError,
			isNot:   apikey.IsNotFoundError,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.Error(); got != tc.message {
				t.Errorf("Error() = %q, want %q", got, tc.message)
			}
			app := tc.err.ToAppError()
			if app.Code() != tc.code {
				t.Errorf("ToAppError().Code() = %q, want %q", app.Code(), tc.code)
			}
			if len(app.Details()) != 1 {
				t.Errorf("ToAppError().Details() len = %d, want 1", len(app.Details()))
			}
			if !tc.is(tc.err) {
				t.Error("the matching Is predicate returned false")
			}
			if tc.isNot(tc.err) {
				t.Error("a foreign Is predicate returned true")
			}
			if tc.is(nil) {
				t.Error("the Is predicate accepted nil")
			}
		})
	}
}
