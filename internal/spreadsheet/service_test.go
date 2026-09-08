package spreadsheet_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/oauth2"
	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/gworkspace"
	"altalune.id/opensheet/gworkspace/gsheet"
	"altalune.id/opensheet/internal/apperror"
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

func (f *fakeTokenSources) TokenSourceFor(_ context.Context, credentialID uuid.UUID) (oauth2.TokenSource, error) {
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

func factoryFor(t *testing.T, baseURL string) gsheet.Factory {
	t.Helper()
	return func(ctx context.Context, ts oauth2.TokenSource) (*gsheet.Client, error) {
		return gsheet.New(ctx, ts, gworkspace.WithBaseURL(baseURL))
	}
}

func writerFactoryFor(t *testing.T, baseURL string) gsheet.WriterFactory {
	t.Helper()
	return func(ctx context.Context, ts oauth2.TokenSource) (*gsheet.Writer, error) {
		return gsheet.NewWriter(ctx, ts, gworkspace.WithBaseURL(baseURL))
	}
}

func newFullSvc(
	t *testing.T,
	store spreadsheet.Store,
	tokens spreadsheet.TokenSources,
	clients gsheet.Factory,
	writeTokens spreadsheet.TokenSources,
	writers gsheet.WriterFactory,
) (*spreadsheet.Service, *int) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	calls := 0
	unexpected := func(_ context.Context, _ string, err error, _ ...any) *apperror.AppError {
		calls++
		return apperror.New(apperror.CodeUnexpectedError, err.Error(), codes.Internal,
			&apperrorv1.ErrorDetail{Code: apperror.CodeUnexpectedError}).WithCause(err)
	}
	return spreadsheet.NewService(store, log, unexpected, tokens, clients, writeTokens, writers), &calls
}

func newSvc(t *testing.T, store spreadsheet.Store, tokens spreadsheet.TokenSources, clients gsheet.Factory) (*spreadsheet.Service, *int) {
	t.Helper()
	return newFullSvc(t, store, tokens, clients, &fakeTokenSources{}, writerFactoryFor(t, "http://127.0.0.1:1"))
}

func newWriteSvc(t *testing.T, store spreadsheet.Store, writeTokens spreadsheet.TokenSources, writers gsheet.WriterFactory) (*spreadsheet.Service, *int) {
	t.Helper()
	return newFullSvc(t, store, &fakeTokenSources{}, factoryFor(t, "http://127.0.0.1:1"), writeTokens, writers)
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

func TestService_SetWritable(t *testing.T) {
	t.Run("persists both directions", func(t *testing.T) {
		store := fakes.NewSpreadsheet()
		svc, _ := newStoreSvc(t, store)
		ctx, tc := tenantCtx(t)
		sp := seed(t, store, tc, goodFileID)
		if sp.Writable {
			t.Fatal("registration must not imply write permission")
		}

		got, err := svc.SetWritable(ctx, sp.ID, true)
		if err != nil {
			t.Fatalf("SetWritable: %v", err)
		}
		if !got.Writable {
			t.Error("returned spreadsheet is not writable")
		}
		reread, err := store.ByID(ctx, sp.ID)
		if err != nil {
			t.Fatalf("ByID: %v", err)
		}
		if !reread.Writable {
			t.Error("not persisted: the flag must reach the store, not just the returned aggregate")
		}

		if _, err := svc.SetWritable(ctx, sp.ID, false); err != nil {
			t.Fatalf("SetWritable(false): %v", err)
		}
		reread, err = store.ByID(ctx, sp.ID)
		if err != nil {
			t.Fatalf("ByID: %v", err)
		}
		if reread.Writable {
			t.Error("clearing the flag was not persisted")
		}
	})
	t.Run("unknown id is NotFoundError", func(t *testing.T) {
		svc, _ := newStoreSvc(t, fakes.NewSpreadsheet())
		ctx, _ := tenantCtx(t)
		_, err := svc.SetWritable(ctx, uuid.New(), true)
		if !spreadsheet.IsNotFoundError(err) {
			t.Fatalf("want IsNotFoundError, got %T: %v", err, err)
		}
	})
	t.Run("another project cannot reach the row", func(t *testing.T) {
		store := fakes.NewSpreadsheet()
		svc, _ := newStoreSvc(t, store)
		_, tc := tenantCtx(t)
		sp := seed(t, store, tc, goodFileID)

		other := tenant.Into(context.Background(), tenant.Context{OrgID: tc.OrgID, ProjectID: uuid.New()})
		_, err := svc.SetWritable(other, sp.ID, true)
		if !spreadsheet.IsNotFoundError(err) {
			t.Fatalf("want IsNotFoundError, got %T: %v", err, err)
		}
	})
	t.Run("missing tenant returns MissingError", func(t *testing.T) {
		svc, _ := newStoreSvc(t, fakes.NewSpreadsheet())
		_, err := svc.SetWritable(context.Background(), uuid.New(), true)
		if !tenant.IsMissingError(err) {
			t.Fatalf("want tenant.MissingError, got %T: %v", err, err)
		}
	})
	t.Run("save failure routes through unexpected", func(t *testing.T) {
		store := fakes.NewSpreadsheet()
		svc, unex := newStoreSvc(t, store)
		ctx, tc := tenantCtx(t)
		sp := seed(t, store, tc, goodFileID)
		store.SaveFn = func(context.Context, *spreadsheet.Spreadsheet) error { return errors.New("boom") }

		if _, err := svc.SetWritable(ctx, sp.ID, true); err == nil {
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
		factory := func(context.Context, oauth2.TokenSource) (*gsheet.Client, error) {
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
		{name: "404 is a gone google document", status: http.StatusNotFound, body: `{"error":{"code":404}}`, wantErr: gworkspace.IsNotFoundError},
		{name: "403 is permission denied", status: http.StatusForbidden, body: `{"error":{"code":403}}`, wantErr: gworkspace.IsPermissionDeniedError},
		{name: "429 is quota exceeded", status: http.StatusTooManyRequests, body: `{"error":{"code":429}}`, wantErr: gworkspace.IsQuotaExceededError},
		{name: "401 is an expired credential", status: http.StatusUnauthorized, body: `{"error":{"code":401}}`, wantErr: gworkspace.IsAuthExpiredError},
		{name: "500 is unavailable", status: http.StatusInternalServerError, body: `{"error":{"code":500}}`, wantErr: gworkspace.IsUnavailableError},
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

type recordedRequest struct {
	method string
	path   string
	body   string
}

type recordingSheets struct {
	mu       sync.Mutex
	requests []recordedRequest

	status int
	body   string
	url    string
}

func newRecordingSheets(t *testing.T, status int, body string) *recordingSheets {
	t.Helper()
	f := &recordingSheets{status: status, body: body}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	f.url = srv.URL
	return f
}

func (f *recordingSheets) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.requests = append(f.requests, recordedRequest{method: r.Method, path: r.URL.Path, body: strings.TrimSpace(string(body))})
	f.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if f.status != http.StatusOK {
		w.WriteHeader(f.status)
	}
	_, _ = w.Write([]byte(f.body))
}

func (f *recordingSheets) recorded() []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.requests)
}

func (f *recordingSheets) writers(t *testing.T) gsheet.WriterFactory {
	t.Helper()
	return writerFactoryFor(t, f.url)
}

func seedWritable(t *testing.T, store *fakes.Spreadsheet, tc tenant.Context, fileID string) *spreadsheet.Spreadsheet {
	t.Helper()
	sp := seed(t, store, tc, fileID)
	sp.SetWritable(true)
	if err := store.Save(context.Background(), sp); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return sp
}

func TestService_AddTab(t *testing.T) {
	t.Run("refuses a non-writable spreadsheet before any google call", func(t *testing.T) {
		store := fakes.NewSpreadsheet()
		ctx, tc := tenantCtx(t)
		sp := seed(t, store, tc, goodFileID)
		google := newRecordingSheets(t, http.StatusOK, `{}`)
		svc, unex := newWriteSvc(t, store, &fakeTokenSources{}, google.writers(t))

		err := svc.AddTab(ctx, sp.ID, "Q2")
		if !spreadsheet.IsNotWritableError(err) {
			t.Fatalf("want IsNotWritableError, got %T: %v", err, err)
		}
		if got := google.recorded(); len(got) != 0 {
			t.Errorf("no request may reach Google for a non-writable spreadsheet, got %v", got)
		}
		if *unex != 0 {
			t.Error("a refusal is expected, not an incident")
		}
		if _, ok := apperror.AsAppError(err); !ok {
			t.Error("the refusal must carry a wire code")
		}
	})
	t.Run("creates the tab through a batchUpdate", func(t *testing.T) {
		store := fakes.NewSpreadsheet()
		ctx, tc := tenantCtx(t)
		sp := seedWritable(t, store, tc, goodFileID)
		google := newRecordingSheets(t, http.StatusOK, `{}`)
		svc, unex := newWriteSvc(t, store, &fakeTokenSources{}, google.writers(t))

		if err := svc.AddTab(ctx, sp.ID, "Q2"); err != nil {
			t.Fatalf("AddTab: %v", err)
		}
		got := google.recorded()
		if len(got) != 1 {
			t.Fatalf("recorded %d requests, want 1: %v", len(got), got)
		}
		if got[0].method != http.MethodPost {
			t.Errorf("method = %q, want POST", got[0].method)
		}
		if want := "/v4/spreadsheets/" + goodFileID + ":batchUpdate"; got[0].path != want {
			t.Errorf("path = %q, want %q", got[0].path, want)
		}
		if want := `{"requests":[{"addSheet":{"properties":{"title":"Q2"}}}]}`; got[0].body != want {
			t.Errorf("body = %s, want %s", got[0].body, want)
		}
		if *unex != 0 {
			t.Errorf("unexpected() called %d times", *unex)
		}
	})
	t.Run("asks the write-scoped token source, not the read one", func(t *testing.T) {
		store := fakes.NewSpreadsheet()
		ctx, tc := tenantCtx(t)
		sp := seedWritable(t, store, tc, goodFileID)
		google := newRecordingSheets(t, http.StatusOK, `{}`)
		read, write := &fakeTokenSources{}, &fakeTokenSources{}
		svc, _ := newFullSvc(t, store, read, factoryFor(t, google.url), write, google.writers(t))

		if err := svc.AddTab(ctx, sp.ID, "Q2"); err != nil {
			t.Fatalf("AddTab: %v", err)
		}
		if len(write.calls) != 1 || write.calls[0] != sp.CredentialID {
			t.Errorf("write token source asked for %v, want %v", write.calls, sp.CredentialID)
		}
		if len(read.calls) != 0 {
			t.Error("a write must not be minted from the read-only token source")
		}
	})
	t.Run("unknown spreadsheet is NotFoundError", func(t *testing.T) {
		google := newRecordingSheets(t, http.StatusOK, `{}`)
		svc, unex := newWriteSvc(t, fakes.NewSpreadsheet(), &fakeTokenSources{}, google.writers(t))
		ctx, _ := tenantCtx(t)

		err := svc.AddTab(ctx, uuid.New(), "Q2")
		if !spreadsheet.IsNotFoundError(err) {
			t.Fatalf("want IsNotFoundError, got %T: %v", err, err)
		}
		if len(google.recorded()) != 0 {
			t.Error("an unresolved id must not reach Google")
		}
		if *unex != 0 {
			t.Error("a miss is expected, not an incident")
		}
	})
	t.Run("another org's spreadsheet is NotFoundError", func(t *testing.T) {
		store := fakes.NewSpreadsheet()
		ctx, _ := tenantCtx(t)
		other := seedWritable(t, store, tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New()}, goodFileID)
		google := newRecordingSheets(t, http.StatusOK, `{}`)
		svc, _ := newWriteSvc(t, store, &fakeTokenSources{}, google.writers(t))

		err := svc.AddTab(ctx, other.ID, "Q2")
		if !spreadsheet.IsNotFoundError(err) {
			t.Fatalf("want IsNotFoundError, got %T: %v", err, err)
		}
		if len(google.recorded()) != 0 {
			t.Error("another tenant's writable document must not be mutated")
		}
	})
	t.Run("missing tenant returns MissingError", func(t *testing.T) {
		google := newRecordingSheets(t, http.StatusOK, `{}`)
		svc, _ := newWriteSvc(t, fakes.NewSpreadsheet(), &fakeTokenSources{}, google.writers(t))

		if err := svc.AddTab(context.Background(), uuid.New(), "Q2"); !tenant.IsMissingError(err) {
			t.Fatalf("want tenant.MissingError, got %T: %v", err, err)
		}
	})
	t.Run("the writer's own title validation surfaces", func(t *testing.T) {
		store := fakes.NewSpreadsheet()
		ctx, tc := tenantCtx(t)
		sp := seedWritable(t, store, tc, goodFileID)
		google := newRecordingSheets(t, http.StatusOK, `{}`)
		svc, _ := newWriteSvc(t, store, &fakeTokenSources{}, google.writers(t))

		for _, title := range []string{"   ", strings.Repeat("x", 101)} {
			err := svc.AddTab(ctx, sp.ID, title)
			if !gsheet.IsInvalidTabTitleError(err) {
				t.Fatalf("title %q: want IsInvalidTabTitleError, got %T: %v", title, err, err)
			}
		}
		if len(google.recorded()) != 0 {
			t.Error("an invalid title must not reach Google")
		}
	})
	t.Run("a coded token-source failure passes through", func(t *testing.T) {
		store := fakes.NewSpreadsheet()
		ctx, tc := tenantCtx(t)
		sp := seedWritable(t, store, tc, goodFileID)
		google := newRecordingSheets(t, http.StatusOK, `{}`)
		svc, unex := newWriteSvc(t, store, &fakeTokenSources{err: &appErrStub{}}, google.writers(t))

		err := svc.AddTab(ctx, sp.ID, "Q2")
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
		sp := seedWritable(t, store, tc, goodFileID)
		google := newRecordingSheets(t, http.StatusOK, `{}`)
		svc, unex := newWriteSvc(t, store, &fakeTokenSources{err: errors.New("boom")}, google.writers(t))

		if err := svc.AddTab(ctx, sp.ID, "Q2"); err == nil {
			t.Fatal("want error")
		}
		if *unex != 1 {
			t.Errorf("unexpected() called %d times, want 1", *unex)
		}
	})
	t.Run("a writer factory failure routes through unexpected", func(t *testing.T) {
		store := fakes.NewSpreadsheet()
		ctx, tc := tenantCtx(t)
		sp := seedWritable(t, store, tc, goodFileID)
		writers := func(context.Context, oauth2.TokenSource) (*gsheet.Writer, error) {
			return nil, errors.New("cannot build writer")
		}
		svc, unex := newWriteSvc(t, store, &fakeTokenSources{}, writers)

		if err := svc.AddTab(ctx, sp.ID, "Q2"); err == nil {
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
		{name: "404 is a gone google document", status: http.StatusNotFound, body: `{"error":{"code":404}}`, wantErr: gworkspace.IsNotFoundError},
		{name: "403 is permission denied", status: http.StatusForbidden, body: `{"error":{"code":403}}`, wantErr: gworkspace.IsPermissionDeniedError},
		{name: "429 is quota exceeded", status: http.StatusTooManyRequests, body: `{"error":{"code":429}}`, wantErr: gworkspace.IsQuotaExceededError},
		{name: "401 is an expired credential", status: http.StatusUnauthorized, body: `{"error":{"code":401}}`, wantErr: gworkspace.IsAuthExpiredError},
		{name: "500 is unavailable", status: http.StatusInternalServerError, body: `{"error":{"code":500}}`, wantErr: gworkspace.IsUnavailableError},
	}
	for _, tc := range googleFailures {
		t.Run(tc.name, func(t *testing.T) {
			store := fakes.NewSpreadsheet()
			ctx, tctx := tenantCtx(t)
			sp := seedWritable(t, store, tctx, goodFileID)
			google := newRecordingSheets(t, tc.status, tc.body)
			svc, unex := newWriteSvc(t, store, &fakeTokenSources{}, google.writers(t))

			err := svc.AddTab(ctx, sp.ID, "Q2")
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
