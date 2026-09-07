package sqlite

import "github.com/go-jet/jet/v2/sqlite"

// Users is the jet binding for the users table.
type Users struct {
	sqlite.Table

	ID              sqlite.ColumnString
	IDPIssuer       sqlite.ColumnString
	IDPSubject      sqlite.ColumnString
	Email           sqlite.ColumnString
	Name            sqlite.ColumnString
	AvatarURL       sqlite.ColumnString
	IsAdmin         sqlite.ColumnInteger
	TermsAcceptedAt sqlite.ColumnString
	CreatedAt       sqlite.ColumnString
	UpdatedAt       sqlite.ColumnString

	AllColumns     sqlite.ColumnList
	MutableColumns sqlite.ColumnList
}

// NewUsers builds the users binding. tablePrefix matches DB.TablePrefix (e.g. "altempl_").
func NewUsers(tablePrefix string) *Users {
	var (
		id              = sqlite.StringColumn("id")
		idpIssuer       = sqlite.StringColumn("idp_issuer")
		idpSubject      = sqlite.StringColumn("idp_subject")
		email           = sqlite.StringColumn("email")
		name            = sqlite.StringColumn("name")
		avatarURL       = sqlite.StringColumn("avatar_url")
		isAdmin         = sqlite.IntegerColumn("is_admin")
		termsAcceptedAt = sqlite.StringColumn("terms_accepted_at")
		createdAt       = sqlite.StringColumn("created_at")
		updatedAt       = sqlite.StringColumn("updated_at")
		all             = sqlite.ColumnList{id, idpIssuer, idpSubject, email, name, avatarURL, isAdmin, termsAcceptedAt, createdAt, updatedAt}
		mutable         = sqlite.ColumnList{idpIssuer, idpSubject, email, name, avatarURL, isAdmin, termsAcceptedAt, updatedAt}
	)
	return &Users{
		Table:           sqlite.NewTable("", tablePrefix+"users", "users", all...),
		ID:              id,
		IDPIssuer:       idpIssuer,
		IDPSubject:      idpSubject,
		Email:           email,
		Name:            name,
		AvatarURL:       avatarURL,
		IsAdmin:         isAdmin,
		TermsAcceptedAt: termsAcceptedAt,
		CreatedAt:       createdAt,
		UpdatedAt:       updatedAt,
		AllColumns:      all,
		MutableColumns:  mutable,
	}
}
