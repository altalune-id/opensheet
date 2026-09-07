package scheduler_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"altalune.id/template/scheduler"
)

func TestTypedErrors_MessagesAndHelpers(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantMsg string
		is      func(error) bool
	}{
		{"unknown job", &scheduler.UnknownJobError{Name: "x"}, `scheduler: unknown job "x"`, scheduler.IsUnknownJobError},
		{"busy", &scheduler.BusyError{Name: "x"}, `scheduler: job "x" is already running`, scheduler.IsBusyError},
		{"duplicate", &scheduler.DuplicateJobError{Name: "x"}, `scheduler: duplicate job name "x"`, scheduler.IsDuplicateJobError},
		{"draining", &scheduler.DrainingError{}, "scheduler: runner is draining", scheduler.IsDrainingError},
		{"not leader", &scheduler.NotLeaderError{Name: "x"}, `scheduler: job "x" is held by another process`, scheduler.IsNotLeaderError},
		{"panic", &scheduler.PanicError{Job: "x", Value: "boom", Stack: "STACK"}, "scheduler: job \"x\" panicked: boom\nSTACK", scheduler.IsPanicError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.wantMsg, tt.err.Error())
			require.True(t, tt.is(tt.err), "helper must match its own type")
			require.True(t, tt.is(fmt.Errorf("wrapped: %w", tt.err)), "helper must walk %%w chains")
			require.False(t, tt.is(errors.New("other")), "helper must not match a plain error")
			require.False(t, tt.is(nil), "helper must not match nil")
		})
	}
}
