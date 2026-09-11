// Package sheet is the published-tab bounded context: a sheet is one tab of a registered spreadsheet, published under a per-project slug.
package sheet

import (
	"regexp"
	"time"

	"github.com/google/uuid"
)

// Visibility decides who may fetch a sheet's rows.
type Visibility string

// The two visibilities a sheet may carry.
const (
	VisibilityKey    Visibility = "key"
	VisibilityPublic Visibility = "public"
)

const (
	// DefaultCacheTTL is the zero TTL, meaning "use the configured cache default".
	DefaultCacheTTL time.Duration = 0
	// MinCacheTTL is the smallest explicit per-sheet cache TTL.
	MinCacheTTL = time.Second
	// MaxCacheTTL is the largest explicit per-sheet cache TTL.
	MaxCacheTTL = 24 * time.Hour
	// SlugMaxLen is the longest permitted slug.
	SlugMaxLen = 64
)

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// Sheet is the aggregate root. Invariants live here.
type Sheet struct {
	ID            uuid.UUID
	OrgID         uuid.UUID
	ProjectID     uuid.UUID
	SpreadsheetID uuid.UUID
	Tab           string
	Slug          string
	Visibility    Visibility
	CacheTTL      time.Duration
	Writable      bool
	CreatedAt     time.Time
	UpdatedAt     time.Time

	Generation     int64
	ValidatedAt    *time.Time
	ContractOK     bool
	ContractReason string
	SoftDelete     bool
	ContentDigest  string
}

// NewParams is the input to New.
type NewParams struct {
	OrgID         uuid.UUID
	ProjectID     uuid.UUID
	SpreadsheetID uuid.UUID
	Tab           string
	Slug          string
	Visibility    Visibility
	CacheTTL      time.Duration
	Writable      bool
}

// CreateRequest is the input to Service.Create.
type CreateRequest struct {
	SpreadsheetID uuid.UUID
	Tab           string
	Slug          string
	Visibility    Visibility
	CacheTTL      time.Duration
	Writable      bool
}

// UpdateInput carries the mutable fields of a sheet; a nil field is left alone.
type UpdateInput struct {
	Tab        *string
	Visibility *Visibility
	CacheTTL   *time.Duration
	Writable   *bool
}

// Valid reports whether v is one of the two defined visibilities.
func (v Visibility) Valid() bool { return v == VisibilityKey || v == VisibilityPublic }

// New enforces creation invariants: slug format and reservation, visibility, cache TTL bounds.
func New(p NewParams) (*Sheet, error) {
	if err := validateSlug(p.Slug); err != nil {
		return nil, err
	}
	if err := validateVisibility(p.Visibility); err != nil {
		return nil, err
	}
	if err := validateTTL(p.CacheTTL); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	return &Sheet{
		ID:            uuid.Must(uuid.NewV7()),
		OrgID:         p.OrgID,
		ProjectID:     p.ProjectID,
		SpreadsheetID: p.SpreadsheetID,
		Tab:           p.Tab,
		Slug:          p.Slug,
		Visibility:    p.Visibility,
		CacheTTL:      p.CacheTTL,
		Writable:      p.Writable,
		CreatedAt:     now,
		UpdatedAt:     now,
		ContractOK:    true,
	}, nil
}

// Publish switches the sheet's visibility.
func (s *Sheet) Publish(vis Visibility) error {
	if err := validateVisibility(vis); err != nil {
		return err
	}
	s.Visibility = vis
	s.UpdatedAt = time.Now().UTC()
	return nil
}

// Retab repoints the sheet at another tab; empty means the spreadsheet's first tab, resolved at read time.
func (s *Sheet) Retab(tab string) {
	s.Tab = tab
	s.UpdatedAt = time.Now().UTC()
}

// SetTTL replaces the per-sheet cache TTL; zero means the configured cache default.
func (s *Sheet) SetTTL(ttl time.Duration) error {
	if err := validateTTL(ttl); err != nil {
		return err
	}
	s.CacheTTL = ttl
	s.UpdatedAt = time.Now().UTC()
	return nil
}

// SetWritable decides whether the data plane may mutate this sheet's rows.
func (s *Sheet) SetWritable(writable bool) {
	s.Writable = writable
	s.UpdatedAt = time.Now().UTC()
}

func validateSlug(slug string) error {
	if reservedSlug(slug) {
		return &InvalidSlugError{Slug: slug, Reason: "reserved"}
	}
	if slug == "" {
		return &InvalidSlugError{Slug: slug, Reason: "empty"}
	}
	if len(slug) > SlugMaxLen {
		return &InvalidSlugError{Slug: slug, Reason: "over 64 characters"}
	}
	if !slugRe.MatchString(slug) {
		return &InvalidSlugError{Slug: slug, Reason: "must start alphanumeric and hold only lowercase letters, digits and dashes"}
	}
	return nil
}

// NOTE: each of these would collide with a fixed segment of the data-plane route shape.
func reservedSlug(slug string) bool {
	switch slug {
	case "cache", "rows", "tabs", "capabilities", "api", "health", "healthz", "readyz", "static", "robots.txt":
		return true
	}
	return false
}

func validateVisibility(vis Visibility) error {
	if !vis.Valid() {
		return &InvalidVisibilityError{Value: string(vis)}
	}
	return nil
}

func validateTTL(ttl time.Duration) error {
	if ttl == DefaultCacheTTL {
		return nil
	}
	if ttl < MinCacheTTL {
		return &InvalidTTLError{TTL: ttl, Reason: "below 1s"}
	}
	if ttl > MaxCacheTTL {
		return &InvalidTTLError{TTL: ttl, Reason: "above 24h"}
	}
	if ttl%time.Second != 0 {
		return &InvalidTTLError{TTL: ttl, Reason: "not a whole number of seconds"}
	}
	return nil
}

// NOTE: both schemas store whole seconds (cache_ttl_secs); validateTTL rejects sub-second precision so this cannot truncate.
func secsFromTTL(ttl time.Duration) int64 { return int64(ttl / time.Second) }

func ttlFromSecs(secs int64) time.Duration { return time.Duration(secs) * time.Second }
