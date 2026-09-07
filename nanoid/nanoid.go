// Package nanoid produces URL-safe short public IDs (RFC 4648 5 alphabet + "-_").
package nanoid

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"
const alphabetLen = byte(len(alphabet))

var ErrInvalidLength = errors.New("nanoid: length must be > 0")

// New returns a length-n nanoid using rejection sampling for uniform distribution.
func New(length int) (string, error) {
	if length <= 0 {
		return "", ErrInvalidLength
	}
	mask := byte(63)
	buf := make([]byte, 0, length)
	pool := make([]byte, length*2)
	for len(buf) < length {
		if _, err := rand.Read(pool); err != nil {
			return "", err
		}
		for _, b := range pool {
			if b&mask < alphabetLen {
				buf = append(buf, alphabet[b&mask])
				if len(buf) == length {
					break
				}
			}
		}
	}
	return string(buf), nil
}

// NewInviteToken returns a fresh 32-char raw token and its sha256 hex hash.
func NewInviteToken() (raw, hash string, err error) {
	raw, err = New(32)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(raw))
	return raw, hex.EncodeToString(sum[:]), nil
}
