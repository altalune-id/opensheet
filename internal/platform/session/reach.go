package session

import (
	"slices"

	"github.com/google/uuid"
)

// ReachesProject reports whether p may act inside projectID of orgID. SECURITY: the one rule every surface asks; the member grant must cover the project, and a key must also name it or reach all projects.
func (p Principal) ReachesProject(orgID, projectID uuid.UUID) bool {
	if orgID == uuid.Nil || orgID != p.ActiveOrgID || !p.Member.Reaches(orgID, projectID) {
		return false
	}
	if p.Source != SourceAPIKey || p.AllProjects {
		return true
	}
	return slices.Contains(p.ProjectIDs, projectID)
}

// ReachesWholeProject reports whether p may act across every resource of projectID, as a list or a create does.
func (p Principal) ReachesWholeProject(orgID, projectID uuid.UUID) bool {
	return p.ReachesProject(orgID, projectID) && len(p.ResourceIDs) == 0
}

// ReachesResource reports whether p may act on resourceID inside projectID of orgID.
func (p Principal) ReachesResource(orgID, projectID, resourceID uuid.UUID) bool {
	if !p.ReachesProject(orgID, projectID) {
		return false
	}
	return len(p.ResourceIDs) == 0 || slices.Contains(p.ResourceIDs, resourceID)
}

// Reaches reports whether g covers projectID of orgID.
func (g Grant) Reaches(orgID, projectID uuid.UUID) bool {
	if orgID == uuid.Nil || g.OrgID != orgID {
		return false
	}
	return g.All || slices.Contains(g.ProjectIDs, projectID)
}
