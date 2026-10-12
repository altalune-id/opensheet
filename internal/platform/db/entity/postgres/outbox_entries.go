package postgres

import "github.com/go-jet/jet/v2/postgres"

// OutboxEntries is the jet binding for the outbox_entries table.
type OutboxEntries struct {
	postgres.Table

	ID            postgres.ColumnString
	EventID       postgres.ColumnString
	OrgID         postgres.ColumnString
	ProjectID     postgres.ColumnString
	Target        postgres.ColumnString
	Payload       postgres.ColumnBytea
	Attempt       postgres.ColumnInteger
	NextAttemptAt postgres.ColumnTimestampz
	Status        postgres.ColumnString
	LastError     postgres.ColumnString
	CreatedAt     postgres.ColumnTimestampz
	DeliveredAt   postgres.ColumnTimestampz

	AllColumns postgres.ColumnList
}

// NewOutboxEntries builds the outbox_entries binding.
func NewOutboxEntries(schema, tablePrefix string) *OutboxEntries {
	if schema == "" {
		schema = "public"
	}
	var (
		id            = postgres.StringColumn("id")
		eventID       = postgres.StringColumn("event_id")
		orgID         = postgres.StringColumn("org_id")
		projectID     = postgres.StringColumn("project_id")
		target        = postgres.StringColumn("target")
		payload       = postgres.ByteaColumn("payload")
		attempt       = postgres.IntegerColumn("attempt")
		nextAttemptAt = postgres.TimestampzColumn("next_attempt_at")
		status        = postgres.StringColumn("status")
		lastError     = postgres.StringColumn("last_error")
		createdAt     = postgres.TimestampzColumn("created_at")
		deliveredAt   = postgres.TimestampzColumn("delivered_at")
		all           = postgres.ColumnList{id, eventID, orgID, projectID, target, payload, attempt, nextAttemptAt, status, lastError, createdAt, deliveredAt}
	)
	return &OutboxEntries{
		Table:         postgres.NewTable(schema, tablePrefix+"outbox_entries", "outbox_entries", all...),
		ID:            id,
		EventID:       eventID,
		OrgID:         orgID,
		ProjectID:     projectID,
		Target:        target,
		Payload:       payload,
		Attempt:       attempt,
		NextAttemptAt: nextAttemptAt,
		Status:        status,
		LastError:     lastError,
		CreatedAt:     createdAt,
		DeliveredAt:   deliveredAt,
		AllColumns:    all,
	}
}
