package db

import (
	"fmt"
	"strings"
)

const maxRoleIdentBytes = 63

func validateRoleIdent(role string) error {
	if role == "" {
		return &InvalidRoleError{Role: role, Reason: "empty"}
	}
	if len(role) > maxRoleIdentBytes {
		return &InvalidRoleError{Role: role, Reason: fmt.Sprintf("longer than %d bytes", maxRoleIdentBytes)}
	}
	if strings.ContainsFunc(role, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return &InvalidRoleError{Role: role, Reason: "contains a control character"}
	}
	return nil
}

// SECURITY: this is the escaping boundary for the role interpolated into SET ROLE.
func quoteIdent(role string) string {
	return `"` + strings.ReplaceAll(role, `"`, `""`) + `"`
}
