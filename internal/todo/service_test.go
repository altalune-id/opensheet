package todo_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/queue"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/testutil/fakes"
	"altalune.id/opensheet/internal/todo"
)

func newSvc(t *testing.T, store todo.Store) (*todo.Service, *int) {
	t.Helper()
	svc, calls, _ := newSvcWithQueue(t, store)
	return svc, calls
}

func newSvcWithQueue(t *testing.T, store todo.Store) (*todo.Service, *int, *fakes.Queue) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	calls := 0
	unexpected := func(_ context.Context, _ string, err error, _ ...any) *apperror.AppError {
		calls++
		return apperror.New("opensheet.unexpected", err.Error(), codes.Internal,
			&apperrorv1.ErrorDetail{Code: "opensheet.unexpected"}).WithCause(err)
	}
	q := &fakes.Queue{}
	return todo.NewService(store, log, unexpected, q), &calls, q
}

func newSvcWithLog(t *testing.T, store todo.Store, w io.Writer) (*todo.Service, *int) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(w, nil))
	calls := 0
	unexpected := func(_ context.Context, _ string, err error, _ ...any) *apperror.AppError {
		calls++
		return apperror.New("opensheet.unexpected", err.Error(), codes.Internal,
			&apperrorv1.ErrorDetail{Code: "opensheet.unexpected"}).WithCause(err)
	}
	return todo.NewService(store, log, unexpected, &fakes.Queue{}), &calls
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

func TestService_Create_SetsAuthorFromPrincipal(t *testing.T) {
	t.Run("key principal sets Author.KeyID, not UserID", func(t *testing.T) {
		svc, _ := newSvc(t, fakes.NewTodo())
		ctx, _ := tenantCtx(t)
		keyID := uuid.New()
		ctx = session.PrincipalInto(ctx, session.Principal{KeyID: keyID, Source: session.SourceAPIKey})

		got, err := svc.Create(ctx, "milk")
		if err != nil {
			t.Fatalf("Create err: %v", err)
		}
		if got.Author.KeyID != keyID {
			t.Errorf("Author.KeyID=%v want %v", got.Author.KeyID, keyID)
		}
		if got.Author.UserID != uuid.Nil {
			t.Errorf("Author.UserID=%v want nil for a key principal", got.Author.UserID)
		}
		if !got.Author.IsKey() {
			t.Error("Author.IsKey() should be true for a key principal")
		}
	})
	t.Run("user principal sets Author.UserID, not KeyID", func(t *testing.T) {
		svc, _ := newSvc(t, fakes.NewTodo())
		ctx, _ := tenantCtx(t)
		userID := uuid.New()
		ctx = session.PrincipalInto(ctx, session.Principal{UserID: userID, Source: session.SourceOIDC})

		got, err := svc.Create(ctx, "milk")
		if err != nil {
			t.Fatalf("Create err: %v", err)
		}
		if got.Author.UserID != userID {
			t.Errorf("Author.UserID=%v want %v", got.Author.UserID, userID)
		}
		if got.Author.KeyID != uuid.Nil {
			t.Errorf("Author.KeyID=%v want nil for a user principal", got.Author.KeyID)
		}
		if got.Author.IsKey() {
			t.Error("Author.IsKey() should be false for a user principal")
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
	t.Run("not done to done submits the completion job once, with the todo's payload", func(t *testing.T) {
		store := fakes.NewTodo()
		svc, _, q := newSvcWithQueue(t, store)
		ctx, _ := tenantCtx(t)
		td, _ := svc.Create(ctx, "milk")

		before := time.Now().UTC()
		got, err := svc.Toggle(ctx, td.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Done {
			t.Fatal("not toggled")
		}
		submitted := q.Recorded()
		if len(submitted) != 1 {
			t.Fatalf("submitted=%d want 1", len(submitted))
		}
		if submitted[0].Job != todo.LogCompletionJob() {
			t.Errorf("job=%+v want %+v", submitted[0].Job, todo.LogCompletionJob())
		}
		payload, ok := submitted[0].Data.(todo.LogCompletionPayload)
		if !ok {
			t.Fatalf("data type=%T want todo.LogCompletionPayload", submitted[0].Data)
		}
		if payload.ID != td.ID {
			t.Errorf("payload.ID=%v want %v", payload.ID, td.ID)
		}
		if payload.Title != td.Title {
			t.Errorf("payload.Title=%q want %q", payload.Title, td.Title)
		}
		if payload.DoneAt.IsZero() || payload.DoneAt.Before(before) {
			t.Errorf("payload.DoneAt=%v want non-zero, at or after %v", payload.DoneAt, before)
		}
		if payload.DoneAt.Location() != time.UTC {
			t.Errorf("payload.DoneAt location=%v want UTC", payload.DoneAt.Location())
		}
	})
	t.Run("done to not done submits nothing", func(t *testing.T) {
		store := fakes.NewTodo()
		svc, _, q := newSvcWithQueue(t, store)
		ctx, _ := tenantCtx(t)
		td, _ := svc.Create(ctx, "milk")
		if _, err := svc.Toggle(ctx, td.ID); err != nil {
			t.Fatal(err)
		}
		q.Reset()

		if _, err := svc.Toggle(ctx, td.ID); err != nil {
			t.Fatal(err)
		}
		if len(q.Recorded()) != 0 {
			t.Errorf("submitted=%d want 0", len(q.Recorded()))
		}
	})
	t.Run("submit failure still returns the todo and reports", func(t *testing.T) {
		store := fakes.NewTodo()
		log := slog.New(slog.NewTextHandler(io.Discard, nil))
		var mu sync.Mutex
		var messages []string
		unexpected := func(_ context.Context, msg string, err error, _ ...any) *apperror.AppError {
			mu.Lock()
			messages = append(messages, msg)
			mu.Unlock()
			return apperror.New("opensheet.unexpected", err.Error(), codes.Internal,
				&apperrorv1.ErrorDetail{Code: "opensheet.unexpected"}).WithCause(err)
		}
		q := &fakes.Queue{Err: errors.New("nats down")}
		svc := todo.NewService(store, log, unexpected, q)
		ctx, _ := tenantCtx(t)
		td, _ := svc.Create(ctx, "milk")

		got, err := svc.Toggle(ctx, td.ID)
		if err != nil {
			t.Fatalf("Toggle err: %v", err)
		}
		if !got.Done {
			t.Error("not toggled")
		}
		mu.Lock()
		gotMsgs := slices.Clone(messages)
		mu.Unlock()
		if !slices.Contains(gotMsgs, "todo.Toggle: submit") {
			t.Errorf("unexpected() messages=%v, want to contain %q", gotMsgs, "todo.Toggle: submit")
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

func TestService_LogCompletion(t *testing.T) {
	t.Run("existing todo logs completion", func(t *testing.T) {
		var buf bytes.Buffer
		store := fakes.NewTodo()
		svc, calls := newSvcWithLog(t, store, &buf)
		ctx, _ := tenantCtx(t)
		td, _ := svc.Create(ctx, "milk")

		if err := svc.LogCompletion(ctx, td.ID); err != nil {
			t.Fatalf("LogCompletion err: %v", err)
		}
		if *calls != 0 {
			t.Errorf("unexpected() called %d times", *calls)
		}
		out := buf.String()
		if !strings.Contains(out, "todo.completion_logged") {
			t.Errorf("log missing todo.completion_logged: %s", out)
		}
		if !strings.Contains(out, td.ID.String()) {
			t.Errorf("log missing todo_id: %s", out)
		}
	})

	t.Run("missing todo returns nil", func(t *testing.T) {
		svc, calls := newSvc(t, fakes.NewTodo())
		ctx, _ := tenantCtx(t)
		if err := svc.LogCompletion(ctx, uuid.New()); err != nil {
			t.Fatalf("want nil, got %v", err)
		}
		if *calls != 0 {
			t.Errorf("unexpected() called %d times", *calls)
		}
	})

	t.Run("cross-tenant todo returns nil and logs nothing", func(t *testing.T) {
		var buf bytes.Buffer
		store := fakes.NewTodo()
		svc, _ := newSvcWithLog(t, store, &buf)
		ctxA, _ := tenantCtx(t)
		td, _ := svc.Create(ctxA, "milk")

		ctxB, _ := tenantCtx(t)
		if err := svc.LogCompletion(ctxB, td.ID); err != nil {
			t.Fatalf("want nil, got %v", err)
		}
		if strings.Contains(buf.String(), "todo.completion_logged") {
			t.Errorf("should not have logged: %s", buf.String())
		}
	})

	t.Run("same org, different project returns nil and logs nothing", func(t *testing.T) {
		var buf bytes.Buffer
		store := fakes.NewTodo()
		svc, _ := newSvcWithLog(t, store, &buf)
		orgID := uuid.New()
		ctxA := tenant.Into(context.Background(), tenant.Context{OrgID: orgID, ProjectID: uuid.New(), UserID: uuid.New()})
		td, _ := svc.Create(ctxA, "milk")

		ctxB := tenant.Into(context.Background(), tenant.Context{OrgID: orgID, ProjectID: uuid.New(), UserID: uuid.New()})
		if err := svc.LogCompletion(ctxB, td.ID); err != nil {
			t.Fatalf("want nil, got %v", err)
		}
		if strings.Contains(buf.String(), "todo.completion_logged") {
			t.Errorf("should not have logged: %s", buf.String())
		}
	})
}

func TestService_Queue_EveryToggleSubmissionHasAConsumerHandler(t *testing.T) {
	store := fakes.NewTodo()
	svc, _, q := newSvcWithQueue(t, store)
	ctx, _ := tenantCtx(t)
	td, _ := svc.Create(ctx, "milk")
	if _, err := svc.Toggle(ctx, td.ID); err != nil {
		t.Fatal(err)
	}

	handled := make(map[queue.Job]bool)
	consumer := todo.NewConsumer(svc, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, h := range consumer.ConsumerHandlers() {
		handled[h.Job] = true
	}
	submitted := q.Recorded()
	if len(submitted) == 0 {
		t.Fatal("want at least one submission to guard")
	}
	for _, c := range submitted {
		if !handled[c.Job] {
			t.Errorf("job %+v submitted with no consumer handler", c.Job)
		}
	}
}
