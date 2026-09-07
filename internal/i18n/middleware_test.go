package i18n_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"altalune.id/template/internal/i18n"
)

func handler(t *testing.T, seen *i18n.Locale) http.HandlerFunc {
	t.Helper()
	return func(_ http.ResponseWriter, r *http.Request) {
		*seen = i18n.From(r.Context())
		if tr := i18n.TranslatorFrom(r.Context()); tr == nil {
			t.Error("Translator missing from ctx")
		}
	}
}

func TestMiddleware_ResolutionChain(t *testing.T) {
	t.Parallel()
	b := i18n.NewEmbeddedBundle(i18n.EnUS)
	tests := []struct {
		name       string
		buildReq   func() *http.Request
		userLocale string
		want       i18n.Locale
	}{
		{
			name: "query overrides all",
			buildReq: func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, "/?lang=id-ID", nil)
				r.AddCookie(&http.Cookie{Name: i18n.CookieName, Value: "ja-JP"})
				r.Header.Set("Accept-Language", "ar-SA")
				return r
			},
			userLocale: "ms-MY",
			want:       i18n.IdID,
		},
		{
			name: "cookie beats user profile and header",
			buildReq: func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, "/", nil)
				r.AddCookie(&http.Cookie{Name: i18n.CookieName, Value: "ja-JP"})
				r.Header.Set("Accept-Language", "ar-SA")
				return r
			},
			userLocale: "ms-MY",
			want:       i18n.JaJP,
		},
		{
			name: "user profile beats header when cookie missing",
			buildReq: func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, "/", nil)
				r.Header.Set("Accept-Language", "ar-SA")
				return r
			},
			userLocale: "ms-MY",
			want:       i18n.MsMY,
		},
		{
			name: "cookie beats header",
			buildReq: func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, "/", nil)
				r.AddCookie(&http.Cookie{Name: i18n.CookieName, Value: "ja-JP"})
				r.Header.Set("Accept-Language", "ar-SA")
				return r
			},
			want: i18n.JaJP,
		},
		{
			name: "header when cookie missing",
			buildReq: func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, "/", nil)
				r.Header.Set("Accept-Language", "ar-SA,en;q=0.9")
				return r
			},
			want: i18n.ArSA,
		},
		{
			name: "default when nothing set",
			buildReq: func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "/", nil)
			},
			want: i18n.EnUS,
		},
		{
			name: "invalid query falls through to next",
			buildReq: func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, "/?lang=zz-ZZ", nil)
				r.AddCookie(&http.Cookie{Name: i18n.CookieName, Value: "ja-JP"})
				return r
			},
			want: i18n.JaJP,
		},
		{
			name: "invalid cookie falls through to header",
			buildReq: func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, "/", nil)
				r.AddCookie(&http.Cookie{Name: i18n.CookieName, Value: "zz-ZZ"})
				r.Header.Set("Accept-Language", "id-ID")
				return r
			},
			want: i18n.IdID,
		},
		{
			name: "empty user lookup is ignored",
			buildReq: func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, "/", nil)
				r.AddCookie(&http.Cookie{Name: i18n.CookieName, Value: "ja-JP"})
				return r
			},
			userLocale: "",
			want:       i18n.JaJP,
		},
		{
			name: "primary subtag matches header en-GB -> en-US",
			buildReq: func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, "/", nil)
				r.Header.Set("Accept-Language", "en-GB")
				return r
			},
			want: i18n.EnUS,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var seen i18n.Locale
			var lookup i18n.UserLocaleLookup
			if tc.userLocale != "" || tc.name == "empty user lookup is ignored" {
				lookup = func(_ context.Context) string { return tc.userLocale }
			}
			mw := i18n.Middleware(i18n.MiddlewareOpts{Bundle: b, Default: i18n.EnUS, UserLookup: lookup})
			mw(handler(t, &seen)).ServeHTTP(httptest.NewRecorder(), tc.buildReq())
			if seen != tc.want {
				t.Errorf("resolved=%q want %q", seen, tc.want)
			}
		})
	}
}

func TestMiddleware_TranslatorInContext(t *testing.T) {
	t.Parallel()
	b := i18n.NewEmbeddedBundle(i18n.EnUS)
	var seenTr *i18n.Translator
	mw := i18n.Middleware(i18n.MiddlewareOpts{Bundle: b, Default: i18n.EnUS})
	mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seenTr = i18n.TranslatorFrom(r.Context())
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/?lang=id-ID", nil))

	if seenTr == nil {
		t.Fatal("Translator must be present")
	}
	if got := seenTr.T("nav.overview"); got != "Ringkasan" {
		t.Errorf("ctx translator got %q, want Indonesian", got)
	}
	if got := seenTr.Locale(); got != i18n.IdID {
		t.Errorf("Translator.Locale=%q want id-ID", got)
	}
}

func TestFrom_DefaultsToEnUS(t *testing.T) {
	t.Parallel()
	if got := i18n.From(context.Background()); got != i18n.EnUS {
		t.Errorf("From(bg)=%q", got)
	}
}

func TestTranslatorFrom_NilOnEmptyCtx(t *testing.T) {
	t.Parallel()
	if tr := i18n.TranslatorFrom(context.Background()); tr != nil {
		t.Error("empty ctx should return nil Translator")
	}
}

func TestMiddleware_NilBundlePanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic")
		}
	}()
	i18n.Middleware(i18n.MiddlewareOpts{})
}
