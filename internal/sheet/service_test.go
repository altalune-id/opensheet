package sheet_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/sheet"
	"altalune.id/opensheet/internal/testutil/fakes"
)

type fakeCaps struct{ public bool }

func (c fakeCaps) PublicSheetsEnabled() bool { return c.public }

func newSvc(t *testing.T, store sheet.Store, publicEnabled bool) (*sheet.Service, *int) {
	t.Helper()
	return newSvcSnaps(t, store, fakes.NewSheetSnapshots(), publicEnabled)
}

func newSvcSnaps(t *testing.T, store sheet.Store, snaps sheet.SnapshotStore, publicEnabled bool) (*sheet.Service, *int) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	calls := 0
	unexpected := func(_ context.Context, _ string, err error, _ ...any) *apperror.AppError {
		calls++
		return apperror.New("opensheet.unexpected", err.Error(), codes.Internal,
			&apperrorv1.ErrorDetail{Code: "opensheet.unexpected"}).WithCause(err)
	}
	return sheet.NewService(store, log, unexpected, fakeCaps{public: publicEnabled}, snaps), &calls
}

func tenantCtx(t *testing.T) (context.Context, tenant.Context) {
	t.Helper()
	tc := tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New(), UserID: uuid.New()}
	return tenant.Into(context.Background(), tc), tc
}

func scopedTo(orgID, projectID uuid.UUID) context.Context {
	return tenant.Into(context.Background(), tenant.Context{
		OrgID: orgID, ProjectID: projectID, UserID: uuid.New(),
	})
}

func TestService_Create(t *testing.T) {
	t.Run("happy path carries tenant scope", func(t *testing.T) {
		svc, unexCalls := newSvc(t, fakes.NewSheet(), false)
		ctx, tc := tenantCtx(t)
		spreadsheetID := uuid.New()

		got, err := svc.Create(ctx, spreadsheetID, "Kamar", "prices", sheet.VisibilityKey, 90*time.Second)
		if err != nil {
			t.Fatalf("Create err = %v", err)
		}
		if got.OrgID != tc.OrgID || got.ProjectID != tc.ProjectID {
			t.Errorf("tenant scope not carried: %+v", got)
		}
		if got.SpreadsheetID != spreadsheetID {
			t.Errorf("SpreadsheetID = %v, want %v", got.SpreadsheetID, spreadsheetID)
		}
		if got.Tab != "Kamar" || got.Slug != "prices" || got.CacheTTL != 90*time.Second {
			t.Errorf("fields not carried: %+v", got)
		}
		if *unexCalls != 0 {
			t.Errorf("unexpected() calls = %d, want 0", *unexCalls)
		}
	})
	t.Run("persists so a later lookup finds it", func(t *testing.T) {
		svc, _ := newSvc(t, fakes.NewSheet(), false)
		ctx, _ := tenantCtx(t)

		created, err := svc.Create(ctx, uuid.New(), "", "prices", sheet.VisibilityKey, 0)
		if err != nil {
			t.Fatalf("Create err = %v", err)
		}
		got, err := svc.ByID(ctx, created.ID)
		if err != nil {
			t.Fatalf("ByID err = %v", err)
		}
		if got.ID != created.ID {
			t.Errorf("ByID returned %v, want %v", got.ID, created.ID)
		}
	})
	t.Run("accepts an empty tab", func(t *testing.T) {
		svc, _ := newSvc(t, fakes.NewSheet(), false)
		ctx, _ := tenantCtx(t)

		got, err := svc.Create(ctx, uuid.New(), "", "prices", sheet.VisibilityKey, 0)
		if err != nil {
			t.Fatalf("Create with empty tab err = %v, want nil", err)
		}
		if got.Tab != "" {
			t.Errorf("Tab = %q, want empty", got.Tab)
		}
	})
	t.Run("public is allowed when the capability is on", func(t *testing.T) {
		svc, unexCalls := newSvc(t, fakes.NewSheet(), true)
		ctx, _ := tenantCtx(t)

		got, err := svc.Create(ctx, uuid.New(), "", "prices", sheet.VisibilityPublic, 0)
		if err != nil {
			t.Fatalf("Create err = %v", err)
		}
		if got.Visibility != sheet.VisibilityPublic {
			t.Errorf("Visibility = %q, want %q", got.Visibility, sheet.VisibilityPublic)
		}
		if *unexCalls != 0 {
			t.Errorf("unexpected() calls = %d, want 0", *unexCalls)
		}
	})
	t.Run("public is refused when the capability is off", func(t *testing.T) {
		svc, unexCalls := newSvc(t, fakes.NewSheet(), false)
		ctx, _ := tenantCtx(t)

		_, err := svc.Create(ctx, uuid.New(), "", "prices", sheet.VisibilityPublic, 0)
		if !sheet.IsPublicDisabledError(err) {
			t.Fatalf("err = %v (%T), want *PublicDisabledError", err, err)
		}
		if *unexCalls != 0 {
			t.Errorf("a capability refusal must not route through unexpected()")
		}
	})
	t.Run("key visibility still works when public is off", func(t *testing.T) {
		svc, _ := newSvc(t, fakes.NewSheet(), false)
		ctx, _ := tenantCtx(t)

		got, err := svc.Create(ctx, uuid.New(), "", "prices", sheet.VisibilityKey, 0)
		if err != nil {
			t.Fatalf("Create err = %v, want nil: the gate covers public only", err)
		}
		if got.Visibility != sheet.VisibilityKey {
			t.Errorf("Visibility = %q, want %q", got.Visibility, sheet.VisibilityKey)
		}
	})
	t.Run("invalid slug bubbles the typed error", func(t *testing.T) {
		svc, unexCalls := newSvc(t, fakes.NewSheet(), false)
		ctx, _ := tenantCtx(t)

		_, err := svc.Create(ctx, uuid.New(), "", "Prices", sheet.VisibilityKey, 0)
		if !sheet.IsInvalidSlugError(err) {
			t.Fatalf("err = %v, want *InvalidSlugError", err)
		}
		if *unexCalls != 0 {
			t.Errorf("an invariant failure must not route through unexpected()")
		}
	})
	t.Run("reserved slug bubbles the typed error", func(t *testing.T) {
		svc, _ := newSvc(t, fakes.NewSheet(), false)
		ctx, _ := tenantCtx(t)

		_, err := svc.Create(ctx, uuid.New(), "", "rows", sheet.VisibilityKey, 0)
		if !sheet.IsInvalidSlugError(err) {
			t.Fatalf("err = %v, want *InvalidSlugError", err)
		}
	})
	t.Run("invalid visibility bubbles the typed error", func(t *testing.T) {
		svc, _ := newSvc(t, fakes.NewSheet(), true)
		ctx, _ := tenantCtx(t)

		_, err := svc.Create(ctx, uuid.New(), "", "prices", "secret", 0)
		if !sheet.IsInvalidVisibilityError(err) {
			t.Fatalf("err = %v, want *InvalidVisibilityError", err)
		}
	})
	t.Run("invalid ttl bubbles the typed error", func(t *testing.T) {
		svc, _ := newSvc(t, fakes.NewSheet(), false)
		ctx, _ := tenantCtx(t)

		_, err := svc.Create(ctx, uuid.New(), "", "prices", sheet.VisibilityKey, 25*time.Hour)
		if !sheet.IsInvalidTTLError(err) {
			t.Fatalf("err = %v, want *InvalidTTLError", err)
		}
	})
	t.Run("a slug already taken in the project is refused", func(t *testing.T) {
		svc, unexCalls := newSvc(t, fakes.NewSheet(), false)
		ctx, _ := tenantCtx(t)
		if _, err := svc.Create(ctx, uuid.New(), "", "prices", sheet.VisibilityKey, 0); err != nil {
			t.Fatal(err)
		}

		_, err := svc.Create(ctx, uuid.New(), "", "prices", sheet.VisibilityKey, 0)
		if !sheet.IsAlreadyExistsError(err) {
			t.Fatalf("err = %v, want *AlreadyExistsError", err)
		}
		if *unexCalls != 0 {
			t.Errorf("a slug collision must not route through unexpected()")
		}
	})
	t.Run("the same slug in another project of the same org is allowed", func(t *testing.T) {
		store := fakes.NewSheet()
		svc, _ := newSvc(t, store, false)
		orgID := uuid.New()
		projectA, projectB := uuid.New(), uuid.New()

		a, err := svc.Create(scopedTo(orgID, projectA), uuid.New(), "", "prices", sheet.VisibilityKey, 0)
		if err != nil {
			t.Fatalf("Create in project A err = %v", err)
		}
		b, err := svc.Create(scopedTo(orgID, projectB), uuid.New(), "", "prices", sheet.VisibilityKey, 0)
		if err != nil {
			t.Fatalf("Create in project B err = %v, want nil: slug is unique per project", err)
		}
		if a.ID == b.ID {
			t.Fatal("the two projects must hold distinct sheets")
		}
	})
	t.Run("missing tenant returns MissingError", func(t *testing.T) {
		svc, _ := newSvc(t, fakes.NewSheet(), false)

		_, err := svc.Create(context.Background(), uuid.New(), "", "prices", sheet.VisibilityKey, 0)
		if !tenant.IsMissingError(err) {
			t.Fatalf("err = %v, want tenant.MissingError", err)
		}
	})
	t.Run("a failing slug pre-check routes through unexpected", func(t *testing.T) {
		store := fakes.NewSheet()
		store.BySlugFn = func(context.Context, uuid.UUID, uuid.UUID, string) (*sheet.Sheet, error) {
			return nil, errors.New("boom")
		}
		svc, unexCalls := newSvc(t, store, false)
		ctx, _ := tenantCtx(t)

		if _, err := svc.Create(ctx, uuid.New(), "", "prices", sheet.VisibilityKey, 0); err == nil {
			t.Fatal("want error")
		}
		if *unexCalls != 1 {
			t.Errorf("unexpected() calls = %d, want 1", *unexCalls)
		}
	})
	t.Run("a failing save routes through unexpected", func(t *testing.T) {
		store := fakes.NewSheet()
		store.SaveFn = func(context.Context, *sheet.Sheet) error { return errors.New("boom") }
		svc, unexCalls := newSvc(t, store, false)
		ctx, _ := tenantCtx(t)

		if _, err := svc.Create(ctx, uuid.New(), "", "prices", sheet.VisibilityKey, 0); err == nil {
			t.Fatal("want error")
		}
		if *unexCalls != 1 {
			t.Errorf("unexpected() calls = %d, want 1", *unexCalls)
		}
	})
	t.Run("a save-time unique violation stays typed", func(t *testing.T) {
		store := fakes.NewSheet()
		store.SaveFn = func(_ context.Context, s *sheet.Sheet) error {
			return &sheet.AlreadyExistsError{Field: "slug", Value: s.Slug}
		}
		svc, unexCalls := newSvc(t, store, false)
		ctx, _ := tenantCtx(t)

		_, err := svc.Create(ctx, uuid.New(), "", "prices", sheet.VisibilityKey, 0)
		if !sheet.IsAlreadyExistsError(err) {
			t.Fatalf("err = %v, want *AlreadyExistsError", err)
		}
		if *unexCalls != 0 {
			t.Errorf("a unique violation must not route through unexpected()")
		}
	})
}

func TestService_ByID(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		svc, _ := newSvc(t, fakes.NewSheet(), false)
		ctx, _ := tenantCtx(t)
		created, err := svc.Create(ctx, uuid.New(), "Kamar", "prices", sheet.VisibilityKey, 0)
		if err != nil {
			t.Fatal(err)
		}

		got, err := svc.ByID(ctx, created.ID)
		if err != nil {
			t.Fatalf("ByID err = %v", err)
		}
		if got.Slug != "prices" || got.Tab != "Kamar" {
			t.Errorf("got %+v", got)
		}
	})
	t.Run("unknown id is NotFound", func(t *testing.T) {
		svc, unexCalls := newSvc(t, fakes.NewSheet(), false)
		ctx, _ := tenantCtx(t)

		_, err := svc.ByID(ctx, uuid.New())
		if !sheet.IsNotFoundError(err) {
			t.Fatalf("err = %v, want *NotFoundError", err)
		}
		if *unexCalls != 0 {
			t.Errorf("NotFound must not route through unexpected()")
		}
	})
	t.Run("another org's sheet is NotFound", func(t *testing.T) {
		store := fakes.NewSheet()
		svc, _ := newSvc(t, store, false)
		ctxA, _ := tenantCtx(t)
		created, err := svc.Create(ctxA, uuid.New(), "", "prices", sheet.VisibilityKey, 0)
		if err != nil {
			t.Fatal(err)
		}

		ctxB, _ := tenantCtx(t)
		if _, err := svc.ByID(ctxB, created.ID); !sheet.IsNotFoundError(err) {
			t.Fatalf("err = %v, want *NotFoundError", err)
		}
	})
	t.Run("another project's sheet is NotFound", func(t *testing.T) {
		store := fakes.NewSheet()
		svc, _ := newSvc(t, store, false)
		orgID := uuid.New()
		projectA, projectB := uuid.New(), uuid.New()
		created, err := svc.Create(scopedTo(orgID, projectA), uuid.New(), "", "prices", sheet.VisibilityKey, 0)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := svc.ByID(scopedTo(orgID, projectB), created.ID); !sheet.IsNotFoundError(err) {
			t.Fatalf("err = %v, want *NotFoundError", err)
		}
	})
	t.Run("missing tenant returns MissingError", func(t *testing.T) {
		svc, _ := newSvc(t, fakes.NewSheet(), false)
		if _, err := svc.ByID(context.Background(), uuid.New()); !tenant.IsMissingError(err) {
			t.Fatalf("err = %v, want tenant.MissingError", err)
		}
	})
	t.Run("a failing store routes through unexpected", func(t *testing.T) {
		store := fakes.NewSheet()
		store.ByIDFn = func(context.Context, uuid.UUID) (*sheet.Sheet, error) { return nil, errors.New("boom") }
		svc, unexCalls := newSvc(t, store, false)
		ctx, _ := tenantCtx(t)

		if _, err := svc.ByID(ctx, uuid.New()); err == nil {
			t.Fatal("want error")
		}
		if *unexCalls != 1 {
			t.Errorf("unexpected() calls = %d, want 1", *unexCalls)
		}
	})
}

func TestService_BySlug(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		svc, _ := newSvc(t, fakes.NewSheet(), false)
		ctx, _ := tenantCtx(t)
		created, err := svc.Create(ctx, uuid.New(), "", "prices", sheet.VisibilityKey, 0)
		if err != nil {
			t.Fatal(err)
		}

		got, err := svc.BySlug(ctx, "prices")
		if err != nil {
			t.Fatalf("BySlug err = %v", err)
		}
		if got.ID != created.ID {
			t.Errorf("got %v, want %v", got.ID, created.ID)
		}
	})
	t.Run("unknown slug is NotFound", func(t *testing.T) {
		svc, unexCalls := newSvc(t, fakes.NewSheet(), false)
		ctx, _ := tenantCtx(t)

		if _, err := svc.BySlug(ctx, "prices"); !sheet.IsNotFoundError(err) {
			t.Fatalf("err = %v, want *NotFoundError", err)
		}
		if *unexCalls != 0 {
			t.Errorf("NotFound must not route through unexpected()")
		}
	})
	t.Run("the same slug in two projects resolves to different sheets", func(t *testing.T) {
		store := fakes.NewSheet()
		svc, _ := newSvc(t, store, false)
		orgID := uuid.New()
		projectA, projectB := uuid.New(), uuid.New()
		ctxA, ctxB := scopedTo(orgID, projectA), scopedTo(orgID, projectB)

		a, err := svc.Create(ctxA, uuid.New(), "Kamar A", "prices", sheet.VisibilityKey, 0)
		if err != nil {
			t.Fatal(err)
		}
		b, err := svc.Create(ctxB, uuid.New(), "Kamar B", "prices", sheet.VisibilityKey, 0)
		if err != nil {
			t.Fatal(err)
		}

		gotA, err := svc.BySlug(ctxA, "prices")
		if err != nil {
			t.Fatalf("BySlug in project A err = %v", err)
		}
		gotB, err := svc.BySlug(ctxB, "prices")
		if err != nil {
			t.Fatalf("BySlug in project B err = %v", err)
		}
		if gotA.ID != a.ID {
			t.Errorf("project A resolved %v, want %v", gotA.ID, a.ID)
		}
		if gotB.ID != b.ID {
			t.Errorf("project B resolved %v, want %v", gotB.ID, b.ID)
		}
		if gotA.Tab == gotB.Tab {
			t.Errorf("both projects resolved the same tab %q; BySlug is not project-scoped", gotA.Tab)
		}
	})
	t.Run("another org's slug is NotFound", func(t *testing.T) {
		store := fakes.NewSheet()
		svc, _ := newSvc(t, store, false)
		ctxA, _ := tenantCtx(t)
		if _, err := svc.Create(ctxA, uuid.New(), "", "prices", sheet.VisibilityKey, 0); err != nil {
			t.Fatal(err)
		}

		ctxB, _ := tenantCtx(t)
		if _, err := svc.BySlug(ctxB, "prices"); !sheet.IsNotFoundError(err) {
			t.Fatalf("err = %v, want *NotFoundError", err)
		}
	})
	t.Run("missing tenant returns MissingError", func(t *testing.T) {
		svc, _ := newSvc(t, fakes.NewSheet(), false)
		if _, err := svc.BySlug(context.Background(), "prices"); !tenant.IsMissingError(err) {
			t.Fatalf("err = %v, want tenant.MissingError", err)
		}
	})
	t.Run("a failing store routes through unexpected", func(t *testing.T) {
		store := fakes.NewSheet()
		store.BySlugFn = func(context.Context, uuid.UUID, uuid.UUID, string) (*sheet.Sheet, error) {
			return nil, errors.New("boom")
		}
		svc, unexCalls := newSvc(t, store, false)
		ctx, _ := tenantCtx(t)

		if _, err := svc.BySlug(ctx, "prices"); err == nil {
			t.Fatal("want error")
		}
		if *unexCalls != 1 {
			t.Errorf("unexpected() calls = %d, want 1", *unexCalls)
		}
	})
}

func TestService_List(t *testing.T) {
	t.Run("returns only the sheets in scope", func(t *testing.T) {
		store := fakes.NewSheet()
		svc, _ := newSvc(t, store, false)
		orgID := uuid.New()
		projectA, projectB := uuid.New(), uuid.New()
		ctxA := scopedTo(orgID, projectA)

		for _, slug := range []string{"prices", "rooms"} {
			if _, err := svc.Create(ctxA, uuid.New(), "", slug, sheet.VisibilityKey, 0); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := svc.Create(scopedTo(orgID, projectB), uuid.New(), "", "prices", sheet.VisibilityKey, 0); err != nil {
			t.Fatal(err)
		}

		got, err := svc.List(ctxA)
		if err != nil {
			t.Fatalf("List err = %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("len = %d, want 2", len(got))
		}
	})
	t.Run("missing tenant returns MissingError", func(t *testing.T) {
		svc, _ := newSvc(t, fakes.NewSheet(), false)
		if _, err := svc.List(context.Background()); !tenant.IsMissingError(err) {
			t.Fatalf("err = %v, want tenant.MissingError", err)
		}
	})
	t.Run("a failing store routes through unexpected", func(t *testing.T) {
		store := fakes.NewSheet()
		store.ListFn = func(context.Context, uuid.UUID, uuid.UUID) ([]*sheet.Sheet, error) {
			return nil, errors.New("boom")
		}
		svc, unexCalls := newSvc(t, store, false)
		ctx, _ := tenantCtx(t)

		if _, err := svc.List(ctx); err == nil {
			t.Fatal("want error")
		}
		if *unexCalls != 1 {
			t.Errorf("unexpected() calls = %d, want 1", *unexCalls)
		}
	})
}

func TestService_Update(t *testing.T) {
	seed := func(t *testing.T, publicEnabled bool) (*sheet.Service, context.Context, *sheet.Sheet, *int) {
		t.Helper()
		svc, unexCalls := newSvc(t, fakes.NewSheet(), publicEnabled)
		ctx, _ := tenantCtx(t)
		sh, err := svc.Create(ctx, uuid.New(), "Kamar", "prices", sheet.VisibilityKey, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		return svc, ctx, sh, unexCalls
	}

	t.Run("retabs", func(t *testing.T) {
		svc, ctx, sh, _ := seed(t, false)
		tab := "Harga"

		got, err := svc.Update(ctx, sh.ID, sheet.UpdateInput{Tab: &tab})
		if err != nil {
			t.Fatalf("Update err = %v", err)
		}
		if got.Tab != "Harga" {
			t.Errorf("Tab = %q, want %q", got.Tab, "Harga")
		}
	})
	t.Run("retabs to empty", func(t *testing.T) {
		svc, ctx, sh, _ := seed(t, false)
		empty := ""

		got, err := svc.Update(ctx, sh.ID, sheet.UpdateInput{Tab: &empty})
		if err != nil {
			t.Fatalf("Update err = %v, want nil: empty means the spreadsheet's first tab", err)
		}
		if got.Tab != "" {
			t.Errorf("Tab = %q, want empty", got.Tab)
		}
	})
	t.Run("sets the ttl", func(t *testing.T) {
		svc, ctx, sh, _ := seed(t, false)
		ttl := 90 * time.Second

		got, err := svc.Update(ctx, sh.ID, sheet.UpdateInput{CacheTTL: &ttl})
		if err != nil {
			t.Fatalf("Update err = %v", err)
		}
		if got.CacheTTL != 90*time.Second {
			t.Errorf("CacheTTL = %v, want 90s", got.CacheTTL)
		}
	})
	t.Run("publishes public when the capability is on", func(t *testing.T) {
		svc, ctx, sh, unexCalls := seed(t, true)
		vis := sheet.VisibilityPublic

		got, err := svc.Update(ctx, sh.ID, sheet.UpdateInput{Visibility: &vis})
		if err != nil {
			t.Fatalf("Update err = %v", err)
		}
		if got.Visibility != sheet.VisibilityPublic {
			t.Errorf("Visibility = %q, want %q", got.Visibility, sheet.VisibilityPublic)
		}
		if *unexCalls != 0 {
			t.Errorf("unexpected() calls = %d, want 0", *unexCalls)
		}
	})
	t.Run("refuses public when the capability is off", func(t *testing.T) {
		svc, ctx, sh, unexCalls := seed(t, false)
		vis := sheet.VisibilityPublic

		_, err := svc.Update(ctx, sh.ID, sheet.UpdateInput{Visibility: &vis})
		if !sheet.IsPublicDisabledError(err) {
			t.Fatalf("err = %v (%T), want *PublicDisabledError", err, err)
		}
		if *unexCalls != 0 {
			t.Errorf("a capability refusal must not route through unexpected()")
		}
		after, err := svc.ByID(ctx, sh.ID)
		if err != nil {
			t.Fatal(err)
		}
		if after.Visibility != sheet.VisibilityKey {
			t.Errorf("stored visibility = %q, a refused Update must not persist", after.Visibility)
		}
	})
	t.Run("setting key back is allowed when public is off", func(t *testing.T) {
		svc, ctx, sh, _ := seed(t, false)
		vis := sheet.VisibilityKey

		got, err := svc.Update(ctx, sh.ID, sheet.UpdateInput{Visibility: &vis})
		if err != nil {
			t.Fatalf("Update err = %v, want nil: the gate covers public only", err)
		}
		if got.Visibility != sheet.VisibilityKey {
			t.Errorf("Visibility = %q, want %q", got.Visibility, sheet.VisibilityKey)
		}
	})
	t.Run("invalid visibility bubbles the typed error", func(t *testing.T) {
		svc, ctx, sh, _ := seed(t, true)
		vis := sheet.Visibility("secret")

		if _, err := svc.Update(ctx, sh.ID, sheet.UpdateInput{Visibility: &vis}); !sheet.IsInvalidVisibilityError(err) {
			t.Fatalf("err = %v, want *InvalidVisibilityError", err)
		}
	})
	t.Run("invalid ttl bubbles the typed error", func(t *testing.T) {
		svc, ctx, sh, unexCalls := seed(t, false)
		ttl := 999 * time.Millisecond

		if _, err := svc.Update(ctx, sh.ID, sheet.UpdateInput{CacheTTL: &ttl}); !sheet.IsInvalidTTLError(err) {
			t.Fatalf("err = %v, want *InvalidTTLError", err)
		}
		if *unexCalls != 0 {
			t.Errorf("an invariant failure must not route through unexpected()")
		}
	})
	t.Run("an empty input leaves the sheet alone", func(t *testing.T) {
		svc, ctx, sh, _ := seed(t, false)

		got, err := svc.Update(ctx, sh.ID, sheet.UpdateInput{})
		if err != nil {
			t.Fatalf("Update err = %v", err)
		}
		if got.Tab != sh.Tab || got.Slug != sh.Slug || got.Visibility != sh.Visibility || got.CacheTTL != sh.CacheTTL {
			t.Errorf("got %+v, want unchanged %+v", got, sh)
		}
	})
	t.Run("unknown id is NotFound", func(t *testing.T) {
		svc, unexCalls := newSvc(t, fakes.NewSheet(), false)
		ctx, _ := tenantCtx(t)

		if _, err := svc.Update(ctx, uuid.New(), sheet.UpdateInput{}); !sheet.IsNotFoundError(err) {
			t.Fatalf("err = %v, want *NotFoundError", err)
		}
		if *unexCalls != 0 {
			t.Errorf("NotFound must not route through unexpected()")
		}
	})
	t.Run("another org's sheet is NotFound", func(t *testing.T) {
		store := fakes.NewSheet()
		svc, _ := newSvc(t, store, false)
		ctxA, _ := tenantCtx(t)
		sh, err := svc.Create(ctxA, uuid.New(), "", "prices", sheet.VisibilityKey, 0)
		if err != nil {
			t.Fatal(err)
		}

		ctxB, _ := tenantCtx(t)
		if _, err := svc.Update(ctxB, sh.ID, sheet.UpdateInput{}); !sheet.IsNotFoundError(err) {
			t.Fatalf("err = %v, want *NotFoundError", err)
		}
	})
	t.Run("missing tenant returns MissingError", func(t *testing.T) {
		svc, _ := newSvc(t, fakes.NewSheet(), false)
		if _, err := svc.Update(context.Background(), uuid.New(), sheet.UpdateInput{}); !tenant.IsMissingError(err) {
			t.Fatalf("err = %v, want tenant.MissingError", err)
		}
	})
	t.Run("a failing load routes through unexpected", func(t *testing.T) {
		store := fakes.NewSheet()
		store.ByIDFn = func(context.Context, uuid.UUID) (*sheet.Sheet, error) { return nil, errors.New("boom") }
		svc, unexCalls := newSvc(t, store, false)
		ctx, _ := tenantCtx(t)

		if _, err := svc.Update(ctx, uuid.New(), sheet.UpdateInput{}); err == nil {
			t.Fatal("want error")
		}
		if *unexCalls != 1 {
			t.Errorf("unexpected() calls = %d, want 1", *unexCalls)
		}
	})
	t.Run("a failing save routes through unexpected", func(t *testing.T) {
		store := fakes.NewSheet()
		svc, unexCalls := newSvc(t, store, false)
		ctx, _ := tenantCtx(t)
		sh, err := svc.Create(ctx, uuid.New(), "", "prices", sheet.VisibilityKey, 0)
		if err != nil {
			t.Fatal(err)
		}
		store.SaveFn = func(context.Context, *sheet.Sheet) error { return errors.New("boom") }

		if _, err := svc.Update(ctx, sh.ID, sheet.UpdateInput{}); err == nil {
			t.Fatal("want error")
		}
		if *unexCalls != 1 {
			t.Errorf("unexpected() calls = %d, want 1", *unexCalls)
		}
	})
	t.Run("a save-time unique violation stays typed", func(t *testing.T) {
		store := fakes.NewSheet()
		svc, unexCalls := newSvc(t, store, false)
		ctx, _ := tenantCtx(t)
		sh, err := svc.Create(ctx, uuid.New(), "", "prices", sheet.VisibilityKey, 0)
		if err != nil {
			t.Fatal(err)
		}
		store.SaveFn = func(_ context.Context, s *sheet.Sheet) error {
			return &sheet.AlreadyExistsError{Field: "slug", Value: s.Slug}
		}

		if _, err := svc.Update(ctx, sh.ID, sheet.UpdateInput{}); !sheet.IsAlreadyExistsError(err) {
			t.Fatalf("err = %v, want *AlreadyExistsError", err)
		}
		if *unexCalls != 0 {
			t.Errorf("a unique violation must not route through unexpected()")
		}
	})
}

func TestService_Delete(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		svc, _ := newSvc(t, fakes.NewSheet(), false)
		ctx, _ := tenantCtx(t)
		sh, err := svc.Create(ctx, uuid.New(), "", "prices", sheet.VisibilityKey, 0)
		if err != nil {
			t.Fatal(err)
		}

		if err := svc.Delete(ctx, sh.ID); err != nil {
			t.Fatalf("Delete err = %v", err)
		}
		if _, err := svc.ByID(ctx, sh.ID); !sheet.IsNotFoundError(err) {
			t.Fatalf("after Delete err = %v, want *NotFoundError", err)
		}
	})
	t.Run("unknown id is NotFound", func(t *testing.T) {
		svc, unexCalls := newSvc(t, fakes.NewSheet(), false)
		ctx, _ := tenantCtx(t)

		if err := svc.Delete(ctx, uuid.New()); !sheet.IsNotFoundError(err) {
			t.Fatalf("err = %v, want *NotFoundError", err)
		}
		if *unexCalls != 0 {
			t.Errorf("NotFound must not route through unexpected()")
		}
	})
	t.Run("another org's sheet is NotFound", func(t *testing.T) {
		store := fakes.NewSheet()
		svc, _ := newSvc(t, store, false)
		ctxA, _ := tenantCtx(t)
		sh, err := svc.Create(ctxA, uuid.New(), "", "prices", sheet.VisibilityKey, 0)
		if err != nil {
			t.Fatal(err)
		}

		ctxB, _ := tenantCtx(t)
		if err := svc.Delete(ctxB, sh.ID); !sheet.IsNotFoundError(err) {
			t.Fatalf("err = %v, want *NotFoundError", err)
		}
		if _, err := svc.ByID(ctxA, sh.ID); err != nil {
			t.Errorf("the owning org lost its sheet to a cross-tenant Delete: %v", err)
		}
	})
	t.Run("missing tenant returns MissingError", func(t *testing.T) {
		svc, _ := newSvc(t, fakes.NewSheet(), false)
		if err := svc.Delete(context.Background(), uuid.New()); !tenant.IsMissingError(err) {
			t.Fatalf("err = %v, want tenant.MissingError", err)
		}
	})
	t.Run("a failing load routes through unexpected", func(t *testing.T) {
		store := fakes.NewSheet()
		store.ByIDFn = func(context.Context, uuid.UUID) (*sheet.Sheet, error) { return nil, errors.New("boom") }
		svc, unexCalls := newSvc(t, store, false)
		ctx, _ := tenantCtx(t)

		if err := svc.Delete(ctx, uuid.New()); err == nil {
			t.Fatal("want error")
		}
		if *unexCalls != 1 {
			t.Errorf("unexpected() calls = %d, want 1", *unexCalls)
		}
	})
	t.Run("a store-level NotFound on delete stays typed", func(t *testing.T) {
		store := fakes.NewSheet()
		svc, unexCalls := newSvc(t, store, false)
		ctx, _ := tenantCtx(t)
		sh, err := svc.Create(ctx, uuid.New(), "", "prices", sheet.VisibilityKey, 0)
		if err != nil {
			t.Fatal(err)
		}
		store.DeleteFn = func(_ context.Context, id uuid.UUID) error {
			return &sheet.NotFoundError{ID: id.String()}
		}

		if err := svc.Delete(ctx, sh.ID); !sheet.IsNotFoundError(err) {
			t.Fatalf("err = %v, want *NotFoundError", err)
		}
		if *unexCalls != 0 {
			t.Errorf("NotFound must not route through unexpected()")
		}
	})
	t.Run("a failing delete routes through unexpected", func(t *testing.T) {
		store := fakes.NewSheet()
		svc, unexCalls := newSvc(t, store, false)
		ctx, _ := tenantCtx(t)
		sh, err := svc.Create(ctx, uuid.New(), "", "prices", sheet.VisibilityKey, 0)
		if err != nil {
			t.Fatal(err)
		}
		store.DeleteFn = func(context.Context, uuid.UUID) error { return errors.New("boom") }

		if err := svc.Delete(ctx, sh.ID); err == nil {
			t.Fatal("want error")
		}
		if *unexCalls != 1 {
			t.Errorf("unexpected() calls = %d, want 1", *unexCalls)
		}
	})
}
