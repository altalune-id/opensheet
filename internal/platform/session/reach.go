package session

import (
	"slices"

	"github.com/google/uuid"
)

// ReachesProject reports whether p may act inside projectID of orgID. SECURITY: the one rule every surface asks; a person reaches every project of their org, a key only the projects it was granted or all of them.
func (p Principal) ReachesProject(orgID, projectID uuid.UUID) bool {
	if orgID == uuid.Nil || orgID != p.ActiveOrgID {
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
