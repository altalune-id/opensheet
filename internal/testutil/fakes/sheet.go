package fakes

import (
	"context"
	"maps"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/oauth2"

	"altalune.id/opensheet/internal/sheet"
)

// Sheet is an in-memory sheet.Store.
type Sheet struct {
	mu   sync.Mutex
	data map[uuid.UUID]*sheet.Sheet

	SaveFn   func(ctx context.Context, s *sheet.Sheet) error
	ByIDFn   func(ctx context.Context, id uuid.UUID) (*sheet.Sheet, error)
	BySlugFn func(ctx context.Context, orgID, projectID uuid.UUID, slug string) (*sheet.Sheet, error)
	ListFn   func(ctx context.Context, orgID, projectID uuid.UUID) ([]*sheet.Sheet, error)
	DeleteFn func(ctx context.Context, id uuid.UUID) error
}

// NewSheet returns an empty in-memory sheet.Store.
func NewSheet() *Sheet { return &Sheet{data: map[uuid.UUID]*sheet.Sheet{}} }

var _ sheet.Store = (*Sheet)(nil)

func (f *Sheet) Save(ctx context.Context, s *sheet.Sheet) error {
	if f.SaveFn != nil {
		return f.SaveFn(ctx, s)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, existing := range f.data {
		if id != s.ID && existing.ProjectID == s.ProjectID && existing.Slug == s.Slug {
			return &sheet.AlreadyExistsError{Field: "slug", Value: s.Slug}
		}
	}
	cp := *s
	f.data[s.ID] = &cp
	return nil
}

func (f *Sheet) ByID(ctx context.Context, id uuid.UUID) (*sheet.Sheet, error) {
	if f.ByIDFn != nil {
		return f.ByIDFn(ctx, id)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.data[id]
	if !ok {
		return nil, &sheet.NotFoundError{ID: id.String()}
	}
	cp := *s
	return &cp, nil
}

func (f *Sheet) BySlug(ctx context.Context, orgID, projectID uuid.UUID, slug string) (*sheet.Sheet, error) {
	if f.BySlugFn != nil {
		return f.BySlugFn(ctx, orgID, projectID, slug)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.data {
		if s.OrgID == orgID && s.ProjectID == projectID && s.Slug == slug {
			cp := *s
			return &cp, nil
		}
	}
	return nil, &sheet.NotFoundError{OrgID: orgID.String(), ProjectID: projectID.String(), Slug: slug}
}

func (f *Sheet) List(ctx context.Context, orgID, projectID uuid.UUID) ([]*sheet.Sheet, error) {
	if f.ListFn != nil {
		return f.ListFn(ctx, orgID, projectID)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*sheet.Sheet, 0, len(f.data))
	for _, s := range f.data {
		if s.OrgID != orgID || s.ProjectID != projectID {
			continue
		}
		cp := *s
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

func (f *Sheet) Delete(ctx context.Context, id uuid.UUID) error {
	if f.DeleteFn != nil {
		return f.DeleteFn(ctx, id)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.data[id]; !ok {
		return &sheet.NotFoundError{ID: id.String()}
	}
	delete(f.data, id)
	return nil
}

// SheetSnapshots is an in-memory sheet.SnapshotStore.
type SheetSnapshots struct {
	mu   sync.Mutex
	data map[sheet.SnapshotKey]sheet.Snapshot
	puts int

	GetErr   error
	PutErr   error
	PurgeErr error
}

// NewSheetSnapshots returns an empty in-memory sheet.SnapshotStore.
func NewSheetSnapshots() *SheetSnapshots {
	return &SheetSnapshots{data: map[sheet.SnapshotKey]sheet.Snapshot{}}
}

var _ sheet.SnapshotStore = (*SheetSnapshots)(nil)

// Seed stores s verbatim under k, honouring the ExpiresAt given rather than deriving one.
func (f *SheetSnapshots) Seed(k sheet.SnapshotKey, s sheet.Snapshot) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data[k] = s
}

// PutCount reports how many Put calls were accepted.
func (f *SheetSnapshots) PutCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.puts
}

func (f *SheetSnapshots) Get(_ context.Context, k sheet.SnapshotKey) (sheet.Snapshot, bool, error) {
	if f.GetErr != nil {
		return sheet.Snapshot{}, false, f.GetErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.data[k]
	if !ok {
		return sheet.Snapshot{}, false, nil
	}
	return s, true, nil
}

func (f *SheetSnapshots) Put(_ context.Context, k sheet.SnapshotKey, s sheet.Snapshot, ttl time.Duration) error {
	if f.PutErr != nil {
		return f.PutErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	s.ExpiresAt = s.FetchedAt.Add(ttl)
	f.data[k] = s
	f.puts++
	return nil
}

func (f *SheetSnapshots) PurgeSheet(_ context.Context, sheetID uuid.UUID) error {
	if f.PurgeErr != nil {
		return f.PurgeErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for k := range f.data {
		if k.SheetID == sheetID {
			delete(f.data, k)
		}
	}
	return nil
}

// SheetSources is an in-memory sheet.Sources.
type SheetSources struct {
	mu    sync.Mutex
	data  map[uuid.UUID]sheet.Source
	asked []uuid.UUID

	Err error
}

// NewSheetSources returns an empty in-memory sheet.Sources.
func NewSheetSources() *SheetSources {
	return &SheetSources{data: map[uuid.UUID]sheet.Source{}}
}

var _ sheet.Sources = (*SheetSources)(nil)

// Set binds a spreadsheet id to the Google document and credential behind it.
func (f *SheetSources) Set(spreadsheetID uuid.UUID, src sheet.Source) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data[spreadsheetID] = src
}

// AskedIDs reports the spreadsheet ids SourceFor was called with, in order.
func (f *SheetSources) AskedIDs() []uuid.UUID {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]uuid.UUID(nil), f.asked...)
}

func (f *SheetSources) SourceFor(_ context.Context, spreadsheetID uuid.UUID) (sheet.Source, error) {
	if f.Err != nil {
		return sheet.Source{}, f.Err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, spreadsheetID)
	src, ok := f.data[spreadsheetID]
	if !ok {
		return sheet.Source{}, &sheet.NotFoundError{ID: spreadsheetID.String()}
	}
	return src, nil
}

// SheetTokenSources is an in-memory sheet.TokenSources handing out a static bearer token.
type SheetTokenSources struct {
	mu    sync.Mutex
	asked []uuid.UUID

	Source oauth2.TokenSource
	Err    error
}

// NewSheetTokenSources returns a sheet.TokenSources whose token never expires.
func NewSheetTokenSources() *SheetTokenSources {
	return &SheetTokenSources{Source: staticTokenSource{}}
}

var _ sheet.TokenSources = (*SheetTokenSources)(nil)

// AskedIDs reports the credential ids TokenSourceFor was called with, in order.
func (f *SheetTokenSources) AskedIDs() []uuid.UUID {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]uuid.UUID(nil), f.asked...)
}

func (f *SheetTokenSources) TokenSourceFor(_ context.Context, credentialID uuid.UUID) (oauth2.TokenSource, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, credentialID)
	return f.Source, nil
}

type staticTokenSource struct{}

func (staticTokenSource) Token() (*oauth2.Token, error) {
	return &oauth2.Token{
		AccessToken: "fake-access-token",
		TokenType:   "Bearer",
		Expiry:      time.Now().Add(time.Hour),
	}, nil
}

// SheetReauthers is an in-memory sheet.Reauthers recording every credential it was asked to flag.
type SheetReauthers struct {
	mu     sync.Mutex
	marked []uuid.UUID

	Err error
}

// NewSheetReauthers returns an empty in-memory sheet.Reauthers.
func NewSheetReauthers() *SheetReauthers { return &SheetReauthers{} }

var _ sheet.Reauthers = (*SheetReauthers)(nil)

// MarkedIDs reports the credential ids MarkReauthNeeded was called with, in order.
func (f *SheetReauthers) MarkedIDs() []uuid.UUID {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]uuid.UUID(nil), f.marked...)
}

func (f *SheetReauthers) MarkReauthNeeded(_ context.Context, credentialID uuid.UUID) error {
	f.mu.Lock()
	f.marked = append(f.marked, credentialID)
	f.mu.Unlock()
	return f.Err
}

// SheetRows is an in-memory sheet.RowStore, generation-guarded like the drivers are.
type SheetRows struct {
	mu       sync.Mutex
	data     map[sheet.SnapshotKey][]sheet.ProjectedRow
	gens     map[uuid.UUID]int64
	states   map[uuid.UUID]sheet.ContractState
	digests  map[uuid.UUID]string
	replaces int

	ReplaceErr      error
	MarkContractErr error
	RowByIDErr      error
	StateOfErr      error
	ListLiveErr     error
	StatsErr        error
}

// NewSheetRows returns an empty in-memory sheet.RowStore.
func NewSheetRows() *SheetRows {
	return &SheetRows{
		data:    map[sheet.SnapshotKey][]sheet.ProjectedRow{},
		gens:    map[uuid.UUID]int64{},
		states:  map[uuid.UUID]sheet.ContractState{},
		digests: map[uuid.UUID]string{},
	}
}

var _ sheet.RowStore = (*SheetRows)(nil)

// Seed stores rows under k without touching the generation.
func (f *SheetRows) Seed(k sheet.SnapshotKey, rows []sheet.ProjectedRow) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data[k] = cloneProjected(rows)
}

// SeedContract stores a sheet's contract state, standing in for what a refresh persisted.
func (f *SheetRows) SeedContract(sheetID uuid.UUID, contract sheet.ContractState) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states[sheetID] = contract
}

// Bump advances a sheet's generation, standing in for a write that landed mid-fetch.
func (f *SheetRows) Bump(sheetID uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gens[sheetID]++
}

// Generation reports a sheet's current generation.
func (f *SheetRows) Generation(sheetID uuid.UUID) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gens[sheetID]
}

// Projected reports every row held under k, tombstones included.
func (f *SheetRows) Projected(k sheet.SnapshotKey) []sheet.ProjectedRow {
	f.mu.Lock()
	defer f.mu.Unlock()
	return cloneProjected(f.data[k])
}

// Contract reports the contract state last persisted for a sheet.
func (f *SheetRows) Contract(sheetID uuid.UUID) sheet.ContractState {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.states[sheetID]
}

// ReplaceCount reports how many Replace calls were applied.
func (f *SheetRows) ReplaceCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.replaces
}

func (f *SheetRows) LockSheet(_ context.Context, sheetID uuid.UUID) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gens[sheetID], nil
}

// NOTE: the generation moves only when the digest moved, exactly as both drivers do — the fake-backed read path asserts that behaviour.
func (f *SheetRows) Replace(
	_ context.Context, k sheet.SnapshotKey, gen int64, rows []sheet.ProjectedRow, contract sheet.ContractState,
) (bool, error) {
	if f.ReplaceErr != nil {
		return false, f.ReplaceErr
	}
	digest, err := sheet.RowsDigest(rows)
	if err != nil {
		return false, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.gens[k.SheetID] != gen {
		return false, nil
	}
	f.data[k] = cloneProjected(rows)
	if digest != f.digests[k.SheetID] {
		f.gens[k.SheetID]++
	}
	f.digests[k.SheetID] = digest
	f.states[k.SheetID] = contract
	f.replaces++
	return true, nil
}

func (f *SheetRows) MarkContract(
	_ context.Context, sheetID uuid.UUID, gen int64, contract sheet.ContractState,
) (bool, error) {
	if f.MarkContractErr != nil {
		return false, f.MarkContractErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.gens[sheetID] != gen {
		return false, nil
	}
	f.states[sheetID] = contract
	return true, nil
}

func (f *SheetRows) UpsertRow(_ context.Context, k sheet.SnapshotKey, row sheet.ProjectedRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	kept := f.data[k]
	for i := range kept {
		if kept[i].RowID != row.RowID {
			continue
		}
		kept[i] = cloneRow(row)
		f.wrote(k.SheetID)
		return nil
	}
	f.data[k] = append(kept, cloneRow(row))
	f.wrote(k.SheetID)
	return nil
}

// NOTE: clearing the digest mirrors both drivers — a write leaves the stored digest describing the pre-write rows, so every outstanding cursor must be refused.
func (f *SheetRows) wrote(sheetID uuid.UUID) {
	f.gens[sheetID]++
	f.digests[sheetID] = ""
}

func (f *SheetRows) RowByID(_ context.Context, k sheet.SnapshotKey, rowID string) (sheet.ProjectedRow, error) {
	if f.RowByIDErr != nil {
		return sheet.ProjectedRow{}, f.RowByIDErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, row := range f.data[k] {
		if row.RowID == rowID {
			return cloneRow(row), nil
		}
	}
	return sheet.ProjectedRow{}, &sheet.RowNotFoundError{ID: rowID}
}

func (f *SheetRows) StateOf(_ context.Context, sheetID uuid.UUID) (sheet.SheetState, error) {
	if f.StateOfErr != nil {
		return sheet.SheetState{}, f.StateOfErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return sheet.SheetState{
		Contract:   f.states[sheetID],
		Digest:     f.digests[sheetID],
		Generation: f.gens[sheetID],
	}, nil
}

func (f *SheetRows) ListLive(_ context.Context, k sheet.SnapshotKey) ([]sheet.ProjectedRow, error) {
	if f.ListLiveErr != nil {
		return nil, f.ListLiveErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sheet.ProjectedRow, 0, len(f.data[k]))
	for _, row := range f.data[k] {
		if row.DeletedAt != nil {
			continue
		}
		out = append(out, cloneRow(row))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RowIndex < out[j].RowIndex })
	return out, nil
}

// NOTE: deliberately unfiltered and unpaged — the filter and keyset semantics are covered only by internal/sheet's two driver test files, because a Go query engine here would be a third implementation to keep in sync with two SQL dialects.
func (f *SheetRows) Query(ctx context.Context, k sheet.SnapshotKey, _ sheet.RowQuery) (sheet.RowPage, error) {
	rows, err := f.ListLive(ctx, k)
	if err != nil {
		return sheet.RowPage{}, err
	}
	return sheet.RowPage{Rows: rows}, nil
}

func (f *SheetRows) Stats(_ context.Context, sheetID uuid.UUID, tab string) (sheet.TableStats, error) {
	if f.StatsErr != nil {
		return sheet.TableStats{}, f.StatsErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := sheet.TableStats{Tab: tab}
	for k, rows := range f.data {
		if k.SheetID != sheetID || (tab != "" && k.Tab != tab) {
			continue
		}
		stats := statsOf(k.Tab, rows)
		if out.Tab != "" && (stats.RowCount < out.RowCount ||
			(stats.RowCount == out.RowCount && stats.Tab > out.Tab)) {
			continue
		}
		out = stats
	}
	return out, nil
}

func statsOf(tab string, rows []sheet.ProjectedRow) sheet.TableStats {
	out := sheet.TableStats{Tab: tab}
	live := make([]sheet.ProjectedRow, 0, len(rows))
	for _, row := range rows {
		if row.DeletedAt != nil {
			continue
		}
		live = append(live, row)
	}
	out.RowCount = int64(len(live))
	sort.Slice(live, func(i, j int) bool { return live[i].RowIndex < live[j].RowIndex })
	if len(live) > 0 {
		out.Columns = slices.Sorted(maps.Keys(live[0].Data))
	}
	return out
}

func (f *SheetRows) PurgeSheet(_ context.Context, sheetID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k := range f.data {
		if k.SheetID == sheetID {
			delete(f.data, k)
		}
	}
	return nil
}

func cloneProjected(rows []sheet.ProjectedRow) []sheet.ProjectedRow {
	out := make([]sheet.ProjectedRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, cloneRow(row))
	}
	return out
}

func cloneRow(row sheet.ProjectedRow) sheet.ProjectedRow {
	row.Data = maps.Clone(row.Data)
	if row.DeletedAt != nil {
		at := *row.DeletedAt
		row.DeletedAt = &at
	}
	return row
}
