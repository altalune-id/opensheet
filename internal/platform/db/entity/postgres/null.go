package postgres

import "github.com/go-jet/jet/v2/postgres"

// NOTE: wrapping jet's shared NULL singleton races; CAST builds a fresh expression instead.
// NOTE: each helper must match the column's declared type — Postgres has no assignment cast.

// NullText returns a fresh SQL NULL typed as text.
func NullText() postgres.StringExpression {
	return postgres.CAST(postgres.NULL).AS_TEXT()
}

// NullDate returns a fresh SQL NULL typed as date.
func NullDate() postgres.DateExpression {
	return postgres.CAST(postgres.NULL).AS_DATE()
}

// NullTimestampz returns a fresh SQL NULL typed as timestamptz.
func NullTimestampz() postgres.TimestampzExpression {
	return postgres.CAST(postgres.NULL).AS_TIMESTAMPZ()
}

// NullUUID returns a fresh SQL NULL typed as uuid.
func NullUUID() postgres.StringExpression {
	return postgres.StringExp(postgres.CAST(postgres.NULL).AS("uuid"))
}

// NullJSONB returns a fresh SQL NULL typed as jsonb.
func NullJSONB() postgres.StringExpression {
	return postgres.StringExp(postgres.CAST(postgres.NULL).AS("jsonb"))
}

// NullBytea returns a fresh SQL NULL typed as bytea.
func NullBytea() postgres.ByteaExpression {
	return postgres.ByteaExp(postgres.CAST(postgres.NULL).AS("bytea"))
}
