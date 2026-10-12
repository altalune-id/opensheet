// Package sealer seals secrets at rest with AES-256-GCM bound to caller-supplied additional data.
package sealer

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// KeyLen is the required key length in bytes.
const KeyLen = 32

// Sealer seals and opens secrets bound to additional authenticated data.
type Sealer interface {
	Seal(plaintext, aad []byte) ([]byte, error)
	Open(ciphertext, aad []byte) ([]byte, error)
}

type gcmSealer struct{ aead cipher.AEAD }

// New builds a Sealer from a 32-byte key.
func New(key []byte) (Sealer, error) {
	if len(key) != KeyLen {
		return nil, &InvalidKeyError{Len: len(key)}
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("sealer: cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("sealer: gcm: %w", err)
	}
	return &gcmSealer{aead: aead}, nil
}

func (s *gcmSealer) Seal(plaintext, aad []byte) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("sealer: nonce: %w", err)
	}
	return s.aead.Seal(nonce, nonce, plaintext, aad), nil
}

func (s *gcmSealer) Open(ciphertext, aad []byte) ([]byte, error) {
	n := s.aead.NonceSize()
	if len(ciphertext) < n {
		return nil, &OpenFailedError{Cause: errors.New("ciphertext shorter than nonce")}
	}
	out, err := s.aead.Open(nil, ciphertext[:n], ciphertext[n:], aad)
	if err != nil {
		return nil, &OpenFailedError{Cause: err}
	}
	return out, nil
}

type disabled struct{}

// Disabled returns a Sealer that refuses every operation.
func Disabled() Sealer { return disabled{} }

func (disabled) Seal(_, _ []byte) ([]byte, error) { return nil, &UnavailableError{} }
func (disabled) Open(_, _ []byte) ([]byte, error) { return nil, &UnavailableError{} }

// GenerateKey mints a random KeyLen-byte key.
func GenerateKey() ([]byte, error) {
	key := make([]byte, KeyLen)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("sealer: generate key: %w", err)
	}
	return key, nil
}

// ParseKey decodes a hex or standard-base64 key and verifies its length.
func ParseKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, &UnavailableError{}
	}
	if b, err := hex.DecodeString(s); err == nil {
		if len(b) != KeyLen {
			return nil, &InvalidKeyError{Len: len(b)}
		}
		return b, nil
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("sealer: key: not hex or base64: %w", err)
	}
	if len(b) != KeyLen {
		return nil, &InvalidKeyError{Len: len(b)}
	}
	return b, nil
}
