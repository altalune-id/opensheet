package worker

import (
	"context"
	"errors"
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
			if err != nil && !shutdown(gctx, err) {
				s.log.Error("worker exited", slog.String("worker", w.Name()), slog.Any("err", err))
				return err
			}
			s.log.Info("worker exited", slog.String("worker", w.Name()))
			return nil
		})
	}
	return g.Wait()
}

// NOTE: a child-context Canceled during shutdown is indistinguishable from the shutdown and is treated as clean.
func shutdown(ctx context.Context, err error) bool {
	return errors.Is(err, context.Canceled) && ctx.Err() != nil
}
