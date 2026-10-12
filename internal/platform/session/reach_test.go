package session

import (
	"testing"

	"github.com/google/uuid"
)

func TestPrincipal_Reach(t *testing.T) {
	orgID, own, sibling, otherOrg := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	postA, postB := uuid.New(), uuid.New()

	person := Principal{UserID: uuid.New(), Source: SourceOIDC, ActiveOrgID: orgID, ActiveProjectID: own}
	key := Principal{Source: SourceAPIKey, ActiveOrgID: orgID, ActiveProjectID: own, ProjectIDs: []uuid.UUID{own}}
	narrow := key
	narrow.ResourceIDs = []uuid.UUID{postA}
	projectless := Principal{Source: SourceAPIKey, ActiveOrgID: orgID, ActiveProjectID: own}
	orgWide := Principal{Source: SourceAPIKey, ActiveOrgID: orgID, AllProjects: true}
	selected := Principal{Source: SourceAPIKey, ActiveOrgID: orgID, ProjectIDs: []uuid.UUID{own, sibling}}

	tests := []struct {
		name         string
		p            Principal
		org, project uuid.UUID
		resource     uuid.UUID
		wantProject  bool
		wantWhole    bool
		wantResource bool
	}{
		{"a person reaches their own project", person, orgID, own, postA, true, true, true},
		{"a person reaches a sibling project in their org", person, orgID, sibling, postB, true, true, true},
		{"a person never reaches another org", person, otherOrg, sibling, postB, false, false, false},
		{"a key reaches its own project", key, orgID, own, postA, true, true, true},
		{"a key never reaches a sibling project", key, orgID, sibling, postB, false, false, false},
		{"a key never reaches another org", key, otherOrg, own, postA, false, false, false},
		{"a resource-bound key reaches its resource", narrow, orgID, own, postA, true, false, true},
		{"a resource-bound key never reaches another resource", narrow, orgID, own, postB, true, false, false},
		{"a key granted no project reaches nothing", projectless, orgID, own, postA, false, false, false},
		{"an all-projects key reaches every project of its org", orgWide, orgID, sibling, postB, true, true, true},
		{"an all-projects key never reaches another org", orgWide, otherOrg, sibling, postB, false, false, false},
		{"a selected-projects key reaches each granted project", selected, orgID, sibling, postB, true, true, true},
		{"a selected-projects key never reaches an ungranted project", selected, orgID, uuid.New(), postB, false, false, false},
		{"the zero principal reaches nothing", Principal{}, uuid.Nil, uuid.Nil, uuid.Nil, false, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.p.ReachesProject(tt.org, tt.project); got != tt.wantProject {
				t.Errorf("ReachesProject = %v, want %v", got, tt.wantProject)
			}
			if got := tt.p.ReachesWholeProject(tt.org, tt.project); got != tt.wantWhole {
				t.Errorf("ReachesWholeProject = %v, want %v", got, tt.wantWhole)
			}
			if got := tt.p.ReachesResource(tt.org, tt.project, tt.resource); got != tt.wantResource {
				t.Errorf("ReachesResource = %v, want %v", got, tt.wantResource)
			}
		})
	}
}
