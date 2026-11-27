package sheet_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/sheet"
	"altalune.id/opensheet/internal/testutil/fakes"
)

func deniedFor(t *testing.T, err error, want authz.Permission) {
	t.Helper()
	d, ok := errors.AsType[*authz.DeniedError](err)
	if !ok {
		t.Fatalf("err = %v (%T), want *authz.DeniedError", err, err)
	}
	if d.Permission != want {
		t.Fatalf("denied %s, want %s", d.Permission, want)
	}
}

func seeded(t *testing.T, writable bool) (*sheet.Service, tenant.Context, *sheet.Sheet) {
	t.Helper()
	svc, _ := newSvc(t, fakes.NewSheet(), false)
	ctx, tc := tenantCtx(t)
	sh, err := svc.Create(ctx, sheet.CreateRequest{SpreadsheetID: uuid.New(), Tab: "Kamar", Slug: "prices", Visibility: sheet.VisibilityKey, Writable: writable})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return svc, tc, sh
}

func TestGatedMethods_RefuseEmptyHeldSet(t *testing.T) {
	svc, tc, sh := seeded(t, false)
	empty := fakes.BindRole(context.Background(), tc, "")
	ttl := time.Minute
	cases := []struct {
		name string
		want authz.Permission
		call func(context.Context) error
	}{
		{"Create", authz.SheetsAdmin, func(ctx context.Context) error {
			_, err := svc.Create(ctx, sheet.CreateRequest{SpreadsheetID: uuid.New(), Slug: "other", Visibility: sheet.VisibilityKey})
			return err
		}},
		{"Update", authz.SheetsAdmin, func(ctx context.Context) error {
			_, err := svc.Update(ctx, sh.ID, sheet.UpdateInput{CacheTTL: &ttl})
			return err
		}},
		{"Delete", authz.SheetsAdmin, func(ctx context.Context) error { return svc.Delete(ctx, sh.ID) }},
		{"PurgeCache", authz.CachePurge, func(ctx context.Context) error { return svc.PurgeCache(ctx, sh.ID) }},
		{"List", authz.SheetsRead, func(ctx context.Context) error {
			_, err := svc.List(ctx)
			return err
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { deniedFor(t, c.call(empty), c.want) })
	}
}

func TestGate_RunsBeforeValidation(t *testing.T) {
	svc, tc, sh := seeded(t, false)
	empty := fakes.BindRole(context.Background(), tc, "")
	bad := sheet.Visibility("nope")

	_, err := svc.Create(empty, sheet.CreateRequest{SpreadsheetID: uuid.New(), Slug: "Not A Slug!", Visibility: bad})
	deniedFor(t, err, authz.SheetsAdmin)
	_, err = svc.Update(empty, sh.ID, sheet.UpdateInput{Visibility: &bad})
	deniedFor(t, err, authz.SheetsAdmin)
}

func TestUngatedLookups_WorkBeforeBind(t *testing.T) {
	svc, tc, sh := seeded(t, false)
	unbound := tenant.Into(context.Background(), tc)

	if _, err := svc.BySlug(unbound, sh.Slug); err != nil {
		t.Errorf("BySlug: %v", err)
	}
	if _, err := svc.ByID(unbound, sh.ID); err != nil {
		t.Errorf("ByID: %v", err)
	}
}

func TestSheet_MemberCannotEnableWrites(t *testing.T) {
	svc, tc, sh := seeded(t, false)
	member := fakes.BindRole(context.Background(), tc, "member")
	on := true

	_, err := svc.Update(member, sh.ID, sheet.UpdateInput{Writable: &on})
	deniedFor(t, err, authz.SheetsEnableWrites)

	_, err = svc.Create(member, sheet.CreateRequest{SpreadsheetID: uuid.New(), Slug: "other", Visibility: sheet.VisibilityKey, Writable: true})
	deniedFor(t, err, authz.SheetsEnableWrites)
}

func TestSheet_AdminEnablesWrites(t *testing.T) {
	svc, tc, sh := seeded(t, false)
	admin := fakes.BindRole(context.Background(), tc, "admin")
	on := true

	got, err := svc.Update(admin, sh.ID, sheet.UpdateInput{Writable: &on})
	if err != nil || !got.Writable {
		t.Fatalf("admin enable writes: %v, %+v", err, got)
	}
}

func TestSheet_MemberCanDisableWrites(t *testing.T) {
	svc, tc, sh := seeded(t, true)
	member := fakes.BindRole(context.Background(), tc, "member")
	off := false

	got, err := svc.Update(member, sh.ID, sheet.UpdateInput{Writable: &off})
	if err != nil || got.Writable {
		t.Fatalf("member disable writes: %v, %+v", err, got)
	}
}

func TestSheet_MemberEditsTTLOnWritableSheet(t *testing.T) {
	svc, tc, sh := seeded(t, true)
	member := fakes.BindRole(context.Background(), tc, "member")
	on := true
	ttl := 2 * time.Minute

	got, err := svc.Update(member, sh.ID, sheet.UpdateInput{CacheTTL: &ttl, Writable: &on})
	if err != nil || got.CacheTTL != ttl {
		t.Fatalf("member TTL save on writable sheet: %v, %+v", err, got)
	}
}

func TestPublishTab_ProvisionPathSkipsWritesCheck(t *testing.T) {
	svc, _ := newSvc(t, fakes.NewSheet(), false)
	_, tc := tenantCtx(t)
	member := fakes.BindRole(context.Background(), tc, "member")

	got, err := svc.PublishTab(member, uuid.New(), "Wallets", "wallets")
	if err != nil || !got.Writable {
		t.Fatalf("member provision publish: %v, %+v", err, got)
	}
	_, err = svc.PublishTab(fakes.BindRole(context.Background(), tc, ""), uuid.New(), "Other", "other")
	deniedFor(t, err, authz.SheetsAdmin)
}

func TestSheet_MemberCannotEnableWritesOnProvisionedSheet(t *testing.T) {
	svc, _ := newSvc(t, fakes.NewSheet(), false)
	_, tc := tenantCtx(t)
	member := fakes.BindRole(context.Background(), tc, "member")
	sh, err := svc.PublishTab(member, uuid.New(), "Wallets", "wallets")
	if err != nil {
		t.Fatal(err)
	}
	off, on := false, true
	if _, err := svc.Update(member, sh.ID, sheet.UpdateInput{Writable: &off}); err != nil {
		t.Fatalf("disable: %v", err)
	}
	_, err = svc.Update(member, sh.ID, sheet.UpdateInput{Writable: &on})
	deniedFor(t, err, authz.SheetsEnableWrites)
}

func TestSheet_MemberCannotRetabWritableSheet(t *testing.T) {
	svc, tc, sh := seeded(t, true)
	tab := "Other"

	_, err := svc.Update(fakes.BindRole(context.Background(), tc, "member"), sh.ID, sheet.UpdateInput{Tab: &tab})
	deniedFor(t, err, authz.SheetsEnableWrites)

	got, err := svc.Update(fakes.BindRole(context.Background(), tc, "admin"), sh.ID, sheet.UpdateInput{Tab: &tab})
	if err != nil || got.Tab != tab {
		t.Fatalf("admin retab: %v, %+v", err, got)
	}
}
