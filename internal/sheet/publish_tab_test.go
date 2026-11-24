package sheet_test

import (
	"testing"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/sheet"
	"altalune.id/opensheet/internal/testutil/fakes"
)

func TestService_PublishTab(t *testing.T) {
	svc, _ := newSvc(t, fakes.NewSheet(), false)
	ctx, _ := tenantCtx(t)
	spreadsheetID := uuid.New()

	created, err := svc.PublishTab(ctx, spreadsheetID, "Wallets", "wallets")
	if err != nil {
		t.Fatalf("first publish: %v", err)
	}
	if created.Visibility != sheet.VisibilityKey || !created.Writable || created.Tab != "Wallets" {
		t.Errorf("published %+v, want a writable key sheet on tab Wallets", created)
	}

	again, err := svc.PublishTab(ctx, spreadsheetID, "Wallets", "wallets")
	if err != nil {
		t.Fatalf("republish: %v", err)
	}
	if again.ID != created.ID {
		t.Errorf("republish made %s, want the existing %s", again.ID, created.ID)
	}

	if _, err := svc.PublishTab(ctx, uuid.New(), "Wallets", "wallets"); !sheet.IsAlreadyExistsError(err) {
		t.Errorf("slug held by another spreadsheet: err = %v, want AlreadyExistsError", err)
	}
}

func TestValidateSlug(t *testing.T) {
	if err := sheet.ValidateSlug("wallets"); err != nil {
		t.Errorf("wallets: %v", err)
	}
	for _, bad := range []string{"", "Bad Slug", "rows"} {
		if err := sheet.ValidateSlug(bad); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
}
