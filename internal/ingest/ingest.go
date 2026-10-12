// Package ingest implements S4, the inbound webhook surface: credential-free machine pushes from a third party, mounted under /hooks/{provider}/ and authenticated by provider signature.
package ingest

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"strings"
)

// DefaultBasePath is the mount prefix this surface reserves.
const DefaultBasePath = "/hooks"

// DefaultMaxBodyBytes bounds a delivery body when HandlerParams leaves the limit unset.
const DefaultMaxBodyBytes int64 = 1 << 20

// Verifier authenticates one provider's delivery against the raw body that was received.
type Verifier interface {
	Verify(r *http.Request, body []byte) error
}

// Provider binds a registered provider's signature verifier to the handler its verified deliveries reach.
type Provider struct {
	Verifier Verifier
	Handler  http.Handler
}

// HandlerParams configures NewHandler, with BasePath as the full mount prefix.
type HandlerParams struct {
	BasePath     string
	Providers    map[string]Provider
	MaxBodyBytes int64
	Log          *slog.Logger
}

// Handler serves S4, reading a delivery body once, verifying it, and only then dispatching.
type Handler struct {
	mux       *http.ServeMux
	providers map[string]Provider
	maxBody   int64
	log       *slog.Logger
}

// NewHandler returns the ingest surface handler, which 404s every delivery until a provider is registered.
func NewHandler(p HandlerParams) *Handler {
	base := strings.TrimRight(p.BasePath, "/")
	if base == "" {
		base = DefaultBasePath
	}
	maxBody := p.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = DefaultMaxBodyBytes
	}
	log := p.Log
	if log == nil {
		log = slog.Default()
	}

	h := &Handler{
		mux:       http.NewServeMux(),
		providers: maps.Clone(p.Providers),
		maxBody:   maxBody,
		log:       log,
	}
	h.mux.Handle(base+"/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, &UnknownProviderError{})
	}))
	h.mux.Handle(base+"/{provider}", http.HandlerFunc(h.deliver))
	h.mux.Handle(base+"/{provider}/", http.HandlerFunc(h.deliver))
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.mux.ServeHTTP(w, r) }

// SECURITY: provider identity IS the authorization here (R4), so every path out before Verify returns nil is a rejection.
func (h *Handler) deliver(w http.ResponseWriter, r *http.Request) {
	p, ok := h.providers[r.PathValue("provider")]
	if !ok {
		writeError(w, &UnknownProviderError{})
		return
	}
	if p.Verifier == nil {
		writeError(w, &UnverifiedError{})
		return
	}

	body, err := h.readBody(w, r)
	if err != nil {
		writeError(w, err)
		return
	}
	if err := p.Verifier.Verify(r, body); err != nil {
		h.log.WarnContext(r.Context(), "ingest: delivery failed verification",
			slog.String("provider", r.PathValue("provider")), slog.String("err", err.Error()))
		writeError(w, &UnverifiedError{})
		return
	}
	if p.Handler == nil {
		writeError(w, &NoHandlerError{})
		return
	}

	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	p.Handler.ServeHTTP(w, r)
}

func (h *Handler) readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.maxBody))
	if err == nil {
		return body, nil
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return nil, &PayloadTooLargeError{}
	}
	return nil, &BadRequestError{}
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// SECURITY: the body names the outcome and nothing else; the cause lives in the log under the request id.
func writeError(w http.ResponseWriter, err error) {
	f := statusFor(err)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(f.status)
	_ = json.NewEncoder(w).Encode(errorBody{Code: f.code, Message: f.code})
}

type failure struct {
	status int
	code   string
}

func statusFor(err error) failure {
	switch {
	case IsUnknownProviderError(err):
		return failure{http.StatusNotFound, "not_found"}
	case IsUnverifiedError(err):
		return failure{http.StatusUnauthorized, "unauthorized"}
	case IsPayloadTooLargeError(err):
		return failure{http.StatusRequestEntityTooLarge, "payload_too_large"}
	case IsBadRequestError(err):
		return failure{http.StatusBadRequest, "bad_request"}
	case IsNoHandlerError(err):
		return failure{http.StatusInternalServerError, "internal"}
	}
	return failure{http.StatusInternalServerError, "internal"}
}
