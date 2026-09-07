package i18n

import (
	"context"
	"net/http"
)

// CookieName is the browser-visible cookie carrying the user's chosen locale.
const CookieName = "alt_locale"

// QueryParam is the URL query parameter that overrides the resolved locale.
const QueryParam = "lang"

// UserLocaleLookup returns the caller's stored locale, or "" if unknown/absent.
type UserLocaleLookup func(ctx context.Context) string

type localeKey struct{}
type translatorKey struct{}

// LocaleInto returns a derived context carrying loc.
func LocaleInto(ctx context.Context, loc Locale) context.Context {
	return context.WithValue(ctx, localeKey{}, loc)
}

// TranslatorInto returns a derived context carrying t.
func TranslatorInto(ctx context.Context, t *Translator) context.Context {
	return context.WithValue(ctx, translatorKey{}, t)
}

// From returns the Locale on ctx; falls back to EnUS when unset.
func From(ctx context.Context) Locale {
	if loc, ok := ctx.Value(localeKey{}).(Locale); ok {
		return loc
	}
	return EnUS
}

// TranslatorFrom returns the Translator on ctx; nil-safe fallback returns key on lookup.
func TranslatorFrom(ctx context.Context) *Translator {
	if t, ok := ctx.Value(translatorKey{}).(*Translator); ok && t != nil {
		return t
	}
	return nil
}

// MiddlewareOpts configures Middleware's resolution chain.
type MiddlewareOpts struct {
	Bundle     *Bundle
	Default    Locale
	UserLookup UserLocaleLookup
}

// Middleware injects the resolved Locale + Translator into the request context.
func Middleware(opts MiddlewareOpts) func(http.Handler) http.Handler {
	if opts.Bundle == nil {
		panic("i18n.Middleware: nil Bundle")
	}
	defaultLoc := opts.Default
	if defaultLoc == "" {
		defaultLoc = opts.Bundle.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			loc := resolve(r, opts.Bundle, defaultLoc, opts.UserLookup)
			ctx := LocaleInto(r.Context(), loc)
			ctx = TranslatorInto(ctx, opts.Bundle.For(loc))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func resolve(r *http.Request, b *Bundle, defaultLoc Locale, lookup UserLocaleLookup) Locale {
	if q := r.URL.Query().Get(QueryParam); q != "" {
		if loc, ok := matchTag(q, b.All()); ok {
			return loc
		}
	}
	if c, err := r.Cookie(CookieName); err == nil && c.Value != "" {
		if loc, ok := matchTag(c.Value, b.All()); ok {
			return loc
		}
	}
	if lookup != nil {
		if tag := lookup(r.Context()); tag != "" {
			if loc, ok := matchTag(tag, b.All()); ok {
				return loc
			}
		}
	}
	if header := r.Header.Get("Accept-Language"); header != "" {
		for _, tag := range parseAcceptLanguage(header) {
			if loc, ok := matchTag(tag, b.All()); ok {
				return loc
			}
		}
	}
	if _, ok := matchTag(string(defaultLoc), b.All()); ok {
		return defaultLoc
	}
	return b.Default()
}
