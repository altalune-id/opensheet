package events

import (
	"time"

	"github.com/google/uuid"
)

// PostSnapshotV1 is the blog post shape shared by the publish and unpublish payloads.
type PostSnapshotV1 struct {
	ID               uuid.UUID   `json:"id"`
	Slug             string      `json:"slug"`
	Title            string      `json:"title"`
	BodyMarkdown     string      `json:"body_markdown"`
	CategoryID       uuid.UUID   `json:"category_id"`
	TagIDs           []uuid.UUID `json:"tag_ids"`
	FirstPublishedAt *time.Time  `json:"first_published_at"`
	UpdatedAt        time.Time   `json:"updated_at"`
	Version          int         `json:"version"`
}

// PostPublishedV1 is the payload for PostPublished.
type PostPublishedV1 PostSnapshotV1

// PostUnpublishedV1 is the payload for PostUnpublished.
type PostUnpublishedV1 PostSnapshotV1

// PostDeletedV1 is the payload for PostDeleted.
type PostDeletedV1 struct {
	ID           uuid.UUID `json:"id"`
	Slug         string    `json:"slug"`
	WasPublished bool      `json:"was_published"`
}

// WebhookPingV1 is the payload for WebhookPing.
type WebhookPingV1 struct {
	EndpointID uuid.UUID `json:"endpoint_id"`
}
