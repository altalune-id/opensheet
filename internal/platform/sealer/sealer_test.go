package sealer

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"

	"altalune.id/opensheet/internal/apperror"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	k, err := hex.DecodeString(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return k
}

func TestSeal_RoundTripsWithMatchingAAD(t *testing.T) {
	s, err := New(testKey(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	plain := []byte(`{"type":"service_account"}`)
	aad := []byte("org|proj|cred")

	ct, err := s.Seal(plain, aad)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Contains(ct, plain) {
		t.Fatal("ciphertext contains plaintext")
	}
	got, err := s.Open(ct, aad)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("Open = %q, want %q", got, plain)
	}
}

func TestOpen_RejectsDifferentAAD(t *testing.T) {
	s, _ := New(testKey(t))
	ct, _ := s.Seal([]byte("secret"), []byte("org|proj|cred-A"))
	if _, err := s.Open(ct, []byte("org|proj|cred-B")); err == nil {
		t.Fatal("Open with foreign AAD succeeded; ciphertext is replayable across rows")
	}
}

func TestSeal_ProducesDistinctCiphertextsForSameInput(t *testing.T) {
	s, _ := New(testKey(t))
	a, _ := s.Seal([]byte("secret"), nil)
	b, _ := s.Seal([]byte("secret"), nil)
	if bytes.Equal(a, b) {
		t.Fatal("identical ciphertexts; nonce is not random")
	}
}

func TestNew_RejectsWrongKeyLength(t *testing.T) {
	for _, n := range []int{0, 16, 31, 33, 64} {
		if _, err := New(make([]byte, n)); !IsInvalidKeyError(err) {
			t.Errorf("New(%d bytes) error = %v, want InvalidKeyError", n, err)
		}
	}
}

func TestDisabled_FailsBothDirections(t *testing.T) {
	s := Disabled()
	if _, err := s.Seal([]byte("x"), nil); !IsUnavailableError(err) {
		t.Errorf("Seal error = %v, want UnavailableError", err)
	}
	if _, err := s.Open([]byte("x"), nil); !IsUnavailableError(err) {
		t.Errorf("Open error = %v, want UnavailableError", err)
	}
}

func TestParseKey_AcceptsHexAndBase64(t *testing.T) {
	hexKey := strings.Repeat("ab", 32)
	k, err := ParseKey(hexKey)
	if err != nil || len(k) != 32 {
		t.Fatalf("ParseKey(hex) = %d bytes, %v", len(k), err)
	}
	if _, err := ParseKey("not-a-key"); err == nil {
		t.Fatal("ParseKey accepted garbage")
	}

	b64 := base64.StdEncoding.EncodeToString(testKey(t))
	k, err = ParseKey("  " + b64 + "\n")
	if err != nil {
		t.Fatalf("ParseKey(base64) error = %v", err)
	}
	if !bytes.Equal(k, testKey(t)) {
		t.Fatalf("ParseKey(base64) = %x, want %x", k, testKey(t))
	}
}

func TestParseKey_RejectsWrongLength(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"hex 16 bytes", strings.Repeat("ab", 16)},
		{"hex 64 bytes", strings.Repeat("ab", 64)},
		{"base64 16 bytes", base64.StdEncoding.EncodeToString(make([]byte, 16))},
		{"base64 64 bytes", base64.StdEncoding.EncodeToString(make([]byte, 64))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseKey(tt.in); !IsInvalidKeyError(err) {
				t.Errorf("ParseKey(%q) error = %v, want InvalidKeyError", tt.in, err)
			}
		})
	}
}

func TestParseKey_TreatsEmptyAsUnavailable(t *testing.T) {
	for _, in := range []string{"", "   ", "\t\n"} {
		if _, err := ParseKey(in); !IsUnavailableError(err) {
			t.Errorf("ParseKey(%q) error = %v, want UnavailableError", in, err)
		}
	}
}

func TestOpen_RejectsCiphertextShorterThanNonce(t *testing.T) {
	s, _ := New(testKey(t))
	if _, err := s.Open([]byte("short"), nil); err == nil {
		t.Fatal("Open accepted a ciphertext shorter than the nonce")
	} else if !IsOpenFailedError(err) {
		t.Fatalf("error = %v, want OpenFailedError", err)
	}
}

func TestOpen_RejectsTamperedCiphertext(t *testing.T) {
	s, _ := New(testKey(t))
	ct, err := s.Seal([]byte("secret"), []byte("aad"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	ct[len(ct)-1] ^= 0xff
	if _, err := s.Open(ct, []byte("aad")); err == nil {
		t.Fatal("Open accepted a tampered ciphertext")
	} else if !IsOpenFailedError(err) {
		t.Fatalf("error = %v, want OpenFailedError", err)
	}
}

func TestUnavailableError_Error(t *testing.T) {
	got := (&UnavailableError{}).Error()
	if got != "sealer: encryption unavailable: no key configured" {
		t.Errorf("Error() = %q", got)
	}
}

func TestUnavailableError_ToAppError(t *testing.T) {
	ae := (&UnavailableError{}).ToAppError()
	if ae == nil {
		t.Fatal("ToAppError returned nil")
	}
	if ae.Code() != apperror.CodeEncryptionUnavailable {
		t.Errorf("Code() = %q, want %q", ae.Code(), apperror.CodeEncryptionUnavailable)
	}
	if ae.GRPCCode() != codes.FailedPrecondition {
		t.Errorf("GRPCCode() = %v, want FailedPrecondition", ae.GRPCCode())
	}
	if len(ae.Details()) != 1 {
		t.Errorf("Details() len = %d, want 1", len(ae.Details()))
	}
}

func TestInvalidKeyError_Error(t *testing.T) {
	got := (&InvalidKeyError{Len: 16}).Error()
	if got != "sealer: key: got 16 bytes, want 32" {
		t.Errorf("Error() = %q", got)
	}
}

func TestInvalidKeyError_ToAppError(t *testing.T) {
	ae := (&InvalidKeyError{Len: 16}).ToAppError()
	if ae == nil {
		t.Fatal("ToAppError returned nil")
	}
	if ae.Code() != apperror.CodeEncryptionUnavailable {
		t.Errorf("Code() = %q, want %q", ae.Code(), apperror.CodeEncryptionUnavailable)
	}
	if ae.GRPCCode() != codes.FailedPrecondition {
		t.Errorf("GRPCCode() = %v, want FailedPrecondition", ae.GRPCCode())
	}
}

func TestPredicates_UnwrapAndRejectForeignErrors(t *testing.T) {
	if !IsUnavailableError(fmt.Errorf("layer: %w", &UnavailableError{})) {
		t.Error("IsUnavailableError must walk %w chains")
	}
	if !IsInvalidKeyError(fmt.Errorf("layer: %w", &InvalidKeyError{Len: 1})) {
		t.Error("IsInvalidKeyError must walk %w chains")
	}
	if IsUnavailableError(nil) || IsUnavailableError(&InvalidKeyError{}) {
		t.Error("IsUnavailableError matched a foreign error")
	}
	if IsInvalidKeyError(nil) || IsInvalidKeyError(&UnavailableError{}) {
		t.Error("IsInvalidKeyError matched a foreign error")
	}
}

func TestAsAppError_ThroughSealerErrors(t *testing.T) {
	for _, err := range []error{&UnavailableError{}, &InvalidKeyError{Len: 3}} {
		ae, ok := apperror.AsAppError(fmt.Errorf("layer: %w", err))
		if !ok {
			t.Fatalf("AsAppError(%T) not discovered", err)
		}
		if ae.Code() != apperror.CodeEncryptionUnavailable {
			t.Errorf("Code() = %q", ae.Code())
		}
	}
}

// A wrong key and a tampered row must be distinguishable from an absent key, so
// credential can tell "re-upload this" from "configure encryption".
func TestOpen_WrongKeyIsOpenFailedNotUnavailable(t *testing.T) {
	a, _ := New(testKey(t))
	other, err := hex.DecodeString(strings.Repeat("cd", 32))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	b, _ := New(other)

	ct, _ := a.Seal([]byte("secret"), []byte("aad"))
	_, err = b.Open(ct, []byte("aad"))
	if !IsOpenFailedError(err) {
		t.Fatalf("error = %v, want OpenFailedError", err)
	}
	if IsUnavailableError(err) {
		t.Fatal("a wrong key must not look like an unconfigured key")
	}
}

func TestOpenFailedError_ToAppError(t *testing.T) {
	e := &OpenFailedError{}
	app := e.ToAppError()
	if app.Code() != apperror.CodeEncryptionOpenFailed {
		t.Fatalf("Code() = %q, want %q", app.Code(), apperror.CodeEncryptionOpenFailed)
	}
	if (&OpenFailedError{}).Error() == "" {
		t.Fatal("Error() is empty")
	}
	wrapped := &OpenFailedError{Cause: errors.New("boom")}
	if !strings.Contains(wrapped.Error(), "boom") {
		t.Fatalf("Error() = %q, want it to mention the cause", wrapped.Error())
	}
	if wrapped.Unwrap() == nil {
		t.Fatal("Unwrap() = nil")
	}
}
