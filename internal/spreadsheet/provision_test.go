package spreadsheet_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	apperrorv1 "altalune.id/opensheet/gen/go/apperror/v1"
	"altalune.id/opensheet/internal/apperror"
	"altalune.id/opensheet/internal/platform/tenant"
	"altalune.id/opensheet/internal/spreadsheet"
	"altalune.id/opensheet/internal/testutil/fakes"
)

type googleRecorder struct {
	mu          sync.Mutex
	creates     []string
	headers     []string
	failHeaders int
}

func (g *googleRecorder) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		g.mu.Lock()
		defer g.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/v4/spreadsheets" {
			g.creates = append(g.creates, string(body))
			_, _ = io.WriteString(w, `{"spreadsheetId":"NEWFILE"}`)
			return
		}
		g.headers = append(g.headers, r.URL.Path+" "+string(body))
		if g.failHeaders > 0 {
			g.failHeaders--
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"error":{"code":403,"message":"denied"}}`)
			return
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (g *googleRecorder) createCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.creates)
}

type fakePublisher struct {
	published map[string]spreadsheet.PublishedSheet
	failOnce  map[string]bool
	calls     []string
}

func (p *fakePublisher) Publish(_ context.Context, spreadsheetID uuid.UUID, tab, slug string) (spreadsheet.PublishedSheet, error) {
	p.calls = append(p.calls, slug)
	if p.failOnce[slug] {
		delete(p.failOnce, slug)
		return spreadsheet.PublishedSheet{}, errors.New("google read failed")
	}
	if got, ok := p.published[slug]; ok {
		return got, nil
	}
	ps := spreadsheet.PublishedSheet{ID: uuid.New(), Slug: slug, Tab: tab}
	p.published[slug] = ps
	_ = spreadsheetID
	return ps, nil
}

func (*fakePublisher) ValidSlug(slug string) error {
	if slug == "" || strings.ToLower(slug) != slug || strings.Contains(slug, " ") {
		return errors.New("invalid slug")
	}
	return nil
}

type provisionRig struct {
	store  *fakes.Spreadsheet
	google *googleRecorder
	pub    *fakePublisher
	tokens *fakeTokenSources
	flow   *spreadsheet.ProvisionWorkflow
	ctx    context.Context
	tc     tenant.Context
}

func newProvisionRig(t *testing.T) *provisionRig {
	t.Helper()
	g := &provisionRig{
		store:  fakes.NewSpreadsheet(),
		google: &googleRecorder{},
		pub:    &fakePublisher{published: map[string]spreadsheet.PublishedSheet{}, failOnce: map[string]bool{}},
		tokens: &fakeTokenSources{},
	}
	g.ctx, g.tc = tenantCtx(t)
	srv := g.google.server(t)
	svc, _ := newWriteSvc(t, g.store, g.tokens, writerFactoryFor(t, srv.URL))
	unexpected := func(_ context.Context, _ string, err error, _ ...any) *apperror.AppError {
		return apperror.New(apperror.CodeUnexpectedError, err.Error(), codes.Internal,
			&apperrorv1.ErrorDetail{Code: apperror.CodeUnexpectedError}).WithCause(err)
	}
	g.flow = spreadsheet.NewProvisionWorkflow(svc, writerFactoryFor(t, srv.URL), g.tokens, g.pub,
		slog.New(slog.DiscardHandler), unexpected)
	return g
}

func validProvision(credentialID uuid.UUID) spreadsheet.ProvisionRequest {
	return spreadsheet.ProvisionRequest{
		CredentialID:   credentialID,
		Title:          "Budget",
		IdempotencyKey: "connect-1",
		Tabs: []spreadsheet.ProvisionTab{
			{Title: "Wallets", Slug: "wallets", Headers: []string{"id", "name", "balance"}},
			{Title: "Transactions", Slug: "transactions", Headers: []string{"id", "amount"}},
		},
	}
}

func TestProvision_CreatesFileTabsHeadersAndSheets(t *testing.T) {
	g := newProvisionRig(t)
	credID := uuid.New()

	sp, sheets, err := g.flow.Run(g.ctx, validProvision(credID))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if g.google.createCount() != 1 {
		t.Fatalf("created %d files, want 1", g.google.createCount())
	}
	for _, want := range []string{`"title":"Budget"`, `"title":"Wallets"`, `"title":"Transactions"`} {
		if !strings.Contains(g.google.creates[0], want) {
			t.Errorf("create body %s lacks %s", g.google.creates[0], want)
		}
	}
	joined := strings.Join(g.google.headers, "\n")
	for _, want := range []string{`["id","name","balance"]`, `["id","amount"]`, "Wallets", "Transactions"} {
		if !strings.Contains(joined, want) {
			t.Errorf("header writes %s lack %s", joined, want)
		}
	}
	if sp.GoogleFileID != "NEWFILE" || sp.ProvisionKey != "connect-1" || !sp.Writable || sp.CredentialID != credID {
		t.Errorf("spreadsheet = %+v, want NEWFILE, key connect-1, writable, the request's credential", sp)
	}
	if sp.OrgID != g.tc.OrgID || sp.ProjectID != g.tc.ProjectID {
		t.Errorf("spreadsheet registered in %s/%s, want the caller's project", sp.OrgID, sp.ProjectID)
	}
	if len(sheets) != 2 || sheets[0].Slug != "wallets" || sheets[1].Slug != "transactions" {
		t.Errorf("sheets = %+v, want wallets and transactions in order", sheets)
	}
	if len(g.tokens.calls) == 0 || g.tokens.calls[0] != credID {
		t.Errorf("token source asked for %v, want the request's credential first", g.tokens.calls)
	}
}

func TestProvision_RetryWithSameKeyCreatesNoSecondFile(t *testing.T) {
	g := newProvisionRig(t)
	req := validProvision(uuid.New())

	first, _, err := g.flow.Run(g.ctx, req)
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	second, sheets, err := g.flow.Run(g.ctx, req)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if g.google.createCount() != 1 {
		t.Errorf("a retry created a second file: %d creates", g.google.createCount())
	}
	if second.ID != first.ID || len(sheets) != 2 {
		t.Errorf("retry returned %s with %d sheets, want %s with 2", second.ID, len(sheets), first.ID)
	}
}

func TestProvision_ResumesAfterSheetFailure(t *testing.T) {
	g := newProvisionRig(t)
	req := validProvision(uuid.New())
	g.pub.failOnce["transactions"] = true

	if _, _, err := g.flow.Run(g.ctx, req); err == nil {
		t.Fatal("a failed publish must fail the call")
	}
	sp, sheets, err := g.flow.Run(g.ctx, req)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if g.google.createCount() != 1 {
		t.Errorf("the resume created a second file: %d creates", g.google.createCount())
	}
	if len(sheets) != 2 || sp.GoogleFileID != "NEWFILE" {
		t.Errorf("resume returned %d sheets on %s, want both on NEWFILE", len(sheets), sp.GoogleFileID)
	}
	if got := len(g.pub.published); got != 2 {
		t.Errorf("published %d distinct sheets, want 2", got)
	}
}

func TestProvision_RetryAfterHeaderFailureCreatesNoSecondFile(t *testing.T) {
	g := newProvisionRig(t)
	req := validProvision(uuid.New())
	g.google.failHeaders = 1

	if _, _, err := g.flow.Run(g.ctx, req); err == nil {
		t.Fatal("a failed header write must fail the call")
	}
	sp, sheets, err := g.flow.Run(g.ctx, req)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if g.google.createCount() != 1 {
		t.Errorf("the retry created a second file: %d creates", g.google.createCount())
	}
	if sp.GoogleFileID != "NEWFILE" || len(sheets) != 2 {
		t.Errorf("retry returned %d sheets on %s, want both on NEWFILE", len(sheets), sp.GoogleFileID)
	}
	if last := g.google.headers[len(g.google.headers)-1]; !strings.Contains(last, `["id","amount"]`) {
		t.Errorf("the retry did not rewrite the headers: %s", last)
	}
}

func TestProvision_ConcurrentLoserReturnsTheWinner(t *testing.T) {
	g := newProvisionRig(t)
	req := validProvision(uuid.New())
	var winner spreadsheet.Spreadsheet
	g.store.SaveFn = func(ctx context.Context, s *spreadsheet.Spreadsheet) error {
		g.store.SaveFn = nil
		winner = *s
		winner.ID = uuid.New()
		winner.GoogleFileID = "WINNERFILE"
		if err := g.store.Save(ctx, &winner); err != nil {
			t.Fatalf("seed winner: %v", err)
		}
		return &spreadsheet.AlreadyExistsError{ProjectID: s.ProjectID.String()}
	}

	sp, _, err := g.flow.Run(g.ctx, req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if sp.ID != winner.ID {
		t.Errorf("loser returned %s, want the winner %s", sp.ID, winner.ID)
	}
}

func TestProvision_SoftDeleteDetected(t *testing.T) {
	g := newProvisionRig(t)
	req := validProvision(uuid.New())
	req.Tabs = req.Tabs[:1]
	req.Tabs[0].Headers = []string{"id", "name", "deleted_at"}

	if _, _, err := g.flow.Run(g.ctx, req); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if joined := strings.Join(g.google.headers, "\n"); !strings.Contains(joined, `["id","name","deleted_at"]`) {
		t.Errorf("header writes %s, want deleted_at in row 1 so publishing detects soft delete", joined)
	}
}

func TestProvision_InvalidRequest(t *testing.T) {
	tabs := func(n int) []spreadsheet.ProvisionTab {
		out := make([]spreadsheet.ProvisionTab, n)
		for i := range out {
			out[i] = spreadsheet.ProvisionTab{Title: "T" + string(rune('a'+i)), Slug: "t" + string(rune('a'+i)), Headers: []string{"id"}}
		}
		return out
	}
	headers := func(n int) []string {
		out := []string{"id"}
		for i := 1; i < n; i++ {
			out = append(out, "c"+strings.Repeat("x", i))
		}
		return out
	}
	tests := []struct {
		name  string
		field string
		edit  func(r *spreadsheet.ProvisionRequest)
	}{
		{"empty key", "idempotency_key", func(r *spreadsheet.ProvisionRequest) { r.IdempotencyKey = "" }},
		{"long key", "idempotency_key", func(r *spreadsheet.ProvisionRequest) { r.IdempotencyKey = strings.Repeat("k", 129) }},
		{"long title", "title", func(r *spreadsheet.ProvisionRequest) { r.Title = strings.Repeat("t", 201) }},
		{"no tabs", "tabs", func(r *spreadsheet.ProvisionRequest) { r.Tabs = nil }},
		{"eleven tabs", "tabs", func(r *spreadsheet.ProvisionRequest) { r.Tabs = tabs(11) }},
		{"blank tab title", "tabs.title", func(r *spreadsheet.ProvisionRequest) { r.Tabs[0].Title = " " }},
		{"long tab title", "tabs.title", func(r *spreadsheet.ProvisionRequest) { r.Tabs[0].Title = strings.Repeat("é", 101) }},
		{"duplicate tab title", "tabs.title", func(r *spreadsheet.ProvisionRequest) { r.Tabs[1].Title = r.Tabs[0].Title }},
		{"no headers", "tabs.headers", func(r *spreadsheet.ProvisionRequest) { r.Tabs[0].Headers = nil }},
		{"fifty-one headers", "tabs.headers", func(r *spreadsheet.ProvisionRequest) { r.Tabs[0].Headers = headers(51) }},
		{"duplicate header", "tabs.headers", func(r *spreadsheet.ProvisionRequest) { r.Tabs[0].Headers = []string{"id", "Name", "name "} }},
		{"blank header", "tabs.headers", func(r *spreadsheet.ProvisionRequest) { r.Tabs[0].Headers = []string{"id", ""} }},
		{"no id header", "tabs.headers", func(r *spreadsheet.ProvisionRequest) { r.Tabs[0].Headers = []string{"name"} }},
		{"bad slug", "tabs.slug", func(r *spreadsheet.ProvisionRequest) { r.Tabs[0].Slug = "Bad Slug" }},
		{"duplicate slug", "tabs.slug", func(r *spreadsheet.ProvisionRequest) { r.Tabs[1].Slug = r.Tabs[0].Slug }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newProvisionRig(t)
			req := validProvision(uuid.New())
			tt.edit(&req)

			_, _, err := g.flow.Run(g.ctx, req)
			invalid, ok := errors.AsType[*spreadsheet.ProvisionInvalidError](err)
			if !ok {
				t.Fatalf("err = %v, want a ProvisionInvalidError", err)
			}
			if invalid.Field != tt.field {
				t.Errorf("field = %q, want %q", invalid.Field, tt.field)
			}
			ae, _ := apperror.AsAppError(err)
			if ae.Code() != apperror.CodeSpreadsheetProvisionInvalid || ae.GRPCCode() != codes.InvalidArgument {
				t.Errorf("app error = %s %v, want %s InvalidArgument", ae.Code(), ae.GRPCCode(), apperror.CodeSpreadsheetProvisionInvalid)
			}
			if g.google.createCount() != 0 || len(g.tokens.calls) != 0 {
				t.Error("an invalid request reached Google or the credential")
			}
		})
	}
}
