package todo

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	orgID := uuid.New()
	projectID := uuid.New()

	tests := []struct {
		name      string
		title     string
		wantTitle string
		wantErrIs func(error) bool
	}{
		{name: "trims title", title: "  buy milk  ", wantTitle: "buy milk"},
		{name: "accepts 200 rune title", title: strings.Repeat("a", 200), wantTitle: strings.Repeat("a", 200)},
		{name: "rejects empty", title: "", wantErrIs: IsInvalidTitleError},
		{name: "rejects whitespace only", title: "   ", wantErrIs: IsInvalidTitleError},
		{name: "rejects over 200 runes", title: strings.Repeat("a", 201), wantErrIs: IsInvalidTitleError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			td, err := New(orgID, projectID, tc.title)
			if tc.wantErrIs != nil {
				if err == nil {
					t.Fatalf("want error, got todo=%+v", td)
				}
				if !tc.wantErrIs(err) {
					t.Fatalf("wrong error type: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if td.Title != tc.wantTitle {
				t.Errorf("title=%q want %q", td.Title, tc.wantTitle)
			}
			if td.OrgID != orgID || td.ProjectID != projectID {
				t.Errorf("tenant ids not carried through: %+v", td)
			}
			if td.ID == uuid.Nil {
				t.Errorf("ID unset")
			}
			if td.Done {
				t.Errorf("Done should be false on creation")
			}
			if td.CreatedAt.IsZero() {
				t.Errorf("CreatedAt unset")
			}
		})
	}
}

func TestToggle_FlipsDone(t *testing.T) {
	td, err := New(uuid.New(), uuid.New(), "milk")
	if err != nil {
		t.Fatal(err)
	}
	if td.Done {
		t.Fatal("Done should start false")
	}
	td.Toggle()
	if !td.Done {
		t.Fatal("Toggle did not set Done true")
	}
	td.Toggle()
	if td.Done {
		t.Fatal("Toggle did not flip Done back to false")
	}
}

func TestListOpts_ValueSemantics(t *testing.T) {
	yes := true
	opts := ListOpts{Done: &yes}
	if opts.Done == nil || *opts.Done != true {
		t.Fatal("ListOpts did not carry Done pointer")
	}
	var zero ListOpts
	if zero.Done != nil {
		t.Fatal("zero ListOpts should have nil Done")
	}
}

func TestNew_SetsUpdatedAtToCreatedAt(t *testing.T) {
	got, err := New(uuid.New(), uuid.New(), "write the spec")
	require.NoError(t, err)
	require.Equal(t, got.CreatedAt, got.UpdatedAt)
}

func TestToggle_BumpsUpdatedAt(t *testing.T) {
	got, err := New(uuid.New(), uuid.New(), "write the spec")
	require.NoError(t, err)
	before := got.UpdatedAt

	got.Toggle()

	require.True(t, got.Done)
	require.True(t, got.UpdatedAt.After(before) || got.UpdatedAt.Equal(before),
		"UpdatedAt must not go backwards")
	require.False(t, got.UpdatedAt.Before(before))
}

func TestInvalidTitleError_ToAppError(t *testing.T) {
	e := &InvalidTitleError{Reason: "empty"}
	if !strings.Contains(e.Error(), "empty") {
		t.Errorf("Error() missing reason: %q", e.Error())
	}
	ae := e.ToAppError()
	if ae == nil {
		t.Fatal("ToAppError returned nil")
	}
	if ae.Code() == "" {
		t.Errorf("AppError.Code empty")
	}
}

func TestNotFoundError_ToAppError(t *testing.T) {
	e := &NotFoundError{ID: "abc"}
	if !strings.Contains(e.Error(), "abc") {
		t.Errorf("Error() missing id: %q", e.Error())
	}
	ae := e.ToAppError()
	if ae == nil {
		t.Fatal("ToAppError returned nil")
	}
	if ae.Code() == "" {
		t.Errorf("AppError.Code empty")
	}
}
