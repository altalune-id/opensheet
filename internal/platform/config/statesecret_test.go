package config

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

func TestParseStateSecret_Hex(t *testing.T) {
	t.Parallel()
	want := bytes.Repeat([]byte{0xAB}, StateSecretMinLen)
	got, err := ParseStateSecret(hex.EncodeToString(want))
	if err != nil {
		t.Fatalf("ParseStateSecret() error = %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("ParseStateSecret() = %x, want %x", got, want)
	}
}

// TestParseStateSecret_HexBeatsBase64 pins the precedence sealer.ParseKey already uses, so one 64-hex value works for both secrets.
func TestParseStateSecret_HexBeatsBase64(t *testing.T) {
	t.Parallel()
	got, err := ParseStateSecret("0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0")
	if err != nil {
		t.Fatalf("ParseStateSecret() error = %v", err)
	}
	if len(got) != StateSecretMinLen {
		t.Fatalf("ParseStateSecret() decoded %d bytes, want %d from hex", len(got), StateSecretMinLen)
	}
}

func TestParseStateSecret_Base64URLAndStd(t *testing.T) {
	t.Parallel()
	want := make([]byte, StateSecretMinLen)
	for i := range want {
		want[i] = byte(i)
	}
	tests := []struct {
		name string
		raw  string
	}{
		{"base64url", base64.RawURLEncoding.EncodeToString(want)},
		{"base64std", base64.StdEncoding.EncodeToString(want)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseStateSecret(tt.raw)
			if err != nil {
				t.Fatalf("ParseStateSecret(%s) error = %v", tt.name, err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("ParseStateSecret(%s) = %x, want %x", tt.name, got, want)
			}
		})
	}
}

func TestParseStateSecret_LongerThanMinimumIsAccepted(t *testing.T) {
	t.Parallel()
	want := bytes.Repeat([]byte{0x11}, 48)
	got, err := ParseStateSecret(hex.EncodeToString(want))
	if err != nil {
		t.Fatalf("ParseStateSecret() error = %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("ParseStateSecret() = %x, want %x", got, want)
	}
}

func TestParseStateSecret_Rejections(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		raw     string
		is      func(error) bool
		wantSub []string
	}{
		{
			name:    "empty",
			raw:     "   ",
			is:      IsStateSecretMissingError,
			wantSub: []string{"http.stateSecret", "OPENSHEET_HTTP_STATE_SECRET"},
		},
		{
			name:    "neither hex nor base64",
			raw:     "not-hex-or-base64!!$",
			is:      IsStateSecretEncodingError,
			wantSub: []string{"hex", "base64", "openssl rand -hex 32"},
		},
		{
			name:    "odd hex length falls through to base64",
			raw:     "abc",
			is:      IsStateSecretTooShortError,
			wantSub: []string{"2 bytes", "at least 32"},
		},
		{
			name:    "hex too short",
			raw:     hex.EncodeToString(bytes.Repeat([]byte{0x01}, 16)),
			is:      IsStateSecretTooShortError,
			wantSub: []string{"16 bytes", "at least 32", "openssl rand -hex 32"},
		},
		{
			name:    "base64 too short",
			raw:     base64.RawURLEncoding.EncodeToString([]byte("short")),
			is:      IsStateSecretTooShortError,
			wantSub: []string{"5 bytes", "at least 32"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseStateSecret(tt.raw)
			if err == nil {
				t.Fatalf("ParseStateSecret(%q) = %x, want an error", tt.raw, got)
			}
			if got != nil {
				t.Fatalf("ParseStateSecret(%q) returned %x alongside an error", tt.raw, got)
			}
			if !tt.is(err) {
				t.Fatalf("ParseStateSecret(%q) error = %T (%v), want the matching typed error", tt.raw, err, err)
			}
			for _, sub := range tt.wantSub {
				if !strings.Contains(err.Error(), sub) {
					t.Errorf("error %q does not mention %q", err.Error(), sub)
				}
			}
		})
	}
}
