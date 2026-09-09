package handlers_test

import (
	"context"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/org"
	"altalune.id/opensheet/internal/platform/capabilities"
	"altalune.id/opensheet/internal/platform/config"
	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/internal/testutil/fakes"
	"altalune.id/opensheet/internal/web"
	"altalune.id/opensheet/internal/web/handlers"
)

func TestPostRemoveMember_SystemProtected_Returns409(t *testing.T) {
	t.Parallel()

	store := fakes.NewOrg()
	unexpected := func(_ context.Context, _ string, cause error, _ ...any) *apperror.AppError {
		return apperror.New("opensheet.unexpected", "unexpected", codes.Internal, &apperrorv1.ErrorDetail{Code: "opensheet.unexpected"}).WithCause(cause)
	}
	orgSvc := org.NewService(store, capabilities.Capabilities{OrgCreation: true}, slog.New(slog.NewTextHandler(io.Discard, nil)), unexpected)

	ctx := context.Background()
	owner := uuid.New()
	target := uuid.New()
	o, err := orgSvc.Create(ctx, org.CreateRequest{Slug: "acme", Name: "Acme", OwnerID: owner})
	if err != nil {
		t.Fatal(err)
	}
	sysMem, err := org.NewMembership(o.ID, target, org.RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	sysMem.System = true
	if err := store.SaveMembership(ctx, sysMem); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	cfg.HTTP.BasePath = ""
	cfg.HTTP.StateSecret = "0123456789abcdef0123456789abcdef"
	sessions := session.NewMemoryStore()

	principal := session.Principal{
		UserID:      owner,
		Email:       "owner@example.com",
		Name:        "Owner",
		Source:      session.SourceLocal,
		ActiveOrgID: o.ID,
		IsAdmin:     true,
		IssuedAt:    time.Now().UTC(),
	}
	sid, err := web.NewSID()
	if err != nil {
		t.Fatal(err)
	}
	if err := sessions.Save(ctx, sid, principal, time.Now().Add(web.SessionTTL)); err != nil {
		t.Fatal(err)
	}
	cookieValue := web.SignCookie([]byte(cfg.HTTP.StateSecret), sid)

	deps := handlers.Deps{
		Cfg:      cfg,
		Caps:     capabilities.Capabilities{OrgCreation: true},
		Sessions: sessions,
		Secret:   []byte(cfg.HTTP.StateSecret),
		Logger:   log.New(io.Discard, "", 0),
	}
	h := handlers.NewOrgHandler(deps, orgSvc)

	mux := http.NewServeMux()
	h.Register(mux)

	target41 := target.String()
	req := httptest.NewRequest(http.MethodPost, "/orgs/acme/members/"+target41+"/remove", strings.NewReader(""))
	req.AddCookie(&http.Cookie{Name: web.SessionCookieName, Value: cookieValue})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d want 409; body=%s", rec.Code, rec.Body.String())
	}
}
