package i18n_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"altalune.id/template/internal/i18n"
)

func TestE2E_QueryLangSwitchesTranslation(t *testing.T) {
	t.Parallel()
	b := i18n.NewEmbeddedBundle(i18n.EnUS)
	mw := i18n.Middleware(i18n.MiddlewareOpts{Bundle: b, Default: i18n.EnUS})

	tests := []struct {
		lang    string
		wantTr  string
		wantDir string
	}{
		{"id-ID", "Ringkasan", "ltr"},
		{"ar-SA", "نظرة عامة", "rtl"},
		{"en-US", "Overview", "ltr"},
	}
	for _, tc := range tests {
		t.Run(tc.lang, func(t *testing.T) {
			var gotTr, gotDir string
			handler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				loc := i18n.From(r.Context())
				tr := i18n.TranslatorFrom(r.Context())
				gotTr = tr.T("nav.overview")
				gotDir = b.Dir(loc)
			})
			req := httptest.NewRequest(http.MethodGet, "/?lang="+tc.lang, nil)
			mw(handler).ServeHTTP(httptest.NewRecorder(), req)
			if gotTr != tc.wantTr {
				t.Errorf("T(nav.overview)=%q want %q", gotTr, tc.wantTr)
			}
			if gotDir != tc.wantDir {
				t.Errorf("dir=%q want %q", gotDir, tc.wantDir)
			}
		})
	}
}
