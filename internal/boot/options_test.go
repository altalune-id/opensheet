package boot

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOptions_Defaults(t *testing.T) {
	o := newOptions()
	require.True(t, o.scheduler, "the scheduler is on by default")
	require.False(t, o.schedulerOnly)
	require.True(t, o.consumer, "the consumer is on by default")
	require.False(t, o.consumerOnly)
}

func TestOptions_ApplyConsumer(t *testing.T) {
	tests := []struct {
		name         string
		opts         []Option
		wantConsumer bool
		wantOnly     bool
	}{
		{"no options", nil, true, false},
		{"consumer off", []Option{WithConsumer(false)}, false, false},
		{"consumer only", []Option{WithConsumerOnly(true)}, true, true},
		{"last wins", []Option{WithConsumer(false), WithConsumer(true)}, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := newOptions()
			for _, opt := range tt.opts {
				opt(o)
			}
			require.Equal(t, tt.wantConsumer, o.consumer)
			require.Equal(t, tt.wantOnly, o.consumerOnly)
		})
	}
}

func TestOptions_Apply(t *testing.T) {
	tests := []struct {
		name          string
		opts          []Option
		wantScheduler bool
		wantOnly      bool
	}{
		{"no options", nil, true, false},
		{"scheduler off", []Option{WithScheduler(false)}, false, false},
		{"scheduler only", []Option{WithSchedulerOnly(true)}, true, true},
		{"last wins", []Option{WithScheduler(false), WithScheduler(true)}, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := newOptions()
			for _, opt := range tt.opts {
				opt(o)
			}
			require.Equal(t, tt.wantScheduler, o.scheduler)
			require.Equal(t, tt.wantOnly, o.schedulerOnly)
		})
	}
}
