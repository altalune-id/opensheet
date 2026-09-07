package handlers

import (
	"net/http"

	"altalune.id/template/internal/legal"
	"altalune.id/template/internal/web/templates"
)

// LegalHandler serves the embedded Terms and Privacy documents at /terms and /privacy.
type LegalHandler struct{ Deps }

// NewLegalHandler wires the handler.
func NewLegalHandler(d Deps) *LegalHandler { return &LegalHandler{Deps: d} }

// Register mounts the /terms and /privacy routes on mux.
func (h *LegalHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /terms", h.get(legal.TermsSlug, "Terms of Service"))
	mux.HandleFunc("GET /privacy", h.get(legal.PrivacySlug, "Privacy Policy"))
}

func (h *LegalHandler) get(slug, fallbackTitle string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		doc, err := legal.BySlug(slug)
		if err != nil {
			h.LogErr("legal: load", err)
			h.ErrorPage(w, r, http.StatusInternalServerError, "Load failed", "Could not load the document.", err)
			return
		}
		title := doc.Title
		if title == "" {
			title = fallbackTitle
		}
		Render(w, r, templates.LegalLayout(h.Base(r, title), templates.LegalView{Doc: doc}))
	}
}
