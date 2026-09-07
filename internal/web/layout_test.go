package web_test

import (
	"testing"

	"altalune.id/template/internal/i18n"
	"altalune.id/template/internal/web"
)

func TestLayoutData_LocaleLabel(t *testing.T) {
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
		{"", "English"},
	}
	for _, tc := range tests {
		t.Run(string(tc.loc), func(t *testing.T) {
			d := web.LayoutData{Locale: tc.loc}
			if got := d.LocaleLabel(); got != tc.want {
				t.Errorf("LocaleLabel=%q want %q", got, tc.want)
			}
		})
	}
}

func TestLayoutData_LocaleOptions(t *testing.T) {
	t.Parallel()
	d := web.LayoutData{
		Locale:        i18n.IdID,
		SupportedLocs: []i18n.Locale{i18n.EnUS, i18n.IdID, i18n.MsMY, i18n.JaJP, i18n.ArSA},
	}
	opts := d.LocaleOptions()
	if len(opts) != 5 {
		t.Fatalf("options=%d want 5", len(opts))
	}
	var activeCount int
	for _, o := range opts {
		if o.Active {
			activeCount++
			if o.Code != "id-ID" {
				t.Errorf("active locale=%q want id-ID", o.Code)
			}
		}
		if o.Native == "" {
			t.Errorf("native name empty for %q", o.Code)
		}
	}
	if activeCount != 1 {
		t.Errorf("active count=%d want 1", activeCount)
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
		{"de-DE", "de-DE"},
	}
	for _, tc := range tests {
		t.Run(string(tc.loc), func(t *testing.T) {
			if got := tc.loc.NativeName(); got != tc.want {
				t.Errorf("NativeName(%q)=%q want %q", tc.loc, got, tc.want)
			}
		})
	}
}

func TestLayoutData_TrAndTrN(t *testing.T) {
	t.Parallel()
	b := i18n.NewEmbeddedBundle(i18n.EnUS)
	d := web.LayoutData{Locale: i18n.IdID, Translator: b.For(i18n.IdID)}
	if got := d.Tr("nav.overview"); got != "Ringkasan" {
		t.Errorf("Tr=%q", got)
	}
	if got := d.TrN("dashboard.projects_count", 3); got != "3 proyek" {
		t.Errorf("TrN=%q", got)
	}
}

func TestLayoutData_TrNilTranslator(t *testing.T) {
	t.Parallel()
	d := web.LayoutData{}
	if got := d.Tr("k"); got != "k" {
		t.Errorf("Tr nil=%q", got)
	}
	if got := d.TrN("k", 2); got != "k" {
		t.Errorf("TrN nil=%q", got)
	}
}
