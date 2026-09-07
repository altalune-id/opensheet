package scheduler

import (
	"fmt"
	"math/rand/v2"
	"sync"
	"time"
)

// EveryInterval fires every d, applying jitter to the first tick only.
func EveryInterval(d, jitter time.Duration) (Schedule, error) {
	if d <= 0 {
		return nil, fmt.Errorf("scheduler.EveryInterval: interval must be positive, got %s", d)
	}
	if jitter < 0 {
		return nil, fmt.Errorf("scheduler.EveryInterval: jitter must be non-negative, got %s", jitter)
	}
	return &everyInterval{
		interval: d,
		jitter:   jitter,
		rng:      rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64())),
	}, nil
}

// MustEveryInterval is EveryInterval that panics on invalid input; use only with constants.
func MustEveryInterval(d, jitter time.Duration) Schedule {
	s, err := EveryInterval(d, jitter)
	if err != nil {
		panic(err)
	}
	return s
}

type everyInterval struct {
	interval time.Duration
	jitter   time.Duration

	mu    sync.Mutex
	fired bool
	rng   *rand.Rand
}

func (e *everyInterval) Next(now time.Time) time.Time {
	d := e.interval
	e.mu.Lock()
	if !e.fired && e.jitter > 0 {
		d += time.Duration(e.rng.Int64N(int64(e.jitter)))
	}
	e.fired = true
	e.mu.Unlock()
	return now.Add(d)
}

func (e *everyInterval) String() string {
	if e.jitter > 0 {
		return fmt.Sprintf("every %s (first-tick jitter %s)", e.interval, e.jitter)
	}
	return "every " + e.interval.String()
}
