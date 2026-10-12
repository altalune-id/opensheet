package sqlite

import "github.com/go-jet/jet/v2/sqlite"

// OutboxEntries is the jet binding for the outbox_entries table.
type OutboxEntries struct {
	sqlite.Table

	ID            sqlite.ColumnString
	EventID       sqlite.ColumnString
	OrgID         sqlite.ColumnString
	ProjectID     sqlite.ColumnString
	Target        sqlite.ColumnString
	Payload       sqlite.ColumnBlob
	Attempt       sqlite.ColumnInteger
	NextAttemptAt sqlite.ColumnString
	Status        sqlite.ColumnString
	LastError     sqlite.ColumnString
	CreatedAt     sqlite.ColumnString
	DeliveredAt   sqlite.ColumnString

	AllColumns sqlite.ColumnList
}

// NewOutboxEntries builds the outbox_entries binding.
func NewOutboxEntries(tablePrefix string) *OutboxEntries {
	var (
		id            = sqlite.StringColumn("id")
		eventID       = sqlite.StringColumn("event_id")
		orgID         = sqlite.StringColumn("org_id")
		projectID     = sqlite.StringColumn("project_id")
		target        = sqlite.StringColumn("target")
		payload       = sqlite.BlobColumn("payload")
		attempt       = sqlite.IntegerColumn("attempt")
		nextAttemptAt = sqlite.StringColumn("next_attempt_at")
		status        = sqlite.StringColumn("status")
		lastError     = sqlite.StringColumn("last_error")
		createdAt     = sqlite.StringColumn("created_at")
		deliveredAt   = sqlite.StringColumn("delivered_at")
		all           = sqlite.ColumnList{id, eventID, orgID, projectID, target, payload, attempt, nextAttemptAt, status, lastError, createdAt, deliveredAt}
	)
	return &OutboxEntries{
		Table:         sqlite.NewTable("", tablePrefix+"outbox_entries", "outbox_entries", all...),
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
