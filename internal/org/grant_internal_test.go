package org

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestProjectGrant_Validate(t *testing.T) {
	p := uuid.New()
	assert.NoError(t, ProjectGrant{All: true}.Validate())
	assert.NoError(t, ProjectGrant{ProjectIDs: []uuid.UUID{p}}.Validate())

	err := ProjectGrant{}.Validate()
	assert.True(t, IsProjectGrantEmptyError(err), "%T %v", err, err)
	err = ProjectGrant{ProjectIDs: []uuid.UUID{uuid.Nil}}.Validate()
	assert.True(t, IsProjectGrantEmptyError(err), "a nil id names no project: %T %v", err, err)
	err = ProjectGrant{All: true, ProjectIDs: []uuid.UUID{p}}.Validate()
	assert.True(t, IsProjectGrantConflictError(err), "%T %v", err, err)
}
