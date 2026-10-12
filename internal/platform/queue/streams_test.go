package queue

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJob_Names(t *testing.T) {
	j := Job{Name: "todo.log_completion", Version: 1}
	subject := j.Subject()

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"subject", subject, "jobs.todo.log_completion.v1"},
		{"durable", durableName(subject), "todo_log_completion_v1"},
		{"dlq subject", dlqSubject(subject), "dlq.jobs.todo.log_completion.v1"},
		{"broadcast subject", Broadcast{Name: "system.onboarding_completed", Version: 2}.Subject(), "broadcast.system.onboarding_completed.v2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.got)
		})
	}
}

func TestJob_Validate(t *testing.T) {
	tests := []struct {
		name    string
		job     Job
		wantErr bool
	}{
		{"accepts domain.verb_object", Job{Name: "todo.log_completion", Version: 1}, false},
		{"accepts three segments", Job{Name: "a.b.c_2", Version: 3}, false},
		{"rejects one segment", Job{Name: "todo", Version: 1}, true},
		{"rejects uppercase", Job{Name: "Todo.x", Version: 1}, true},
		{"rejects empty segment", Job{Name: "todo..x", Version: 1}, true},
		{"rejects hyphen", Job{Name: "todo.x-y", Version: 1}, true},
		{"rejects empty name", Job{Name: "", Version: 1}, true},
		{"rejects version 0", Job{Name: "todo.log_completion", Version: 0}, true},
		{"rejects negative version", Job{Name: "todo.log_completion", Version: -1}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.job.Validate()
			if !tt.wantErr {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.True(t, IsInvalidJobError(err))
		})
	}
}

func TestBroadcast_ValidateSharesTheNameRule(t *testing.T) {
	require.NoError(t, Broadcast{Name: "system.onboarding_completed", Version: 1}.Validate())

	err := Broadcast{Name: "system", Version: 1}.Validate()
	require.Error(t, err)
	assert.True(t, IsInvalidBroadcastError(err))
	assert.False(t, IsInvalidJobError(err), "a broadcast must not report itself as a job")
	assert.Contains(t, err.Error(), "broadcast")

	assert.True(t, IsInvalidBroadcastError(Broadcast{Name: "system.x", Version: 0}.Validate()))
}
