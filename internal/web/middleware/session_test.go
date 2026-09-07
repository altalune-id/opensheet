package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"altalune.id/template/internal/platform/session"
	"altalune.id/template/internal/web"
	"altalune.id/template/internal/web/middleware"
)

func TestSession_LoadsPrincipal(t *testing.T) {
	t.Parallel()
	secret := []byte("supersecret-1234567890abcdef")
	store := session.NewMemoryStore()
	sid := "sid-123"
	uid := uuid.New()
	_ = store.Save(context.Background(), sid, session.Principal{UserID: uid, Email: "u@e.com"}, time.Now().Add(time.Hour))

	var seen session.Principal
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = session.PrincipalFrom(r.Context())
	})
	mw := middleware.Session(middleware.SessionConfig{Store: store, Secret: secret})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: web.SessionCookieName, Value: web.SignCookie(secret, sid)})
	mw(next).ServeHTTP(rr, req)

	if seen.UserID != uid {
		t.Errorf("principal UserID=%v, want %v", seen.UserID, uid)
	}
}

func TestSession_MissingCookieIsPassthrough(t *testing.T) {
	t.Parallel()
	store := session.NewMemoryStore()
	seen := session.Principal{UserID: uuid.New()}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = session.PrincipalFrom(r.Context())
	})
	mw := middleware.Session(middleware.SessionConfig{Store: store, Secret: []byte("k")})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	mw(next).ServeHTTP(rr, req)

	if seen.UserID != uuid.Nil {
		t.Errorf("expected empty principal, got %+v", seen)
	}
}

func TestSession_TamperedCookieIsPassthrough(t *testing.T) {
	t.Parallel()
	store := session.NewMemoryStore()
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		p := session.PrincipalFrom(r.Context())
		if p.UserID != uuid.Nil {
			t.Errorf("expected empty principal, got %v", p.UserID)
		}
	})
	mw := middleware.Session(middleware.SessionConfig{Store: store, Secret: []byte("k")})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: web.SessionCookieName, Value: "forged"})
	mw(next).ServeHTTP(rr, req)
}
