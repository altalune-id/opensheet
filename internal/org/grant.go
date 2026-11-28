package org

import (
	"slices"

	"github.com/google/uuid"
)

// ProjectGrant names the projects a membership or invite reaches: every project, or the listed ones.
type ProjectGrant struct {
	All        bool
	ProjectIDs []uuid.UUID
}

// Validate reports a grant that names no project as ProjectGrantEmptyError, and one that is both all projects and a list as ProjectGrantConflictError.
func (g ProjectGrant) Validate() error {
	if g.All {
		if len(g.ProjectIDs) > 0 {
			return &ProjectGrantConflictError{}
		}
		return nil
	}
	if len(g.ProjectIDs) == 0 {
		return &ProjectGrantEmptyError{}
	}
	for _, id := range g.ProjectIDs {
		if id == uuid.Nil {
			return &ProjectGrantEmptyError{}
		}
	}
	return nil
}

// Within refuses g with ProjectGrantEscalationError unless outer reaches every project g grants.
func (g ProjectGrant) Within(outer ProjectGrant) error {
	if outer.All {
		return nil
	}
	if g.All {
		return &ProjectGrantEscalationError{}
	}
	for _, id := range g.ProjectIDs {
		if !slices.Contains(outer.ProjectIDs, id) {
			return &ProjectGrantEscalationError{ProjectID: id}
		}
	}
	return nil
}

// InOrg refuses g with ProjectGrantEscalationError unless every project it names is in orgProjects; no one reaches a project outside the org or one that does not exist.
func (g ProjectGrant) InOrg(orgProjects []uuid.UUID) error {
	for _, id := range g.ProjectIDs {
		if !slices.Contains(orgProjects, id) {
			return &ProjectGrantEscalationError{ProjectID: id}
		}
	}
	return nil
}
