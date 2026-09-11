package sheet

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/google/uuid"

	"altalune.id/opensheet/gworkspace/gsheet"
)

const rowDigestVersion = "sheet.rows.v1"

// ProjectedRow is one row of a published tab as the projection holds it.
type ProjectedRow struct {
	RowID     string
	RowIndex  int
	Data      gsheet.Row
	DeletedAt *time.Time
}

// ContractState is what a refresh learned from the tab's header row: its table contract, and its soft-delete opt-in.
type ContractState struct {
	OK         bool
	Reason     string
	SoftDelete bool
}

// SheetState is what a sheet's row bookkeeping holds: the contract its last refresh learned, the digest of its projected rows, and its generation.
type SheetState struct {
	Contract   ContractState
	Digest     string
	Generation int64
}

// TableStats is what the projection knows about one tab without reading every row of it.
type TableStats struct {
	Tab      string
	Columns  []string
	RowCount int64
}

// RowStore persists the queryable projection of a published tab.
type RowStore interface {
	// LockSheet takes the sheet's write lock and returns its current generation.
	LockSheet(ctx context.Context, sheetID uuid.UUID) (int64, error)
	// Replace swaps every row for one (sheet, tab) and bumps the generation, iff gen still matches.
	Replace(ctx context.Context, k SnapshotKey, gen int64, rows []ProjectedRow, contract ContractState) (bool, error)
	// MarkContract persists what a refresh learned about the contract, leaving the projected rows alone, iff gen still matches.
	MarkContract(ctx context.Context, sheetID uuid.UUID, gen int64, contract ContractState) (bool, error)
	// UpsertRow writes one row and bumps the generation, enrolling in the caller's transaction.
	UpsertRow(ctx context.Context, k SnapshotKey, row ProjectedRow) error
	// RowByID returns one row with its tombstone state, reporting absence rather than a zero value.
	RowByID(ctx context.Context, k SnapshotKey, rowID string) (ProjectedRow, error)
	// StateOf reports the contract, content digest and generation the sheet's row bookkeeping holds, read as one row so the three cannot disagree.
	StateOf(ctx context.Context, sheetID uuid.UUID) (SheetState, error)
	// ListLive returns the live rows for one (sheet, tab) in row_index order.
	ListLive(ctx context.Context, k SnapshotKey) ([]ProjectedRow, error)
	// Stats counts one tab's live rows and names its columns; an empty tab means the tab the projection holds most of.
	Stats(ctx context.Context, sheetID uuid.UUID, tab string) (TableStats, error)
	// PurgeSheet drops every projected row for a sheet, across tabs.
	PurgeSheet(ctx context.Context, sheetID uuid.UUID) error
}

// RowsDigest is the content digest of one tab's projected rows: the signal a refresh compares to decide whether the generation moves.
// NOTE: the tombstone is hashed as a flag, never as its timestamp — tombstoneAt stands an unparseable marker in with the fetch time, which would otherwise churn the digest on every refresh.
func RowsDigest(rows []ProjectedRow) (string, error) {
	h := sha256.New()
	digestField(h, rowDigestVersion)
	digestField(h, strconv.Itoa(len(rows)))
	for i := range rows {
		data, err := marshalRowData(rows[i].Data)
		if err != nil {
			return "", err
		}
		digestField(h, strconv.Itoa(rows[i].RowIndex))
		digestField(h, rows[i].RowID)
		digestField(h, tombstoneFlag(rows[i].DeletedAt))
		digestField(h, data)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func digestField(h hash.Hash, field string) {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(field)))
	_, _ = h.Write(n[:])
	_, _ = h.Write([]byte(field))
}

func tombstoneFlag(at *time.Time) string {
	if at == nil {
		return "live"
	}
	return "deleted"
}

func dataColumns(data gsheet.Row) []string {
	return slices.Sorted(maps.Keys(data))
}

func marshalRowData(data gsheet.Row) (string, error) {
	if data == nil {
		data = gsheet.Row{}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("sheet.rows: marshal data: %w", err)
	}
	return string(raw), nil
}

func unmarshalRowData(raw string) (gsheet.Row, error) {
	data := gsheet.Row{}
	if raw == "" {
		return data, nil
	}
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return nil, fmt.Errorf("sheet.rows: unmarshal data: %w", err)
	}
	return data, nil
}
