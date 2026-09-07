package worker

import (
	"context"
	"log/slog"
	"slices"

	"golang.org/x/sync/errgroup"
)

// Supervisor runs registered workers under one errgroup.
type Supervisor struct {
	workers []Worker
	log     *slog.Logger
}

// New returns an empty Supervisor.
func New(log *slog.Logger) *Supervisor {
	if log == nil {
		log = slog.Default()
	}
	return &Supervisor{log: log}
}

// Register adds w to the set of workers Run will start.
func (s *Supervisor) Register(w Worker) { s.workers = append(s.workers, w) }

// Workers returns the registered workers in registration order.
func (s *Supervisor) Workers() []Worker { return slices.Clone(s.workers) }

// Run starts every worker under one errgroup and returns the first non-nil error.
func (s *Supervisor) Run(ctx context.Context) error {
	g, gctx := errgroup.WithContext(ctx)
	for _, w := range s.workers {
		w := w
		s.log.Info("worker starting", slog.String("worker", w.Name()))
		g.Go(func() error {
			err := w.Run(gctx)
			if err != nil {
				s.log.Error("worker exited", slog.String("worker", w.Name()), slog.Any("err", err))
			} else {
				s.log.Info("worker exited", slog.String("worker", w.Name()))
			}
			return err
		})
	}
	return g.Wait()
}
