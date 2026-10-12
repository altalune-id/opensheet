package todo

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/platform/queue"
)

// Consumer adapts *Service to queue.Provider.
type Consumer struct {
	svc *Service
	log *slog.Logger
}

func logCompletionJob() queue.Job { return queue.Job{Name: "todo.log_completion", Version: 1} }

type logCompletionV1 struct {
	ID     uuid.UUID `json:"id"`
	Title  string    `json:"title"`
	DoneAt time.Time `json:"done_at"`
}

// NewConsumer binds svc to the todo queue handlers.
func NewConsumer(svc *Service, log *slog.Logger) *Consumer {
	return &Consumer{svc: svc, log: log.With("module", "todo")}
}

// ConsumerHandlers implements queue.Provider.
func (c *Consumer) ConsumerHandlers() []queue.Handler {
	return []queue.Handler{{
		Job: logCompletionJob(),
		Handle: func(ctx context.Context, m queue.Message) error {
			p, err := queue.Decode[logCompletionV1](m)
			if err != nil {
				return queue.Permanent(err)
			}
			return c.svc.LogCompletion(ctx, p.ID)
		},
	}}
}
