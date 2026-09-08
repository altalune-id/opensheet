package sheet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/singleflight"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/gsheets"
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
	TokenSourceFor(ctx context.Context, credentialID uuid.UUID) (gsheets.TokenSource, error)
}

// Reauthers flags a credential Google has rejected as needing reauthorization.
type Reauthers interface {
	MarkReauthNeeded(ctx context.Context, credentialID uuid.UUID) error
}

// Rows is one read of a published tab, carrying its cache provenance.
type Rows struct {
	Values    []gsheets.Row
	Warnings  []string
	ETag      string
	FetchedAt time.Time
	Cached    bool
	Stale     bool
}

// ReadWorkflow serves a published tab from the snapshot cache, falling back to Google and to a stale snapshot.
type ReadWorkflow struct {
	snaps           SnapshotStore
	sources         Sources
	tokens          TokenSources
	reauth          Reauthers
	clients         gsheets.Factory
	caps            Capabilities
	defaultTTL      time.Duration
	maxPayloadBytes int64
	log             *slog.Logger
	unexpected      apperror.UnexpectedFunc
	flight          singleflight.Group
}

// NewReadWorkflow binds the read path to its dependencies.
func NewReadWorkflow(
	snaps SnapshotStore,
	sources Sources,
	tokens TokenSources,
	reauth Reauthers,
	clients gsheets.Factory,
	caps Capabilities,
	defaultTTL time.Duration,
	maxPayloadBytes int64,
	log *slog.Logger,
	unexpected apperror.UnexpectedFunc,
) *ReadWorkflow {
	return &ReadWorkflow{
		snaps:           snaps,
		sources:         sources,
		tokens:          tokens,
		reauth:          reauth,
		clients:         clients,
		caps:            caps,
		defaultTTL:      defaultTTL,
		maxPayloadBytes: maxPayloadBytes,
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
	var client *gsheets.Client
	if tab == "" {
		client, err = w.client(ctx, sh, src)
		if err != nil {
			span.RecordError(err)
			return Rows{}, err
		}
		tab, err = client.FirstTab(ctx, src.GoogleFileID)
		if err != nil {
			span.RecordError(err)
			return Rows{}, w.passthrough(ctx, "sheet.Rows: first tab", err, sh)
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

	fresh, err := w.fetch(ctx, client, sh, src, key)
	if err == nil {
		return fresh, nil
	}
	span.RecordError(err)

	if gsheets.IsAuthExpiredError(err) {
		if mErr := w.reauth.MarkReauthNeeded(ctx, src.CredentialID); mErr != nil {
			_ = w.unexpected(ctx, "sheet.Rows: mark reauth needed", mErr,
				"sheet_id", sh.ID, "credential_id", src.CredentialID)
		}
	}
	if !isGoogleFailure(err) || !found {
		return Rows{}, err
	}

	stale, sErr := w.fromSnapshot(ctx, sh, snap, true)
	if sErr != nil {
		return Rows{}, err
	}
	w.log.WarnContext(ctx, "sheet: serving a stale snapshot", "sheet_id", sh.ID, "tab", tab, "err", err)
	span.SetAttributes(attribute.Bool("sheet.stale", true))
	return stale, nil
}

func (w *ReadWorkflow) client(ctx context.Context, sh *Sheet, src Source) (*gsheets.Client, error) {
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

// NOTE: singleflight.Do runs the loader on the calling goroutine, so no goroutine is started here and there is nothing to shut down.
func (w *ReadWorkflow) fetch(ctx context.Context, client *gsheets.Client, sh *Sheet, src Source, key SnapshotKey) (Rows, error) {
	v, err, shared := w.flight.Do(key.SheetID.String()+"\x00"+key.Tab, func() (any, error) {
		return w.load(ctx, client, sh, src, key)
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

func (w *ReadWorkflow) load(ctx context.Context, client *gsheets.Client, sh *Sheet, src Source, key SnapshotKey) (Rows, error) {
	values, warnings, err := client.Rows(ctx, src.GoogleFileID, key.Tab)
	if err != nil {
		return Rows{}, err
	}
	if values == nil {
		values = []gsheets.Row{}
	}
	fetchedAt := time.Now().UTC()

	payload, err := json.Marshal(values)
	if err != nil {
		return Rows{}, w.unexpected(ctx, "sheet.Rows: serialize", err, "sheet_id", sh.ID, "tab", key.Tab)
	}
	if size := int64(len(payload)); w.maxPayloadBytes > 0 && size > w.maxPayloadBytes {
		return Rows{}, &PayloadTooLargeError{Bytes: size, MaxBytes: w.maxPayloadBytes}
	}

	etag := etagOf(payload)
	snap := Snapshot{ETag: etag, FetchedAt: fetchedAt, Payload: payload}
	if pErr := w.snaps.Put(ctx, key, snap, w.ttl(sh)); pErr != nil {
		_ = w.unexpected(ctx, "sheet.Rows: snapshot put", pErr, "sheet_id", sh.ID, "tab", key.Tab)
	}
	return Rows{Values: values, Warnings: warnings, ETag: etag, FetchedAt: fetchedAt}, nil
}

func (w *ReadWorkflow) fromSnapshot(ctx context.Context, sh *Sheet, snap Snapshot, stale bool) (Rows, error) {
	var values []gsheets.Row
	if err := json.Unmarshal(snap.Payload, &values); err != nil {
		return Rows{}, w.unexpected(ctx, "sheet.Rows: decode snapshot", err,
			"sheet_id", sh.ID, "etag", snap.ETag)
	}
	if values == nil {
		values = []gsheets.Row{}
	}
	return Rows{
		Values:    values,
		ETag:      snap.ETag,
		FetchedAt: snap.FetchedAt,
		Cached:    true,
		Stale:     stale,
	}, nil
}

func (w *ReadWorkflow) ttl(sh *Sheet) time.Duration {
	if sh.CacheTTL != DefaultCacheTTL {
		return sh.CacheTTL
	}
	return w.defaultTTL
}

// NOTE: a gsheets or credential failure already carries a wire code and is an expected outcome the surfaces map, so it passes through instead of being reported as an incident.
func (w *ReadWorkflow) passthrough(ctx context.Context, situation string, err error, sh *Sheet) error {
	if _, ok := apperror.AsAppError(err); ok {
		return err
	}
	return w.unexpected(ctx, situation, err, "sheet_id", sh.ID, "spreadsheet_id", sh.SpreadsheetID)
}

func etagOf(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])[:etagHexLen]
}

func isGoogleFailure(err error) bool {
	return gsheets.IsNotFoundError(err) ||
		gsheets.IsPermissionDeniedError(err) ||
		gsheets.IsQuotaExceededError(err) ||
		gsheets.IsUnavailableError(err) ||
		gsheets.IsAuthExpiredError(err) ||
		gsheets.IsTabNotFoundError(err)
}

func cloneRows(in []gsheets.Row) []gsheets.Row {
	out := make([]gsheets.Row, len(in))
	for i, row := range in {
		out[i] = maps.Clone(row)
	}
	return out
}
