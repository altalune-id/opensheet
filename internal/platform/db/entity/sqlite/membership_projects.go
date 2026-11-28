package sqlite

import "github.com/go-jet/jet/v2/sqlite"

// MembershipProjects is the jet binding for the membership_projects table.
type MembershipProjects struct {
	sqlite.Table

	OrgID        sqlite.ColumnString
	MembershipID sqlite.ColumnString
	ProjectID    sqlite.ColumnString
	CreatedAt    sqlite.ColumnString

	AllColumns sqlite.ColumnList
}

// NewMembershipProjects builds the membership_projects binding.
func NewMembershipProjects(tablePrefix string) *MembershipProjects {
	var (
		orgID        = sqlite.StringColumn("org_id")
		membershipID = sqlite.StringColumn("membership_id")
		projectID    = sqlite.StringColumn("project_id")
		createdAt    = sqlite.StringColumn("created_at")
		all          = sqlite.ColumnList{orgID, membershipID, projectID, createdAt}
	)
	return &MembershipProjects{
		Table:        sqlite.NewTable("", tablePrefix+"membership_projects", "membership_projects", all...),
		OrgID:        orgID,
		MembershipID: membershipID,
		ProjectID:    projectID,
		CreatedAt:    createdAt,
		AllColumns:   all,
	}
}
