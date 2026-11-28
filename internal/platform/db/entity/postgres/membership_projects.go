package postgres

import "github.com/go-jet/jet/v2/postgres"

// MembershipProjects is the jet binding for the membership_projects table.
type MembershipProjects struct {
	postgres.Table

	OrgID        postgres.ColumnString
	MembershipID postgres.ColumnString
	ProjectID    postgres.ColumnString
	CreatedAt    postgres.ColumnTimestampz

	AllColumns postgres.ColumnList
}

// NewMembershipProjects builds the membership_projects binding.
func NewMembershipProjects(schema, tablePrefix string) *MembershipProjects {
	if schema == "" {
		schema = "public"
	}
	var (
		orgID        = postgres.StringColumn("org_id")
		membershipID = postgres.StringColumn("membership_id")
		projectID    = postgres.StringColumn("project_id")
		createdAt    = postgres.TimestampzColumn("created_at")
		all          = postgres.ColumnList{orgID, membershipID, projectID, createdAt}
	)
	return &MembershipProjects{
		Table:        postgres.NewTable(schema, tablePrefix+"membership_projects", "membership_projects", all...),
		OrgID:        orgID,
		MembershipID: membershipID,
		ProjectID:    projectID,
		CreatedAt:    createdAt,
		AllColumns:   all,
	}
}
