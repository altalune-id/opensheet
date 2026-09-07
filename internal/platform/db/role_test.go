package db

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateRoleIdent(t *testing.T) {
	cases := []struct {
		name string
		in   string
		ok   bool
	}{
		{"simple", "altempl_owner", true},
		{"mixed case", "AltemplOwner", true},
		{"leading underscore", "_owner", true},
		{"empty", "", false},
		{"nul byte", "own\x00er", false},
		{"newline", "own\ner", false},
		{"too long", strings.Repeat("a", 64), false},
		{"max length", strings.Repeat("a", 63), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRoleIdent(tc.in)
			if tc.ok {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.True(t, IsInvalidRoleError(err), "want InvalidRoleError, got %T", err)
		})
	}
}

func TestQuoteIdent(t *testing.T) {
	cases := []struct{ in, want string }{
		{"altempl_owner", `"altempl_owner"`},
		{"Weird Role", `"Weird Role"`},
		{`has"quote`, `"has""quote"`},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			require.Equal(t, tc.want, quoteIdent(tc.in))
		})
	}
}

func TestIsInvalidRoleError(t *testing.T) {
	err := &InvalidRoleError{Role: "x", Reason: "test"}
	require.True(t, IsInvalidRoleError(err))
	require.True(t, IsInvalidRoleError(fmt.Errorf("open: %w", err)))
	require.False(t, IsInvalidRoleError(fmt.Errorf("unrelated")))
	require.False(t, IsInvalidRoleError(nil))
}
