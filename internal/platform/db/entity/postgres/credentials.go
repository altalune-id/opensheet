package postgres

import "github.com/go-jet/jet/v2/postgres"

// Credentials is the jet binding for the credentials table.
type Credentials struct {
	postgres.Table

	ID                 postgres.ColumnString
	OrgID              postgres.ColumnString
	ProjectID          postgres.ColumnString
	Name               postgres.ColumnString
	Kind               postgres.ColumnString
	Status             postgres.ColumnString
	AuthorizedByUserID postgres.ColumnString
	GoogleAccountEmail postgres.ColumnString
	Sealed             postgres.ColumnBytea
	CreatedAt          postgres.ColumnTimestampz
	UpdatedAt          postgres.ColumnTimestampz

	AllColumns     postgres.ColumnList
	MutableColumns postgres.ColumnList
	// PublicColumns omits sealed. SECURITY: select through it unless the ciphertext is the point.
	PublicColumns postgres.ColumnList
}

// NewCredentials builds the credentials binding.
func NewCredentials(schema, tablePrefix string) *Credentials {
	if schema == "" {
		schema = "public"
	}
	var (
		id                 = postgres.StringColumn("id")
		orgID              = postgres.StringColumn("org_id")
		projectID          = postgres.StringColumn("project_id")
		name               = postgres.StringColumn("name")
		kind               = postgres.StringColumn("kind")
		status             = postgres.StringColumn("status")
		authorizedByUserID = postgres.StringColumn("authorized_by_user_id")
		googleAccountEmail = postgres.StringColumn("google_account_email")
		sealed             = postgres.ByteaColumn("sealed")
		createdAt          = postgres.TimestampzColumn("created_at")
		updatedAt          = postgres.TimestampzColumn("updated_at")
		all                = postgres.ColumnList{
			id, orgID, projectID, name, kind, status,
			authorizedByUserID, googleAccountEmail, sealed, createdAt, updatedAt,
		}
		mutable = postgres.ColumnList{
			orgID, projectID, name, kind, status,
			authorizedByUserID, googleAccountEmail, sealed, updatedAt,
		}
		public = postgres.ColumnList{
			id, orgID, projectID, name, kind, status,
			authorizedByUserID, googleAccountEmail, createdAt, updatedAt,
		}
	)
	return &Credentials{
		Table:              postgres.NewTable(schema, tablePrefix+"credentials", "credentials", all...),
		ID:                 id,
		OrgID:              orgID,
		ProjectID:          projectID,
		Name:               name,
		Kind:               kind,
		Status:             status,
		AuthorizedByUserID: authorizedByUserID,
		GoogleAccountEmail: googleAccountEmail,
		Sealed:             sealed,
		CreatedAt:          createdAt,
		UpdatedAt:          updatedAt,
		AllColumns:         all,
		MutableColumns:     mutable,
		PublicColumns:      public,
	}
}
