// Package worker supervises long-running loops (HTTP servers, background clients) under one errgroup.
package worker

import "context"

// Worker is a named long-running task run under a Supervisor.
type Worker interface {
	Name() string
	Run(ctx context.Context) error
}
