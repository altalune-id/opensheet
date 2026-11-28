package postgres

import "github.com/go-jet/jet/v2/postgres"

// InviteProjects is the jet binding for the invite_projects table.
type InviteProjects struct {
	postgres.Table

	OrgID     postgres.ColumnString
	InviteID  postgres.ColumnString
	ProjectID postgres.ColumnString
	CreatedAt postgres.ColumnTimestampz

	AllColumns postgres.ColumnList
}

// NewInviteProjects builds the invite_projects binding.
func NewInviteProjects(schema, tablePrefix string) *InviteProjects {
	if schema == "" {
		schema = "public"
	}
	var (
		orgID     = postgres.StringColumn("org_id")
		inviteID  = postgres.StringColumn("invite_id")
		projectID = postgres.StringColumn("project_id")
		createdAt = postgres.TimestampzColumn("created_at")
		all       = postgres.ColumnList{orgID, inviteID, projectID, createdAt}
	)
	return &InviteProjects{
		Table:      postgres.NewTable(schema, tablePrefix+"invite_projects", "invite_projects", all...),
		OrgID:      orgID,
		InviteID:   inviteID,
		ProjectID:  projectID,
		CreatedAt:  createdAt,
		AllColumns: all,
	}
}
