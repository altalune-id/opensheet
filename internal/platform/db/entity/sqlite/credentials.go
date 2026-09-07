package sqlite

import "github.com/go-jet/jet/v2/sqlite"

// Credentials is the jet binding for the credentials table.
type Credentials struct {
	sqlite.Table

	ID                 sqlite.ColumnString
	OrgID              sqlite.ColumnString
	ProjectID          sqlite.ColumnString
	Name               sqlite.ColumnString
	Kind               sqlite.ColumnString
	Status             sqlite.ColumnString
	AuthorizedByUserID sqlite.ColumnString
	GoogleAccountEmail sqlite.ColumnString
	Sealed             sqlite.ColumnBlob
	CreatedAt          sqlite.ColumnString
	UpdatedAt          sqlite.ColumnString

	AllColumns     sqlite.ColumnList
	MutableColumns sqlite.ColumnList
	// PublicColumns omits sealed. SECURITY: select through it unless the ciphertext is the point.
	PublicColumns sqlite.ColumnList
}

// NewCredentials builds the credentials binding.
func NewCredentials(tablePrefix string) *Credentials {
	var (
		id                 = sqlite.StringColumn("id")
		orgID              = sqlite.StringColumn("org_id")
		projectID          = sqlite.StringColumn("project_id")
		name               = sqlite.StringColumn("name")
		kind               = sqlite.StringColumn("kind")
		status             = sqlite.StringColumn("status")
		authorizedByUserID = sqlite.StringColumn("authorized_by_user_id")
		googleAccountEmail = sqlite.StringColumn("google_account_email")
		sealed             = sqlite.BlobColumn("sealed")
		createdAt          = sqlite.StringColumn("created_at")
		updatedAt          = sqlite.StringColumn("updated_at")
		all                = sqlite.ColumnList{
			id, orgID, projectID, name, kind, status,
			authorizedByUserID, googleAccountEmail, sealed, createdAt, updatedAt,
		}
		mutable = sqlite.ColumnList{
			orgID, projectID, name, kind, status,
			authorizedByUserID, googleAccountEmail, sealed, updatedAt,
		}
		public = sqlite.ColumnList{
			id, orgID, projectID, name, kind, status,
			authorizedByUserID, googleAccountEmail, createdAt, updatedAt,
		}
	)
	return &Credentials{
		Table:              sqlite.NewTable("", tablePrefix+"credentials", "credentials", all...),
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
