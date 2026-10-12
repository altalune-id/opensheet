package notify

import (
	"errors"
	"strings"
	"testing"

	"altalune.id/opensheet/internal/apperror"
)

func TestFormatEmailBody_UserValueCannotForgeReportLines(t *testing.T) {
	inc := &apperror.Incident{
		Code:      "BLG002",
		Message:   "create post",
		RequestID: "real-request",
		Cause:     errors.New("blog: post already exists: slug=x\nRequest ID: forged\nTrace ID: deadbeef"),
	}

	lines := strings.Split(strings.TrimRight(formatEmailBody(inc), "\n"), "\n")

	field := func(prefix string) []string {
		var out []string
		for _, l := range lines {
			if strings.HasPrefix(l, prefix) {
				out = append(out, l)
			}
		}
		return out
	}

	if got := field("Request ID:"); len(got) != 1 || !strings.Contains(got[0], "real-request") {
		t.Fatalf("Request ID lines = %q, want exactly the genuine one", got)
	}
	if got := field("Trace ID:"); len(got) != 0 {
		t.Fatalf("Trace ID lines = %q, want none — the incident carried no trace id", got)
	}
	if got := field("Cause:"); len(got) != 1 {
		t.Fatalf("Cause lines = %q, want 1 — the value must not span lines", got)
	}
}
