package sheet

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/google/uuid"

	"altalune.id/opensheet/gworkspace/gsheet"
)

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
	// ListLive returns the live rows for one (sheet, tab) in row_index order.
	ListLive(ctx context.Context, k SnapshotKey) ([]ProjectedRow, error)
	// Stats counts one tab's live rows and names its columns; an empty tab means the tab the projection holds most of.
	Stats(ctx context.Context, sheetID uuid.UUID, tab string) (TableStats, error)
	// PurgeSheet drops every projected row for a sheet, across tabs.
	PurgeSheet(ctx context.Context, sheetID uuid.UUID) error
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
