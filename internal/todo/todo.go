// Package todo is the todos bounded context.
package todo

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// StaleAfter is how long an open todo may sit before the scheduler marks it done.
const StaleAfter = 14 * 24 * time.Hour

// SweepBatchSize is how many rows one MarkDoneOlderThan statement updates.
const SweepBatchSize = 1000

// Todo is the aggregate root. Invariants live here.
type Todo struct {
	ID        uuid.UUID
	OrgID     uuid.UUID
	ProjectID uuid.UUID
	Title     string
	Done      bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// New enforces creation invariants: title trimmed, non-empty, <= 200 runes.
func New(orgID, projectID uuid.UUID, title string) (*Todo, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, &InvalidTitleError{Reason: "empty"}
	}
	if utf8.RuneCountInString(title) > 200 {
		return nil, &InvalidTitleError{Reason: "over 200 characters"}
	}
	now := time.Now().UTC()
	return &Todo{
		ID:        uuid.Must(uuid.NewV7()),
		OrgID:     orgID,
		ProjectID: projectID,
		Title:     title,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// Toggle flips the done flag and bumps UpdatedAt.
func (t *Todo) Toggle() {
	t.Done = !t.Done
	t.UpdatedAt = time.Now().UTC()
}

// ListOpts filters Store.List. Zero value returns every todo in scope.
type ListOpts struct {
	Done *bool
}
