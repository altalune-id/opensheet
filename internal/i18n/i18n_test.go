package i18n_test

import (
	"strings"
	"testing"
	"testing/fstest"

	"altalune.id/template/internal/i18n"
)

func TestTranslator_MissingKeyInLocaleFallsBackToDefault(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"active.en-US.yaml": &fstest.MapFile{Data: []byte("greeting: \"Hi\"\nbye: \"Bye\"\n")},
		"active.ar-SA.yaml": &fstest.MapFile{Data: []byte("greeting: \"مرحبا\"\n")},
	}
	b := i18n.NewBundle(fsys, i18n.EnUS)
	if got := b.For(i18n.Locale("ar-SA")).T("greeting"); got != "مرحبا" {
		t.Errorf("greeting=%q — should render Arabic", got)
	}
	if got := b.For(i18n.Locale("ar-SA")).T("bye"); got != "Bye" {
		t.Errorf("bye=%q — missing in ar-SA, must fall back to en-US", got)
	}
	if got := b.For(i18n.Locale("ar-SA")).T("nowhere"); got != "nowhere" {
		t.Errorf("truly-missing key=%q — should return key literal", got)
	}
}

func TestBundle_DefaultReportsFallback(t *testing.T) {
	t.Parallel()
	b := i18n.NewEmbeddedBundle(i18n.IdID)
	if got := b.Default(); got != i18n.IdID {
		t.Errorf("Default=%q want id-ID", got)
	}
}

func TestEmbeddedLocalesFS_HasFiles(t *testing.T) {
	t.Parallel()
	fsys := i18n.EmbeddedLocalesFS()
	if fsys == nil {
		t.Fatal("nil fs")
	}
	f, err := fsys.Open("locales/active.en-US.yaml")
	if err != nil {
		t.Fatalf("open en-US: %v", err)
	}
	_ = f.Close()
}

func TestBundle_LoadsAllLocales(t *testing.T) {
	t.Parallel()
	b := i18n.NewEmbeddedBundle(i18n.EnUS)
	all := b.All()
	if len(all) < 5 {
		t.Fatalf("expected >=5 locales loaded, got %d: %v", len(all), all)
	}
	want := map[i18n.Locale]bool{i18n.EnUS: true, i18n.IdID: true, i18n.MsMY: true, i18n.JaJP: true, i18n.ArSA: true}
	for _, l := range all {
		delete(want, l)
	}
	if len(want) != 0 {
		t.Fatalf("missing locales: %v (loaded: %v)", want, b.All())
	}
}

func TestTranslator_T_SanityAcrossLocales(t *testing.T) {
	t.Parallel()
	b := i18n.NewEmbeddedBundle(i18n.EnUS)
	tests := []struct {
		loc  i18n.Locale
		want string
	}{
		{i18n.EnUS, "Overview"},
		{i18n.IdID, "Ringkasan"},
		{i18n.MsMY, "Gambaran keseluruhan"},
		{i18n.JaJP, "概要"},
		{i18n.ArSA, "نظرة عامة"},
	}
	for _, tc := range tests {
		t.Run(string(tc.loc), func(t *testing.T) {
			got := b.For(tc.loc).T("nav.overview")
			if got != tc.want {
				t.Errorf("T(nav.overview)=%q want %q", got, tc.want)
			}
		})
	}
}

func TestTranslator_T_MissingKeyReturnsKey(t *testing.T) {
	t.Parallel()
	b := i18n.NewEmbeddedBundle(i18n.EnUS)
	got := b.For(i18n.EnUS).T("nav.does_not_exist")
	if got != "nav.does_not_exist" {
		t.Errorf("missing key should return the key, got %q", got)
	}
}

func TestTranslator_T_NilTranslator(t *testing.T) {
	t.Parallel()
	var tr *i18n.Translator
	got := tr.T("some.key")
	if got != "some.key" {
		t.Errorf("nil Translator should return key, got %q", got)
	}
	if got := tr.Tn("some.key", 3); got != "some.key" {
		t.Errorf("nil Translator should return key, got %q", got)
	}
}

func TestTranslator_Tn_EnglishPlurals(t *testing.T) {
	t.Parallel()
	b := i18n.NewEmbeddedBundle(i18n.EnUS)
	tr := b.For(i18n.EnUS)
	tests := []struct {
		n    int
		want string
	}{
		{0, "0 projects"},
		{1, "1 project"},
		{2, "2 projects"},
	}
	for _, tc := range tests {
		got := tr.Tn("dashboard.projects_count", tc.n)
		if got != tc.want {
			t.Errorf("Tn(count=%d)=%q want %q", tc.n, got, tc.want)
		}
	}
}

func TestTranslator_Tn_ArabicPluralForms(t *testing.T) {
	t.Parallel()
	b := i18n.NewEmbeddedBundle(i18n.EnUS)
	tr := b.For(i18n.ArSA)
	tests := []struct {
		n       int
		wantSub string
	}{
		{0, "لا توجد مشاريع"},
		{1, "مشروع واحد"},
		{2, "مشروعان"},
		{3, "مشاريع"},
		{11, "مشروعًا"},
		{100, "مشروع"},
	}
	for _, tc := range tests {
		got := tr.Tn("dashboard.projects_count", tc.n)
		if !strings.Contains(got, tc.wantSub) {
			t.Errorf("Tn(count=%d)=%q must contain %q", tc.n, got, tc.wantSub)
		}
	}
}

func TestTranslator_Tn_ExtraArgs(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"active.en-US.yaml": &fstest.MapFile{Data: []byte(`
greeting:
  one: "hi {{.Name}} — {{.Count}} message"
  other: "hi {{.Name}} — {{.Count}} messages"
`)},
	}
	b := i18n.NewBundle(fsys, i18n.EnUS)
	got := b.For(i18n.EnUS).Tn("greeting", 3, "Name", "Alice")
	want := "hi Alice — 3 messages"
	if got != want {
		t.Errorf("Tn=%q want %q", got, want)
	}
}

func TestTranslator_ArgsInterpolation(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"active.en-US.yaml": &fstest.MapFile{Data: []byte(`greeting: "Hi {{.Name}}"`)},
		"active.id-ID.yaml": &fstest.MapFile{Data: []byte(`greeting: "Halo {{.Name}}"`)},
	}
	b := i18n.NewBundle(fsys, i18n.EnUS)
	if got := b.For(i18n.EnUS).T("greeting", "Name", "Alice"); got != "Hi Alice" {
		t.Errorf("en-US=%q", got)
	}
	if got := b.For(i18n.IdID).T("greeting", "Name", "Alice"); got != "Halo Alice" {
		t.Errorf("id-ID=%q", got)
	}
}

func TestBundle_Has(t *testing.T) {
	t.Parallel()
	b := i18n.NewEmbeddedBundle(i18n.EnUS)
	if !b.Has("en-US") {
		t.Error("Has(en-US) should be true")
	}
	if b.Has("zz-ZZ") {
		t.Error("Has(zz-ZZ) should be false")
	}
}

func TestBundle_Dir_UsesFileHeader(t *testing.T) {
	t.Parallel()
	b := i18n.NewEmbeddedBundle(i18n.EnUS)
	if got := b.Dir(i18n.ArSA); got != "rtl" {
		t.Errorf("Dir(ar-SA)=%q want rtl", got)
	}
	if got := b.Dir(i18n.EnUS); got != "ltr" {
		t.Errorf("Dir(en-US)=%q want ltr", got)
	}
}

func TestBundle_DynamicLocale_FromFSPreservesFilename(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"active.en-US.yaml": &fstest.MapFile{Data: []byte(`greeting: "hi"`)},
		"active.de-DE.yaml": &fstest.MapFile{Data: []byte(`greeting: "hallo"`)},
	}
	b := i18n.NewBundle(fsys, i18n.EnUS)
	got, err := b.Parse("de-DE")
	if err != nil {
		t.Fatalf("Parse(de-DE) err=%v", err)
	}
	if got != i18n.Locale("de-DE") {
		t.Errorf("Parse(de-DE)=%q", got)
	}
	if s := b.For(i18n.Locale("de-DE")).T("greeting"); s != "hallo" {
		t.Errorf("de-DE greeting=%q", s)
	}
}

func TestBundle_MetaBlockOverridesDir(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"active.en-US.yaml": &fstest.MapFile{Data: []byte("greeting: \"hi\"\n")},
		"active.sw-KE.yaml": &fstest.MapFile{Data: []byte("_meta:\n  dir: rtl\ngreeting: \"salaam\"\n")},
	}
	b := i18n.NewBundle(fsys, i18n.EnUS)
	if got := b.Dir(i18n.Locale("sw-KE")); got != "rtl" {
		t.Errorf("Dir(sw-KE)=%q want rtl from _meta", got)
	}
	if got := b.For(i18n.Locale("sw-KE")).T("greeting"); got != "salaam" {
		t.Errorf("T(greeting)=%q — _meta should have been stripped from message tree", got)
	}
}

func TestNewBundle_NoLocaleFiles_Panics(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic with empty FS")
		}
	}()
	i18n.NewBundle(fstest.MapFS{}, i18n.EnUS)
}

func TestNewBundle_InvalidTagInFilename_Panics(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for invalid tag")
		}
	}()
	i18n.NewBundle(fstest.MapFS{
		"active.notatag!.yaml": &fstest.MapFile{Data: []byte(`k: v`)},
	}, i18n.EnUS)
}
