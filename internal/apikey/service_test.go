package apikey_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/internal/apikey"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/fakes"
)

type harness struct {
	svc    *apikey.Service
	store  *fakes.APIKey
	sheets *fakes.APIKeySheets
	unex   *int
	ctx    context.Context //nolint:containedctx // fixture convenience
	tc     tenant.Context
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	store := fakes.NewAPIKey()
	sheets := fakes.NewAPIKeySheets()
	calls := 0
	unexpected := func(_ context.Context, _ string, err error, _ ...any) *apperror.AppError {
		calls++
		return apperror.New("opensheet.unexpected", err.Error(), codes.Internal,
			&apperrorv1.ErrorDetail{Code: "opensheet.unexpected"}).WithCause(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	tc := tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New(), UserID: uuid.New()}
	return &harness{
		svc:    apikey.NewService(store, log, unexpected, sheets),
		store:  store,
		sheets: sheets,
		unex:   &calls,
		ctx:    tenant.Into(context.Background(), tc),
		tc:     tc,
	}
}

func (h *harness) mint(t *testing.T, scopes ...string) (*apikey.APIKey, string) {
	t.Helper()
	if len(scopes) == 0 {
		scopes = []string{authn.ScopeSheetsRead}
	}
	k, plaintext, err := h.svc.Create(h.ctx, apikey.CreateRequest{Name: "ci", Scopes: scopes})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return k, plaintext
}

func TestService_Create(t *testing.T) {
	t.Run("happy path returns a usable plaintext once", func(t *testing.T) {
		h := newHarness(t)
		k, plaintext := h.mint(t)

		if !plaintextPattern.MatchString(plaintext) {
			t.Fatalf("plaintext = %q, want the osk wire format", plaintext)
		}
		if k.OrgID != h.tc.OrgID || k.ProjectID != h.tc.ProjectID {
			t.Error("the key was not minted in the caller's tenant scope")
		}
		if h.store.Len() != 1 {
			t.Errorf("stored keys = %d, want 1", h.store.Len())
		}
		if *h.unex != 0 {
			t.Errorf("unexpected() called %d times", *h.unex)
		}

		got, err := h.svc.Authenticate(context.Background(), plaintext)
		if err != nil {
			t.Fatalf("Authenticate with the returned plaintext: %v", err)
		}
		if got.ID != k.ID {
			t.Error("Authenticate resolved a different key")
		}
	})
	t.Run("off-catalog scope is rejected", func(t *testing.T) {
		h := newHarness(t)
		_, _, err := h.svc.Create(h.ctx, apikey.CreateRequest{Name: "ci", Scopes: []string{"sheets:destroy"}})
		if !apikey.IsInvalidScopeError(err) {
			t.Fatalf("err = %v, want *InvalidScopeError", err)
		}
		app, ok := apperror.AsAppError(err)
		if !ok || app.Code() != apperror.CodeAPIKeyInvalidScope {
			t.Errorf("AsAppError = (%v, %v), want KEY003", app, ok)
		}
		if *h.unex != 0 {
			t.Error("an invariant failure must not route through unexpected")
		}
	})
	t.Run("empty scope set is rejected", func(t *testing.T) {
		h := newHarness(t)
		_, _, err := h.svc.Create(h.ctx, apikey.CreateRequest{Name: "ci"})
		if !apikey.IsInvalidScopeError(err) {
			t.Fatalf("err = %v, want *InvalidScopeError", err)
		}
	})
	t.Run("invalid name is rejected", func(t *testing.T) {
		h := newHarness(t)
		_, _, err := h.svc.Create(h.ctx, apikey.CreateRequest{Name: " ", Scopes: []string{authn.ScopeSheetsRead}})
		if !apikey.IsInvalidNameError(err) {
			t.Fatalf("err = %v, want *InvalidNameError", err)
		}
	})
	t.Run("past expiry is rejected", func(t *testing.T) {
		h := newHarness(t)
		past := time.Now().UTC().Add(-time.Hour)
		_, _, err := h.svc.Create(h.ctx, apikey.CreateRequest{
			Name: "ci", Scopes: []string{authn.ScopeSheetsRead}, ExpiresAt: &past,
		})
		if !apikey.IsInvalidExpiryError(err) {
			t.Fatalf("err = %v, want *InvalidExpiryError", err)
		}
	})
	t.Run("sheet in the caller's project is granted", func(t *testing.T) {
		h := newHarness(t)
		sheetID := uuid.New()
		h.sheets.Add(h.tc.OrgID, h.tc.ProjectID, sheetID)

		k, _, err := h.svc.Create(h.ctx, apikey.CreateRequest{
			Name: "ci", Scopes: []string{authn.ScopeSheetsRead}, SheetIDs: []uuid.UUID{sheetID},
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if len(k.SheetIDs) != 1 || k.SheetIDs[0] != sheetID {
			t.Fatalf("SheetIDs = %v, want [%v]", k.SheetIDs, sheetID)
		}
	})
	t.Run("sheet outside the caller's project is rejected", func(t *testing.T) {
		h := newHarness(t)
		foreign := uuid.New()
		h.sheets.Add(h.tc.OrgID, uuid.New(), foreign)

		_, _, err := h.svc.Create(h.ctx, apikey.CreateRequest{
			Name: "ci", Scopes: []string{authn.ScopeSheetsRead}, SheetIDs: []uuid.UUID{foreign},
		})
		if !apikey.IsUnknownSheetError(err) {
			t.Fatalf("err = %v, want *UnknownSheetError", err)
		}
		if h.store.Len() != 0 {
			t.Error("a rejected grant must not persist a key")
		}
	})
	t.Run("missing tenant scope is refused", func(t *testing.T) {
		h := newHarness(t)
		_, _, err := h.svc.Create(context.Background(), apikey.CreateRequest{
			Name: "ci", Scopes: []string{authn.ScopeSheetsRead},
		})
		if !tenant.IsMissingError(err) {
			t.Fatalf("err = %v, want tenant.MissingError", err)
		}
	})
	t.Run("sheet lookup failure routes through unexpected", func(t *testing.T) {
		h := newHarness(t)
		h.sheets.Err = errors.New("boom")
		_, _, err := h.svc.Create(h.ctx, apikey.CreateRequest{
			Name: "ci", Scopes: []string{authn.ScopeSheetsRead}, SheetIDs: []uuid.UUID{uuid.New()},
		})
		if err == nil {
			t.Fatal("want an error")
		}
		if *h.unex != 1 {
			t.Errorf("unexpected() called %d times, want 1", *h.unex)
		}
	})
	t.Run("unwired sheets dependency routes through unexpected", func(t *testing.T) {
		calls := 0
		unexpected := func(_ context.Context, _ string, err error, _ ...any) *apperror.AppError {
			calls++
			return apperror.New("opensheet.unexpected", err.Error(), codes.Internal,
				&apperrorv1.ErrorDetail{Code: "opensheet.unexpected"}).WithCause(err)
		}
		svc := apikey.NewService(fakes.NewAPIKey(), slog.New(slog.NewTextHandler(io.Discard, nil)), unexpected, nil)
		ctx := tenant.Into(context.Background(), tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New()})
		if _, _, err := svc.Create(ctx, apikey.CreateRequest{
			Name: "ci", Scopes: []string{authn.ScopeSheetsRead}, SheetIDs: []uuid.UUID{uuid.New()},
		}); err == nil {
			t.Fatal("want an error")
		}
		if calls != 1 {
			t.Errorf("unexpected() called %d times, want 1", calls)
		}
	})
	t.Run("store save failure routes through unexpected", func(t *testing.T) {
		h := newHarness(t)
		h.store.SaveErr = errors.New("boom")
		_, _, err := h.svc.Create(h.ctx, apikey.CreateRequest{Name: "ci", Scopes: []string{authn.ScopeSheetsRead}})
		if err == nil {
			t.Fatal("want an error")
		}
		if *h.unex != 1 {
			t.Errorf("unexpected() called %d times, want 1", *h.unex)
		}
	})
	t.Run("prefix collision bubbles AlreadyExistsError", func(t *testing.T) {
		h := newHarness(t)
		h.store.SaveErr = &apikey.AlreadyExistsError{Field: "key_prefix", Value: "x"}
		_, _, err := h.svc.Create(h.ctx, apikey.CreateRequest{Name: "ci", Scopes: []string{authn.ScopeSheetsRead}})
		if !apikey.IsAlreadyExistsError(err) {
			t.Fatalf("err = %v, want *AlreadyExistsError", err)
		}
		if *h.unex != 0 {
			t.Error("a typed store error must not route through unexpected")
		}
	})
}

func TestService_List_CarriesNoSecretMaterial(t *testing.T) {
	h := newHarness(t)
	h.mint(t)
	h.mint(t)

	got, err := h.svc.List(h.ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List len = %d, want 2", len(got))
	}
	for _, k := range got {
		if len(k.SecretHash) != 0 {
			t.Errorf("key %s carries secret material in a List result", k.ID)
		}
		if k.KeyPrefix == "" {
			t.Errorf("key %s lost its public prefix", k.ID)
		}
	}
}

func TestService_List(t *testing.T) {
	t.Run("another project's keys are not listed", func(t *testing.T) {
		h := newHarness(t)
		h.mint(t)

		other := tenant.Into(context.Background(), tenant.Context{OrgID: h.tc.OrgID, ProjectID: uuid.New()})
		got, err := h.svc.List(other)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("List len = %d, want 0", len(got))
		}
	})
	t.Run("missing tenant scope is refused", func(t *testing.T) {
		h := newHarness(t)
		if _, err := h.svc.List(context.Background()); !tenant.IsMissingError(err) {
			t.Fatalf("err = %v, want tenant.MissingError", err)
		}
	})
	t.Run("store failure routes through unexpected", func(t *testing.T) {
		h := newHarness(t)
		h.store.ListErr = errors.New("boom")
		if _, err := h.svc.List(h.ctx); err == nil {
			t.Fatal("want an error")
		}
		if *h.unex != 1 {
			t.Errorf("unexpected() called %d times, want 1", *h.unex)
		}
	})
}

func TestService_Revoke(t *testing.T) {
	t.Run("revoked key stops authenticating and keeps its hash", func(t *testing.T) {
		h := newHarness(t)
		k, plaintext := h.mint(t)

		got, err := h.svc.Revoke(h.ctx, k.ID)
		if err != nil {
			t.Fatalf("Revoke: %v", err)
		}
		if got.RevokedAt == nil {
			t.Fatal("RevokedAt is nil after Revoke")
		}
		if _, err := h.svc.Authenticate(context.Background(), plaintext); !apikey.IsUnauthorizedError(err) {
			t.Fatalf("err = %v, want *UnauthorizedError", err)
		}

		stored, err := h.store.ByPrefix(context.Background(), k.KeyPrefix)
		if err != nil {
			t.Fatalf("ByPrefix: %v", err)
		}
		if len(stored.SecretHash) == 0 {
			t.Fatal("Revoke blanked the stored secret hash")
		}
	})
	t.Run("a second Revoke is idempotent", func(t *testing.T) {
		h := newHarness(t)
		k, _ := h.mint(t)
		first, err := h.svc.Revoke(h.ctx, k.ID)
		if err != nil {
			t.Fatalf("Revoke: %v", err)
		}
		second, err := h.svc.Revoke(h.ctx, k.ID)
		if err != nil {
			t.Fatalf("second Revoke: %v", err)
		}
		if !second.RevokedAt.Equal(*first.RevokedAt) {
			t.Errorf("RevokedAt moved from %v to %v", first.RevokedAt, second.RevokedAt)
		}
	})
	t.Run("another org's key is not found", func(t *testing.T) {
		h := newHarness(t)
		k, _ := h.mint(t)
		foreign := tenant.Into(context.Background(), tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New()})
		if _, err := h.svc.Revoke(foreign, k.ID); !apikey.IsNotFoundError(err) {
			t.Fatalf("err = %v, want *NotFoundError", err)
		}
	})
	t.Run("another project in the same org is not found", func(t *testing.T) {
		h := newHarness(t)
		k, _ := h.mint(t)
		sibling := tenant.Into(context.Background(), tenant.Context{OrgID: h.tc.OrgID, ProjectID: uuid.New()})
		if _, err := h.svc.Revoke(sibling, k.ID); !apikey.IsNotFoundError(err) {
			t.Fatalf("err = %v, want *NotFoundError", err)
		}
	})
	t.Run("unknown id is not found", func(t *testing.T) {
		h := newHarness(t)
		if _, err := h.svc.Revoke(h.ctx, uuid.New()); !apikey.IsNotFoundError(err) {
			t.Fatalf("err = %v, want *NotFoundError", err)
		}
	})
	t.Run("missing tenant scope is refused", func(t *testing.T) {
		h := newHarness(t)
		if _, err := h.svc.Revoke(context.Background(), uuid.New()); !tenant.IsMissingError(err) {
			t.Fatalf("err = %v, want tenant.MissingError", err)
		}
	})
	t.Run("lookup failure routes through unexpected", func(t *testing.T) {
		h := newHarness(t)
		h.store.ByIDErr = errors.New("boom")
		if _, err := h.svc.Revoke(h.ctx, uuid.New()); err == nil {
			t.Fatal("want an error")
		}
		if *h.unex != 1 {
			t.Errorf("unexpected() called %d times, want 1", *h.unex)
		}
	})
	t.Run("save failure routes through unexpected", func(t *testing.T) {
		h := newHarness(t)
		k, _ := h.mint(t)
		h.store.SaveErr = errors.New("boom")
		if _, err := h.svc.Revoke(h.ctx, k.ID); err == nil {
			t.Fatal("want an error")
		}
		if *h.unex != 1 {
			t.Errorf("unexpected() called %d times, want 1", *h.unex)
		}
	})
}

func TestService_Delete(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		h := newHarness(t)
		k, _ := h.mint(t)
		if err := h.svc.Delete(h.ctx, k.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if h.store.Len() != 0 {
			t.Errorf("stored keys = %d, want 0", h.store.Len())
		}
	})
	t.Run("another org's key is not found", func(t *testing.T) {
		h := newHarness(t)
		k, _ := h.mint(t)
		foreign := tenant.Into(context.Background(), tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New()})
		if err := h.svc.Delete(foreign, k.ID); !apikey.IsNotFoundError(err) {
			t.Fatalf("err = %v, want *NotFoundError", err)
		}
		if h.store.Len() != 1 {
			t.Error("a cross-org Delete removed the key")
		}
	})
	t.Run("unknown id is not found", func(t *testing.T) {
		h := newHarness(t)
		if err := h.svc.Delete(h.ctx, uuid.New()); !apikey.IsNotFoundError(err) {
			t.Fatalf("err = %v, want *NotFoundError", err)
		}
	})
	t.Run("missing tenant scope is refused", func(t *testing.T) {
		h := newHarness(t)
		if err := h.svc.Delete(context.Background(), uuid.New()); !tenant.IsMissingError(err) {
			t.Fatalf("err = %v, want tenant.MissingError", err)
		}
	})
	t.Run("store failure routes through unexpected", func(t *testing.T) {
		h := newHarness(t)
		k, _ := h.mint(t)
		h.store.DeleteErr = errors.New("boom")
		if err := h.svc.Delete(h.ctx, k.ID); err == nil {
			t.Fatal("want an error")
		}
		if *h.unex != 1 {
			t.Errorf("unexpected() called %d times, want 1", *h.unex)
		}
	})
}

// TestService_Authenticate_EveryFailurePathIsByteIdentical is the point of the module: telling an
// attacker which guesses were closer is the leak, so all five rejections must be indistinguishable.
func TestService_Authenticate_EveryFailurePathIsByteIdentical(t *testing.T) {
	collect := func(t *testing.T, name string, fn func(h *harness) error) (string, string) {
		t.Helper()
		h := newHarness(t)
		err := fn(h)
		if err == nil {
			t.Fatalf("%s: want an error", name)
		}
		if !apikey.IsUnauthorizedError(err) {
			t.Fatalf("%s: err = %#v, want *UnauthorizedError", name, err)
		}
		if *h.unex != 0 {
			t.Fatalf("%s: an authentication failure must not route through unexpected", name)
		}
		app, ok := apperror.AsAppError(err)
		if !ok {
			t.Fatalf("%s: err does not convert to an AppError", name)
		}
		return err.Error(), app.Code()
	}

	paths := []struct {
		name string
		fn   func(h *harness) error
	}{
		{name: "missing", fn: func(h *harness) error {
			_, err := h.svc.Authenticate(context.Background(), "")
			return err
		}},
		{name: "malformed", fn: func(h *harness) error {
			_, err := h.svc.Authenticate(context.Background(), "osk_nope_nope")
			return err
		}},
		{name: "unknown", fn: func(h *harness) error {
			_, plaintext := h.mint(t)
			h.store = fakes.NewAPIKey()
			svc := apikey.NewService(h.store, slog.New(slog.NewTextHandler(io.Discard, nil)),
				func(_ context.Context, _ string, err error, _ ...any) *apperror.AppError {
					*h.unex++
					return apperror.New("opensheet.unexpected", err.Error(), codes.Internal,
						&apperrorv1.ErrorDetail{Code: "opensheet.unexpected"}).WithCause(err)
				}, h.sheets)
			_, err := svc.Authenticate(context.Background(), plaintext)
			return err
		}},
		{name: "wrong secret", fn: func(h *harness) error {
			k, _ := h.mint(t)
			_, err := h.svc.Authenticate(context.Background(), "osk_"+k.KeyPrefix+"_"+strings.Repeat("A", 43))
			return err
		}},
		{name: "revoked", fn: func(h *harness) error {
			k, plaintext := h.mint(t)
			if _, rErr := h.svc.Revoke(h.ctx, k.ID); rErr != nil {
				t.Fatalf("Revoke: %v", rErr)
			}
			_, err := h.svc.Authenticate(context.Background(), plaintext)
			return err
		}},
		{name: "expired", fn: func(h *harness) error {
			soon := time.Now().UTC().Add(50 * time.Millisecond)
			k, plaintext, cErr := h.svc.Create(h.ctx, apikey.CreateRequest{
				Name: "ci", Scopes: []string{authn.ScopeSheetsRead}, ExpiresAt: &soon,
			})
			if cErr != nil {
				t.Fatalf("Create: %v", cErr)
			}
			expired := soon.Add(-time.Hour)
			k.ExpiresAt = &expired
			if sErr := h.store.Save(context.Background(), k); sErr != nil {
				t.Fatalf("Save: %v", sErr)
			}
			_, err := h.svc.Authenticate(context.Background(), plaintext)
			return err
		}},
	}

	var messages, codesSeen []string
	for _, p := range paths {
		msg, code := collect(t, p.name, p.fn)
		messages = append(messages, msg)
		codesSeen = append(codesSeen, code)
	}
	for i := range messages {
		if messages[i] != messages[0] {
			t.Errorf("%s message = %q, want %q — the rejection is distinguishable",
				paths[i].name, messages[i], messages[0])
		}
		if codesSeen[i] != apperror.CodeAPIKeyUnauthorized {
			t.Errorf("%s code = %q, want KEY004", paths[i].name, codesSeen[i])
		}
	}
}

// TestService_Authenticate_UnknownPrefixPaysTheDummyHashCost pins the timing-oracle defence: the
// unknown-prefix path must run the same hash-and-compare a hit runs.
func TestService_Authenticate_UnknownPrefixPaysTheDummyHashCost(t *testing.T) {
	h := newHarness(t)
	_, plaintext := h.mint(t)

	if got := h.svc.DummyVerifications(); got != 0 {
		t.Fatalf("DummyVerifications = %d, want 0 before any miss", got)
	}
	if _, err := h.svc.Authenticate(context.Background(), plaintext); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if got := h.svc.DummyVerifications(); got != 0 {
		t.Errorf("DummyVerifications = %d after a hit, want 0", got)
	}

	unknown := "osk_" + strings.Repeat("0", 16) + "_" + strings.Repeat("A", 43)
	if _, err := h.svc.Authenticate(context.Background(), unknown); !apikey.IsUnauthorizedError(err) {
		t.Fatalf("err = %v, want *UnauthorizedError", err)
	}
	if got := h.svc.DummyVerifications(); got != 1 {
		t.Fatalf("DummyVerifications = %d after one miss, want exactly 1 — "+
			"0 means the compare was skipped, 2 means the dummy hash matched", got)
	}

	if _, err := h.svc.Authenticate(context.Background(), unknown); !apikey.IsUnauthorizedError(err) {
		t.Fatalf("err = %v, want *UnauthorizedError", err)
	}
	if got := h.svc.DummyVerifications(); got != 2 {
		t.Errorf("DummyVerifications = %d after two misses, want 2", got)
	}
}

func TestService_Authenticate(t *testing.T) {
	t.Run("a resolved key carries its own tenant identity", func(t *testing.T) {
		h := newHarness(t)
		k, plaintext := h.mint(t)
		got, err := h.svc.Authenticate(context.Background(), plaintext)
		if err != nil {
			t.Fatalf("Authenticate: %v", err)
		}
		if got.OrgID != k.OrgID || got.ProjectID != k.ProjectID {
			t.Error("the resolved key does not carry the minting tenant")
		}
	})
	t.Run("another org's scope is refused", func(t *testing.T) {
		h := newHarness(t)
		_, plaintext := h.mint(t)
		foreign := tenant.Into(context.Background(), tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New()})
		if _, err := h.svc.Authenticate(foreign, plaintext); !apikey.IsUnauthorizedError(err) {
			t.Fatalf("err = %v, want *UnauthorizedError", err)
		}
	})
	t.Run("its own org's scope is honoured", func(t *testing.T) {
		h := newHarness(t)
		k, plaintext := h.mint(t)
		got, err := h.svc.Authenticate(h.ctx, plaintext)
		if err != nil {
			t.Fatalf("Authenticate: %v", err)
		}
		if got.ID != k.ID {
			t.Error("Authenticate resolved a different key")
		}
	})
	t.Run("the resolved key carries no secret material out", func(t *testing.T) {
		h := newHarness(t)
		_, plaintext := h.mint(t)
		got, err := h.svc.Authenticate(context.Background(), plaintext)
		if err != nil {
			t.Fatalf("Authenticate: %v", err)
		}
		if len(got.SecretHash) != 0 {
			t.Error("Authenticate handed the secret digest to its caller")
		}
	})
	t.Run("the granted sheet set survives the round trip", func(t *testing.T) {
		h := newHarness(t)
		sheetID := uuid.New()
		h.sheets.Add(h.tc.OrgID, h.tc.ProjectID, sheetID)
		_, plaintext, err := h.svc.Create(h.ctx, apikey.CreateRequest{
			Name: "ci", Scopes: []string{authn.ScopeSheetsRead}, SheetIDs: []uuid.UUID{sheetID},
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		got, err := h.svc.Authenticate(context.Background(), plaintext)
		if err != nil {
			t.Fatalf("Authenticate: %v", err)
		}
		if !got.Allows(authn.ScopeSheetsRead, sheetID) {
			t.Error("Allows = false for the granted sheet")
		}
		if got.Allows(authn.ScopeSheetsRead, uuid.New()) {
			t.Error("Allows = true for a sheet outside the grant")
		}
	})
	t.Run("a store failure is reported, not disguised as unauthorized", func(t *testing.T) {
		h := newHarness(t)
		_, plaintext := h.mint(t)
		h.store.ByPrefixErr = errors.New("boom")
		_, err := h.svc.Authenticate(context.Background(), plaintext)
		if err == nil {
			t.Fatal("want an error")
		}
		if apikey.IsUnauthorizedError(err) {
			t.Error("an infrastructure failure must not present as an authentication failure")
		}
		if *h.unex != 1 {
			t.Errorf("unexpected() called %d times, want 1", *h.unex)
		}
	})
	t.Run("LastUsedAt is left to the worker", func(t *testing.T) {
		h := newHarness(t)
		k, plaintext := h.mint(t)
		got, err := h.svc.Authenticate(context.Background(), plaintext)
		if err != nil {
			t.Fatalf("Authenticate: %v", err)
		}
		if got.LastUsedAt != nil {
			t.Error("Authenticate wrote LastUsedAt on the read path")
		}
		stored, err := h.store.ByPrefix(context.Background(), k.KeyPrefix)
		if err != nil {
			t.Fatalf("ByPrefix: %v", err)
		}
		if stored.LastUsedAt != nil {
			t.Error("Authenticate persisted LastUsedAt on the read path")
		}
	})
}
