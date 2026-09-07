// Package postgres holds jet table bindings for the Postgres dialect.
package postgres

import "github.com/go-jet/jet/v2/postgres"

// Users is the jet binding for the users table.
type Users struct {
	postgres.Table

	ID              postgres.ColumnString
	IDPIssuer       postgres.ColumnString
	IDPSubject      postgres.ColumnString
	Email           postgres.ColumnString
	Name            postgres.ColumnString
	AvatarURL       postgres.ColumnString
	PasswordHash    postgres.ColumnString
	IsAdmin         postgres.ColumnBool
	Locale          postgres.ColumnString
	TermsAcceptedAt postgres.ColumnTimestampz
	CreatedAt       postgres.ColumnTimestampz
	UpdatedAt       postgres.ColumnTimestampz

	AllColumns     postgres.ColumnList
	MutableColumns postgres.ColumnList
}

// NewUsers builds the users binding.
func NewUsers(schema, tablePrefix string) *Users {
	if schema == "" {
		schema = "public"
	}
	var (
		id              = postgres.StringColumn("id")
		idpIssuer       = postgres.StringColumn("idp_issuer")
		idpSubject      = postgres.StringColumn("idp_subject")
		email           = postgres.StringColumn("email")
		name            = postgres.StringColumn("name")
		avatarURL       = postgres.StringColumn("avatar_url")
		passwordHash    = postgres.StringColumn("password_hash")
		isAdmin         = postgres.BoolColumn("is_admin")
		locale          = postgres.StringColumn("locale")
		termsAcceptedAt = postgres.TimestampzColumn("terms_accepted_at")
		createdAt       = postgres.TimestampzColumn("created_at")
		updatedAt       = postgres.TimestampzColumn("updated_at")
		all             = postgres.ColumnList{id, idpIssuer, idpSubject, email, name, avatarURL, passwordHash, isAdmin, locale, termsAcceptedAt, createdAt, updatedAt}
		mutable         = postgres.ColumnList{idpIssuer, idpSubject, email, name, avatarURL, passwordHash, isAdmin, locale, termsAcceptedAt, updatedAt}
	)
	return &Users{
		Table:           postgres.NewTable(schema, tablePrefix+"users", "users", all...),
		ID:              id,
		IDPIssuer:       idpIssuer,
		IDPSubject:      idpSubject,
		Email:           email,
		Name:            name,
		AvatarURL:       avatarURL,
		PasswordHash:    passwordHash,
		IsAdmin:         isAdmin,
		Locale:          locale,
		TermsAcceptedAt: termsAcceptedAt,
		CreatedAt:       createdAt,
		UpdatedAt:       updatedAt,
		AllColumns:      all,
		MutableColumns:  mutable,
	}
}
