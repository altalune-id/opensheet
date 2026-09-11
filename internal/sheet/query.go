package sheet

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// RowOp is one text comparison a ?where= clause can name.
type RowOp string

// The text operators a filtered read understands.
const (
	RowOpEq       RowOp = "eq"
	RowOpNe       RowOp = "ne"
	RowOpGt       RowOp = "gt"
	RowOpGte      RowOp = "gte"
	RowOpLt       RowOp = "lt"
	RowOpLte      RowOp = "lte"
	RowOpContains RowOp = "contains"
	RowOpStarts   RowOp = "starts"
	RowOpIn       RowOp = "in"
	// NOTE: RowOpEmpty means `cell IS NULL OR cell = ''` and RowOpPresent is its negation; the NULL arm keeps ne the complement of eq for a row missing the key.
	RowOpEmpty   RowOp = "empty"
	RowOpPresent RowOp = "present"
)

// RowHint is the comparison domain a clause asks for.
type RowHint string

// The comparison domains a hinted clause can name. NOTE: a hint rides the operator (qty:num.gt:10) because a fourth colon-separated field would break the value's right to contain colons.
const (
	RowHintNone RowHint = ""
	RowHintNum  RowHint = "num"
	RowHintDate RowHint = "date"
)

// RowLikeEscape is the escape character a Pattern carries; SQLite has no default one, so a driver must bind it into an explicit ESCAPE clause.
const RowLikeEscape = `\`

// DefaultMaxQueryRows bounds a filtered read when sheets.maxQueryRows is unset.
const DefaultMaxQueryRows = 1000

// DefaultMaxSortPages bounds a sorted walk when sheets.maxSortPages is unset.
const DefaultMaxSortPages = 20

const (
	rowSortAsc  = "asc"
	rowSortDesc = "desc"
)

const rowSortForm = "column:asc or column:desc, optionally with a num. or date. hint on the direction"

const (
	rowCursorVersion       = 1
	rowSortedCursorVersion = 2
)

const (
	reasonUnsortedCursorOnSortedRead = "it came from an unsorted read and this read is sorted"
	reasonSortedCursorOnUnsortedRead = "it came from a sorted read and this read is unsorted"
)

// RowClause is one validated predicate over a column the projection stores.
type RowClause struct {
	Column  string
	Op      RowOp
	Hint    RowHint
	Value   string
	Values  []string
	Pattern string
}

// RowSort is the validated ordering a filtered read asks for.
type RowSort struct {
	Column string
	Hint   RowHint
	Desc   bool
}

// RowCursor is the keyset position an opaque ?cursor= carries. NOTE: Version is filled by DecodeRowCursor; each encoder writes its own.
type RowCursor struct {
	Digest    string
	RowIndex  int
	NullRank  int
	SortValue *string
	Page      int
	Version   int
}

// RowWindow is the validated page a filtered read returns.
type RowWindow struct {
	Limit  int
	Cursor *RowCursor
}

// RowQuery is a validated row filter and the window it pages with. NOTE: the window is parsed before the freshness gate and the clauses after it, because the column list a clause validates against does not exist until the projection has been written.
type RowQuery struct {
	Clauses []RowClause
	Sort    *RowSort
	Window  RowWindow
}

type unsortedRowCursorEnvelope struct {
	Version  int    `json:"v"`
	Digest   string `json:"d"`
	RowIndex *int   `json:"r"`
}

type rowCursorEnvelope struct {
	Version   int     `json:"v"`
	Digest    string  `json:"d"`
	RowIndex  *int    `json:"r"`
	NullRank  *int    `json:"n"`
	SortValue *string `json:"s"`
	Page      *int    `json:"p"`
}

// ParseRowWindow validates ?limit= and ?cursor= without reading the projection.
func ParseRowWindow(limit, cursor string, maxRows int) (RowWindow, error) {
	capRows := maxRows
	if capRows <= 0 {
		capRows = DefaultMaxQueryRows
	}
	window := RowWindow{Limit: capRows}
	if asked := strings.TrimSpace(limit); asked != "" {
		n, err := strconv.Atoi(asked)
		if err != nil || n <= 0 || n > capRows {
			return RowWindow{}, &InvalidLimitError{Limit: limit, MaxRows: capRows}
		}
		window.Limit = n
	}
	if strings.TrimSpace(cursor) == "" {
		return window, nil
	}
	decoded, err := DecodeRowCursor(cursor)
	if err != nil {
		return RowWindow{}, err
	}
	window.Cursor = &decoded
	return window, nil
}

// ParseRowClauses validates repeated ?where= clauses against the columns the tab's projection stores.
func ParseRowClauses(where, columns []string, tab string) ([]RowClause, error) {
	if len(where) == 0 {
		return nil, nil
	}
	del, err := deletedAtColumnOf(columns, tab)
	if err != nil {
		return nil, err
	}
	out := make([]RowClause, 0, len(where))
	for _, raw := range where {
		clause, cErr := parseRowClause(raw, columns, tab, del)
		if cErr != nil {
			return nil, cErr
		}
		out = append(out, clause)
	}
	return out, nil
}

func parseRowClause(raw string, columns []string, tab string, del int) (RowClause, error) {
	parts := strings.SplitN(raw, ":", 3)
	if len(parts) < 2 {
		return RowClause{}, &InvalidClauseError{Clause: raw, Reason: "expected column:operator[:value]"}
	}
	if strings.TrimSpace(parts[0]) == "" {
		return RowClause{}, &InvalidClauseError{Clause: raw, Reason: "no column"}
	}
	if parts[1] == "" {
		return RowClause{}, &InvalidClauseError{Clause: raw, Reason: "no operator"}
	}
	hint, token := splitRowHint(parts[1])
	op := RowOp(token)
	takesValue, known := rowOpTakesValue(op)
	if !known {
		return RowClause{}, &InvalidClauseError{
			Clause: raw, Reason: fmt.Sprintf("unknown operator %q", token),
		}
	}
	if hint != RowHintNone && !rowOpTakesHint(op) {
		return RowClause{}, &HintNotApplicableError{Column: parts[0], Op: op, Hint: hint}
	}
	if !takesValue && len(parts) == 3 {
		return RowClause{}, &InvalidClauseError{
			Clause: raw, Reason: fmt.Sprintf("operator %q takes no value", op),
		}
	}
	value := ""
	if len(parts) == 3 {
		value = parts[2]
	}
	if takesValue && value == "" {
		return RowClause{}, &InvalidClauseError{
			Clause: raw, Reason: fmt.Sprintf("operator %q needs a value", op),
		}
	}
	if !hintAcceptsOperand(hint, value) {
		return RowClause{}, &HintOperandError{Column: parts[0], Hint: hint, Value: value}
	}
	col, err := columnOf(columns, parts[0], tab)
	if err != nil {
		return RowClause{}, err
	}
	if col == del {
		return RowClause{}, &UnqueryableColumnError{Column: parts[0], Tab: tab}
	}
	clause := RowClause{Column: columns[col], Op: op, Hint: hint, Value: value}
	switch op {
	case RowOpIn:
		clause.Values = splitInList(value)
		if len(clause.Values) == 0 {
			return RowClause{}, &InvalidClauseError{
				Clause: raw, Reason: "operator \"in\" needs at least one value",
			}
		}
	case RowOpContains:
		clause.Pattern = "%" + escapeLikeValue(value) + "%"
	case RowOpStarts:
		clause.Pattern = escapeLikeValue(value) + "%"
	default:
	}
	return clause, nil
}

// ParseRowSort validates repeated ?sort= values; a nil result means an unsorted read. NOTE: the column is not resolved here, because the tab's column list does not exist until the projection has been written.
func ParseRowSort(raw []string) (*RowSort, error) {
	if len(raw) > 1 {
		return nil, &InvalidSortError{
			Sort:   strings.Join(raw, ","),
			Reason: fmt.Sprintf("?sort= was given %d times; a read has one sort", len(raw)),
		}
	}
	if len(raw) == 0 || strings.TrimSpace(raw[0]) == "" {
		return nil, nil
	}
	spec := raw[0]
	column, token, found := strings.Cut(spec, ":")
	if !found {
		return nil, &InvalidSortError{Sort: spec, Reason: "no direction; write " + rowSortForm}
	}
	if strings.TrimSpace(column) == "" {
		return nil, &InvalidSortError{Sort: spec, Reason: "no column; write " + rowSortForm}
	}
	hint, dir := splitRowHint(token)
	switch dir {
	case rowSortAsc:
		return &RowSort{Column: column, Hint: hint}, nil
	case rowSortDesc:
		return &RowSort{Column: column, Hint: hint, Desc: true}, nil
	}
	return nil, &InvalidSortError{
		Sort:   spec,
		Reason: fmt.Sprintf("unknown direction %q; write %s or %s", dir, rowSortAsc, rowSortDesc),
	}
}

func rowHintPrefix(hint RowHint) string {
	if hint == RowHintNone {
		return ""
	}
	return string(hint) + "."
}

// NOTE: RowSort carries only a bool, so the direction is spelled out rather than formatted, or asc and desc would share a tag under %v's zero value.
func rowSortDirection(sort RowSort) string {
	if sort.Desc {
		return rowSortDesc
	}
	return rowSortAsc
}

func maxSortPagesOf(configured int) int {
	if configured <= 0 {
		return DefaultMaxSortPages
	}
	return configured
}

// NOTE: the operator token is split on the first "." and the prefix is checked against the known hints first, so col:foo.bar stays 3a's unknown-operator refusal rather than becoming a hint error.
func splitRowHint(token string) (hint RowHint, op string) {
	prefix, rest, found := strings.Cut(token, ".")
	if !found {
		return RowHintNone, token
	}
	switch RowHint(prefix) {
	case RowHintNum, RowHintDate:
		return RowHint(prefix), rest
	default:
		return RowHintNone, token
	}
}

func rowOpTakesHint(op RowOp) bool {
	switch op {
	case RowOpEq, RowOpNe, RowOpGt, RowOpGte, RowOpLt, RowOpLte:
		return true
	default:
		return false
	}
}

// NOTE: the float ParseNum yields is deliberately not kept on the clause; a driver calls ParseNum again at bind time, so no second representation can drift from Value.
func hintAcceptsOperand(hint RowHint, value string) bool {
	switch hint {
	case RowHintNum:
		_, ok := ParseNum(value)
		return ok
	case RowHintDate:
		return MatchesDateShape(value)
	default:
		return true
	}
}

func rowOpTakesValue(op RowOp) (takesValue, known bool) {
	switch op {
	case RowOpEmpty, RowOpPresent:
		return false, true
	case RowOpEq, RowOpNe, RowOpGt, RowOpGte, RowOpLt, RowOpLte, RowOpContains, RowOpStarts, RowOpIn:
		return true, true
	default:
		return false, false
	}
}

// NOTE: a blank item is dropped rather than bound, so a trailing comma is harmless and IN () can never reach a driver — it is a syntax error on Postgres and matches nothing on SQLite.
func splitInList(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func escapeLikeValue(value string) string {
	escaped := strings.ReplaceAll(value, RowLikeEscape, RowLikeEscape+RowLikeEscape)
	escaped = strings.ReplaceAll(escaped, "%", RowLikeEscape+"%")
	return strings.ReplaceAll(escaped, "_", RowLikeEscape+"_")
}

// EncodeRowCursor renders the opaque keyset position an unsorted next-page link carries.
func EncodeRowCursor(c RowCursor) (string, error) {
	if c.Digest == "" || c.RowIndex < 0 {
		return "", fmt.Errorf("sheet.cursor: encode digest=%q row_index=%d", c.Digest, c.RowIndex)
	}
	rowIndex := c.RowIndex
	raw, err := json.Marshal(unsortedRowCursorEnvelope{
		Version: rowCursorVersion, Digest: c.Digest, RowIndex: &rowIndex,
	})
	if err != nil {
		return "", fmt.Errorf("sheet.cursor: encode: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// EncodeSortedRowCursor renders the opaque keyset position a sorted next-page link carries. NOTE: SortValue rides as JSON null when the cell is null, never as "", or a text or date walk drops the rows after the first null.
func EncodeSortedRowCursor(c RowCursor) (string, error) {
	if c.Digest == "" || c.RowIndex < 0 || c.Page < 0 || (c.NullRank != 0 && c.NullRank != 1) {
		return "", fmt.Errorf("sheet.cursor: encode sorted digest=%q row_index=%d null_rank=%d page=%d",
			c.Digest, c.RowIndex, c.NullRank, c.Page)
	}
	rowIndex, nullRank, page := c.RowIndex, c.NullRank, c.Page
	raw, err := json.Marshal(rowCursorEnvelope{
		Version:  rowSortedCursorVersion,
		Digest:   c.Digest,
		RowIndex: &rowIndex,
		NullRank: &nullRank,
		// NOTE: no omitempty — a nil SortValue must reach the wire as null and decode back to nil.
		SortValue: c.SortValue,
		Page:      &page,
	})
	if err != nil {
		return "", fmt.Errorf("sheet.cursor: encode sorted: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// CheckRowCursorVersion refuses a cursor whose version does not pair with the sortedness of the read presenting it.
func CheckRowCursorVersion(c *RowCursor, sorted bool) error {
	if c == nil {
		return nil
	}
	if sorted && c.Version != rowSortedCursorVersion {
		return &InvalidCursorError{Reason: reasonUnsortedCursorOnSortedRead}
	}
	if !sorted && c.Version != rowCursorVersion {
		return &InvalidCursorError{Reason: reasonSortedCursorOnUnsortedRead}
	}
	return nil
}

// DecodeRowCursor reads an opaque cursor of either version and reports which one it read. NOTE: whether the digest still matches is the route's decision, not the codec's.
func DecodeRowCursor(raw string) (RowCursor, error) {
	body, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return RowCursor{}, &InvalidCursorError{Reason: "not base64"}
	}
	var env rowCursorEnvelope
	if jErr := json.Unmarshal(body, &env); jErr != nil {
		return RowCursor{}, &InvalidCursorError{Reason: "not a cursor"}
	}
	if env.Version != rowCursorVersion && env.Version != rowSortedCursorVersion {
		return RowCursor{}, &InvalidCursorError{
			Reason: fmt.Sprintf("unsupported version %d", env.Version),
		}
	}
	if env.Digest == "" {
		return RowCursor{}, &InvalidCursorError{Reason: "no content digest"}
	}
	if env.RowIndex == nil || *env.RowIndex < 0 {
		return RowCursor{}, &InvalidCursorError{Reason: "no row index"}
	}
	out := RowCursor{Digest: env.Digest, RowIndex: *env.RowIndex, Version: env.Version}
	if env.Version == rowCursorVersion {
		return out, nil
	}
	if env.NullRank == nil || (*env.NullRank != 0 && *env.NullRank != 1) {
		return RowCursor{}, &InvalidCursorError{Reason: "no null rank"}
	}
	if env.Page == nil || *env.Page < 0 {
		return RowCursor{}, &InvalidCursorError{Reason: "no page count"}
	}
	out.NullRank, out.SortValue, out.Page = *env.NullRank, env.SortValue, *env.Page
	return out, nil
}
