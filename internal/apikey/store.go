package apikey

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Store is the driven port.
type Store interface {
	// Save persists the key and its sheet grant atomically. SECURITY: it never overwrites a stored secret_hash with an empty one.
	Save(ctx context.Context, k *APIKey) error
	// ByID returns the key in the caller's tenant scope, without secret material.
	ByID(ctx context.Context, id uuid.UUID) (*APIKey, error)
	// ByPrefix resolves a presented key before any tenant scope exists, through the SECURITY DEFINER wrapper.
	// SECURITY: the only method that returns SecretHash, and the only one that bypasses RLS.
	ByPrefix(ctx context.Context, prefix string) (*APIKey, error)
	// List returns the project's keys, without secret material.
	List(ctx context.Context, orgID, projectID uuid.UUID) ([]*APIKey, error)
	Delete(ctx context.Context, id uuid.UUID) error
	// TouchLastUsed stamps the key's last_used_at without loading the aggregate.
	// NOTE: a missing row is not an error — the key may have been deleted between use and flush.
	TouchLastUsed(ctx context.Context, orgID, projectID, id uuid.UUID, at time.Time) error
}
