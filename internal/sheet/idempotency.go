package sheet

import (
	"bytes"
	"context"
	"time"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/tenant"
)

// IdempotencyTTL bounds how long one write attempt stays reserved and replayable.
const IdempotencyTTL = 24 * time.Hour

// IdempotencyKey identifies one write attempt by sheet, resolved tab and caller-supplied key.
type IdempotencyKey struct {
	SheetID uuid.UUID
	Tab     string
	Key     string
}

// Attempt is one recorded write attempt: the request fingerprint and, once done, the response to replay.
type Attempt struct {
	BodyHash  string
	Payload   []byte
	Done      bool
	CreatedAt time.Time
}

func (a Attempt) clone() Attempt {
	a.Payload = bytes.Clone(a.Payload)
	return a
}

// IdempotencyStore is the driven port that makes an append retryable. Reserve is a lock, not a cache: taking it before the upstream call is what stops two simultaneous retries from both appending.
type IdempotencyStore interface {
	Reserve(ctx context.Context, k IdempotencyKey, bodyHash string, ttl time.Duration) (claimed bool, prior Attempt, err error)
	Complete(ctx context.Context, k IdempotencyKey, payload []byte, ttl time.Duration) error
	Release(ctx context.Context, k IdempotencyKey) error
	// DeleteExpired removes attempts whose expiry has passed and reports how many went.
	DeleteExpired(ctx context.Context) (int, error)
}

// NewIdempotencyStore dispatches on db.driver alone, so no cache setting can silently downgrade a cross-replica lock to a per-process one.
func NewIdempotencyStore(cfg db.DBConfig, pc *tenant.PgConn) IdempotencyStore {
	if cfg.Driver == db.DriverPostgres {
		return newPostgresIdempotencyStore(pc, cfg.Schema, cfg.TablePrefix)
	}
	return NewMemoryIdempotencyStore()
}
