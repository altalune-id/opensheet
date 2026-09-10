package sheet

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/apperror"
)

func TestNew_Slug(t *testing.T) {
	tests := []struct {
		name string
		slug string
		ok   bool
	}{
		{name: "accepts lowercase word", slug: "prices", ok: true},
		{name: "accepts digits", slug: "2026", ok: true},
		{name: "accepts interior dashes", slug: "q1-prices-2026", ok: true},
		{name: "accepts trailing dash", slug: "prices-", ok: true},
		{name: "accepts single character", slug: "a", ok: true},
		{name: "accepts 64 characters", slug: strings.Repeat("a", 64), ok: true},
		{name: "rejects uppercase", slug: "Prices"},
		{name: "rejects leading dash", slug: "-prices"},
		{name: "rejects underscore", slug: "price_list"},
		{name: "rejects dot", slug: "prices.csv"},
		{name: "rejects slash", slug: "prices/rows"},
		{name: "rejects space", slug: "price list"},
		{name: "rejects leading space", slug: " prices"},
		{name: "rejects trailing space", slug: "prices "},
		{name: "rejects non-ascii", slug: "harga-rupiah-€"},
		{name: "rejects empty", slug: ""},
		{name: "rejects 65 characters", slug: strings.Repeat("a", 65)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := New(NewParams{OrgID: uuid.New(), ProjectID: uuid.New(), SpreadsheetID: uuid.New(), Tab: "Sheet1", Slug: tc.slug, Visibility: VisibilityKey, CacheTTL: 0})
			if tc.ok {
				if err != nil {
					t.Fatalf("New(%q) err = %v, want nil", tc.slug, err)
				}
				if got.Slug != tc.slug {
					t.Errorf("Slug = %q, want %q", got.Slug, tc.slug)
				}
				return
			}
			if !IsInvalidSlugError(err) {
				t.Fatalf("New(%q) err = %v (%T), want *InvalidSlugError", tc.slug, err, err)
			}
		})
	}
}

func TestNew_RejectsReservedSlugs(t *testing.T) {
	for _, slug := range []string{"cache", "rows", "tabs", "capabilities", "api", "health", "healthz", "readyz", "static", "robots.txt"} {
		t.Run(slug, func(t *testing.T) {
			_, err := New(NewParams{OrgID: uuid.New(), ProjectID: uuid.New(), SpreadsheetID: uuid.New(), Tab: "", Slug: slug, Visibility: VisibilityKey, CacheTTL: 0})
			if !IsInvalidSlugError(err) {
				t.Fatalf("New(%q) err = %v, want *InvalidSlugError", slug, err)
			}
			var target *InvalidSlugError
			if !errors.As(err, &target) || target.Reason != "reserved" {
				t.Fatalf("err = %v, want reason %q", err, "reserved")
			}
		})
	}
}

func TestNew_TabMayBeEmpty(t *testing.T) {
	got, err := New(NewParams{OrgID: uuid.New(), ProjectID: uuid.New(), SpreadsheetID: uuid.New(), Tab: "", Slug: "prices", Visibility: VisibilityKey, CacheTTL: 0})
	if err != nil {
		t.Fatalf("New with empty tab err = %v, want nil (empty means the spreadsheet's first tab)", err)
	}
	if got.Tab != "" {
		t.Errorf("Tab = %q, want empty", got.Tab)
	}
}

func TestNew_TabIsCarriedVerbatim(t *testing.T) {
	got, err := New(NewParams{OrgID: uuid.New(), ProjectID: uuid.New(), SpreadsheetID: uuid.New(), Tab: "Harga Kamar 2026", Slug: "prices", Visibility: VisibilityKey, CacheTTL: 0})
	if err != nil {
		t.Fatalf("New err = %v", err)
	}
	if got.Tab != "Harga Kamar 2026" {
		t.Errorf("Tab = %q, want %q", got.Tab, "Harga Kamar 2026")
	}
}

func TestNew_Visibility(t *testing.T) {
	tests := []struct {
		name string
		vis  Visibility
		ok   bool
	}{
		{name: "accepts key", vis: VisibilityKey, ok: true},
		{name: "accepts public", vis: VisibilityPublic, ok: true},
		{name: "rejects empty", vis: ""},
		{name: "rejects unknown", vis: "secret"},
		{name: "rejects wrong case", vis: "Key"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := New(NewParams{OrgID: uuid.New(), ProjectID: uuid.New(), SpreadsheetID: uuid.New(), Tab: "", Slug: "prices", Visibility: tc.vis, CacheTTL: 0})
			if tc.ok {
				if err != nil {
					t.Fatalf("New(%q) err = %v, want nil", tc.vis, err)
				}
				if got.Visibility != tc.vis {
					t.Errorf("Visibility = %q, want %q", got.Visibility, tc.vis)
				}
				return
			}
			if !IsInvalidVisibilityError(err) {
				t.Fatalf("New(%q) err = %v (%T), want *InvalidVisibilityError", tc.vis, err, err)
			}
		})
	}
}

func TestVisibility_Valid(t *testing.T) {
	for _, tc := range []struct {
		vis  Visibility
		want bool
	}{
		{vis: VisibilityKey, want: true},
		{vis: VisibilityPublic, want: true},
		{vis: "", want: false},
		{vis: "other", want: false},
	} {
		if got := tc.vis.Valid(); got != tc.want {
			t.Errorf("Visibility(%q).Valid() = %v, want %v", tc.vis, got, tc.want)
		}
	}
}

func TestNew_CacheTTL(t *testing.T) {
	tests := []struct {
		name string
		ttl  time.Duration
		ok   bool
	}{
		{name: "accepts zero meaning the configured default", ttl: 0, ok: true},
		{name: "accepts 1s lower bound", ttl: time.Second, ok: true},
		{name: "accepts 90s", ttl: 90 * time.Second, ok: true},
		{name: "accepts 24h upper bound", ttl: 24 * time.Hour, ok: true},
		{name: "rejects 999ms", ttl: 999 * time.Millisecond},
		{name: "rejects 25h", ttl: 25 * time.Hour},
		{name: "rejects negative", ttl: -time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := New(NewParams{OrgID: uuid.New(), ProjectID: uuid.New(), SpreadsheetID: uuid.New(), Tab: "", Slug: "prices", Visibility: VisibilityKey, CacheTTL: tc.ttl})
			if tc.ok {
				if err != nil {
					t.Fatalf("New(ttl=%v) err = %v, want nil", tc.ttl, err)
				}
				if got.CacheTTL != tc.ttl {
					t.Errorf("CacheTTL = %v, want %v", got.CacheTTL, tc.ttl)
				}
				return
			}
			if !IsInvalidTTLError(err) {
				t.Fatalf("New(ttl=%v) err = %v (%T), want *InvalidTTLError", tc.ttl, err, err)
			}
		})
	}
}

func TestNew_StampsIdentityAndTimestamps(t *testing.T) {
	orgID, projectID, spreadsheetID := uuid.New(), uuid.New(), uuid.New()

	got, err := New(NewParams{OrgID: orgID, ProjectID: projectID, SpreadsheetID: spreadsheetID, Tab: "Sheet1", Slug: "prices", Visibility: VisibilityPublic, CacheTTL: time.Minute})
	if err != nil {
		t.Fatalf("New err = %v", err)
	}
	if got.ID == uuid.Nil {
		t.Error("ID unset")
	}
	if got.OrgID != orgID || got.ProjectID != projectID || got.SpreadsheetID != spreadsheetID {
		t.Errorf("ids not carried through: %+v", got)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Error("timestamps unset")
	}
	if !got.CreatedAt.Equal(got.UpdatedAt) {
		t.Errorf("UpdatedAt = %v, want equal to CreatedAt %v", got.UpdatedAt, got.CreatedAt)
	}
	if got.CreatedAt.Location() != time.UTC {
		t.Errorf("CreatedAt location = %v, want UTC", got.CreatedAt.Location())
	}
}

func TestPublish(t *testing.T) {
	t.Run("switches visibility and bumps UpdatedAt", func(t *testing.T) {
		sh := mustSheet(t, "prices", VisibilityKey, 0)
		before := sh.UpdatedAt

		if err := sh.Publish(VisibilityPublic); err != nil {
			t.Fatalf("Publish err = %v", err)
		}
		if sh.Visibility != VisibilityPublic {
			t.Errorf("Visibility = %q, want %q", sh.Visibility, VisibilityPublic)
		}
		if sh.UpdatedAt.Before(before) {
			t.Error("UpdatedAt went backwards")
		}
	})
	t.Run("enforces the same invariant as New", func(t *testing.T) {
		sh := mustSheet(t, "prices", VisibilityKey, 0)
		err := sh.Publish("everyone")
		if !IsInvalidVisibilityError(err) {
			t.Fatalf("Publish err = %v, want *InvalidVisibilityError", err)
		}
		if sh.Visibility != VisibilityKey {
			t.Errorf("Visibility = %q, a rejected Publish must not mutate", sh.Visibility)
		}
	})
}

func TestRetab(t *testing.T) {
	t.Run("repoints the tab", func(t *testing.T) {
		sh := mustSheet(t, "prices", VisibilityKey, 0)
		before := sh.UpdatedAt

		sh.Retab("Kamar")

		if sh.Tab != "Kamar" {
			t.Errorf("Tab = %q, want %q", sh.Tab, "Kamar")
		}
		if sh.UpdatedAt.Before(before) {
			t.Error("UpdatedAt went backwards")
		}
	})
	t.Run("accepts empty meaning the spreadsheet's first tab", func(t *testing.T) {
		sh := mustSheet(t, "prices", VisibilityKey, 0)
		sh.Retab("Kamar")

		sh.Retab("")

		if sh.Tab != "" {
			t.Errorf("Tab = %q, want empty", sh.Tab)
		}
	})
}

func TestSetTTL(t *testing.T) {
	tests := []struct {
		name string
		ttl  time.Duration
		ok   bool
	}{
		{name: "accepts zero", ttl: 0, ok: true},
		{name: "accepts 1s", ttl: time.Second, ok: true},
		{name: "accepts 24h", ttl: 24 * time.Hour, ok: true},
		{name: "rejects 999ms", ttl: 999 * time.Millisecond},
		{name: "rejects 25h", ttl: 25 * time.Hour},
		{name: "rejects negative", ttl: -time.Hour},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sh := mustSheet(t, "prices", VisibilityKey, time.Minute)
			err := sh.SetTTL(tc.ttl)
			if tc.ok {
				if err != nil {
					t.Fatalf("SetTTL(%v) err = %v, want nil", tc.ttl, err)
				}
				if sh.CacheTTL != tc.ttl {
					t.Errorf("CacheTTL = %v, want %v", sh.CacheTTL, tc.ttl)
				}
				return
			}
			if !IsInvalidTTLError(err) {
				t.Fatalf("SetTTL(%v) err = %v, want *InvalidTTLError", tc.ttl, err)
			}
			if sh.CacheTTL != time.Minute {
				t.Errorf("CacheTTL = %v, a rejected SetTTL must not mutate", sh.CacheTTL)
			}
		})
	}
}

func TestNotFoundError(t *testing.T) {
	t.Run("by id", func(t *testing.T) {
		e := &NotFoundError{ID: "sheet-1"}
		if !strings.Contains(e.Error(), "sheet-1") {
			t.Errorf("Error() = %q, want it to carry the id", e.Error())
		}
		assertAppError(t, e.ToAppError(), "SHT001")
	})
	t.Run("by slug", func(t *testing.T) {
		e := &NotFoundError{ProjectID: "proj-1", Slug: "prices"}
		msg := e.Error()
		if !strings.Contains(msg, "prices") || !strings.Contains(msg, "proj-1") {
			t.Errorf("Error() = %q, want it to carry project and slug", msg)
		}
		assertAppError(t, e.ToAppError(), "SHT001")
	})
	t.Run("every identifier", func(t *testing.T) {
		e := &NotFoundError{ID: "sheet-1", OrgID: "org-1", ProjectID: "proj-1", Slug: "prices"}
		assertAppError(t, e.ToAppError(), "SHT001")
	})
	t.Run("bare", func(t *testing.T) {
		e := &NotFoundError{}
		if !strings.HasPrefix(e.Error(), "sheet: ") {
			t.Errorf("Error() = %q, want the %q prefix", e.Error(), "sheet: ")
		}
		assertAppError(t, e.ToAppError(), "SHT001")
	})
}

func TestAlreadyExistsError(t *testing.T) {
	e := &AlreadyExistsError{Field: "slug", Value: "prices"}
	if !strings.Contains(e.Error(), "prices") {
		t.Errorf("Error() = %q, want it to carry the value", e.Error())
	}
	assertAppError(t, e.ToAppError(), "SHT002")

	bare := &AlreadyExistsError{}
	if !strings.HasPrefix(bare.Error(), "sheet: ") {
		t.Errorf("Error() = %q, want the %q prefix", bare.Error(), "sheet: ")
	}
	assertAppError(t, bare.ToAppError(), "SHT002")
}

func TestInvalidSlugError(t *testing.T) {
	e := &InvalidSlugError{Slug: "Prices", Reason: "reserved"}
	if !strings.Contains(e.Error(), "reserved") {
		t.Errorf("Error() = %q, want it to carry the reason", e.Error())
	}
	assertAppError(t, e.ToAppError(), "SHT003")

	bare := &InvalidSlugError{}
	if !strings.HasPrefix(bare.Error(), "sheet: slug: ") {
		t.Errorf("Error() = %q, want the %q prefix", bare.Error(), "sheet: slug: ")
	}
	assertAppError(t, bare.ToAppError(), "SHT003")
}

func TestInvalidVisibilityError(t *testing.T) {
	e := &InvalidVisibilityError{Value: "secret"}
	if !strings.Contains(e.Error(), "secret") {
		t.Errorf("Error() = %q, want it to carry the value", e.Error())
	}
	assertAppError(t, e.ToAppError(), "SHT004")
}

func TestInvalidTTLError(t *testing.T) {
	e := &InvalidTTLError{TTL: 25 * time.Hour, Reason: "above 24h"}
	if !strings.Contains(e.Error(), "above 24h") {
		t.Errorf("Error() = %q, want it to carry the reason", e.Error())
	}
	assertAppError(t, e.ToAppError(), "SHT005")

	bare := &InvalidTTLError{}
	if !strings.HasPrefix(bare.Error(), "sheet: cache ttl: ") {
		t.Errorf("Error() = %q, want the %q prefix", bare.Error(), "sheet: cache ttl: ")
	}
	assertAppError(t, bare.ToAppError(), "SHT005")
}

func TestPublicDisabledError(t *testing.T) {
	e := &PublicDisabledError{Slug: "prices"}
	if !strings.HasPrefix(e.Error(), "sheet: ") {
		t.Errorf("Error() = %q, want the %q prefix", e.Error(), "sheet: ")
	}
	assertAppError(t, e.ToAppError(), "SHT006")

	bare := &PublicDisabledError{}
	if !strings.HasPrefix(bare.Error(), "sheet: ") {
		t.Errorf("Error() = %q, want the %q prefix", bare.Error(), "sheet: ")
	}
	assertAppError(t, bare.ToAppError(), "SHT006")

	if !IsPublicDisabledError(e) {
		t.Error("IsPublicDisabledError = false, want true")
	}
	if IsPublicDisabledError(&NotFoundError{}) {
		t.Error("IsPublicDisabledError matched the wrong type")
	}
}

func TestIsPredicates_RejectForeignErrors(t *testing.T) {
	foreign := &AlreadyExistsError{}
	for name, pred := range map[string]func(error) bool{
		"IsNotFoundError":          IsNotFoundError,
		"IsInvalidSlugError":       IsInvalidSlugError,
		"IsInvalidVisibilityError": IsInvalidVisibilityError,
		"IsInvalidTTLError":        IsInvalidTTLError,
		"IsPublicDisabledError":    IsPublicDisabledError,
	} {
		if pred(foreign) {
			t.Errorf("%s matched an *AlreadyExistsError", name)
		}
		if pred(nil) {
			t.Errorf("%s matched nil", name)
		}
	}
	if !IsAlreadyExistsError(foreign) {
		t.Error("IsAlreadyExistsError = false, want true")
	}
}

func mustSheet(t *testing.T, slug string, vis Visibility, ttl time.Duration) *Sheet {
	t.Helper()
	sh, err := New(NewParams{OrgID: uuid.New(), ProjectID: uuid.New(), SpreadsheetID: uuid.New(), Tab: "Sheet1", Slug: slug, Visibility: vis, CacheTTL: ttl})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return sh
}

func assertAppError(t *testing.T, ae *apperror.AppError, wantCode string) {
	t.Helper()
	if ae == nil {
		t.Fatal("ToAppError returned nil")
	}
	if ae.Code() != wantCode {
		t.Errorf("AppError code = %q, want %q", ae.Code(), wantCode)
	}
	if ae.Error() == "" {
		t.Error("AppError message empty")
	}
}

// The store column is cache_ttl_secs, so a value the aggregate accepts must be
// representable in whole seconds — otherwise it silently truncates on save.
func TestNew_RejectsSubSecondTTL(t *testing.T) {
	for _, ttl := range []time.Duration{1500 * time.Millisecond, 2*time.Second + time.Nanosecond, 90*time.Second + 500*time.Millisecond} {
		_, err := New(NewParams{OrgID: uuid.Must(uuid.NewV7()), ProjectID: uuid.Must(uuid.NewV7()), SpreadsheetID: uuid.Must(uuid.NewV7()), Tab: "Q1", Slug: "prices", Visibility: VisibilityKey, CacheTTL: ttl})
		if !IsInvalidTTLError(err) {
			t.Errorf("New(ttl=%v) error = %v, want InvalidTTLError", ttl, err)
		}
	}
	for _, ttl := range []time.Duration{DefaultCacheTTL, time.Second, 90 * time.Second, MaxCacheTTL} {
		if _, err := New(NewParams{OrgID: uuid.Must(uuid.NewV7()), ProjectID: uuid.Must(uuid.NewV7()), SpreadsheetID: uuid.Must(uuid.NewV7()), Tab: "Q1", Slug: "prices", Visibility: VisibilityKey, CacheTTL: ttl}); err != nil {
			t.Errorf("New(ttl=%v) = %v, want nil", ttl, err)
		}
	}
}

func TestNew_DefaultsToNotWritable(t *testing.T) {
	got, err := New(NewParams{
		OrgID:         uuid.Must(uuid.NewV7()),
		ProjectID:     uuid.Must(uuid.NewV7()),
		SpreadsheetID: uuid.Must(uuid.NewV7()),
		Tab:           "Rates",
		Slug:          "rates",
		Visibility:    VisibilityKey,
		CacheTTL:      time.Minute,
	})
	if err != nil {
		t.Fatalf("New err = %v", err)
	}
	if got.Writable {
		t.Error("Writable = true, want false: a sheet must not be writable unless asked for")
	}
}

func TestNew_CarriesWritable(t *testing.T) {
	got, err := New(NewParams{
		OrgID:         uuid.Must(uuid.NewV7()),
		ProjectID:     uuid.Must(uuid.NewV7()),
		SpreadsheetID: uuid.Must(uuid.NewV7()),
		Slug:          "rates",
		Visibility:    VisibilityKey,
		Writable:      true,
	})
	if err != nil {
		t.Fatalf("New err = %v", err)
	}
	if !got.Writable {
		t.Error("Writable = false, want true")
	}
}

func TestSetWritable_TogglesAndBumpsUpdatedAt(t *testing.T) {
	sh := mustSheet(t, "prices", VisibilityKey, 0)
	before := sh.UpdatedAt

	sh.SetWritable(true)
	if !sh.Writable {
		t.Error("Writable = false, want true")
	}
	if !sh.UpdatedAt.After(before) && !sh.UpdatedAt.Equal(before) {
		t.Errorf("UpdatedAt = %v, want at or after %v", sh.UpdatedAt, before)
	}
	sh.SetWritable(false)
	if sh.Writable {
		t.Error("Writable = true, want false")
	}
}
