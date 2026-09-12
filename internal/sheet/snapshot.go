package sheet

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/db"
	"altalune.id/opensheet/internal/platform/tenant"
)

// SnapshotKey identifies one cached grid by sheet and resolved tab name.
type SnapshotKey struct {
	SheetID uuid.UUID
	Tab     string
}

// Snapshot is one cached serialization of a tab's rows, with the ETag over exactly those bytes.
type Snapshot struct {
	ETag      string
	FetchedAt time.Time
	ExpiresAt time.Time
	Payload   []byte
}

// SnapshotStore is the cache driven port. Get reports an expired entry as found, so a caller can serve it stale when the upstream is down.
type SnapshotStore interface {
	Get(ctx context.Context, k SnapshotKey) (Snapshot, bool, error)
	Put(ctx context.Context, k SnapshotKey, s Snapshot, ttl time.Duration) error
	PurgeSheet(ctx context.Context, sheetID uuid.UUID) error
}

// NewSnapshotStore dispatches to the driver that cache.Resolve picks for the configured database.
// NOTE: there is deliberately no sqlite driver — cache.driver=auto resolves to memory on sqlite, so the sheet_snapshots table in schema/migrations/sqlite/004_opensheet.sql exists only to keep the two schemas symmetrical.
func NewSnapshotStore(
	cache config.CacheConfig,
	dbCfg db.DBConfig,
	pc *tenant.PgConn,
	log *slog.Logger,
) (SnapshotStore, error) {
	driver := cache.Resolve(dbCfg.Driver)
	log.Debug("sheet: snapshot cache driver resolved", "driver", string(driver))

	if driver != config.CacheDriverPostgres {
		return newMemorySnapshotStore(cache.MaxBytes), nil
	}
	if pc == nil {
		return nil, &CacheUnavailableError{
			Driver: string(config.CacheDriverPostgres),
			Reason: "no tenant-scoped connection",
		}
	}
	return newPostgresSnapshotStore(pc, dbCfg.Schema, dbCfg.TablePrefix), nil
}
