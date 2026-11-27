package role_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/role"
)

func TestRole_NewNormalizesToCatalogueOrder(t *testing.T) {
	r, err := role.New(uuid.New(), "  Clerk  ", " desc ", []authz.Permission{authz.SheetsWrite, authz.SheetsRead, authz.SheetsRead}, time.Now())
	require.NoError(t, err)
	assert.Equal(t, "Clerk", r.Name)
	assert.Equal(t, "desc", r.Description)
	assert.Equal(t, []authz.Permission{authz.SheetsRead, authz.SheetsWrite}, r.Permissions)
	assert.Equal(t, int64(1), r.Version)
}

func TestRole_NewRefusesBadInput(t *testing.T) {
	for _, name := range []string{"", "   ", strings.Repeat("x", role.MaxNameLen+1)} {
		_, err := role.New(uuid.New(), name, "", nil, time.Now())
		assert.True(t, role.IsInvalidNameError(err), "name %q: %T: %v", name, err, err)
	}
	_, err := role.New(uuid.New(), strings.Repeat("é", role.MaxNameLen), "", nil, time.Now())
	require.NoError(t, err, "the limit counts characters, not bytes")

	for _, p := range []authz.Permission{"sheets:*", "nope:read", ""} {
		_, err := role.New(uuid.New(), "Clerk", "", []authz.Permission{p}, time.Now())
		assert.True(t, role.IsInvalidPermissionError(err), "permission %q: %T: %v", p, err, err)
	}
}
