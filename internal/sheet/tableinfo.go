package sheet

import (
	"context"
	"slices"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// TableInfo is what one published tab's projection and persisted contract state say about it.
type TableInfo struct {
	Columns           []string   `json:"columns"`
	IDColumn          bool       `json:"idColumn"`
	SoftDelete        bool       `json:"softDelete"`
	Writable          bool       `json:"writable"`
	SatisfiesContract bool       `json:"satisfiesContract"`
	ContractReason    string     `json:"contractReason"`
	RowCount          int64      `json:"rowCount"`
	Generation        int64      `json:"generation"`
	ValidatedAt       *time.Time `json:"validatedAt"`
}

// TableInfo reports sh's table shape from the projection and the sheets row, with no Google call.
func (w *ReadWorkflow) TableInfo(ctx context.Context, sh *Sheet) (TableInfo, error) {
	ctx, span := tracer.Start(ctx, "sheet.TableInfo",
		trace.WithAttributes(
			attribute.String("sheet.id", sh.ID.String()),
			attribute.String("sheet.slug", sh.Slug),
		))
	defer span.End()

	// SECURITY: the capability is re-checked here too, so a public sheet stops describing itself once it is off.
	if sh.Visibility == VisibilityPublic && !w.caps.PublicSheetsEnabled() {
		err := &PublicDisabledError{Slug: sh.Slug}
		span.RecordError(err)
		return TableInfo{}, err
	}

	stats, err := w.rows.Stats(ctx, sh.ID, strings.TrimSpace(sh.Tab))
	if err != nil {
		span.RecordError(err)
		return TableInfo{}, w.unexpected(ctx, "sheet.TableInfo: stats", err,
			"sheet_id", sh.ID, "tab", sh.Tab)
	}
	span.SetAttributes(attribute.String("sheet.tab", stats.Tab))

	columns := tableColumns(stats)
	return TableInfo{
		Columns:           columns,
		IDColumn:          idColumnKnown(columns, sh),
		SoftDelete:        stats.SoftDelete,
		Writable:          sh.Writable,
		SatisfiesContract: sh.ContractOK,
		ContractReason:    sh.ContractReason,
		RowCount:          stats.RowCount,
		Generation:        sh.Generation,
		ValidatedAt:       sh.ValidatedAt,
	}, nil
}

// NOTE: deleted_at is stripped from data, so a tombstoned row is the only trace of the column the projection keeps.
func tableColumns(stats TableStats) []string {
	out := make([]string, 0, len(stats.Columns)+1)
	out = append(out, stats.Columns...)
	if stats.SoftDelete && !slices.Contains(out, deletedAtColumn) {
		out = append(out, deletedAtColumn)
	}
	slices.Sort(out)
	return out
}

// NOTE: the projection is empty until the first read, so an unread sheet falls back to publish's verdict rather than reporting a validated tab as untyped.
func idColumnKnown(columns []string, sh *Sheet) bool {
	if len(columns) > 0 {
		return hasIDColumn(columns)
	}
	return sh.ContractOK && sh.ValidatedAt != nil
}

func hasIDColumn(columns []string) bool {
	for _, name := range columns {
		if foldHeader(name) == idColumn {
			return true
		}
	}
	return false
}
