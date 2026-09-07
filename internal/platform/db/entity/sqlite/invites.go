package sqlite

import "github.com/go-jet/jet/v2/sqlite"

// Invites is the jet binding for the invites table.
type Invites struct {
	sqlite.Table

	ID         sqlite.ColumnString
	OrgID      sqlite.ColumnString
	Email      sqlite.ColumnString
	Role       sqlite.ColumnString
	TokenHash  sqlite.ColumnString
	ExpiresAt  sqlite.ColumnString
	AcceptedAt sqlite.ColumnString
	InvitedBy  sqlite.ColumnString
	CreatedAt  sqlite.ColumnString

	AllColumns     sqlite.ColumnList
	MutableColumns sqlite.ColumnList
}

// NewInvites builds the invites binding.
func NewInvites(tablePrefix string) *Invites {
	var (
		id         = sqlite.StringColumn("id")
		orgID      = sqlite.StringColumn("org_id")
		email      = sqlite.StringColumn("email")
		role       = sqlite.StringColumn("role")
		tokenHash  = sqlite.StringColumn("token_hash")
		expiresAt  = sqlite.StringColumn("expires_at")
		acceptedAt = sqlite.StringColumn("accepted_at")
		invitedBy  = sqlite.StringColumn("invited_by")
		createdAt  = sqlite.StringColumn("created_at")
		all        = sqlite.ColumnList{id, orgID, email, role, tokenHash, expiresAt, acceptedAt, invitedBy, createdAt}
		mutable    = sqlite.ColumnList{orgID, email, role, tokenHash, expiresAt, acceptedAt, invitedBy}
	)
	return &Invites{
		Table:          sqlite.NewTable("", tablePrefix+"invites", "invites", all...),
		ID:             id,
		OrgID:          orgID,
		Email:          email,
		Role:           role,
		TokenHash:      tokenHash,
		ExpiresAt:      expiresAt,
		AcceptedAt:     acceptedAt,
		InvitedBy:      invitedBy,
		CreatedAt:      createdAt,
		AllColumns:     all,
		MutableColumns: mutable,
	}
}
