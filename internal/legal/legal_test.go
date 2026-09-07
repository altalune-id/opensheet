package legal_test

import (
	"strings"
	"testing"

	"altalune.id/template/internal/legal"
)

func TestTerms_ParsesAndRenders(t *testing.T) {
	t.Parallel()
	d, err := legal.Terms()
	if err != nil {
		t.Fatalf("Terms() error: %v", err)
	}
	if d.Slug != legal.TermsSlug {
		t.Errorf("slug = %q, want %q", d.Slug, legal.TermsSlug)
	}
	if d.Title == "" {
		t.Error("title empty — frontmatter not parsed")
	}
	if d.UpdatedAt.IsZero() {
		t.Error("updatedAt zero — frontmatter date not parsed")
	}
	if !strings.Contains(d.HTML, "<h1>") {
		t.Error("HTML missing <h1> — markdown not rendered")
	}
}

func TestPrivacy_ParsesAndRenders(t *testing.T) {
	t.Parallel()
	d, err := legal.Privacy()
	if err != nil {
		t.Fatalf("Privacy() error: %v", err)
	}
	if d.Title == "" {
		t.Error("title empty")
	}
	if !strings.Contains(d.HTML, "<h1>") {
		t.Error("HTML missing <h1>")
	}
}

func TestBySlug(t *testing.T) {
	t.Parallel()
	tests := []struct {
		slug    string
		wantErr bool
	}{
		{legal.TermsSlug, false},
		{legal.PrivacySlug, false},
		{"unknown", true},
	}
	for _, tc := range tests {
		t.Run(tc.slug, func(t *testing.T) {
			t.Parallel()
			_, err := legal.BySlug(tc.slug)
			if (err != nil) != tc.wantErr {
				t.Errorf("err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}
