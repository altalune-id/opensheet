package sqlite

import "github.com/go-jet/jet/v2/sqlite"

// InviteProjects is the jet binding for the invite_projects table.
type InviteProjects struct {
	sqlite.Table

	OrgID     sqlite.ColumnString
	InviteID  sqlite.ColumnString
	ProjectID sqlite.ColumnString
	CreatedAt sqlite.ColumnString

	AllColumns sqlite.ColumnList
}

// NewInviteProjects builds the invite_projects binding.
func NewInviteProjects(tablePrefix string) *InviteProjects {
	var (
		orgID     = sqlite.StringColumn("org_id")
		inviteID  = sqlite.StringColumn("invite_id")
		projectID = sqlite.StringColumn("project_id")
		createdAt = sqlite.StringColumn("created_at")
		all       = sqlite.ColumnList{orgID, inviteID, projectID, createdAt}
	)
	return &InviteProjects{
		Table:      sqlite.NewTable("", tablePrefix+"invite_projects", "invite_projects", all...),
		OrgID:      orgID,
		InviteID:   inviteID,
		ProjectID:  projectID,
		CreatedAt:  createdAt,
		AllColumns: all,
	}
}
