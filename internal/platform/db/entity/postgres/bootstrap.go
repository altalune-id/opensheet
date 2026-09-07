package postgres

import "github.com/go-jet/jet/v2/postgres"

// Bootstrap is the jet binding for the bootstrap table.
type Bootstrap struct {
	postgres.Table

	ID          postgres.ColumnInteger
	OnboardedAt postgres.ColumnTimestampz
	OnboardedBy postgres.ColumnString
	Method      postgres.ColumnString

	AllColumns postgres.ColumnList
}

// NewBootstrap builds the bootstrap binding.
func NewBootstrap(schema, tablePrefix string) *Bootstrap {
	if schema == "" {
		schema = "public"
	}
	var (
		id          = postgres.IntegerColumn("id")
		onboardedAt = postgres.TimestampzColumn("onboarded_at")
		onboardedBy = postgres.StringColumn("onboarded_by")
		method      = postgres.StringColumn("method")
		all         = postgres.ColumnList{id, onboardedAt, onboardedBy, method}
	)
	return &Bootstrap{
		Table:       postgres.NewTable(schema, tablePrefix+"bootstrap", "bootstrap", all...),
		ID:          id,
		OnboardedAt: onboardedAt,
		OnboardedBy: onboardedBy,
		Method:      method,
		AllColumns:  all,
	}
}
