package spreadsheet_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/oauth2"
	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/gsheets"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/spreadsheet"
	"altalune.id/opensheet/internal/testutil/fakes"
)

const goodFileID = "1AbC-_dEf"

type staticTokenSource struct{}

func (staticTokenSource) Token() (*oauth2.Token, error) {
	return &oauth2.Token{AccessToken: "dummy", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour)}, nil
}

type fakeTokenSources struct {
	err   error
	calls []uuid.UUID
}

func (f *fakeTokenSources) TokenSourceFor(_ context.Context, credentialID uuid.UUID) (gsheets.TokenSource, error) {
	f.calls = append(f.calls, credentialID)
	if f.err != nil {
		return nil, f.err
	}
	return staticTokenSource{}, nil
}

// appErrStub stands in for another module's typed error: it carries a wire code, so it must pass through.
type appErrStub struct{}

func (*appErrStub) Error() string { return "credential: reauthorization needed" }

func (*appErrStub) ToAppError() *apperror.AppError {
	return apperror.New("CRD006", "reconnect it", codes.FailedPrecondition,
		&apperrorv1.ErrorDetail{Code: "CRD006"})
}

func sheetsServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if status != http.StatusOK {
			w.WriteHeader(status)
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func factoryFor(t *testing.T, baseURL string) gsheets.Factory {
	t.Helper()
	return func(ctx context.Context, ts gsheets.TokenSource) (*gsheets.Client, error) {
		return gsheets.New(ctx, ts, gsheets.WithBaseURL(baseURL))
	}
}

func newSvc(t *testing.T, store spreadsheet.Store, tokens spreadsheet.TokenSources, clients gsheets.Factory) (*spreadsheet.Service, *int) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	calls := 0
	unexpected := func(_ context.Context, _ string, err error, _ ...any) *apperror.AppError {
		calls++
		return apperror.New(apperror.CodeUnexpectedError, err.Error(), codes.Internal,
			&apperrorv1.ErrorDetail{Code: apperror.CodeUnexpectedError}).WithCause(err)
	}
	return spreadsheet.NewService(store, log, unexpected, tokens, clients), &calls
}

func newStoreSvc(t *testing.T, store spreadsheet.Store) (*spreadsheet.Service, *int) {
	t.Helper()
	return newSvc(t, store, &fakeTokenSources{}, factoryFor(t, "http://127.0.0.1:1"))
}

func tenantCtx(t *testing.T) (context.Context, tenant.Context) {
	t.Helper()
	tc := tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New(), UserID: uuid.New()}
	return tenant.Into(context.Background(), tc), tc
}

func seed(t *testing.T, store *fakes.Spreadsheet, tc tenant.Context, fileID string) *spreadsheet.Spreadsheet {
	t.Helper()
	sp, err := spreadsheet.New(tc.OrgID, tc.ProjectID, uuid.New(), fileID, "Prices")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := store.Save(context.Background(), sp); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return sp
}

func TestService_Register(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		svc, unex := newStoreSvc(t, fakes.NewSpreadsheet())
		ctx, tc := tenantCtx(t)
		credID := uuid.New()

		got, err := svc.Register(ctx, credID, goodFileID, "  Prices  ")
		if err != nil {
			t.Fatalf("Register: %v", err)
		}
		if got.GoogleFileID != goodFileID || got.Title != "Prices" {
			t.Errorf("got %+v", got)
		}
		if got.OrgID != tc.OrgID || got.ProjectID != tc.ProjectID {
			t.Error("tenant scope not propagated")
		}
		if got.CredentialID != credID {
			t.Error("credential not bound")
		}
		if *unex != 0 {
			t.Errorf("unexpected() called %d times", *unex)
		}
	})
	t.Run("pasted url bubbles InvalidFileIDError", func(t *testing.T) {
		svc, unex := newStoreSvc(t, fakes.NewSpreadsheet())
		ctx, _ := tenantCtx(t)
		_, err := svc.Register(ctx, uuid.New(), "https://docs.google.com/spreadsheets/d/ABC/edit", "x")
		if !spreadsheet.IsInvalidFileIDError(err) {
			t.Fatalf("want IsInvalidFileIDError, got %T: %v", err, err)
		}
		if *unex != 0 {
			t.Error("an invariant failure must not route through unexpected")
		}
	})
	t.Run("long title bubbles InvalidTitleError", func(t *testing.T) {
		svc, _ := newStoreSvc(t, fakes.NewSpreadsheet())
		ctx, _ := tenantCtx(t)
		_, err := svc.Register(ctx, uuid.New(), goodFileID, strings.Repeat("a", 201))
		if !spreadsheet.IsInvalidTitleError(err) {
			t.Fatalf("want IsInvalidTitleError, got %T: %v", err, err)
		}
	})
	t.Run("nil credential bubbles InvalidCredentialError", func(t *testing.T) {
		svc, _ := newStoreSvc(t, fakes.NewSpreadsheet())
		ctx, _ := tenantCtx(t)
		_, err := svc.Register(ctx, uuid.Nil, goodFileID, "x")
		if !spreadsheet.IsInvalidCredentialError(err) {
			t.Fatalf("want IsInvalidCredentialError, got %T: %v", err, err)
		}
	})
	t.Run("registering the same document twice is AlreadyExistsError", func(t *testing.T) {
		svc, unex := newStoreSvc(t, fakes.NewSpreadsheet())
		ctx, _ := tenantCtx(t)
		if _, err := svc.Register(ctx, uuid.New(), goodFileID, "x"); err != nil {
			t.Fatalf("first Register: %v", err)
		}
		_, err := svc.Register(ctx, uuid.New(), goodFileID, "x")
		if !spreadsheet.IsAlreadyExistsError(err) {
			t.Fatalf("want IsAlreadyExistsError, got %T: %v", err, err)
		}
		if *unex != 0 {
			t.Error("a duplicate is expected, not an incident")
		}
	})
	t.Run("missing tenant returns MissingError", func(t *testing.T) {
		svc, _ := newStoreSvc(t, fakes.NewSpreadsheet())
		_, err := svc.Register(context.Background(), uuid.New(), goodFileID, "x")
		if !tenant.IsMissingError(err) {
			t.Fatalf("want tenant.MissingError, got %T: %v", err, err)
		}
	})
	t.Run("store failure routes through unexpected", func(t *testing.T) {
		store := fakes.NewSpreadsheet()
		store.SaveFn = func(context.Context, *spreadsheet.Spreadsheet) error { return errors.New("boom") }
		svc, unex := newStoreSvc(t, store)
		ctx, _ := tenantCtx(t)
		_, err := svc.Register(ctx, uuid.New(), goodFileID, "x")
		if err == nil {
			t.Fatal("want error")
		}
		if *unex != 1 {
			t.Errorf("unexpected() called %d times, want 1", *unex)
		}
	})
}

func TestService_List(t *testing.T) {
	t.Run("returns only the caller's project", func(t *testing.T) {
		store := fakes.NewSpreadsheet()
		svc, _ := newStoreSvc(t, store)
		ctx, tc := tenantCtx(t)
		seed(t, store, tc, "AAA")
		seed(t, store, tc, "BBB")
		seed(t, store, tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New()}, "CCC")

		got, err := svc.List(ctx)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d spreadsheets, want 2", len(got))
		}
	})
	t.Run("missing tenant returns MissingError", func(t *testing.T) {
		svc, _ := newStoreSvc(t, fakes.NewSpreadsheet())
		_, err := svc.List(context.Background())
		if !tenant.IsMissingError(err) {
			t.Fatalf("want tenant.MissingError, got %T: %v", err, err)
		}
	})
	t.Run("store failure routes through unexpected", func(t *testing.T) {
		store := fakes.NewSpreadsheet()
		store.ListFn = func(context.Context, uuid.UUID, uuid.UUID) ([]*spreadsheet.Spreadsheet, error) {
			return nil, errors.New("boom")
		}
		svc, unex := newStoreSvc(t, store)
		ctx, _ := tenantCtx(t)
		if _, err := svc.List(ctx); err == nil {
			t.Fatal("want error")
		}
		if *unex != 1 {
			t.Errorf("unexpected() called %d times, want 1", *unex)
		}
	})
}

func TestService_ByID(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		store := fakes.NewSpreadsheet()
		svc, _ := newStoreSvc(t, store)
		ctx, tc := tenantCtx(t)
		sp := seed(t, store, tc, goodFileID)

		got, err := svc.ByID(ctx, sp.ID)
		if err != nil {
			t.Fatalf("ByID: %v", err)
		}
		if got.ID != sp.ID {
			t.Errorf("got %v, want %v", got.ID, sp.ID)
		}
	})
	t.Run("unknown id is NotFoundError", func(t *testing.T) {
		svc, unex := newStoreSvc(t, fakes.NewSpreadsheet())
		ctx, _ := tenantCtx(t)
		_, err := svc.ByID(ctx, uuid.New())
		if !spreadsheet.IsNotFoundError(err) {
			t.Fatalf("want IsNotFoundError, got %T: %v", err, err)
		}
		if *unex != 0 {
			t.Error("a miss is expected, not an incident")
		}
	})
	t.Run("another org's row is NotFoundError", func(t *testing.T) {
		store := fakes.NewSpreadsheet()
		svc, _ := newStoreSvc(t, store)
		ctx, _ := tenantCtx(t)
		other := seed(t, store, tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New()}, goodFileID)

		_, err := svc.ByID(ctx, other.ID)
		if !spreadsheet.IsNotFoundError(err) {
			t.Fatalf("want IsNotFoundError, got %T: %v", err, err)
		}
	})
	t.Run("another project in the same org is NotFoundError", func(t *testing.T) {
		store := fakes.NewSpreadsheet()
		svc, _ := newStoreSvc(t, store)
		ctx, tc := tenantCtx(t)
		other := seed(t, store, tenant.Context{OrgID: tc.OrgID, ProjectID: uuid.New()}, goodFileID)

		_, err := svc.ByID(ctx, other.ID)
		if !spreadsheet.IsNotFoundError(err) {
			t.Fatalf("want IsNotFoundError, got %T: %v", err, err)
		}
	})
	t.Run("missing tenant returns MissingError", func(t *testing.T) {
		svc, _ := newStoreSvc(t, fakes.NewSpreadsheet())
		_, err := svc.ByID(context.Background(), uuid.New())
		if !tenant.IsMissingError(err) {
			t.Fatalf("want tenant.MissingError, got %T: %v", err, err)
		}
	})
	t.Run("store failure routes through unexpected", func(t *testing.T) {
		store := fakes.NewSpreadsheet()
		store.ByIDFn = func(context.Context, uuid.UUID) (*spreadsheet.Spreadsheet, error) {
			return nil, errors.New("boom")
		}
		svc, unex := newStoreSvc(t, store)
		ctx, _ := tenantCtx(t)
		if _, err := svc.ByID(ctx, uuid.New()); err == nil {
			t.Fatal("want error")
		}
		if *unex != 1 {
			t.Errorf("unexpected() called %d times, want 1", *unex)
		}
	})
}

func TestService_ByGoogleFileID(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		store := fakes.NewSpreadsheet()
		svc, _ := newStoreSvc(t, store)
		ctx, tc := tenantCtx(t)
		sp := seed(t, store, tc, goodFileID)

		got, err := svc.ByGoogleFileID(ctx, goodFileID)
		if err != nil {
			t.Fatalf("ByGoogleFileID: %v", err)
		}
		if got.ID != sp.ID {
			t.Errorf("got %v, want %v", got.ID, sp.ID)
		}
	})
	t.Run("unknown file id is NotFoundError", func(t *testing.T) {
		svc, unex := newStoreSvc(t, fakes.NewSpreadsheet())
		ctx, _ := tenantCtx(t)
		_, err := svc.ByGoogleFileID(ctx, "MISSING")
		if !spreadsheet.IsNotFoundError(err) {
			t.Fatalf("want IsNotFoundError, got %T: %v", err, err)
		}
		if *unex != 0 {
			t.Error("a miss is expected, not an incident")
		}
	})
	t.Run("another project's registration is not visible", func(t *testing.T) {
		store := fakes.NewSpreadsheet()
		svc, _ := newStoreSvc(t, store)
		ctx, tc := tenantCtx(t)
		seed(t, store, tenant.Context{OrgID: tc.OrgID, ProjectID: uuid.New()}, goodFileID)

		_, err := svc.ByGoogleFileID(ctx, goodFileID)
		if !spreadsheet.IsNotFoundError(err) {
			t.Fatalf("want IsNotFoundError, got %T: %v", err, err)
		}
	})
	t.Run("missing tenant returns MissingError", func(t *testing.T) {
		svc, _ := newStoreSvc(t, fakes.NewSpreadsheet())
		_, err := svc.ByGoogleFileID(context.Background(), goodFileID)
		if !tenant.IsMissingError(err) {
			t.Fatalf("want tenant.MissingError, got %T: %v", err, err)
		}
	})
	t.Run("store failure routes through unexpected", func(t *testing.T) {
		store := fakes.NewSpreadsheet()
		store.ByGoogleFileIDFn = func(context.Context, uuid.UUID, uuid.UUID, string) (*spreadsheet.Spreadsheet, error) {
			return nil, errors.New("boom")
		}
		svc, unex := newStoreSvc(t, store)
		ctx, _ := tenantCtx(t)
		if _, err := svc.ByGoogleFileID(ctx, goodFileID); err == nil {
			t.Fatal("want error")
		}
		if *unex != 1 {
			t.Errorf("unexpected() called %d times, want 1", *unex)
		}
	})
}

func TestService_Retitle(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		store := fakes.NewSpreadsheet()
		svc, _ := newStoreSvc(t, store)
		ctx, tc := tenantCtx(t)
		sp := seed(t, store, tc, goodFileID)

		got, err := svc.Retitle(ctx, sp.ID, "  Renamed  ")
		if err != nil {
			t.Fatalf("Retitle: %v", err)
		}
		if got.Title != "Renamed" {
			t.Errorf("Title = %q", got.Title)
		}
		reread, err := store.ByID(ctx, sp.ID)
		if err != nil {
			t.Fatalf("ByID: %v", err)
		}
		if reread.Title != "Renamed" {
			t.Errorf("not persisted: %q", reread.Title)
		}
	})
	t.Run("long title bubbles InvalidTitleError", func(t *testing.T) {
		store := fakes.NewSpreadsheet()
		svc, unex := newStoreSvc(t, store)
		ctx, tc := tenantCtx(t)
		sp := seed(t, store, tc, goodFileID)

		_, err := svc.Retitle(ctx, sp.ID, strings.Repeat("a", 201))
		if !spreadsheet.IsInvalidTitleError(err) {
			t.Fatalf("want IsInvalidTitleError, got %T: %v", err, err)
		}
		if *unex != 0 {
			t.Error("an invariant failure must not route through unexpected")
		}
	})
	t.Run("unknown id is NotFoundError", func(t *testing.T) {
		svc, _ := newStoreSvc(t, fakes.NewSpreadsheet())
		ctx, _ := tenantCtx(t)
		_, err := svc.Retitle(ctx, uuid.New(), "x")
		if !spreadsheet.IsNotFoundError(err) {
			t.Fatalf("want IsNotFoundError, got %T: %v", err, err)
		}
	})
	t.Run("save failure routes through unexpected", func(t *testing.T) {
		store := fakes.NewSpreadsheet()
		svc, unex := newStoreSvc(t, store)
		ctx, tc := tenantCtx(t)
		sp := seed(t, store, tc, goodFileID)
		store.SaveFn = func(context.Context, *spreadsheet.Spreadsheet) error { return errors.New("boom") }

		if _, err := svc.Retitle(ctx, sp.ID, "x"); err == nil {
			t.Fatal("want error")
		}
		if *unex != 1 {
			t.Errorf("unexpected() called %d times, want 1", *unex)
		}
	})
}

func TestService_Rebind(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		store := fakes.NewSpreadsheet()
		svc, _ := newStoreSvc(t, store)
		ctx, tc := tenantCtx(t)
		sp := seed(t, store, tc, goodFileID)
		next := uuid.New()

		got, err := svc.Rebind(ctx, sp.ID, next)
		if err != nil {
			t.Fatalf("Rebind: %v", err)
		}
		if got.CredentialID != next {
			t.Errorf("CredentialID = %v, want %v", got.CredentialID, next)
		}
		reread, err := store.ByID(ctx, sp.ID)
		if err != nil {
			t.Fatalf("ByID: %v", err)
		}
		if reread.CredentialID != next {
			t.Error("not persisted")
		}
	})
	t.Run("nil credential bubbles InvalidCredentialError", func(t *testing.T) {
		store := fakes.NewSpreadsheet()
		svc, unex := newStoreSvc(t, store)
		ctx, tc := tenantCtx(t)
		sp := seed(t, store, tc, goodFileID)

		_, err := svc.Rebind(ctx, sp.ID, uuid.Nil)
		if !spreadsheet.IsInvalidCredentialError(err) {
			t.Fatalf("want IsInvalidCredentialError, got %T: %v", err, err)
		}
		if *unex != 0 {
			t.Error("an invariant failure must not route through unexpected")
		}
	})
	t.Run("unknown id is NotFoundError", func(t *testing.T) {
		svc, _ := newStoreSvc(t, fakes.NewSpreadsheet())
		ctx, _ := tenantCtx(t)
		_, err := svc.Rebind(ctx, uuid.New(), uuid.New())
		if !spreadsheet.IsNotFoundError(err) {
			t.Fatalf("want IsNotFoundError, got %T: %v", err, err)
		}
	})
	t.Run("save failure routes through unexpected", func(t *testing.T) {
		store := fakes.NewSpreadsheet()
		svc, unex := newStoreSvc(t, store)
		ctx, tc := tenantCtx(t)
		sp := seed(t, store, tc, goodFileID)
		store.SaveFn = func(context.Context, *spreadsheet.Spreadsheet) error { return errors.New("boom") }

		if _, err := svc.Rebind(ctx, sp.ID, uuid.New()); err == nil {
			t.Fatal("want error")
		}
		if *unex != 1 {
			t.Errorf("unexpected() called %d times, want 1", *unex)
		}
	})
}

func TestService_Delete(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		store := fakes.NewSpreadsheet()
		svc, _ := newStoreSvc(t, store)
		ctx, tc := tenantCtx(t)
		sp := seed(t, store, tc, goodFileID)

		if err := svc.Delete(ctx, sp.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := store.ByID(ctx, sp.ID); !spreadsheet.IsNotFoundError(err) {
			t.Fatalf("row survived: %v", err)
		}
	})
	t.Run("unknown id is NotFoundError", func(t *testing.T) {
		svc, unex := newStoreSvc(t, fakes.NewSpreadsheet())
		ctx, _ := tenantCtx(t)
		err := svc.Delete(ctx, uuid.New())
		if !spreadsheet.IsNotFoundError(err) {
			t.Fatalf("want IsNotFoundError, got %T: %v", err, err)
		}
		if *unex != 0 {
			t.Error("a miss is expected, not an incident")
		}
	})
	t.Run("another org's row is NotFoundError", func(t *testing.T) {
		store := fakes.NewSpreadsheet()
		svc, _ := newStoreSvc(t, store)
		ctx, _ := tenantCtx(t)
		other := seed(t, store, tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New()}, goodFileID)

		if err := svc.Delete(ctx, other.ID); !spreadsheet.IsNotFoundError(err) {
			t.Fatalf("want IsNotFoundError, got %T: %v", err, err)
		}
	})
	t.Run("missing tenant returns MissingError", func(t *testing.T) {
		svc, _ := newStoreSvc(t, fakes.NewSpreadsheet())
		if err := svc.Delete(context.Background(), uuid.New()); !tenant.IsMissingError(err) {
			t.Fatalf("want tenant.MissingError, got %T: %v", err, err)
		}
	})
	t.Run("delete failure routes through unexpected", func(t *testing.T) {
		store := fakes.NewSpreadsheet()
		svc, unex := newStoreSvc(t, store)
		ctx, tc := tenantCtx(t)
		sp := seed(t, store, tc, goodFileID)
		store.DeleteFn = func(context.Context, uuid.UUID) error { return errors.New("boom") }

		if err := svc.Delete(ctx, sp.ID); err == nil {
			t.Fatal("want error")
		}
		if *unex != 1 {
			t.Errorf("unexpected() called %d times, want 1", *unex)
		}
	})
	t.Run("a delete race reports the miss, not an incident", func(t *testing.T) {
		store := fakes.NewSpreadsheet()
		svc, unex := newStoreSvc(t, store)
		ctx, tc := tenantCtx(t)
		sp := seed(t, store, tc, goodFileID)
		store.DeleteFn = func(_ context.Context, id uuid.UUID) error {
			return &spreadsheet.NotFoundError{ID: id.String()}
		}

		if err := svc.Delete(ctx, sp.ID); !spreadsheet.IsNotFoundError(err) {
			t.Fatalf("want IsNotFoundError, got %T: %v", err, err)
		}
		if *unex != 0 {
			t.Error("a miss is expected, not an incident")
		}
	})
}

func TestService_ListTabs(t *testing.T) {
	const tabsBody = `{"properties":{"title":"Prices"},"sheets":[{"properties":{"title":"Q1"}},{"properties":{"title":"Q2"}}]}`

	t.Run("returns google tab titles in sheet order", func(t *testing.T) {
		store := fakes.NewSpreadsheet()
		ctx, tc := tenantCtx(t)
		sp := seed(t, store, tc, goodFileID)
		tokens := &fakeTokenSources{}
		svc, unex := newSvc(t, store, tokens, factoryFor(t, sheetsServer(t, http.StatusOK, tabsBody).URL))

		got, err := svc.ListTabs(ctx, sp.ID)
		if err != nil {
			t.Fatalf("ListTabs: %v", err)
		}
		if len(got) != 2 || got[0] != "Q1" || got[1] != "Q2" {
			t.Fatalf("tabs = %v, want [Q1 Q2]", got)
		}
		if len(tokens.calls) != 1 || tokens.calls[0] != sp.CredentialID {
			t.Errorf("token source asked for %v, want %v", tokens.calls, sp.CredentialID)
		}
		if *unex != 0 {
			t.Errorf("unexpected() called %d times", *unex)
		}
	})
	t.Run("unknown spreadsheet is NotFoundError", func(t *testing.T) {
		svc, _ := newSvc(t, fakes.NewSpreadsheet(), &fakeTokenSources{}, factoryFor(t, "http://127.0.0.1:1"))
		ctx, _ := tenantCtx(t)
		_, err := svc.ListTabs(ctx, uuid.New())
		if !spreadsheet.IsNotFoundError(err) {
			t.Fatalf("want IsNotFoundError, got %T: %v", err, err)
		}
	})
	t.Run("another org's spreadsheet is NotFoundError", func(t *testing.T) {
		store := fakes.NewSpreadsheet()
		ctx, _ := tenantCtx(t)
		other := seed(t, store, tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New()}, goodFileID)
		svc, _ := newSvc(t, store, &fakeTokenSources{}, factoryFor(t, sheetsServer(t, http.StatusOK, tabsBody).URL))

		_, err := svc.ListTabs(ctx, other.ID)
		if !spreadsheet.IsNotFoundError(err) {
			t.Fatalf("want IsNotFoundError, got %T: %v", err, err)
		}
	})
	t.Run("a coded token-source failure passes through", func(t *testing.T) {
		store := fakes.NewSpreadsheet()
		ctx, tc := tenantCtx(t)
		sp := seed(t, store, tc, goodFileID)
		svc, unex := newSvc(t, store, &fakeTokenSources{err: &appErrStub{}}, factoryFor(t, "http://127.0.0.1:1"))

		_, err := svc.ListTabs(ctx, sp.ID)
		var stub *appErrStub
		if !errors.As(err, &stub) {
			t.Fatalf("want *appErrStub, got %T: %v", err, err)
		}
		if *unex != 0 {
			t.Error("a coded credential failure is expected, not an incident")
		}
	})
	t.Run("an uncoded token-source failure routes through unexpected", func(t *testing.T) {
		store := fakes.NewSpreadsheet()
		ctx, tc := tenantCtx(t)
		sp := seed(t, store, tc, goodFileID)
		svc, unex := newSvc(t, store, &fakeTokenSources{err: errors.New("boom")}, factoryFor(t, "http://127.0.0.1:1"))

		if _, err := svc.ListTabs(ctx, sp.ID); err == nil {
			t.Fatal("want error")
		}
		if *unex != 1 {
			t.Errorf("unexpected() called %d times, want 1", *unex)
		}
	})
	t.Run("a factory failure routes through unexpected", func(t *testing.T) {
		store := fakes.NewSpreadsheet()
		ctx, tc := tenantCtx(t)
		sp := seed(t, store, tc, goodFileID)
		factory := func(context.Context, gsheets.TokenSource) (*gsheets.Client, error) {
			return nil, errors.New("cannot build client")
		}
		svc, unex := newSvc(t, store, &fakeTokenSources{}, factory)

		if _, err := svc.ListTabs(ctx, sp.ID); err == nil {
			t.Fatal("want error")
		}
		if *unex != 1 {
			t.Errorf("unexpected() called %d times, want 1", *unex)
		}
	})

	googleFailures := []struct {
		name    string
		status  int
		body    string
		wantErr func(error) bool
	}{
		{name: "404 is a gone google document", status: http.StatusNotFound, body: `{"error":{"code":404}}`, wantErr: gsheets.IsNotFoundError},
		{name: "403 is permission denied", status: http.StatusForbidden, body: `{"error":{"code":403}}`, wantErr: gsheets.IsPermissionDeniedError},
		{name: "429 is quota exceeded", status: http.StatusTooManyRequests, body: `{"error":{"code":429}}`, wantErr: gsheets.IsQuotaExceededError},
		{name: "401 is an expired credential", status: http.StatusUnauthorized, body: `{"error":{"code":401}}`, wantErr: gsheets.IsAuthExpiredError},
		{name: "500 is unavailable", status: http.StatusInternalServerError, body: `{"error":{"code":500}}`, wantErr: gsheets.IsUnavailableError},
	}
	for _, tc := range googleFailures {
		t.Run(tc.name, func(t *testing.T) {
			store := fakes.NewSpreadsheet()
			ctx, tctx := tenantCtx(t)
			sp := seed(t, store, tctx, goodFileID)
			svc, unex := newSvc(t, store, &fakeTokenSources{}, factoryFor(t, sheetsServer(t, tc.status, tc.body).URL))

			_, err := svc.ListTabs(ctx, sp.ID)
			if !tc.wantErr(err) {
				t.Fatalf("wrong error type: %T: %v", err, err)
			}
			if *unex != 0 {
				t.Error("a google failure is expected and shown to the user, not an incident")
			}
			if _, ok := apperror.AsAppError(err); !ok {
				t.Error("the translated error must carry a wire code")
			}
		})
	}
}
