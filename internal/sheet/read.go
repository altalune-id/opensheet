package sheet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/oauth2"
	"golang.org/x/sync/singleflight"

	"altalune.id/opensheet/gworkspace"
	"altalune.id/opensheet/gworkspace/gsheet"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/gwerr"
)

const etagHexLen = 32

// Source is the Google document a sheet reads from, referenced across the module boundary by id.
type Source struct {
	GoogleFileID string
	CredentialID uuid.UUID
}

// Sources resolves a spreadsheet id to the Google document and credential behind it.
type Sources interface {
	SourceFor(ctx context.Context, spreadsheetID uuid.UUID) (Source, error)
}

// TokenSources resolves a credential id to a Google token source.
type TokenSources interface {
	TokenSourceFor(ctx context.Context, credentialID uuid.UUID) (oauth2.TokenSource, error)
}

// Reauthers flags a credential Google has rejected as needing reauthorization.
type Reauthers interface {
	MarkReauthNeeded(ctx context.Context, credentialID uuid.UUID) error
}

// Rows is one read of a published tab, carrying its cache provenance.
type Rows struct {
	Values []gsheet.Row
	// SECURITY: Payload is the exact bytes ETag hashes, shared across singleflight callers. Read it, never mutate it.
	Payload   []byte
	Warnings  []string
	ETag      string
	FetchedAt time.Time
	Cached    bool
	Stale     bool
}

// ReadWorkflow serves a published tab from the snapshot cache, falling back to Google and to a stale snapshot.
type ReadWorkflow struct {
	snaps           SnapshotStore
	rows            RowStore
	sources         Sources
	tokens          TokenSources
	reauth          Reauthers
	clients         gsheet.Factory
	caps            Capabilities
	defaultTTL      time.Duration
	maxPayloadBytes int64
	maxQueryRows    int
	maxSortPages    int
	log             *slog.Logger
	unexpected      apperror.UnexpectedFunc
	flight          singleflight.Group
}

// NewReadWorkflow binds the read path to its dependencies.
func NewReadWorkflow(
	snaps SnapshotStore,
	rows RowStore,
	sources Sources,
	tokens TokenSources,
	reauth Reauthers,
	clients gsheet.Factory,
	caps Capabilities,
	defaultTTL time.Duration,
	maxPayloadBytes int64,
	maxQueryRows int,
	maxSortPages int,
	log *slog.Logger,
	unexpected apperror.UnexpectedFunc,
) *ReadWorkflow {
	return &ReadWorkflow{
		snaps:           snaps,
		rows:            rows,
		sources:         sources,
		tokens:          tokens,
		reauth:          reauth,
		clients:         clients,
		caps:            caps,
		defaultTTL:      defaultTTL,
		maxPayloadBytes: maxPayloadBytes,
		maxQueryRows:    maxQueryRows,
		maxSortPages:    maxSortPages,
		log:             log.With("module", "sheet"),
		unexpected:      unexpected,
	}
}

// Rows returns sh's tab, from the cache when fresh, from Google otherwise, and stale when Google is down.
func (w *ReadWorkflow) Rows(ctx context.Context, sh *Sheet) (Rows, error) {
	ctx, span := tracer.Start(ctx, "sheet.Rows",
		trace.WithAttributes(
			attribute.String("sheet.id", sh.ID.String()),
			attribute.String("sheet.slug", sh.Slug),
		))
	defer span.End()

	// SECURITY: the capability is re-checked on every read, so turning it off stops sheets that were published while it was on.
	if sh.Visibility == VisibilityPublic && !w.caps.PublicSheetsEnabled() {
		err := &PublicDisabledError{Slug: sh.Slug}
		span.RecordError(err)
		return Rows{}, err
	}

	src, err := w.sources.SourceFor(ctx, sh.SpreadsheetID)
	if err != nil {
		span.RecordError(err)
		return Rows{}, w.passthrough(ctx, "sheet.Rows: source", err, sh)
	}

	tab := strings.TrimSpace(sh.Tab)
	var client *gsheet.Client
	if tab == "" {
		client, err = w.client(ctx, sh, src)
		if err != nil {
			span.RecordError(err)
			return Rows{}, err
		}
		tab, err = client.FirstTab(ctx, src.GoogleFileID)
		if err != nil {
			span.RecordError(err)
			return Rows{}, w.passthrough(ctx, "sheet.Rows: first tab", gwerr.AppError(err), sh)
		}
	}
	span.SetAttributes(attribute.String("sheet.tab", tab))

	key := SnapshotKey{SheetID: sh.ID, Tab: tab}
	snap, found, err := w.snaps.Get(ctx, key)
	if err != nil {
		span.RecordError(err)
		_ = w.unexpected(ctx, "sheet.Rows: snapshot get", err, "sheet_id", sh.ID, "tab", tab)
		found = false
	}
	if found && snap.ExpiresAt.After(time.Now().UTC()) {
		span.SetAttributes(attribute.Bool("sheet.cached", true))
		return w.fromSnapshot(ctx, sh, snap, false)
	}

	if client == nil {
		client, err = w.client(ctx, sh, src)
		if err != nil {
			span.RecordError(err)
			return Rows{}, err
		}
	}

	fresh, err := w.fetch(ctx, client, sh, src, key, found)
	if err == nil {
		return fresh, nil
	}
	span.RecordError(err)

	w.markReauth(ctx, sh, src, err)
	// NOTE: drift is a parallel branch on purpose — isGoogleFailure keeps meaning "Google is unreachable".
	if found && isContractDrift(err) {
		if stale, ok := w.stale(ctx, sh, tab, snap, "sheet: serving a stale snapshot for a tab that no longer satisfies the contract", err); ok {
			span.SetAttributes(attribute.Bool("sheet.stale", true))
			return stale, nil
		}
		return Rows{}, err
	}
	if found && isGoogleFailure(err) {
		if stale, ok := w.stale(ctx, sh, tab, snap, "sheet: serving a stale snapshot", err); ok {
			span.SetAttributes(attribute.Bool("sheet.stale", true))
			return stale, nil
		}
	}
	return Rows{}, err
}

func (w *ReadWorkflow) stale(
	ctx context.Context, sh *Sheet, tab string, snap Snapshot, situation string, cause error,
) (Rows, bool) {
	out, err := w.fromSnapshot(ctx, sh, snap, true)
	if err != nil {
		return Rows{}, false
	}
	w.log.WarnContext(ctx, situation, "sheet_id", sh.ID, "tab", tab, "err", cause)
	return out, true
}

func (w *ReadWorkflow) client(ctx context.Context, sh *Sheet, src Source) (*gsheet.Client, error) {
	ts, err := w.tokens.TokenSourceFor(ctx, src.CredentialID)
	if err != nil {
		return nil, w.passthrough(ctx, "sheet.Rows: token source", err, sh)
	}
	client, err := w.clients(ctx, ts)
	if err != nil {
		return nil, w.passthrough(ctx, "sheet.Rows: build client", err, sh)
	}
	return client, nil
}

func (w *ReadWorkflow) markReauth(ctx context.Context, sh *Sheet, src Source, cause error) {
	if !gworkspace.IsAuthExpiredError(cause) {
		return
	}
	if err := w.reauth.MarkReauthNeeded(ctx, src.CredentialID); err != nil {
		_ = w.unexpected(ctx, "sheet.Rows: mark reauth needed", err,
			"sheet_id", sh.ID, "credential_id", src.CredentialID)
	}
}

// NOTE: singleflight.Do runs the loader on the calling goroutine, so no goroutine is started here and there is nothing to shut down.
func (w *ReadWorkflow) fetch(
	ctx context.Context, client *gsheet.Client, sh *Sheet, src Source, key SnapshotKey, staleAvailable bool,
) (Rows, error) {
	v, err, shared := w.flight.Do(key.SheetID.String()+"\x00"+key.Tab, func() (any, error) {
		return w.load(ctx, client, sh, src, key, staleAvailable)
	})
	if err != nil {
		return Rows{}, err
	}
	out, _ := v.(Rows)
	if shared {
		out.Values = cloneRows(out.Values)
		out.Warnings = slices.Clone(out.Warnings)
	}
	return out, nil
}

func (w *ReadWorkflow) load(
	ctx context.Context, client *gsheet.Client, sh *Sheet, src Source, key SnapshotKey, staleAvailable bool,
) (Rows, error) {
	got, err := w.reproject(ctx, client, sh, src, key, staleAvailable)
	if err != nil {
		return Rows{}, err
	}
	if !got.applied {
		return w.committed(ctx, sh, key)
	}
	return w.serve(ctx, sh, key, got.values, got.warnings, got.fetchedAt)
}

// reprojected is what one refresh committed, and the live picture a snapshot would hold.
type reprojected struct {
	values    []gsheet.Row
	warnings  []string
	fetchedAt time.Time
	applied   bool
}

// NOTE: the projection is written before anything is marshalled, so a tab over maxPayloadBytes is still queryable — it loses only its snapshot.
// NOTE: gen0 comes from the caller's freshly resolved aggregate, and the fetch runs outside any transaction — holding the sheet's row lock across a Google call would serialize every reader.
func (w *ReadWorkflow) reproject(
	ctx context.Context, client *gsheet.Client, sh *Sheet, src Source, key SnapshotKey, staleAvailable bool,
) (reprojected, error) {
	gen0 := sh.Generation
	tbl, err := client.Table(ctx, src.GoogleFileID, key.Tab)
	if err != nil {
		return reprojected{}, gwerr.AppError(err)
	}
	fetchedAt := time.Now().UTC()

	contract, _, cErr := validateContract(tbl, key.Tab)
	if cErr != nil {
		w.markDrift(ctx, sh, key, gen0, contract, cErr)
		if staleAvailable {
			return reprojected{}, cErr
		}
		// NOTE: a whole-tab read needs no id column, so a drifted tab with nothing cached is served verbatim rather than refused.
		verbatim, warnings := keyRows(tbl)
		return reprojected{values: verbatim, warnings: warnings, fetchedAt: fetchedAt, applied: true}, nil
	}

	projected, pErr := projectRows(tbl, key.Tab, fetchedAt)
	if pErr != nil {
		return reprojected{}, w.unexpected(ctx, "sheet.Rows: project rows", pErr, "sheet_id", sh.ID, "tab", key.Tab)
	}
	_, warnings := gsheet.NormalizeHeaders(tbl.Headers)
	out := reprojected{
		values: liveValues(projected), warnings: warnings, fetchedAt: fetchedAt, applied: true,
	}
	switch applied, rErr := w.rows.Replace(ctx, key, gen0, projected, contract); {
	case rErr != nil:
		_ = w.unexpected(ctx, "sheet.Rows: replace projection", rErr, "sheet_id", sh.ID, "tab", key.Tab)
	case !applied:
		out.applied = false
	}
	return out, nil
}

// NOTE: a discarded refresh holds the older picture, so serving its own fetch would answer with a body no snapshot holds and an ETag nothing can revalidate.
func (w *ReadWorkflow) committed(ctx context.Context, sh *Sheet, key SnapshotKey) (Rows, error) {
	rows, err := w.rows.ListLive(ctx, key)
	if err != nil {
		return Rows{}, w.unexpected(ctx, "sheet.Rows: committed projection", err, "sheet_id", sh.ID, "tab", key.Tab)
	}
	out, err := w.rowsOf(ctx, sh, key, liveValues(rows), nil, time.Now().UTC())
	if err != nil {
		return Rows{}, err
	}
	snap, found, gErr := w.snaps.Get(ctx, key)
	if gErr != nil {
		_ = w.unexpected(ctx, "sheet.Rows: snapshot get", gErr, "sheet_id", sh.ID, "tab", key.Tab)
	}
	if found && snap.ETag == out.ETag {
		return w.fromSnapshot(ctx, sh, snap, false)
	}
	w.cache(ctx, sh, key, out)
	out.Cached = true
	return out, nil
}

func (w *ReadWorkflow) serve(
	ctx context.Context, sh *Sheet, key SnapshotKey, values []gsheet.Row, warnings []string, fetchedAt time.Time,
) (Rows, error) {
	out, err := w.rowsOf(ctx, sh, key, values, warnings, fetchedAt)
	if err != nil {
		return Rows{}, err
	}
	w.cache(ctx, sh, key, out)
	return out, nil
}

func (w *ReadWorkflow) rowsOf(
	ctx context.Context, sh *Sheet, key SnapshotKey, values []gsheet.Row, warnings []string, fetchedAt time.Time,
) (Rows, error) {
	if values == nil {
		values = []gsheet.Row{}
	}
	payload, err := json.Marshal(values)
	if err != nil {
		return Rows{}, w.unexpected(ctx, "sheet.Rows: serialize", err, "sheet_id", sh.ID, "tab", key.Tab)
	}
	if size := int64(len(payload)); w.maxPayloadBytes > 0 && size > w.maxPayloadBytes {
		return Rows{}, &PayloadTooLargeError{Bytes: size, MaxBytes: w.maxPayloadBytes}
	}
	return Rows{
		Values:    values,
		Payload:   payload,
		Warnings:  warnings,
		ETag:      etagOf(payload),
		FetchedAt: fetchedAt,
	}, nil
}

func (w *ReadWorkflow) cache(ctx context.Context, sh *Sheet, key SnapshotKey, out Rows) {
	snap := Snapshot{ETag: out.ETag, FetchedAt: out.FetchedAt, Payload: out.Payload}
	if err := w.snaps.Put(ctx, key, snap, ttlOf(sh, w.defaultTTL)); err != nil {
		_ = w.unexpected(ctx, "sheet.Rows: snapshot put", err, "sheet_id", sh.ID, "tab", key.Tab)
	}
}

// NOTE: the projected rows are left alone — a drifted tab keeps its last good picture, and only the finding is persisted.
func (w *ReadWorkflow) markDrift(
	ctx context.Context, sh *Sheet, key SnapshotKey, gen int64, contract ContractState, cause error,
) {
	w.log.WarnContext(ctx, "sheet: the tab no longer satisfies the table contract",
		"sheet_id", sh.ID, "tab", key.Tab, "reason", contract.Reason, "err", cause)
	if _, err := w.rows.MarkContract(ctx, sh.ID, gen, contract); err != nil {
		_ = w.unexpected(ctx, "sheet.Rows: persist contract drift", err, "sheet_id", sh.ID, "tab", key.Tab)
	}
}

func (w *ReadWorkflow) fromSnapshot(ctx context.Context, sh *Sheet, snap Snapshot, stale bool) (Rows, error) {
	var values []gsheet.Row
	if err := json.Unmarshal(snap.Payload, &values); err != nil {
		return Rows{}, w.unexpected(ctx, "sheet.Rows: decode snapshot", err,
			"sheet_id", sh.ID, "etag", snap.ETag)
	}
	if values == nil {
		values = []gsheet.Row{}
	}
	return Rows{
		Values:    values,
		Payload:   snap.Payload,
		ETag:      snap.ETag,
		FetchedAt: snap.FetchedAt,
		Cached:    true,
		Stale:     stale,
	}, nil
}

// NOTE: a Google or credential failure already carries a wire code and is an expected outcome the surfaces map, so it passes through instead of being reported as an incident.
func (w *ReadWorkflow) passthrough(ctx context.Context, situation string, err error, sh *Sheet) error {
	if _, ok := apperror.AsAppError(err); ok {
		return err
	}
	return w.unexpected(ctx, situation, err, "sheet_id", sh.ID, "spreadsheet_id", sh.SpreadsheetID)
}

func ttlOf(sh *Sheet, defaultTTL time.Duration) time.Duration {
	if sh.CacheTTL != DefaultCacheTTL {
		return sh.CacheTTL
	}
	return defaultTTL
}

func etagOf(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])[:etagHexLen]
}

// MatchesETag reports whether an If-Match or If-None-Match header names etag.
func MatchesETag(header, etag string) bool {
	if header == "" {
		return false
	}
	quoted := strconv.Quote(etag)
	for tag := range strings.SplitSeq(header, ",") {
		tag = strings.TrimSpace(tag)
		if tag == "*" || strings.TrimPrefix(tag, "W/") == quoted {
			return true
		}
	}
	return false
}

// NOTE: row_index is the row's position in the fetched tab, so an interior blank row consumes its index rather than compacting the ones below it.
func projectRows(tbl gsheet.Table, tab string, fetchedAt time.Time) ([]ProjectedRow, error) {
	idCol, err := idColumnOf(tbl.Headers, tab)
	if err != nil {
		return nil, err
	}
	delCol, err := deletedAtColumnOf(tbl.Headers, tab)
	if err != nil {
		return nil, err
	}
	names, _ := gsheet.NormalizeHeaders(tbl.Headers)
	out := make([]ProjectedRow, 0, len(tbl.Rows))
	for i, cells := range tbl.Rows {
		if blankRow(cells) {
			continue
		}
		data := make(gsheet.Row, len(names))
		for j, name := range names {
			if j == delCol {
				continue
			}
			data[name] = cellAt(cells, j)
		}
		out = append(out, ProjectedRow{
			RowID:     cellAt(cells, idCol),
			RowIndex:  i,
			Data:      data,
			DeletedAt: tombstoneAt(cellAt(cells, delCol), fetchedAt),
		})
	}
	return out, nil
}

// NOTE: an unparseable marker still means deleted, so the fetch time stands in rather than resurrecting the row.
func tombstoneAt(cell string, fetchedAt time.Time) *time.Time {
	if !isTombstoneCell(cell) {
		return nil
	}
	cell = strings.TrimSpace(cell)
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if at, err := time.Parse(layout, cell); err == nil {
			utc := at.UTC()
			return &utc
		}
	}
	return &fetchedAt
}

func isTombstoneCell(cell string) bool {
	return strings.TrimSpace(cell) != ""
}

func liveValues(rows []ProjectedRow) []gsheet.Row {
	out := make([]gsheet.Row, 0, len(rows))
	for i := range rows {
		if rows[i].DeletedAt != nil {
			continue
		}
		out = append(out, rows[i].Data)
	}
	return out
}

// NOTE: the same keying gsheet.Client.Rows does, from a table the caller already holds, so a drifted tab costs one Google read rather than two.
func keyRows(tbl gsheet.Table) (rows []gsheet.Row, warnings []string) {
	if tbl.Headers == nil {
		return nil, nil
	}
	var names []string
	names, warnings = gsheet.NormalizeHeaders(tbl.Headers)
	rows = make([]gsheet.Row, 0, len(tbl.Rows))
	for _, cells := range tbl.Rows {
		row := make(gsheet.Row, len(names))
		for i, cell := range cells {
			if i >= len(names) {
				break
			}
			row[names[i]] = cell
		}
		rows = append(rows, row)
	}
	return rows, warnings
}

func isContractDrift(err error) bool {
	return IsNoIDColumnError(err) ||
		IsAmbiguousIDColumnError(err) ||
		IsDuplicateColumnError(err) ||
		IsEmptyIDError(err) ||
		IsDuplicateIDError(err)
}

func isGoogleFailure(err error) bool {
	return gworkspace.IsNotFoundError(err) ||
		gworkspace.IsPermissionDeniedError(err) ||
		gworkspace.IsQuotaExceededError(err) ||
		gworkspace.IsUnavailableError(err) ||
		gworkspace.IsAuthExpiredError(err) ||
		gsheet.IsTabNotFoundError(err)
}

func cloneRows(in []gsheet.Row) []gsheet.Row {
	out := make([]gsheet.Row, len(in))
	for i, row := range in {
		out[i] = maps.Clone(row)
	}
	return out
}
