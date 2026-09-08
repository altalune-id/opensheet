package sheet

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/go-jet/jet/v2/qrm"
	"github.com/google/uuid"

	pgent "altalune.id/opensheet/internal/platform/db/entity/postgres"
	"altalune.id/opensheet/internal/platform/tenant"
)

type postgresSnapshotStore struct {
	pgTxn

	table *pgent.SheetSnapshots
}

func newPostgresSnapshotStore(pc *tenant.PgConn, schema, tablePrefix string) *postgresSnapshotStore {
	return &postgresSnapshotStore{
		pgTxn: pgTxn{pc: pc},
		table: pgent.NewSheetSnapshots(schema, tablePrefix),
	}
}

type pgSnapshotRow struct {
	ETag      string    `alias:"sheet_snapshots.etag"`
	Payload   []byte    `alias:"sheet_snapshots.payload"`
	FetchedAt time.Time `alias:"sheet_snapshots.fetched_at"`
	ExpiresAt time.Time `alias:"sheet_snapshots.expires_at"`
}

func (r *pgSnapshotRow) toSnapshot() Snapshot {
	return Snapshot{
		ETag:      r.ETag,
		FetchedAt: r.FetchedAt.UTC(),
		ExpiresAt: r.ExpiresAt.UTC(),
		Payload:   r.Payload,
	}
}

func (s *postgresSnapshotStore) Get(ctx context.Context, k SnapshotKey) (Snapshot, bool, error) {
	tx, owned, err := s.txAcquire(ctx)
	if err != nil {
		return Snapshot{}, false, err
	}
	if owned {
		defer func() { _ = tx.Rollback() }()
	}
	stmt := postgres.SELECT(s.table.ETag, s.table.Payload, s.table.FetchedAt, s.table.ExpiresAt).
		FROM(s.table).
		WHERE(s.table.SheetID.EQ(postgres.UUID(k.SheetID)).
			AND(s.table.Tab.EQ(postgres.String(k.Tab)))).
		LIMIT(1)
	var row pgSnapshotRow
	if qErr := stmt.QueryContext(ctx, tx, &row); qErr != nil {
		if errors.Is(qErr, qrm.ErrNoRows) || errors.Is(qErr, sql.ErrNoRows) {
			return Snapshot{}, false, nil
		}
		return Snapshot{}, false, fmt.Errorf("sheet.snapshot.postgres.Get: %w", qErr)
	}
	return row.toSnapshot(), true, nil
}

func (s *postgresSnapshotStore) Put(ctx context.Context, k SnapshotKey, snap Snapshot, ttl time.Duration) error {
	tc, err := tenant.From(ctx)
	if err != nil {
		return err
	}
	tx, owned, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	fetchedAt := snap.FetchedAt.UTC()
	expiresAt := fetchedAt.Add(ttl)
	stmt := s.table.INSERT(s.table.AllColumns).
		VALUES(
			k.SheetID, k.Tab, snap.ETag, snap.Payload,
			fetchedAt, expiresAt, tc.OrgID, tc.ProjectID,
		).
		ON_CONFLICT(s.table.SheetID, s.table.Tab).
		DO_UPDATE(
			postgres.SET(
				s.table.ETag.SET(postgres.String(snap.ETag)),
				s.table.Payload.SET(postgres.Bytea(snap.Payload)),
				s.table.FetchedAt.SET(postgres.TimestampzT(fetchedAt)),
				s.table.ExpiresAt.SET(postgres.TimestampzT(expiresAt)),
			),
		)
	if _, execErr := stmt.ExecContext(ctx, tx); execErr != nil {
		return s.endTx(tx, owned, fmt.Errorf("sheet.snapshot.postgres.Put: %w", execErr))
	}
	return s.endTx(tx, owned, nil)
}

func (s *postgresSnapshotStore) PurgeSheet(ctx context.Context, sheetID uuid.UUID) error {
	tx, owned, err := s.txAcquire(ctx)
	if err != nil {
		return err
	}
	stmt := s.table.DELETE().WHERE(s.table.SheetID.EQ(postgres.UUID(sheetID)))
	if _, execErr := stmt.ExecContext(ctx, tx); execErr != nil {
		return s.endTx(tx, owned, fmt.Errorf("sheet.snapshot.postgres.PurgeSheet: %w", execErr))
	}
	return s.endTx(tx, owned, nil)
}
