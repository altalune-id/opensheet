package password_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/template/internal/password"
)

func TestHash_RoundTrip(t *testing.T) {
	t.Parallel()
	h, err := password.Hash("hunter2")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(h, "$argon2id$"))
	assert.NoError(t, password.Verify(h, "hunter2"))
}

func TestHash_DifferentSaltsProduceDifferentHashes(t *testing.T) {
	t.Parallel()
	a, err := password.Hash("same")
	require.NoError(t, err)
	b, err := password.Hash("same")
	require.NoError(t, err)
	assert.NotEqual(t, a, b, "salt should make each hash unique")
}

func TestVerify_WrongPassword(t *testing.T) {
	t.Parallel()
	h, err := password.Hash("correct")
	require.NoError(t, err)
	assert.ErrorIs(t, password.Verify(h, "wrong"), password.ErrMismatch)
}

func TestVerify_MalformedHash(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		hash string
	}{
		{"empty", ""},
		{"wrong scheme", "$bcrypt$foo"},
		{"missing params", "$argon2id$v=19"},
		{"bad version", "$argon2id$v=1$m=1024,t=1,p=1$YWJj$ZGVm"},
		{"bad params format", "$argon2id$v=19$mmm=1$YWJj$ZGVm"},
		{"non-base64 salt", "$argon2id$v=19$m=1024,t=1,p=1$!!!$ZGVm"},
		{"non-base64 key", "$argon2id$v=19$m=1024,t=1,p=1$YWJj$!!!"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.ErrorIs(t, password.Verify(tc.hash, "anything"), password.ErrInvalidHash)
		})
	}
}

func TestVerify_LongPassword(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", 500)
	h, err := password.Hash(long)
	require.NoError(t, err)
	assert.NoError(t, password.Verify(h, long))
}
