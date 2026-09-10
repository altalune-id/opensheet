package data

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/platform/authn"
	"altalune.id/opensheet/internal/sheet"
)

const maxWriteBodyBytes = 1 << 20

const (
	idempotencyHeader = "Idempotency-Key"
	numericColumnsKey = "numeric_columns"
)

type appendRequest struct {
	Values         []any    `json:"values"`
	NumericColumns []string `json:"numeric_columns"`
}

type appendResponse struct {
	Appended int `json:"appended"`
}

type tabsResponse struct {
	Tabs []string `json:"tabs"`
}

type batchRequest struct {
	Rows []json.RawMessage `json:"rows"`
}

type batchResponse struct {
	IDs []string `json:"ids"`
}

type createTabRequest struct {
	Title string `json:"title"`
}

type createTabResponse struct {
	Created string `json:"created"`
}

func (h *handler) appendRow(w http.ResponseWriter, r *http.Request) {
	// SECURITY: resolve then authorize, as rows does. The writable flag is checked inside the workflow,
	// strictly after authorization, so it never becomes an unauthenticated oracle on a slug.
	r, sc, err := h.resolve(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if aErr := h.authorize(r, sc, authn.ScopeSheetsWrite); aErr != nil {
		h.fail(w, r, aErr)
		return
	}
	body, err := readBody(w, r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	cells, err := parseAppend(body)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	hash, err := cellsHash(cells)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	n, err := h.writer.Append(r.Context(), sc.sheet, cells,
		strings.TrimSpace(r.Header.Get(idempotencyHeader)), hash)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, http.StatusOK, appendResponse{Appended: n})
}

func (h *handler) patchRow(w http.ResponseWriter, r *http.Request) {
	// SECURITY: resolve then authorize, as rows does.
	r, sc, err := h.resolve(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if aErr := h.authorize(r, sc, authn.ScopeSheetsWrite); aErr != nil {
		h.fail(w, r, aErr)
		return
	}
	body, err := readBody(w, r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	patch, err := parsePatch(body)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	row, err := h.writer.PatchRow(r.Context(), sc.sheet, r.PathValue("id"), patch)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, http.StatusOK, row)
}

func (h *handler) createRow(w http.ResponseWriter, r *http.Request) {
	// SECURITY: resolve then authorize, as rows does. allowRead is deliberately not reused: it bypasses
	// authorization for a public sheet, which on a write route would make every public sheet anonymously writable.
	r, sc, err := h.resolve(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if aErr := h.authorize(r, sc, authn.ScopeSheetsWrite); aErr != nil {
		h.fail(w, r, aErr)
		return
	}
	body, err := readBody(w, r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	fields, err := parsePatch(body)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	row, err := h.writer.CreateRow(r.Context(), sc.sheet, fields)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeRow(w, r, http.StatusCreated, row)
}

func (h *handler) createRows(w http.ResponseWriter, r *http.Request) {
	// SECURITY: resolve then authorize, as createRow does, and for the same reason.
	r, sc, err := h.resolve(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if aErr := h.authorize(r, sc, authn.ScopeSheetsWrite); aErr != nil {
		h.fail(w, r, aErr)
		return
	}
	body, err := readBody(w, r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	rows, err := parseBatch(body)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	ids, err := h.writer.CreateRows(r.Context(), sc.sheet, rows)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, r, http.StatusCreated, batchResponse{IDs: ids})
}

func (h *handler) replaceRow(w http.ResponseWriter, r *http.Request) {
	// SECURITY: resolve then authorize, as createRow does, and for the same reason.
	r, sc, err := h.resolve(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if aErr := h.authorize(r, sc, authn.ScopeSheetsWrite); aErr != nil {
		h.fail(w, r, aErr)
		return
	}
	body, err := readBody(w, r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	fields, err := parsePatch(body)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	row, err := h.writer.ReplaceRow(r.Context(), sc.sheet, r.PathValue("id"), fields)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeRow(w, r, http.StatusOK, row)
}

func (h *handler) listTabs(w http.ResponseWriter, r *http.Request) {
	// SECURITY: resolve then authorize, as rows does.
	r, sc, err := h.resolveProject(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if aErr := h.authorizeProject(r, sc, authn.ScopeSpreadsheetsRead); aErr != nil {
		h.fail(w, r, aErr)
		return
	}
	id, err := spreadsheetID(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	tabs, err := h.tabs.ListTabs(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if tabs == nil {
		tabs = []string{}
	}
	h.writeJSON(w, r, http.StatusOK, tabsResponse{Tabs: tabs})
}

func (h *handler) createTab(w http.ResponseWriter, r *http.Request) {
	// SECURITY: resolve then authorize, as rows does. The spreadsheet's writable flag is checked inside
	// the service, strictly after authorization.
	r, sc, err := h.resolveProject(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if aErr := h.authorizeProject(r, sc, authn.ScopeSpreadsheetsWrite); aErr != nil {
		h.fail(w, r, aErr)
		return
	}
	id, err := spreadsheetID(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	body, err := readBody(w, r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	title, err := parseCreateTab(body)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if aErr := h.tabs.AddTab(r.Context(), id, title); aErr != nil {
		h.fail(w, r, aErr)
		return
	}
	h.writeJSON(w, r, http.StatusCreated, createTabResponse{Created: title})
}

func (h *handler) writeRow(w http.ResponseWriter, r *http.Request, status int, row sheet.WrittenRow) {
	if row.ETag != "" {
		w.Header().Set("ETag", strconv.Quote(row.ETag))
	}
	h.writeJSON(w, r, status, row.Data)
}

func (h *handler) writeJSON(w http.ResponseWriter, r *http.Request, status int, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	head := w.Header()
	head.Set("Content-Type", "application/json; charset=utf-8")
	head.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func spreadsheetID(r *http.Request) (uuid.UUID, error) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		return uuid.Nil, &InvalidBodyError{Reason: "the spreadsheet id must be a UUID"}
	}
	return id, nil
}

func readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWriteBodyBytes))
	if err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return nil, &PayloadTooLargeError{Limit: maxWriteBodyBytes}
		}
		return nil, err
	}
	return body, nil
}

// NOTE: the cells, not the raw bytes — a retry that re-serializes its body, or sends the bare-array
// shape, must replay rather than read as a different body and be refused 422.
func cellsHash(cells []any) (string, error) {
	canonical, err := json.Marshal(cells)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

// NOTE: numeric_columns names header columns, which this layer cannot map to positions — the append
// body is positional and the workflow reads no header row. A numeric cell is sent as a JSON number.
func parseAppend(body []byte) ([]any, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, &InvalidBodyError{Reason: "the body must name values, or be a JSON array of cells"}
	}
	if trimmed[0] == '[' {
		var cells []any
		if err := json.Unmarshal(trimmed, &cells); err != nil {
			return nil, &InvalidBodyError{Reason: "the body must be a JSON array of cells"}
		}
		return checkCells(cells)
	}
	var req appendRequest
	if err := json.Unmarshal(trimmed, &req); err != nil {
		return nil, &InvalidBodyError{Reason: "the body must be a JSON object naming values"}
	}
	if len(req.NumericColumns) > 0 {
		return nil, &InvalidBodyError{
			Reason: numericColumnsKey + " is not supported on append, whose values are positional; send a numeric cell as a JSON number",
		}
	}
	return checkCells(req.Values)
}

func checkCells(cells []any) ([]any, error) {
	for i, cell := range cells {
		if !scalar(cell) {
			return nil, &InvalidBodyError{Reason: "cell " + strconv.Itoa(i+1) + " must be a string, number, boolean or null"}
		}
	}
	return cells, nil
}

// NOTE: every row goes through parsePatch, so a batch row reads exactly like a keyed create body — numeric_columns included.
func parseBatch(body []byte) ([]map[string]any, error) {
	var req batchRequest
	if err := json.Unmarshal(bytes.TrimSpace(body), &req); err != nil {
		return nil, &InvalidBodyError{Reason: "the body must be a JSON object naming rows"}
	}
	if len(req.Rows) == 0 {
		return nil, &InvalidBodyError{Reason: "the body must name at least one row"}
	}
	rows := make([]map[string]any, 0, len(req.Rows))
	for _, raw := range req.Rows {
		fields, err := parsePatch(raw)
		if err != nil {
			return nil, err
		}
		rows = append(rows, fields)
	}
	return rows, nil
}

func parsePatch(body []byte) (map[string]any, error) {
	var mixed map[string]json.RawMessage
	if err := json.Unmarshal(bytes.TrimSpace(body), &mixed); err != nil {
		return nil, &InvalidBodyError{Reason: "the body must be a JSON object of column to value"}
	}
	hints, err := numericHints(mixed)
	if err != nil {
		return nil, err
	}
	patch := make(map[string]any, len(mixed))
	for column, raw := range mixed {
		var value any
		if uErr := json.Unmarshal(raw, &value); uErr != nil {
			return nil, &InvalidBodyError{Reason: "column " + strconv.Quote(column) + " has a value this surface cannot read"}
		}
		if !scalar(value) {
			return nil, &InvalidBodyError{Reason: "column " + strconv.Quote(column) + " must be a string, number, boolean or null"}
		}
		patch[column] = value
	}
	for _, hint := range hints {
		column, ok := columnNamed(patch, hint)
		if !ok {
			return nil, &InvalidBodyError{Reason: numericColumnsKey + " names " + strconv.Quote(hint) + ", which the patch does not set"}
		}
		number, nErr := numeric(patch[column])
		if nErr != nil {
			return nil, &InvalidCellError{Column: column, Reason: nErr.Error()}
		}
		patch[column] = number
	}
	return patch, nil
}

func numericHints(mixed map[string]json.RawMessage) ([]string, error) {
	raw, ok := mixed[numericColumnsKey]
	if !ok {
		return nil, nil
	}
	delete(mixed, numericColumnsKey)
	var hints []string
	if err := json.Unmarshal(raw, &hints); err != nil {
		return nil, &InvalidBodyError{Reason: numericColumnsKey + " must be an array of column names"}
	}
	return hints, nil
}

func columnNamed(patch map[string]any, hint string) (string, bool) {
	want := strings.ToLower(strings.TrimSpace(hint))
	for column := range patch {
		if strings.ToLower(strings.TrimSpace(column)) == want {
			return column, true
		}
	}
	return "", false
}

// SECURITY: ParseFloat accepts Inf and NaN, which encoding/json then refuses to marshal — that would
// surface as a 500 from inside the Google client rather than the caller's 400.
func numeric(value any) (float64, error) {
	switch t := value.(type) {
	case float64:
		if math.IsInf(t, 0) || math.IsNaN(t) {
			return 0, errors.New("not a finite number")
		}
		return t, nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		if err != nil {
			return 0, errors.New("not a number")
		}
		if math.IsInf(f, 0) || math.IsNaN(f) {
			return 0, errors.New("not a finite number")
		}
		return f, nil
	}
	return 0, errors.New("not a number")
}

func parseCreateTab(body []byte) (string, error) {
	var req createTabRequest
	if err := json.Unmarshal(bytes.TrimSpace(body), &req); err != nil {
		return "", &InvalidBodyError{Reason: "the body must be a JSON object naming a title"}
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return "", &InvalidBodyError{Reason: "the body must name a non-blank title"}
	}
	return title, nil
}

func scalar(value any) bool {
	switch value.(type) {
	case nil, string, float64, bool:
		return true
	}
	return false
}
