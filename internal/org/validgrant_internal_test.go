package org

import (
	"testing"

	"github.com/google/uuid"
)

func TestValidGrant_RoleIDExactlyForCustom(t *testing.T) {
	id := uuid.New()
	for _, c := range []struct {
		role   Role
		roleID uuid.UUID
		want   bool
	}{
		{RoleCustom, id, true},
		{RoleCustom, uuid.Nil, false},
		{RoleOwner, uuid.Nil, true},
		{RoleMember, id, false},
		{"viewer", uuid.Nil, false},
	} {
		if got := validGrant(c.role, c.roleID); got != c.want {
			t.Errorf("validGrant(%q, %s) = %v, want %v", c.role, c.roleID, got, c.want)
		}
	}
}
