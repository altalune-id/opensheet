package todo_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"altalune.id/opensheet/internal/platform/queue"
	"altalune.id/opensheet/internal/testutil/fakes"
	"altalune.id/opensheet/internal/todo"
)

func TestConsumer_ConsumerHandlers(t *testing.T) {
	svc, _ := newSvc(t, fakes.NewTodo())
	c := todo.NewConsumer(svc, discardLogger())

	hs := c.ConsumerHandlers()
	if len(hs) != 1 {
		t.Fatalf("len=%d want 1", len(hs))
	}
	if hs[0].Job != todo.LogCompletionJob() {
		t.Errorf("job=%+v want %+v", hs[0].Job, todo.LogCompletionJob())
	}
}

func TestConsumer_Handle_UndecodableDataIsPermanent(t *testing.T) {
	svc, _ := newSvc(t, fakes.NewTodo())
	c := todo.NewConsumer(svc, discardLogger())

	err := c.ConsumerHandlers()[0].Handle(t.Context(), queue.Message{Data: []byte("not json")})
	if !queue.IsPermanentError(err) {
		t.Fatalf("want queue.IsPermanentError, got %v", err)
	}
}

func TestConsumer_Handle_ValidPayloadLogsCompletion(t *testing.T) {
	var buf bytes.Buffer
	store := fakes.NewTodo()
	svc, _ := newSvcWithLog(t, store, &buf)
	c := todo.NewConsumer(svc, discardLogger())
	ctx, _ := tenantCtx(t)
	td, err := svc.Create(ctx, "milk")
	if err != nil {
		t.Fatal(err)
	}

	body, err := json.Marshal(todo.LogCompletionPayload{ID: td.ID, Title: td.Title, DoneAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	job := todo.LogCompletionJob()
	m := queue.Message{Name: job.Name, Version: job.Version, Data: body}

	if err := c.ConsumerHandlers()[0].Handle(ctx, m); err != nil {
		t.Fatalf("Handle err: %v", err)
	}
	if !strings.Contains(buf.String(), "todo.completion_logged") {
		t.Errorf("log missing todo.completion_logged: %s", buf.String())
	}
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
