package postgres

import "github.com/go-jet/jet/v2/postgres"

// Invites is the jet binding for the invites table.
type Invites struct {
	postgres.Table

	ID         postgres.ColumnString
	OrgID      postgres.ColumnString
	Email      postgres.ColumnString
	Role       postgres.ColumnString
	TokenHash  postgres.ColumnString
	ExpiresAt  postgres.ColumnTimestampz
	AcceptedAt postgres.ColumnTimestampz
	InvitedBy  postgres.ColumnString
	CreatedAt  postgres.ColumnTimestampz

	AllColumns     postgres.ColumnList
	MutableColumns postgres.ColumnList
}

// NewInvites builds the invites binding.
func NewInvites(schema, tablePrefix string) *Invites {
	if schema == "" {
		schema = "public"
	}
	var (
		id         = postgres.StringColumn("id")
		orgID      = postgres.StringColumn("org_id")
		email      = postgres.StringColumn("email")
		role       = postgres.StringColumn("role")
		tokenHash  = postgres.StringColumn("token_hash")
		expiresAt  = postgres.TimestampzColumn("expires_at")
		acceptedAt = postgres.TimestampzColumn("accepted_at")
		invitedBy  = postgres.StringColumn("invited_by")
		createdAt  = postgres.TimestampzColumn("created_at")
		all        = postgres.ColumnList{id, orgID, email, role, tokenHash, expiresAt, acceptedAt, invitedBy, createdAt}
		mutable    = postgres.ColumnList{orgID, email, role, tokenHash, expiresAt, acceptedAt, invitedBy}
	)
	return &Invites{
		Table:          postgres.NewTable(schema, tablePrefix+"invites", "invites", all...),
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
