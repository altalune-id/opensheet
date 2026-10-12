package boot

import (
	"context"
	"sync/atomic"
	"time"

	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/queue"
)

func onboardingCompleted() queue.Broadcast {
	return queue.Broadcast{Name: "system.onboarding_completed", Version: 1}
}

type onboardingCompletedV1 struct {
	CompletedAt time.Time `json:"completed_at"`
}

type onboardingGate struct {
	required   *atomic.Bool
	queue      *queue.Client
	unexpected apperror.UnexpectedFunc
}

func (g *onboardingGate) Complete(ctx context.Context) {
	g.required.Store(false)
	if err := g.queue.Emit(ctx, onboardingCompleted(), onboardingCompletedV1{CompletedAt: time.Now().UTC()}); err != nil {
		_ = g.unexpected(ctx, "boot.onboarding: emit", err)
	}
}

func (g *onboardingGate) Listeners() []queue.Listener {
	return []queue.Listener{{
		Broadcast: onboardingCompleted(),
		Handle: func(context.Context, queue.Message) error {
			g.required.Store(false)
			return nil
		},
	}}
}
