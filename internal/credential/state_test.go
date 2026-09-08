package credential

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/platform/session"
)

func testState(t *testing.T, issued time.Time) state {
	t.Helper()
	st, err := newState(uuid.New(), uuid.New(), uuid.New(), "/orgs/a/projects/b/credentials", issued)
	if err != nil {
		t.Fatalf("newState: %v", err)
	}
	return st
}

func TestNewState(t *testing.T) {
	issued := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)

	t.Run("carries the scope, the actor and a nonce", func(t *testing.T) {
		org, project, user := uuid.New(), uuid.New(), uuid.New()
		st, err := newState(org, project, user, "/back", issued)
		if err != nil {
			t.Fatalf("newState: %v", err)
		}
		if st.OrgID != org || st.ProjectID != project || st.UserID != user {
			t.Errorf("scope not carried: %+v", st)
		}
		if st.ReturnTo != "/back" {
			t.Errorf("ReturnTo=%q", st.ReturnTo)
		}
		if !st.IssuedAt.Equal(issued) {
			t.Errorf("IssuedAt=%s want %s", st.IssuedAt, issued)
		}
		if len(st.Nonce) != stateNonceLen {
			t.Errorf("Nonce=%q want %d chars", st.Nonce, stateNonceLen)
		}
	})

	t.Run("every state gets a distinct nonce", func(t *testing.T) {
		a := testState(t, issued)
		b := testState(t, issued)
		if a.Nonce == b.Nonce {
			t.Fatal("two states share a nonce")
		}
	})

	t.Run("an off-site returnTo is refused before it is ever signed", func(t *testing.T) {
		_, err := newState(uuid.New(), uuid.New(), uuid.New(), "https://evil.example.com/", issued)
		if !IsStateInvalidError(err) {
			t.Fatalf("error = %T %v, want *StateInvalidError", err, err)
		}
	})
}

func TestSafeReturnTo(t *testing.T) {
	tests := []struct {
		name  string
		raw   string
		want  string
		valid bool
	}{
		{"empty is allowed", "", "", true},
		{"rooted path", "/orgs/a/credentials", "/orgs/a/credentials", true},
		{"rooted path with query", "/x?y=1", "/x?y=1", true},
		{"protocol relative", "//evil.example.com/x", "", false},
		{"absolute url", "https://evil.example.com/x", "", false},
		{"scheme relative backslash", "/\\evil.example.com", "", false},
		{"unrooted", "orgs/a", "", false},
		{"header injection", "/x\r\nSet-Cookie: a=b", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := safeReturnTo(tt.raw)
			if tt.valid && err != nil {
				t.Fatalf("safeReturnTo(%q) = %v, want no error", tt.raw, err)
			}
			if !tt.valid {
				if !IsStateInvalidError(err) {
					t.Fatalf("error = %T %v, want *StateInvalidError", err, err)
				}
				return
			}
			if got != tt.want {
				t.Errorf("safeReturnTo(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestStateCodec(t *testing.T) {
	secret := []byte("state-secret-for-connect-tests-0123456789")
	issued := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)

	t.Run("round trips every field", func(t *testing.T) {
		want := testState(t, issued)
		raw, err := encodeState(secret, want)
		if err != nil {
			t.Fatalf("encodeState: %v", err)
		}
		got, err := decodeState(secret, raw, issued.Add(time.Minute))
		if err != nil {
			t.Fatalf("decodeState: %v", err)
		}
		if got.OrgID != want.OrgID || got.ProjectID != want.ProjectID || got.UserID != want.UserID {
			t.Errorf("scope lost: got %+v want %+v", got, want)
		}
		if got.Nonce != want.Nonce || got.ReturnTo != want.ReturnTo {
			t.Errorf("payload lost: got %+v want %+v", got, want)
		}
		if !got.IssuedAt.Equal(want.IssuedAt) {
			t.Errorf("IssuedAt=%s want %s", got.IssuedAt, want.IssuedAt)
		}
	})

	t.Run("surrounding whitespace still verifies", func(t *testing.T) {
		raw, err := encodeState(secret, testState(t, issued))
		if err != nil {
			t.Fatalf("encodeState: %v", err)
		}
		if _, err := decodeState(secret, " "+raw+"\n", issued); err != nil {
			t.Fatalf("decodeState: %v", err)
		}
	})

	t.Run("rejects a tampered payload", func(t *testing.T) {
		raw, err := encodeState(secret, testState(t, issued))
		if err != nil {
			t.Fatalf("encodeState: %v", err)
		}
		tampered := "A" + raw[1:]
		if _, err := decodeState(secret, tampered, issued); !IsStateInvalidError(err) {
			t.Fatalf("error = %T %v, want *StateInvalidError", err, err)
		}
	})

	t.Run("rejects a state signed with another secret", func(t *testing.T) {
		raw, err := encodeState([]byte("a-completely-different-state-secret-xxxx"), testState(t, issued))
		if err != nil {
			t.Fatalf("encodeState: %v", err)
		}
		if _, err := decodeState(secret, raw, issued); !IsStateInvalidError(err) {
			t.Fatalf("error = %T %v, want *StateInvalidError", err, err)
		}
	})

	t.Run("rejects a malformed envelope", func(t *testing.T) {
		for _, raw := range []string{"", "no-separator", "|", "value|"} {
			if _, err := decodeState(secret, raw, issued); !IsStateInvalidError(err) {
				t.Fatalf("decodeState(%q) = %T %v, want *StateInvalidError", raw, err, err)
			}
		}
	})

	t.Run("rejects a correctly signed non-base64 payload", func(t *testing.T) {
		raw := session.Sign(secret, "not!base64!")
		if _, err := decodeState(secret, raw, issued); !IsStateInvalidError(err) {
			t.Fatalf("error = %T %v, want *StateInvalidError", err, err)
		}
	})

	t.Run("rejects a correctly signed non-JSON payload", func(t *testing.T) {
		raw := session.Sign(secret, base64.RawURLEncoding.EncodeToString([]byte("not json")))
		if _, err := decodeState(secret, raw, issued); !IsStateInvalidError(err) {
			t.Fatalf("error = %T %v, want *StateInvalidError", err, err)
		}
	})

	t.Run("rejects a correctly signed state naming no tenant", func(t *testing.T) {
		for _, st := range []state{
			{ProjectID: uuid.New(), UserID: uuid.New(), Nonce: "n", IssuedAt: issued},
			{OrgID: uuid.New(), UserID: uuid.New(), Nonce: "n", IssuedAt: issued},
			{OrgID: uuid.New(), ProjectID: uuid.New(), Nonce: "n", IssuedAt: issued},
			{OrgID: uuid.New(), ProjectID: uuid.New(), UserID: uuid.New(), IssuedAt: issued},
		} {
			raw, err := encodeState(secret, st)
			if err != nil {
				t.Fatalf("encodeState: %v", err)
			}
			if _, err := decodeState(secret, raw, issued); !IsStateInvalidError(err) {
				t.Fatalf("decodeState(%+v) = %T %v, want *StateInvalidError", st, err, err)
			}
		}
	})

	t.Run("rejects a state older than StateMaxAge", func(t *testing.T) {
		raw, err := encodeState(secret, testState(t, issued))
		if err != nil {
			t.Fatalf("encodeState: %v", err)
		}
		if _, err := decodeState(secret, raw, issued.Add(StateMaxAge+time.Second)); !IsStateInvalidError(err) {
			t.Fatalf("error = %T %v, want *StateInvalidError", err, err)
		}
	})

	t.Run("accepts a state exactly at StateMaxAge", func(t *testing.T) {
		raw, err := encodeState(secret, testState(t, issued))
		if err != nil {
			t.Fatalf("encodeState: %v", err)
		}
		if _, err := decodeState(secret, raw, issued.Add(StateMaxAge)); err != nil {
			t.Fatalf("decodeState: %v", err)
		}
	})

	t.Run("rejects a state dated far in the future", func(t *testing.T) {
		raw, err := encodeState(secret, testState(t, issued.Add(time.Hour)))
		if err != nil {
			t.Fatalf("encodeState: %v", err)
		}
		if _, err := decodeState(secret, raw, issued); !IsStateInvalidError(err) {
			t.Fatalf("error = %T %v, want *StateInvalidError", err, err)
		}
	})

	// SECURITY: a leaked secret must not turn the state into an open-redirect carrier.
	t.Run("rejects a correctly signed off-site returnTo", func(t *testing.T) {
		st := testState(t, issued)
		st.ReturnTo = "https://evil.example.com/"
		raw, err := encodeState(secret, st)
		if err != nil {
			t.Fatalf("encodeState: %v", err)
		}
		if _, err := decodeState(secret, raw, issued); !IsStateInvalidError(err) {
			t.Fatalf("error = %T %v, want *StateInvalidError", err, err)
		}
	})

	// SECURITY: a signature over one secret must not verify under another, byte-for-byte.
	t.Run("the signature covers the whole payload", func(t *testing.T) {
		st := testState(t, issued)
		raw, err := encodeState(secret, st)
		if err != nil {
			t.Fatalf("encodeState: %v", err)
		}
		value, sig, ok := strings.Cut(raw, "|")
		if !ok {
			t.Fatal("encodeState produced no separator")
		}
		payload, err := base64.RawURLEncoding.DecodeString(value)
		if err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		var decoded state
		if err := json.Unmarshal(payload, &decoded); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if decoded.OrgID != st.OrgID {
			t.Error("payload is not the state")
		}
		swapped := base64.RawURLEncoding.EncodeToString(payload[:len(payload)-1]) + "|" + sig
		if _, err := decodeState(secret, swapped, issued); !IsStateInvalidError(err) {
			t.Fatalf("error = %T %v, want *StateInvalidError", err, err)
		}
	})
}
