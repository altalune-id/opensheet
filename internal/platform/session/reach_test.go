package session

import (
	"testing"

	"github.com/google/uuid"
)

func TestPrincipal_Reach(t *testing.T) {
	orgID, own, sibling, otherOrg := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	postA, postB := uuid.New(), uuid.New()

	member := Grant{OrgID: orgID, All: true}
	person := Principal{UserID: uuid.New(), Source: SourceOIDC, ActiveOrgID: orgID, ActiveProjectID: own, Member: member}
	key := Principal{Source: SourceAPIKey, ActiveOrgID: orgID, ActiveProjectID: own, ProjectIDs: []uuid.UUID{own}, Member: member}
	narrow := key
	narrow.ResourceIDs = []uuid.UUID{postA}
	projectless := Principal{Source: SourceAPIKey, ActiveOrgID: orgID, ActiveProjectID: own, Member: member}
	orgWide := Principal{Source: SourceAPIKey, ActiveOrgID: orgID, AllProjects: true, Member: member}
	selected := Principal{Source: SourceAPIKey, ActiveOrgID: orgID, ProjectIDs: []uuid.UUID{own, sibling}, Member: member}

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

func TestReach_UnboundPrincipalReachesNothing(t *testing.T) {
	orgID, project := uuid.New(), uuid.New()
	for name, p := range map[string]Principal{
		"person": {UserID: uuid.New(), Source: SourceOIDC, ActiveOrgID: orgID},
		"token":  {UserID: uuid.New(), Source: SourceToken, ActiveOrgID: orgID},
		"key":    {Source: SourceAPIKey, ActiveOrgID: orgID, AllProjects: true},
	} {
		if p.ReachesProject(orgID, project) {
			t.Errorf("%s with no member grant reaches a project", name)
		}
	}
}

func TestReach_GrantOfOtherOrgReachesNothing(t *testing.T) {
	orgID, otherOrg, project := uuid.New(), uuid.New(), uuid.New()
	p := Principal{UserID: uuid.New(), Source: SourceOIDC, ActiveOrgID: orgID, Member: Grant{OrgID: otherOrg, All: true}}
	if p.ReachesProject(orgID, project) {
		t.Error("a grant of another org reaches a project of this org")
	}
	p.Member = Grant{OrgID: otherOrg, ProjectIDs: []uuid.UUID{project}}
	if p.ReachesProject(orgID, project) {
		t.Error("a restricted grant of another org naming the project reaches it")
	}
}

func TestReach_KeyNeedsBothHalves(t *testing.T) {
	orgID, granted, other := uuid.New(), uuid.New(), uuid.New()
	tests := []struct {
		name   string
		key    []uuid.UUID
		member Grant
		want   bool
	}{
		{"both halves name the project", []uuid.UUID{granted}, Grant{OrgID: orgID, ProjectIDs: []uuid.UUID{granted}}, true},
		{"the key names it, the creator lost it", []uuid.UUID{granted}, Grant{OrgID: orgID, ProjectIDs: []uuid.UUID{other}}, false},
		{"the creator reaches it, the key does not name it", []uuid.UUID{other}, Grant{OrgID: orgID, All: true}, false},
	}
	for _, tt := range tests {
		p := Principal{Source: SourceAPIKey, ActiveOrgID: orgID, ProjectIDs: tt.key, Member: tt.member}
		if got := p.ReachesProject(orgID, granted); got != tt.want {
			t.Errorf("%s: ReachesProject = %v, want %v", tt.name, got, tt.want)
		}
	}
	all := Principal{Source: SourceAPIKey, ActiveOrgID: orgID, AllProjects: true, Member: Grant{OrgID: orgID, ProjectIDs: []uuid.UUID{granted}}}
	if all.ReachesProject(orgID, other) {
		t.Error("an all-projects key reaches past its creator's grant")
	}
}

func TestGrant_Reaches(t *testing.T) {
	orgID, p1, p2 := uuid.New(), uuid.New(), uuid.New()
	if (Grant{}).Reaches(orgID, p1) {
		t.Error("the zero grant reaches a project")
	}
	if (Grant{OrgID: orgID, All: true}).Reaches(uuid.Nil, p1) {
		t.Error("a grant reaches the nil org")
	}
	g := Grant{OrgID: orgID, ProjectIDs: []uuid.UUID{p1}}
	if !g.Reaches(orgID, p1) || g.Reaches(orgID, p2) {
		t.Error("a restricted grant must reach exactly its projects")
	}
}
