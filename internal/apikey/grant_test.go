package apikey_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/platform/authn"
)

func mintOrg(t *testing.T, grant apikey.ProjectGrant) *apikey.APIKey {
	t.Helper()
	k, _, err := apikey.Scheme{}.MintOrg(uuid.New(), "org", []string{authn.ScopeSheetsRead}, grant, nil, time.Now().UTC())
	require.NoError(t, err)
	return k
}

func TestMintOrg(t *testing.T) {
	a, b := uuid.New(), uuid.New()

	t.Run("a selected grant keeps each project once", func(t *testing.T) {
		k := mintOrg(t, apikey.ProjectGrant{ProjectIDs: []uuid.UUID{a, b, a}})
		assert.Equal(t, apikey.KindOrg, k.Kind)
		assert.False(t, k.AllProjects)
		assert.Equal(t, []uuid.UUID{a, b}, k.ProjectIDs)
		assert.Equal(t, uuid.Nil, k.ProjectID, "an org key is bound to no single project")
	})
	t.Run("an all-projects grant names none", func(t *testing.T) {
		k := mintOrg(t, apikey.ProjectGrant{All: true})
		assert.True(t, k.AllProjects)
		assert.Empty(t, k.ProjectIDs)
	})
	t.Run("an empty grant is refused", func(t *testing.T) {
		_, _, err := apikey.Scheme{}.MintOrg(uuid.New(), "x", nil, apikey.ProjectGrant{}, nil, time.Now())
		assert.True(t, apikey.IsEmptyGrantError(err), "got %v", err)
	})
	t.Run("all projects plus names is refused", func(t *testing.T) {
		_, _, err := apikey.Scheme{}.MintOrg(uuid.New(), "x", nil, apikey.ProjectGrant{All: true, ProjectIDs: []uuid.UUID{a}}, nil, time.Now())
		assert.True(t, apikey.IsGrantConflictError(err), "got %v", err)
	})
}

func TestMintStoresOnlyASuffixHint(t *testing.T) {
	k, plaintext, err := apikey.Scheme{}.Mint(uuid.New(), uuid.New(), "ci", nil, nil, nil, time.Now())
	require.NoError(t, err)
	assert.Equal(t, apikey.KindProject, k.Kind)
	assert.Len(t, k.SecretHint, 4)
	assert.True(t, strings.HasSuffix(plaintext, k.SecretHint))
}

// SECURITY: a grant only ever widens; these pin that no verb narrows one and that a project key has no grant to change.
func TestGrantOnlyWidens(t *testing.T) {
	a, b := uuid.New(), uuid.New()

	t.Run("adding projects widens a selected grant", func(t *testing.T) {
		k := mintOrg(t, apikey.ProjectGrant{ProjectIDs: []uuid.UUID{a}})
		require.NoError(t, k.GrantProjects([]uuid.UUID{b, a}))
		assert.Equal(t, []uuid.UUID{a, b}, k.ProjectIDs)
	})
	t.Run("promoting to all projects clears the named ones", func(t *testing.T) {
		k := mintOrg(t, apikey.ProjectGrant{ProjectIDs: []uuid.UUID{a}})
		require.NoError(t, k.GrantAllProjects())
		assert.True(t, k.AllProjects)
		assert.Empty(t, k.ProjectIDs)
	})
	t.Run("an all-projects key cannot be given named projects", func(t *testing.T) {
		k := mintOrg(t, apikey.ProjectGrant{All: true})
		assert.True(t, apikey.IsAlreadyAllProjectsError(k.GrantProjects([]uuid.UUID{a})))
		assert.True(t, apikey.IsAlreadyAllProjectsError(k.GrantAllProjects()))
		assert.True(t, k.AllProjects, "an all-projects key must stay all-projects")
	})
	t.Run("adding nothing is refused", func(t *testing.T) {
		k := mintOrg(t, apikey.ProjectGrant{ProjectIDs: []uuid.UUID{a}})
		assert.True(t, apikey.IsEmptyGrantError(k.GrantProjects(nil)))
	})
	t.Run("a project key has no grant to change", func(t *testing.T) {
		k, _, err := apikey.Scheme{}.Mint(uuid.New(), a, "p", nil, nil, nil, time.Now())
		require.NoError(t, err)
		assert.True(t, apikey.IsBoundToProjectError(k.GrantProjects([]uuid.UUID{b})))
		assert.True(t, apikey.IsBoundToProjectError(k.GrantAllProjects()))
		assert.Equal(t, []uuid.UUID{a}, k.ReachableProjects())
	})
	t.Run("a revoked key cannot be promoted", func(t *testing.T) {
		k := mintOrg(t, apikey.ProjectGrant{ProjectIDs: []uuid.UUID{a}})
		now := time.Now()
		k.RevokedAt = &now
		assert.True(t, apikey.IsRevokedError(k.GrantAllProjects()))
	})
}
