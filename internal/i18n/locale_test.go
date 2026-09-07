package i18n_test

import (
	"errors"
	"fmt"
	"testing"

	"altalune.id/template/internal/i18n"
)

func TestLocale_Dir(t *testing.T) {
	t.Parallel()
	tests := []struct {
		loc  i18n.Locale
		want string
	}{
		{i18n.EnUS, "ltr"},
		{i18n.IdID, "ltr"},
		{i18n.MsMY, "ltr"},
		{i18n.JaJP, "ltr"},
		{i18n.ArSA, "rtl"},
		{i18n.Locale("he-IL"), "rtl"},
		{i18n.Locale("fa-IR"), "rtl"},
		{i18n.Locale("ur-PK"), "rtl"},
		{i18n.Locale(""), "ltr"},
	}
	for _, tc := range tests {
		t.Run(string(tc.loc), func(t *testing.T) {
			if got := tc.loc.Dir(); got != tc.want {
				t.Errorf("Dir(%q)=%q want %q", tc.loc, got, tc.want)
			}
		})
	}
}

func TestLocale_IsRTL(t *testing.T) {
	t.Parallel()
	if !i18n.ArSA.IsRTL() {
		t.Error("ArSA must be RTL")
	}
	if i18n.EnUS.IsRTL() {
		t.Error("EnUS must not be RTL")
	}
}

func TestLocale_String(t *testing.T) {
	t.Parallel()
	if got := i18n.EnUS.String(); got != "en-US" {
		t.Errorf("String=%q", got)
	}
}

func TestLocale_NativeName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		loc  i18n.Locale
		want string
	}{
		{i18n.EnUS, "English"},
		{i18n.IdID, "Bahasa Indonesia"},
		{i18n.MsMY, "Bahasa Melayu"},
		{i18n.JaJP, "日本語"},
		{i18n.ArSA, "العربية"},
		{i18n.Locale("de-DE"), "de-DE"},
	}
	for _, tc := range tests {
		if got := tc.loc.NativeName(); got != tc.want {
			t.Errorf("NativeName(%q)=%q want %q", tc.loc, got, tc.want)
		}
	}
}

func TestInvalidLocaleError_Error(t *testing.T) {
	t.Parallel()
	if got := (&i18n.InvalidLocaleError{}).Error(); got == "" {
		t.Error("empty tag: expected non-empty error")
	}
	if got := (&i18n.InvalidLocaleError{Tag: "xx"}).Error(); got == "" {
		t.Error("with tag: expected non-empty error")
	}
}

func TestBundle_ParseHappyPath(t *testing.T) {
	t.Parallel()
	b := i18n.NewEmbeddedBundle(i18n.EnUS)
	tests := []struct {
		in   string
		want i18n.Locale
	}{
		{"en-US", i18n.EnUS},
		{"EN-us", i18n.EnUS},
		{"id-ID", i18n.IdID},
		{"ms-MY", i18n.MsMY},
		{"ja-JP", i18n.JaJP},
		{"ar-SA", i18n.ArSA},
		{"en_US", i18n.EnUS},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, err := b.Parse(tc.in)
			if err != nil {
				t.Fatalf("Parse(%q) err=%v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("Parse(%q)=%q want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestBundle_ParsePrimarySubtagFallback(t *testing.T) {
	t.Parallel()
	b := i18n.NewEmbeddedBundle(i18n.EnUS)
	got, err := b.Parse("en-GB")
	if err != nil {
		t.Fatalf("Parse(en-GB) err=%v", err)
	}
	if got != i18n.EnUS {
		t.Errorf("Parse(en-GB)=%q want en-US", got)
	}
}

func TestBundle_ParseUnknownReturnsInvalidLocaleError(t *testing.T) {
	t.Parallel()
	b := i18n.NewEmbeddedBundle(i18n.EnUS)
	got, err := b.Parse("zz-ZZ")
	if err == nil {
		t.Fatalf("expected error for unknown, got nil (%q)", got)
	}
	if !i18n.IsInvalidLocaleError(err) {
		t.Errorf("want IsInvalidLocaleError, got %T: %v", err, err)
	}
	if got != i18n.EnUS {
		t.Errorf("fallback locale=%q want en-US", got)
	}
}

func TestBundle_ParseEmpty(t *testing.T) {
	t.Parallel()
	b := i18n.NewEmbeddedBundle(i18n.EnUS)
	_, err := b.Parse("")
	if err == nil {
		t.Fatal("expected error for empty tag")
	}
	if !i18n.IsInvalidLocaleError(err) {
		t.Errorf("want IsInvalidLocaleError, got %T", err)
	}
}

func TestIsInvalidLocaleError_Wrapped(t *testing.T) {
	t.Parallel()
	base := &i18n.InvalidLocaleError{Tag: "xx"}
	wrapped := fmt.Errorf("outer: %w", base)
	if !i18n.IsInvalidLocaleError(wrapped) {
		t.Error("wrapped InvalidLocaleError not detected")
	}
	if i18n.IsInvalidLocaleError(errors.New("plain")) {
		t.Error("plain error must not match")
	}
	if i18n.IsInvalidLocaleError(nil) {
		t.Error("nil must not match")
	}
}

func TestBundle_Negotiate(t *testing.T) {
	t.Parallel()
	b := i18n.NewEmbeddedBundle(i18n.EnUS)
	tests := []struct {
		name      string
		fallback  i18n.Locale
		preferred []string
		want      i18n.Locale
	}{
		{"first supported wins", i18n.EnUS, []string{"id-ID", "en-US"}, i18n.IdID},
		{"skips unknown", i18n.EnUS, []string{"zz-ZZ", "ja-JP"}, i18n.JaJP},
		{"empty falls back", i18n.MsMY, nil, i18n.MsMY},
		{"all-unknown uses default fallback", i18n.EnUS, []string{"zz", "yy"}, i18n.EnUS},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := b.Negotiate(tc.fallback, tc.preferred...)
			if got != tc.want {
				t.Errorf("Negotiate=%q want %q", got, tc.want)
			}
		})
	}
}
