package todo_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/template/gen/go/apperror/v1"
	"altalune.id/template/internal/apperror"
	"altalune.id/template/internal/platform/tenant"
	"altalune.id/template/internal/testutil/fakes"
	"altalune.id/template/internal/todo"
)

func newSvc(t *testing.T, store todo.Store) (*todo.Service, *int) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	calls := 0
	unexpected := func(_ context.Context, _ string, err error, _ ...any) *apperror.AppError {
		calls++
		return apperror.New("altempl.unexpected", err.Error(), codes.Internal,
			&apperrorv1.ErrorDetail{Code: "altempl.unexpected"}).WithCause(err)
	}
	return todo.NewService(store, log, unexpected), &calls
}

func tenantCtx(t *testing.T) (context.Context, tenant.Context) {
	t.Helper()
	tc := tenant.Context{OrgID: uuid.New(), ProjectID: uuid.New(), UserID: uuid.New()}
	return tenant.Into(context.Background(), tc), tc
}

func TestService_Create(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		svc, unexCalls := newSvc(t, fakes.NewTodo())
		ctx, tc := tenantCtx(t)
		got, err := svc.Create(ctx, "  buy milk  ")
		if err != nil {
			t.Fatalf("Create err: %v", err)
		}
		if got.Title != "buy milk" {
			t.Errorf("title=%q", got.Title)
		}
		if got.OrgID != tc.OrgID {
			t.Errorf("OrgID not propagated")
		}
		if *unexCalls != 0 {
			t.Errorf("unexpected() called %d times", *unexCalls)
		}
	})
	t.Run("invalid title bubbles typed error", func(t *testing.T) {
		svc, unexCalls := newSvc(t, fakes.NewTodo())
		ctx, _ := tenantCtx(t)
		_, err := svc.Create(ctx, "  ")
		if !todo.IsInvalidTitleError(err) {
			t.Fatalf("want IsInvalidTitleError, got %v", err)
		}
		if *unexCalls != 0 {
			t.Errorf("invariant error should not route through unexpected")
		}
	})
	t.Run("missing tenant returns MissingError", func(t *testing.T) {
		svc, _ := newSvc(t, fakes.NewTodo())
		_, err := svc.Create(context.Background(), "milk")
		if !tenant.IsMissingError(err) {
			t.Fatalf("want tenant.MissingError, got %v", err)
		}
	})
	t.Run("store save error routes through unexpected", func(t *testing.T) {
		svc, unexCalls := newSvc(t, &failingStore{onSave: errors.New("boom")})
		ctx, _ := tenantCtx(t)
		_, err := svc.Create(ctx, "milk")
		if err == nil {
			t.Fatal("want err")
		}
		if *unexCalls != 1 {
			t.Errorf("unexpected() calls=%d want 1", *unexCalls)
		}
	})
}

func TestService_List(t *testing.T) {
	t.Run("returns tenant-scoped todos", func(t *testing.T) {
		store := fakes.NewTodo()
		svc, _ := newSvc(t, store)
		ctx, _ := tenantCtx(t)
		if _, err := svc.Create(ctx, "one"); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Create(ctx, "two"); err != nil {
			t.Fatal(err)
		}
		out, err := svc.List(ctx, todo.ListOpts{})
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != 2 {
			t.Errorf("len=%d want 2", len(out))
		}
	})
	t.Run("done filter", func(t *testing.T) {
		store := fakes.NewTodo()
		svc, _ := newSvc(t, store)
		ctx, _ := tenantCtx(t)
		a, _ := svc.Create(ctx, "a")
		_, _ = svc.Create(ctx, "b")
		if _, err := svc.Toggle(ctx, a.ID); err != nil {
			t.Fatal(err)
		}
		yes := true
		got, err := svc.List(ctx, todo.ListOpts{Done: &yes})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || !got[0].Done {
			t.Errorf("filter mismatch: %+v", got)
		}
	})
	t.Run("store error routes through unexpected", func(t *testing.T) {
		svc, unexCalls := newSvc(t, &failingStore{onList: errors.New("boom")})
		ctx, _ := tenantCtx(t)
		if _, err := svc.List(ctx, todo.ListOpts{}); err == nil {
			t.Fatal("want err")
		}
		if *unexCalls != 1 {
			t.Errorf("unexpected() calls=%d want 1", *unexCalls)
		}
	})
}

func TestService_Toggle(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		store := fakes.NewTodo()
		svc, _ := newSvc(t, store)
		ctx, _ := tenantCtx(t)
		td, _ := svc.Create(ctx, "milk")
		got, err := svc.Toggle(ctx, td.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Done {
			t.Error("not toggled")
		}
	})
	t.Run("missing todo bubbles NotFoundError", func(t *testing.T) {
		svc, unexCalls := newSvc(t, fakes.NewTodo())
		ctx, _ := tenantCtx(t)
		_, err := svc.Toggle(ctx, uuid.New())
		if !todo.IsNotFoundError(err) {
			t.Fatalf("want IsNotFoundError, got %v", err)
		}
		if *unexCalls != 0 {
			t.Errorf("expected NotFound bypassed unexpected()")
		}
	})
	t.Run("cross-tenant todo behaves as not found", func(t *testing.T) {
		store := fakes.NewTodo()
		svc, _ := newSvc(t, store)
		ctxA, _ := tenantCtx(t)
		td, _ := svc.Create(ctxA, "milk")

		ctxB, _ := tenantCtx(t)
		_, err := svc.Toggle(ctxB, td.ID)
		if !todo.IsNotFoundError(err) {
			t.Fatalf("want IsNotFoundError, got %v", err)
		}
	})
}

func TestService_Delete(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		store := fakes.NewTodo()
		svc, _ := newSvc(t, store)
		ctx, _ := tenantCtx(t)
		td, _ := svc.Create(ctx, "milk")
		if err := svc.Delete(ctx, td.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Toggle(ctx, td.ID); !todo.IsNotFoundError(err) {
			t.Errorf("post-delete want IsNotFoundError, got %v", err)
		}
	})
	t.Run("cross-tenant delete is NotFound", func(t *testing.T) {
		store := fakes.NewTodo()
		svc, _ := newSvc(t, store)
		ctxA, _ := tenantCtx(t)
		td, _ := svc.Create(ctxA, "milk")

		ctxB, _ := tenantCtx(t)
		if err := svc.Delete(ctxB, td.ID); !todo.IsNotFoundError(err) {
			t.Fatalf("want IsNotFoundError, got %v", err)
		}
	})
}

func TestService_ClearDone(t *testing.T) {
	store := fakes.NewTodo()
	svc, _ := newSvc(t, store)
	ctx, _ := tenantCtx(t)
	a, _ := svc.Create(ctx, "a")
	b, _ := svc.Create(ctx, "b")
	_, _ = svc.Create(ctx, "c")
	if _, err := svc.Toggle(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Toggle(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	n, err := svc.ClearDone(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("cleared=%d want 2", n)
	}
	rest, _ := svc.List(ctx, todo.ListOpts{})
	if len(rest) != 1 {
		t.Errorf("remaining=%d want 1", len(rest))
	}
}

type failingStore struct {
	onSave, onList error
}

func (f *failingStore) Save(_ context.Context, _ *todo.Todo) error {
	if f.onSave != nil {
		return f.onSave
	}
	return nil
}
func (f *failingStore) ByID(_ context.Context, id uuid.UUID) (*todo.Todo, error) {
	return nil, &todo.NotFoundError{ID: id.String()}
}
func (f *failingStore) List(_ context.Context, _, _ uuid.UUID, _ todo.ListOpts) ([]*todo.Todo, error) {
	if f.onList != nil {
		return nil, f.onList
	}
	return nil, nil
}
func (f *failingStore) Delete(_ context.Context, _ uuid.UUID) error { return nil }
func (f *failingStore) ClearDone(_ context.Context, _, _ uuid.UUID) (int, error) {
	return 0, nil
}
func (f *failingStore) MarkDoneOlderThan(_ context.Context, _ uuid.UUID, _ time.Time, _ int) (int, error) {
	return 0, nil
}

func TestService_AutoCompleteStale(t *testing.T) {
	store := fakes.NewTodo()
	svc, unexCalls := newSvc(t, store)
	ctx, tc := tenantCtx(t)

	store.MarkDoneOlderThanFn = func(_ context.Context, gotOrg uuid.UUID, cutoff time.Time, batch int) (int, error) {
		if gotOrg != tc.OrgID {
			t.Errorf("orgID=%v want %v", gotOrg, tc.OrgID)
		}
		if batch != todo.SweepBatchSize {
			t.Errorf("batch=%d want %d", batch, todo.SweepBatchSize)
		}
		if skew := time.Since(cutoff.Add(todo.StaleAfter)); skew < 0 || skew > time.Minute {
			t.Errorf("cutoff=%v skew=%v want within a minute of now-StaleAfter", cutoff, skew)
		}
		return 7, nil
	}

	n, err := svc.AutoCompleteStale(ctx, todo.StaleAfter)
	if err != nil {
		t.Fatalf("AutoCompleteStale: %v", err)
	}
	if n != 7 {
		t.Errorf("swept=%d want 7", n)
	}
	if *unexCalls != 0 {
		t.Errorf("unexpected() called %d times", *unexCalls)
	}
}

func TestService_AutoCompleteStale_RequiresTenant(t *testing.T) {
	svc, _ := newSvc(t, fakes.NewTodo())
	_, err := svc.AutoCompleteStale(context.Background(), todo.StaleAfter)
	if !tenant.IsMissingError(err) {
		t.Fatalf("want tenant.MissingError, got %v", err)
	}
}

func TestService_AutoCompleteStale_StoreFailureIsUnexpected(t *testing.T) {
	store := fakes.NewTodo()
	svc, unexCalls := newSvc(t, store)
	ctx, _ := tenantCtx(t)
	store.MarkDoneOlderThanFn = func(_ context.Context, _ uuid.UUID, _ time.Time, _ int) (int, error) {
		return 0, errors.New("boom")
	}

	if _, err := svc.AutoCompleteStale(ctx, todo.StaleAfter); err == nil {
		t.Fatal("want error")
	}
	if *unexCalls != 1 {
		t.Errorf("unexpected() called %d times, want 1", *unexCalls)
	}
}
