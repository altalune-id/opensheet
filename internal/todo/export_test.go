package todo

import "altalune.id/opensheet/internal/platform/queue"

// LogCompletionJob exposes logCompletionJob for tests outside the package.
func LogCompletionJob() queue.Job { return logCompletionJob() }

// LogCompletionPayload exposes logCompletionV1 for tests outside the package.
type LogCompletionPayload = logCompletionV1
