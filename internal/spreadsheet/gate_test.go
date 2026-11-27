package spreadsheet_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/platform/authz"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/spreadsheet"
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

func TestGatedMethods_RefuseEmptyHeldSet(t *testing.T) {
	store := fakes.NewSpreadsheet()
	svc, _ := newStoreSvc(t, store)
	_, tc := tenantCtx(t)
	sp := seed(t, store, tc, goodFileID)
	empty := fakes.BindRole(context.Background(), tc, "")
	g := newProvisionRig(t)
	cases := []struct {
		name string
		want authz.Permission
		call func(context.Context) error
	}{
		{"Register", authz.SpreadsheetsWrite, func(ctx context.Context) error {
			_, err := svc.Register(ctx, uuid.New(), goodFileID, "Prices")
			return err
		}},
		{"Retitle", authz.SpreadsheetsWrite, func(ctx context.Context) error {
			_, err := svc.Retitle(ctx, sp.ID, "New")
			return err
		}},
		{"Rebind", authz.SpreadsheetsWrite, func(ctx context.Context) error {
			_, err := svc.Rebind(ctx, sp.ID, uuid.New())
			return err
		}},
		{"AddTab", authz.SpreadsheetsWrite, func(ctx context.Context) error { return svc.AddTab(ctx, sp.ID, "Q2") }},
		{"SetWritable", authz.SpreadsheetsWrite, func(ctx context.Context) error {
			_, err := svc.SetWritable(ctx, sp.ID, false)
			return err
		}},
		{"List", authz.SpreadsheetsRead, func(ctx context.Context) error {
			_, err := svc.List(ctx)
			return err
		}},
		{"ListTabs", authz.SpreadsheetsRead, func(ctx context.Context) error {
			_, err := svc.ListTabs(ctx, sp.ID)
			return err
		}},
		{"Delete", authz.SpreadsheetsWrite, func(ctx context.Context) error { return svc.Delete(ctx, sp.ID) }},
		{"Provision", authz.SheetsAdmin, func(context.Context) error {
			_, _, err := g.flow.Run(fakes.BindRole(context.Background(), g.tc, ""), validProvision(uuid.New()))
			return err
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { deniedFor(t, c.call(empty), c.want) })
	}
	if g.google.createCount() != 0 {
		t.Errorf("refused provision created %d files", g.google.createCount())
	}
}

func TestSpreadsheetGate_RunsBeforeValidation(t *testing.T) {
	store := fakes.NewSpreadsheet()
	svc, _ := newStoreSvc(t, store)
	_, tc := tenantCtx(t)
	sp := seed(t, store, tc, goodFileID)
	empty := fakes.BindRole(context.Background(), tc, "")

	_, err := svc.Register(empty, uuid.New(), "", "")
	deniedFor(t, err, authz.SpreadsheetsWrite)
	_, err = svc.Retitle(empty, sp.ID, "")
	deniedFor(t, err, authz.SpreadsheetsWrite)
	g := newProvisionRig(t)
	_, _, err = g.flow.Run(fakes.BindRole(context.Background(), g.tc, ""), spreadsheet.ProvisionRequest{})
	deniedFor(t, err, authz.SheetsAdmin)
}

func TestSpreadsheetLookups_WorkBeforeBind(t *testing.T) {
	store := fakes.NewSpreadsheet()
	svc, _ := newStoreSvc(t, store)
	_, tc := tenantCtx(t)
	sp := seed(t, store, tc, goodFileID)
	unbound := tenant.Into(context.Background(), tc)

	if _, err := svc.ByID(unbound, sp.ID); err != nil {
		t.Errorf("ByID: %v", err)
	}
	if _, err := svc.ByGoogleFileID(unbound, goodFileID); err != nil {
		t.Errorf("ByGoogleFileID: %v", err)
	}
}

func TestSpreadsheet_MemberCannotSetWritable(t *testing.T) {
	store := fakes.NewSpreadsheet()
	svc, _ := newStoreSvc(t, store)
	_, tc := tenantCtx(t)
	sp := seed(t, store, tc, goodFileID)
	member := fakes.BindRole(context.Background(), tc, "member")

	_, err := svc.SetWritable(member, sp.ID, true)
	deniedFor(t, err, authz.SpreadsheetsEnableWrites)

	admin := fakes.BindRole(context.Background(), tc, "admin")
	if _, err := svc.SetWritable(admin, sp.ID, true); err != nil {
		t.Fatalf("admin enable: %v", err)
	}
	if _, err := svc.SetWritable(member, sp.ID, true); err != nil {
		t.Errorf("member re-saving an already writable document: %v", err)
	}
	if got, err := svc.SetWritable(member, sp.ID, false); err != nil || got.Writable {
		t.Errorf("member disable: %v, %+v", err, got)
	}
}

func TestProvision_MemberTokenStillProvisions(t *testing.T) {
	g := newProvisionRig(t)
	member := fakes.BindRole(context.Background(), g.tc, "member")

	sp, _, err := g.flow.Run(member, validProvision(uuid.New()))
	if err != nil || !sp.Writable {
		t.Fatalf("member provision: %v, %+v", err, sp)
	}
}

func TestProvision_MemberRetryOnWritableSpreadsheetResumes(t *testing.T) {
	g := newProvisionRig(t)
	member := fakes.BindRole(context.Background(), g.tc, "member")
	req := validProvision(uuid.New())
	g.pub.failOnce["transactions"] = true
	if _, _, err := g.flow.Run(member, req); err == nil {
		t.Fatal("first provision: want the publish failure")
	}

	if _, sheets, err := g.flow.Run(member, req); err != nil || len(sheets) != 2 {
		t.Fatalf("member retry: %v, %d sheets", err, len(sheets))
	}
}

func TestProvision_MemberResumeAfterWritesTurnedOffRefused(t *testing.T) {
	g := newProvisionRig(t)
	req := validProvision(uuid.New())
	sp, _, err := g.flow.Run(g.ctx, req)
	if err != nil {
		t.Fatalf("first provision: %v", err)
	}
	sp.SetWritable(false)
	if err := g.store.Save(context.Background(), sp); err != nil {
		t.Fatalf("turn writes off: %v", err)
	}
	req.Tabs = append(req.Tabs, spreadsheet.ProvisionTab{Title: "Extra", Slug: "extra", Headers: []string{"id"}})

	_, _, err = g.flow.Run(fakes.BindRole(context.Background(), g.tc, "member"), req)
	deniedFor(t, err, authz.SheetsEnableWrites)
	if _, ok := g.pub.published["extra"]; ok {
		t.Error("member re-run published a writable sheet on a spreadsheet an admin made read-only")
	}
}
