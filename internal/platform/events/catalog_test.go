package events_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/platform/events"
)

//nolint:gochecknoglobals // a -update flag for golden files has to be package level.
var updateGolden = flag.Bool("update", false, "rewrite golden files")

func checkGolden(t *testing.T, path string, v any) {
	t.Helper()

	got, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got = append(got, '\n')

	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("mkdir golden dir: %v", err)
		}
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}

	want, err := os.ReadFile(path) //nolint:gosec // fixed test path
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("golden mismatch for %s:\ngot:\n%s\nwant:\n%s", path, got, want)
	}
}

func fixedSnapshot() events.PostSnapshotV1 {
	firstPublished := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	return events.PostSnapshotV1{
		ID:               uuid.MustParse("018f9c3e-1111-7000-8000-000000000001"),
		Slug:             "hello-world",
		Title:            "Hello, world",
		BodyMarkdown:     "# Hello\n\nBody.",
		CategoryID:       uuid.MustParse("018f9c3e-2222-7000-8000-000000000002"),
		TagIDs:           []uuid.UUID{},
		FirstPublishedAt: &firstPublished,
		UpdatedAt:        time.Date(2026, 9, 27, 4, 11, 0, 0, time.UTC),
		Version:          3,
	}
}

func TestGoldenPayloads(t *testing.T) {
	snap := fixedSnapshot()

	checkGolden(t, "testdata/post_published_v1.golden.json", events.PostPublishedV1(snap))
	checkGolden(t, "testdata/post_unpublished_v1.golden.json", events.PostUnpublishedV1(snap))
	checkGolden(t, "testdata/post_deleted_v1.golden.json", events.PostDeletedV1{
		ID:           uuid.MustParse("018f9c3e-1111-7000-8000-000000000001"),
		Slug:         "hello-world",
		WasPublished: true,
	})
	checkGolden(t, "testdata/webhook_ping_v1.golden.json", events.WebhookPingV1{
		EndpointID: uuid.MustParse("018f9c3e-4444-7000-8000-000000000004"),
	})
}

func TestAllLookupRoundTrip(t *testing.T) {
	for _, s := range events.All() {
		t.Run(string(s.Type), func(t *testing.T) {
			got, ok := events.Lookup(s.Type)
			if !ok {
				t.Fatalf("Lookup(%q) not found", s.Type)
			}
			if got != s {
				t.Errorf("Lookup(%q) = %+v, want %+v", s.Type, got, s)
			}
		})
	}
}

func TestLookupUnknownType(t *testing.T) {
	if _, ok := events.Lookup(events.Type("nope")); ok {
		t.Error("Lookup(\"nope\") ok = true, want false")
	}
}

func TestSubscribable(t *testing.T) {
	subs := events.Subscribable()
	want := []events.Type{events.PostPublished, events.PostUnpublished, events.PostDeleted}

	if len(subs) != len(want) {
		t.Fatalf("Subscribable() = %d entries, want %d", len(subs), len(want))
	}
	for i, s := range subs {
		if s.Type != want[i] {
			t.Errorf("Subscribable()[%d].Type = %q, want %q", i, s.Type, want[i])
		}
		if !s.Subscribable {
			t.Errorf("Subscribable()[%d].Subscribable = false, want true", i)
		}
	}
}

func TestPostDeletedIsSubscribable(t *testing.T) {
	s, ok := events.Lookup(events.PostDeleted)
	if !ok || !s.Subscribable {
		t.Errorf("PostDeleted: Subscribable = %v, ok = %v, want true, true", s.Subscribable, ok)
	}
}

func TestWebhookPingIsNotSubscribable(t *testing.T) {
	s, ok := events.Lookup(events.WebhookPing)
	if !ok || s.Subscribable {
		t.Errorf("WebhookPing: Subscribable = %v, ok = %v, want false, true", s.Subscribable, ok)
	}
}

func TestAPIVersion(t *testing.T) {
	s, ok := events.Lookup(events.PostPublished)
	if !ok {
		t.Fatal("PostPublished not found")
	}
	if got := s.APIVersion(); got != "v1" {
		t.Errorf("APIVersion() = %q, want %q", got, "v1")
	}
}

func TestCheckPayloadMismatch(t *testing.T) {
	err := events.CheckPayload(events.PostPublished, events.PostDeletedV1{})
	if !events.IsPayloadMismatchError(err) {
		t.Errorf("CheckPayload(PostPublished, PostDeletedV1{}) = %v, want *PayloadMismatchError", err)
	}
}

func TestCheckPayloadUnknownType(t *testing.T) {
	err := events.CheckPayload(events.Type("nope"), struct{}{})
	if !events.IsUnknownTypeError(err) {
		t.Errorf("CheckPayload(\"nope\", ...) = %v, want *UnknownTypeError", err)
	}
}

func TestCheckPayloadOK(t *testing.T) {
	cases := []struct {
		typ  events.Type
		data any
	}{
		{events.PostPublished, events.PostPublishedV1{}},
		{events.PostUnpublished, events.PostUnpublishedV1{}},
		{events.PostDeleted, events.PostDeletedV1{}},
		{events.WebhookPing, events.WebhookPingV1{}},
	}
	for _, tt := range cases {
		t.Run(string(tt.typ), func(t *testing.T) {
			if err := events.CheckPayload(tt.typ, tt.data); err != nil {
				t.Errorf("CheckPayload(%q, %T{}) = %v, want nil", tt.typ, tt.data, err)
			}
		})
	}
}

// TestCheckPayloadCoversEveryCatalogEntry fails when an All() entry has no case in CheckPayload's switch.
func TestCheckPayloadCoversEveryCatalogEntry(t *testing.T) {
	for _, s := range events.All() {
		t.Run(string(s.Type), func(t *testing.T) {
			err := events.CheckPayload(s.Type, nil)
			if !events.IsPayloadMismatchError(err) {
				t.Errorf("CheckPayload(%q, nil) = %v, want *PayloadMismatchError (add a case to CheckPayload's switch)", s.Type, err)
			}
		})
	}
}
