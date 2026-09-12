package sheet

import (
	"encoding/base64"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func testColumns() []string {
	return []string{"id", "note", "Status", "created_at"}
}

func TestParseRowClauses_EveryOperator(t *testing.T) {
	tests := []struct {
		name        string
		clause      string
		wantColumn  string
		wantOp      RowOp
		wantValue   string
		wantValues  []string
		wantPattern string
	}{
		{name: "eq", clause: "note:eq:open", wantColumn: "note", wantOp: RowOpEq, wantValue: "open"},
		{name: "ne", clause: "note:ne:open", wantColumn: "note", wantOp: RowOpNe, wantValue: "open"},
		{name: "gt", clause: "note:gt:a", wantColumn: "note", wantOp: RowOpGt, wantValue: "a"},
		{name: "gte", clause: "note:gte:a", wantColumn: "note", wantOp: RowOpGte, wantValue: "a"},
		{name: "lt", clause: "note:lt:z", wantColumn: "note", wantOp: RowOpLt, wantValue: "z"},
		{name: "lte", clause: "note:lte:z", wantColumn: "note", wantOp: RowOpLte, wantValue: "z"},
		{
			name: "contains", clause: "note:contains:ada", wantColumn: "note",
			wantOp: RowOpContains, wantValue: "ada", wantPattern: "%ada%",
		},
		{
			name: "starts", clause: "note:starts:ad", wantColumn: "note",
			wantOp: RowOpStarts, wantValue: "ad", wantPattern: "ad%",
		},
		{
			name: "in", clause: "note:in:a,b,c", wantColumn: "note",
			wantOp: RowOpIn, wantValue: "a,b,c", wantValues: []string{"a", "b", "c"},
		},
		{name: "empty", clause: "note:empty", wantColumn: "note", wantOp: RowOpEmpty},
		{name: "present", clause: "note:present", wantColumn: "note", wantOp: RowOpPresent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseRowClauses([]string{tt.clause}, testColumns(), "Sheet1")
			if err != nil {
				t.Fatalf("ParseRowClauses(%q) error = %v, want nil", tt.clause, err)
			}
			if len(got) != 1 {
				t.Fatalf("ParseRowClauses(%q) = %d clauses, want 1", tt.clause, len(got))
			}
			c := got[0]
			if c.Column != tt.wantColumn || c.Op != tt.wantOp || c.Value != tt.wantValue {
				t.Errorf("clause = {%q %q %q}, want {%q %q %q}",
					c.Column, c.Op, c.Value, tt.wantColumn, tt.wantOp, tt.wantValue)
			}
			if !slices.Equal(c.Values, tt.wantValues) {
				t.Errorf("Values = %q, want %q", c.Values, tt.wantValues)
			}
			if c.Pattern != tt.wantPattern {
				t.Errorf("Pattern = %q, want %q", c.Pattern, tt.wantPattern)
			}
		})
	}
}

func TestParseRowClauses_ANDsEveryClause(t *testing.T) {
	got, err := ParseRowClauses(
		[]string{"Status:eq:open", "note:contains:ada"}, testColumns(), "Sheet1")
	if err != nil {
		t.Fatalf("ParseRowClauses() error = %v, want nil", err)
	}
	if len(got) != 2 {
		t.Fatalf("ParseRowClauses() = %d clauses, want 2", len(got))
	}
	if got[0].Op != RowOpEq || got[1].Op != RowOpContains {
		t.Errorf("clause order = %q,%q, want eq,contains", got[0].Op, got[1].Op)
	}
}

func TestParseRowClauses_NoClausesIsNotAnError(t *testing.T) {
	got, err := ParseRowClauses(nil, testColumns(), "Sheet1")
	if err != nil || got != nil {
		t.Fatalf("ParseRowClauses(nil) = %v, %v, want nil, nil", got, err)
	}
}

// The projection keys its rows with the tab's own header spelling, so the clause
// must carry that spelling and not the caller's, or the bound field name misses.
func TestParseRowClauses_CarriesTheKnownColumnSpelling(t *testing.T) {
	got, err := ParseRowClauses([]string{"status:eq:open"}, testColumns(), "Sheet1")
	if err != nil {
		t.Fatalf("ParseRowClauses() error = %v, want nil", err)
	}
	if got[0].Column != "Status" {
		t.Errorf("Column = %q, want %q", got[0].Column, "Status")
	}
}

func TestParseRowClauses_SplitsOnTheFirstTwoColonsOnly(t *testing.T) {
	const want = "2026-09-11T00:00:00Z"
	got, err := ParseRowClauses([]string{"created_at:gte:" + want}, testColumns(), "Sheet1")
	if err != nil {
		t.Fatalf("ParseRowClauses() error = %v, want nil", err)
	}
	if got[0].Column != "created_at" || got[0].Op != RowOpGte || got[0].Value != want {
		t.Errorf("clause = {%q %q %q}, want {created_at gte %q}",
			got[0].Column, got[0].Op, got[0].Value, want)
	}
}

func TestParseRowClauses_RefusesMalformedClauses(t *testing.T) {
	tests := []struct {
		clause     string
		wantReason string
	}{
		{clause: "", wantReason: "expected column:operator"},
		{clause: "note", wantReason: "expected column:operator"},
		{clause: "note:", wantReason: "no operator"},
		{clause: "note::open", wantReason: "no operator"},
		{clause: ":eq:open", wantReason: "no column"},
		{clause: "   :eq:open", wantReason: "no column"},
		{clause: "::", wantReason: "no column"},
		{clause: "note:nope:open", wantReason: `unknown operator "nope"`},
		{clause: "note:EQ:open", wantReason: `unknown operator "EQ"`},
		{clause: "note:eq ", wantReason: `unknown operator "eq "`},
	}
	for _, tt := range tests {
		t.Run(tt.clause, func(t *testing.T) {
			_, err := ParseRowClauses([]string{tt.clause}, testColumns(), "Sheet1")
			if !IsInvalidClauseError(err) {
				t.Fatalf("ParseRowClauses(%q) error = %v, want InvalidClauseError", tt.clause, err)
			}
			clauseErr, _ := errors.AsType[*InvalidClauseError](err)
			if !strings.Contains(clauseErr.Reason, tt.wantReason) {
				t.Errorf("Reason = %q, want it to carry %q", clauseErr.Reason, tt.wantReason)
			}
		})
	}
}

func TestParseRowClauses_RefusesAMissingValue(t *testing.T) {
	for _, clause := range []string{
		"note:eq", "note:eq:", "note:ne:", "note:gt:", "note:gte:", "note:lt:", "note:lte:",
		"note:contains:", "note:starts:", "note:in:", "note:in",
	} {
		t.Run(clause, func(t *testing.T) {
			_, err := ParseRowClauses([]string{clause}, testColumns(), "Sheet1")
			if !IsInvalidClauseError(err) {
				t.Fatalf("ParseRowClauses(%q) error = %v, want InvalidClauseError", clause, err)
			}
		})
	}
}

func TestParseRowClauses_RefusesAValueOnEmptyAndPresent(t *testing.T) {
	for _, clause := range []string{"note:empty:x", "note:present:x", "note:empty:", "note:present:"} {
		t.Run(clause, func(t *testing.T) {
			_, err := ParseRowClauses([]string{clause}, testColumns(), "Sheet1")
			if !IsInvalidClauseError(err) {
				t.Fatalf("ParseRowClauses(%q) error = %v, want InvalidClauseError", clause, err)
			}
		})
	}
}

func TestParseRowClauses_RefusesAnUnknownColumn(t *testing.T) {
	_, err := ParseRowClauses([]string{"statuz:eq:open"}, testColumns(), "Sheet1")
	if !IsUnknownColumnError(err) {
		t.Fatalf("ParseRowClauses() error = %v, want UnknownColumnError", err)
	}
}

// capabilities advertises deleted_at whenever soft delete is on, but projectRows
// strips it from data, so a caller that believes capabilities must be refused.
func TestParseRowClauses_RefusesDeletedAtInEitherSpelling(t *testing.T) {
	tests := []struct {
		name    string
		columns []string
		clause  string
	}{
		{name: "snake case header", columns: []string{"id", "deleted_at"}, clause: "deleted_at:present"},
		{name: "spaced header", columns: []string{"id", "Deleted At"}, clause: "Deleted At:present"},
		{name: "mixed case request", columns: []string{"id", "Deleted_At"}, clause: "deleted_at:eq:x"},
		{name: "hyphenated header", columns: []string{"id", "deleted-at"}, clause: "deleted-at:empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseRowClauses([]string{tt.clause}, tt.columns, "Sheet1")
			if !IsUnqueryableColumnError(err) {
				t.Fatalf("ParseRowClauses(%q) error = %v, want UnqueryableColumnError", tt.clause, err)
			}
		})
	}
}

func TestParseRowClauses_RefusesADuplicateDeletedAtColumn(t *testing.T) {
	_, err := ParseRowClauses([]string{"id:eq:a"}, []string{"id", "deleted_at", "Deleted At"}, "Sheet1")
	if !IsDuplicateColumnError(err) {
		t.Fatalf("ParseRowClauses() error = %v, want DuplicateColumnError", err)
	}
}

// IN () is a syntax error on Postgres and matches nothing on SQLite, so an empty
// list can never reach a driver.
func TestParseRowClauses_InList(t *testing.T) {
	tests := []struct {
		name   string
		clause string
		want   []string
	}{
		{name: "one value", clause: "note:in:a", want: []string{"a"}},
		{name: "three values", clause: "note:in:a,b,c", want: []string{"a", "b", "c"}},
		{name: "trailing comma", clause: "note:in:a,", want: []string{"a"}},
		{name: "interior blank", clause: "note:in:a,,b", want: []string{"a", "b"}},
		{name: "spaces are values", clause: "note:in:a, b", want: []string{"a", " b"}},
		{name: "colons survive", clause: "note:in:a:b,c", want: []string{"a:b", "c"}},
		{name: "empty list", clause: "note:in:,"},
		{name: "all blank", clause: "note:in:,,,"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseRowClauses([]string{tt.clause}, testColumns(), "Sheet1")
			if tt.want == nil {
				if !IsInvalidClauseError(err) {
					t.Fatalf("ParseRowClauses(%q) error = %v, want InvalidClauseError", tt.clause, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseRowClauses(%q) error = %v, want nil", tt.clause, err)
			}
			if !slices.Equal(got[0].Values, tt.want) {
				t.Errorf("Values = %q, want %q", got[0].Values, tt.want)
			}
		})
	}
}

// A value must reach LIKE as literal text: an unescaped % is a wildcard and an
// unescaped _ matches any single character.
func TestParseRowClauses_ContainsTreatsMetacharactersLiterally(t *testing.T) {
	esc := RowLikeEscape
	tests := []struct {
		name   string
		clause string
		want   string
	}{
		{name: "percent", clause: "note:contains:100%", want: "%100" + esc + "%%"},
		{name: "underscore", clause: "note:contains:_", want: "%" + esc + "_%"},
		{name: "escape character", clause: "note:contains:" + esc, want: "%" + esc + esc + "%"},
		{
			name: "escaped percent", clause: "note:contains:" + esc + "%",
			want: "%" + esc + esc + esc + "%%",
		},
		{name: "starts percent", clause: "note:starts:100%", want: "100" + esc + "%%"},
		{name: "starts underscore", clause: "note:starts:_x", want: esc + "_x%"},
		{name: "plain", clause: "note:contains:ada", want: "%ada%"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseRowClauses([]string{tt.clause}, testColumns(), "Sheet1")
			if err != nil {
				t.Fatalf("ParseRowClauses(%q) error = %v, want nil", tt.clause, err)
			}
			if got[0].Pattern != tt.want {
				t.Errorf("Pattern = %q, want %q", got[0].Pattern, tt.want)
			}
		})
	}
}

func TestParseRowClauses_LeavesOtherOperatorValuesVerbatim(t *testing.T) {
	got, err := ParseRowClauses([]string{"note:eq:100%_" + RowLikeEscape}, testColumns(), "Sheet1")
	if err != nil {
		t.Fatalf("ParseRowClauses() error = %v, want nil", err)
	}
	if got[0].Value != "100%_"+RowLikeEscape {
		t.Errorf("Value = %q, want it verbatim", got[0].Value)
	}
	if got[0].Pattern != "" {
		t.Errorf("Pattern = %q, want empty for eq", got[0].Pattern)
	}
}

func TestParseRowWindow_Limit(t *testing.T) {
	tests := []struct {
		name  string
		limit string
		want  int
	}{
		{name: "absent defaults to the cap", limit: "", want: 1000},
		{name: "blank defaults to the cap", limit: "   ", want: 1000},
		{name: "one", limit: "1", want: 1},
		{name: "below the cap", limit: "999", want: 999},
		{name: "at the cap", limit: "1000", want: 1000},
		{name: "padded", limit: " 10 ", want: 10},
		{name: "over the cap", limit: "1001"},
		{name: "far over the cap", limit: "1000000"},
		{name: "zero", limit: "0"},
		{name: "negative", limit: "-1"},
		{name: "not a number", limit: "abc"},
		{name: "fractional", limit: "3.5"},
		{name: "hex", limit: "0x10"},
		{name: "overflows int64", limit: "99999999999999999999999"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseRowWindow(tt.limit, "", 1000)
			if tt.want == 0 {
				if !IsInvalidLimitError(err) {
					t.Fatalf("ParseRowWindow(%q) error = %v, want InvalidLimitError", tt.limit, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseRowWindow(%q) error = %v, want nil", tt.limit, err)
			}
			if got.Limit != tt.want {
				t.Errorf("Limit = %d, want %d", got.Limit, tt.want)
			}
			if got.Cursor != nil {
				t.Errorf("Cursor = %+v, want nil", got.Cursor)
			}
		})
	}
}

func TestParseRowWindow_LimitRefusalNamesTheCap(t *testing.T) {
	_, err := ParseRowWindow("5000", "", 1000)
	if !IsInvalidLimitError(err) {
		t.Fatalf("ParseRowWindow() error = %v, want InvalidLimitError", err)
	}
	if !strings.Contains(err.Error(), "1000") {
		t.Errorf("Error() = %q, want it to name the 1000 row cap", err.Error())
	}
	limitErr, ok := errors.AsType[*InvalidLimitError](err)
	if !ok {
		t.Fatal("error does not carry *InvalidLimitError")
	}
	if limitErr.MaxRows != 1000 {
		t.Errorf("MaxRows = %d, want 1000", limitErr.MaxRows)
	}
	if msg := limitErr.ToAppError().Message(); !strings.Contains(msg, "1000") {
		t.Errorf("AppError message = %q, want it to name the cap", msg)
	}
}

// A filtered read can never be unbounded, so an unset cap falls back rather than
// meaning "no limit" the way sheets.maxPayloadBytes does.
func TestParseRowWindow_UnsetCapFallsBackToTheDefault(t *testing.T) {
	for _, maxRows := range []int{0, -1} {
		got, err := ParseRowWindow("", "", maxRows)
		if err != nil {
			t.Fatalf("ParseRowWindow(maxRows=%d) error = %v, want nil", maxRows, err)
		}
		if got.Limit != DefaultMaxQueryRows {
			t.Errorf("Limit = %d, want %d", got.Limit, DefaultMaxQueryRows)
		}
		if _, err := ParseRowWindow("1001", "", maxRows); !IsInvalidLimitError(err) {
			t.Errorf("ParseRowWindow(1001, maxRows=%d) error = %v, want InvalidLimitError", maxRows, err)
		}
	}
}

func TestParseRowWindow_CarriesTheDecodedCursor(t *testing.T) {
	raw, err := EncodeRowCursor(RowCursor{Digest: "abc", RowIndex: 142})
	if err != nil {
		t.Fatalf("EncodeRowCursor() error = %v, want nil", err)
	}
	got, err := ParseRowWindow("10", raw, 1000)
	if err != nil {
		t.Fatalf("ParseRowWindow() error = %v, want nil", err)
	}
	if got.Cursor == nil {
		t.Fatal("Cursor = nil, want the decoded cursor")
	}
	if got.Cursor.Digest != "abc" || got.Cursor.RowIndex != 142 {
		t.Errorf("Cursor = %+v, want {abc 142}", *got.Cursor)
	}
}

func TestParseRowWindow_RefusesAMalformedCursor(t *testing.T) {
	if _, err := ParseRowWindow("", "!!!not base64!!!", 1000); !IsInvalidCursorError(err) {
		t.Fatalf("ParseRowWindow() error = %v, want InvalidCursorError", err)
	}
}

func TestRowCursor_RoundTrips(t *testing.T) {
	for _, want := range []RowCursor{
		{Digest: "abc", RowIndex: 0, Version: 1},
		{Digest: "abc", RowIndex: 142, Version: 1},
		{Digest: strings.Repeat("f", 64), RowIndex: 999999, Version: 1},
	} {
		raw, err := EncodeRowCursor(want)
		if err != nil {
			t.Fatalf("EncodeRowCursor(%+v) error = %v, want nil", want, err)
		}
		if strings.ContainsAny(raw, "+/=") {
			t.Errorf("EncodeRowCursor() = %q, want a raw URL-safe encoding", raw)
		}
		got, err := DecodeRowCursor(raw)
		if err != nil {
			t.Fatalf("DecodeRowCursor(%q) error = %v, want nil", raw, err)
		}
		if got != want {
			t.Errorf("DecodeRowCursor() = %+v, want %+v", got, want)
		}
	}
}

// row_index 0 is the first data row, so a missing "r" must not decode as it.
func TestDecodeRowCursor_Malformed(t *testing.T) {
	tests := []struct {
		name string
		body string
		raw  string
	}{
		{name: "empty", raw: ""},
		{name: "not base64", raw: "!!!"},
		{name: "padded base64", raw: base64.StdEncoding.EncodeToString([]byte(`{"v":1,"d":"ab","r":1}`))},
		{name: "not json", body: "not json"},
		{name: "json array", body: `[1,2,3]`},
		{name: "json null", body: `null`},
		{name: "version zero", body: `{"v":0,"d":"a","r":1}`},
		{name: "version two", body: `{"v":2,"d":"a","r":1}`},
		{name: "version missing", body: `{"d":"a","r":1}`},
		{name: "digest missing", body: `{"v":1,"r":1}`},
		{name: "digest empty", body: `{"v":1,"d":"","r":1}`},
		{name: "row index missing", body: `{"v":1,"d":"a"}`},
		{name: "row index negative", body: `{"v":1,"d":"a","r":-1}`},
		{name: "row index not a number", body: `{"v":1,"d":"a","r":"1"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := tt.raw
			if tt.body != "" {
				raw = base64.RawURLEncoding.EncodeToString([]byte(tt.body))
			}
			got, err := DecodeRowCursor(raw)
			if !IsInvalidCursorError(err) {
				t.Fatalf("DecodeRowCursor(%q) = %+v, %v, want InvalidCursorError", raw, got, err)
			}
		})
	}
}

// The digest match is the route's 409, not the codec's business, so decoding
// must not care which digest the cursor names.
func TestDecodeRowCursor_DoesNotJudgeTheDigest(t *testing.T) {
	raw, err := EncodeRowCursor(RowCursor{Digest: "stale", RowIndex: 7})
	if err != nil {
		t.Fatalf("EncodeRowCursor() error = %v, want nil", err)
	}
	got, err := DecodeRowCursor(raw)
	if err != nil {
		t.Fatalf("DecodeRowCursor() error = %v, want nil", err)
	}
	if got.Digest != "stale" {
		t.Errorf("Digest = %q, want %q", got.Digest, "stale")
	}
}

func TestEncodeRowCursor_RefusesAnUnusableCursor(t *testing.T) {
	for _, c := range []RowCursor{
		{Digest: "", RowIndex: 1},
		{Digest: "abc", RowIndex: -1},
	} {
		raw, err := EncodeRowCursor(c)
		if err == nil {
			t.Fatalf("EncodeRowCursor(%+v) = %q, want an error", c, raw)
		}
		if IsInvalidCursorError(err) {
			t.Errorf("EncodeRowCursor(%+v) error = %v, want an internal failure, not a client refusal", c, err)
		}
	}
}

func TestParseRowClauses_EveryHintOnEveryHintableOperator(t *testing.T) {
	operands := map[RowHint]string{RowHintNum: "10", RowHintDate: "2026-09-11"}
	for _, hint := range []RowHint{RowHintNum, RowHintDate} {
		for _, op := range []RowOp{RowOpEq, RowOpNe, RowOpGt, RowOpGte, RowOpLt, RowOpLte} {
			clause := "id:" + string(hint) + "." + string(op) + ":" + operands[hint]
			t.Run(clause, func(t *testing.T) {
				got, err := ParseRowClauses([]string{clause}, testColumns(), "Sheet1")
				if err != nil {
					t.Fatalf("ParseRowClauses(%q) error = %v, want nil", clause, err)
				}
				if len(got) != 1 {
					t.Fatalf("ParseRowClauses(%q) = %d clauses, want 1", clause, len(got))
				}
				c := got[0]
				if c.Column != "id" || c.Op != op || c.Hint != hint || c.Value != operands[hint] {
					t.Errorf("clause = {%q %q %q %q}, want {id %q %q %q}",
						c.Column, c.Op, c.Hint, c.Value, op, hint, operands[hint])
				}
			})
		}
	}
}

func TestParseRowClauses_RefusesAHintOnANonHintableOperator(t *testing.T) {
	for _, hint := range []RowHint{RowHintNum, RowHintDate} {
		for _, tail := range []string{
			"contains:x", "starts:x", "in:a,b", "empty", "present", "empty:x", "present:x",
		} {
			clause := "note:" + string(hint) + "." + tail
			t.Run(clause, func(t *testing.T) {
				_, err := ParseRowClauses([]string{clause}, testColumns(), "Sheet1")
				if !IsHintNotApplicableError(err) {
					t.Fatalf("ParseRowClauses(%q) error = %v, want HintNotApplicableError", clause, err)
				}
				hintErr, _ := errors.AsType[*HintNotApplicableError](err)
				if hintErr.Hint != hint || hintErr.Column != "note" {
					t.Errorf("error = {%q %q %q}, want column note and hint %q",
						hintErr.Column, hintErr.Op, hintErr.Hint, hint)
				}
			})
		}
	}
}

// The operator token splits on the first "." and the prefix is checked against
// the known hints FIRST: a known hint on a text-only operator is SHT037, while
// an unknown prefix stays 3a's SHT031 and never becomes a hint error.
func TestParseRowClauses_HintCheckPrecedesTheOperatorLookup(t *testing.T) {
	t.Run("known hint, non-hintable operator", func(t *testing.T) {
		_, err := ParseRowClauses([]string{"note:num.contains:x"}, testColumns(), "Sheet1")
		if !IsHintNotApplicableError(err) {
			t.Fatalf("error = %v, want HintNotApplicableError", err)
		}
		if IsInvalidClauseError(err) {
			t.Error("num.contains fell through to the unknown-operator refusal")
		}
	})
	t.Run("unknown prefix", func(t *testing.T) {
		_, err := ParseRowClauses([]string{"note:foo.bar:x"}, testColumns(), "Sheet1")
		if !IsInvalidClauseError(err) {
			t.Fatalf("error = %v, want InvalidClauseError", err)
		}
		if IsHintNotApplicableError(err) {
			t.Error("the hint check fired on an unknown prefix")
		}
		clauseErr, _ := errors.AsType[*InvalidClauseError](err)
		if want := `unknown operator "foo.bar"`; clauseErr.Reason != want {
			t.Errorf("Reason = %q, want %q", clauseErr.Reason, want)
		}
	})
	t.Run("known hint, unknown operator", func(t *testing.T) {
		_, err := ParseRowClauses([]string{"note:num.nope:x"}, testColumns(), "Sheet1")
		if !IsInvalidClauseError(err) {
			t.Fatalf("error = %v, want InvalidClauseError", err)
		}
		clauseErr, _ := errors.AsType[*InvalidClauseError](err)
		if want := `unknown operator "nope"`; clauseErr.Reason != want {
			t.Errorf("Reason = %q, want %q", clauseErr.Reason, want)
		}
	})
}

func TestParseRowClauses_RefusesANumOperandTheGrammarDoesNotAdmit(t *testing.T) {
	for _, value := range []string{
		"abc", "1abc", "3.5xyz", " 7", "7 ", " 7 ", ".5", "5.", "--1", "1.2.3", "1e3",
		"NaN", "Infinity", "-", "+7", "0x10", "1,000",
		"9007199254740993",
		"1234567890123456",
		"123456789012345.101",
		strings.Repeat("9", 309),
		strings.Repeat("9", 16386),
	} {
		t.Run(truncateForName(value), func(t *testing.T) {
			clause := "id:num.gt:" + value
			_, err := ParseRowClauses([]string{clause}, testColumns(), "Sheet1")
			if !IsHintOperandError(err) {
				t.Fatalf("ParseRowClauses(%q) error = %v, want HintOperandError", clause, err)
			}
			opErr, _ := errors.AsType[*HintOperandError](err)
			if opErr.Hint != RowHintNum || opErr.Value != value || opErr.Column != "id" {
				t.Errorf("error = {%q %q} hint %q, want column id and the refused value",
					opErr.Column, truncateForName(opErr.Value), opErr.Hint)
			}
		})
	}
}

// 15 significant digits is the bound both engines agree on; 16 is not, so the
// pair either side of it is the case that matters.
func TestParseRowClauses_NumOperandAtTheSignificanceBoundary(t *testing.T) {
	accepted := []string{"123456789012345", "0.123456789012345", "-123456789012345", "000000000000001"}
	for _, value := range accepted {
		t.Run("accepted "+value, func(t *testing.T) {
			got, err := ParseRowClauses([]string{"id:num.lte:" + value}, testColumns(), "Sheet1")
			if err != nil {
				t.Fatalf("ParseRowClauses(num.lte:%s) error = %v, want nil", value, err)
			}
			if got[0].Value != value {
				t.Errorf("Value = %q, want %q", got[0].Value, value)
			}
		})
	}
	for _, value := range []string{
		"1234567890123456", "0.1234567890123456", "-1234567890123456", "000123456789012345",
	} {
		t.Run("refused "+value, func(t *testing.T) {
			_, err := ParseRowClauses([]string{"id:num.lte:" + value}, testColumns(), "Sheet1")
			if !IsHintOperandError(err) {
				t.Fatalf("ParseRowClauses(num.lte:%s) error = %v, want HintOperandError", value, err)
			}
		})
	}
}

func TestParseRowClauses_RefusesADateOperandTheGrammarDoesNotAdmit(t *testing.T) {
	for _, value := range []string{
		"abc", "2026", "2026-9-11", "01/02/2026", "11-09-2026",
		"2026-09-11 00:00:00", "2026-09-11T00:00:00", "2026-09-11t00:00:00Z",
		"2026-09-11T00:00:00.5Z", "2026-09-11T10:00:00+07:00", "2026-09-11T00:00Z",
		" 2026-09-11", "2026-09-11 ",
	} {
		t.Run(value, func(t *testing.T) {
			clause := "created_at:date.gte:" + value
			_, err := ParseRowClauses([]string{clause}, testColumns(), "Sheet1")
			if !IsHintOperandError(err) {
				t.Fatalf("ParseRowClauses(%q) error = %v, want HintOperandError", clause, err)
			}
			opErr, _ := errors.AsType[*HintOperandError](err)
			if opErr.Hint != RowHintDate || opErr.Value != value || opErr.Column != "created_at" {
				t.Errorf("error = {%q %q} hint %q, want created_at and the refused value",
					opErr.Column, opErr.Value, opErr.Hint)
			}
		})
	}
}

// A hinted value keeps 3a's right to contain colons, so SplitN(clause, ":", 3)
// must stay exactly as it is.
func TestParseRowClauses_AHintedValueMayContainColons(t *testing.T) {
	const want = "2026-09-11T00:00:00Z"
	got, err := ParseRowClauses([]string{"created_at:date.gte:" + want}, testColumns(), "Sheet1")
	if err != nil {
		t.Fatalf("ParseRowClauses() error = %v, want nil", err)
	}
	c := got[0]
	if c.Column != "created_at" || c.Op != RowOpGte || c.Hint != RowHintDate || c.Value != want {
		t.Errorf("clause = {%q %q %q %q}, want {created_at gte date %q}",
			c.Column, c.Op, c.Hint, c.Value, want)
	}
}

// Every 3a clause must still parse with no hint, so no shipped request changes meaning.
func TestParseRowClauses_UnhintedClausesCarryNoHint(t *testing.T) {
	for _, clause := range []string{
		"note:eq:open", "note:ne:open", "note:gt:a", "note:gte:a", "note:lt:z", "note:lte:z",
		"note:contains:ada", "note:starts:ad", "note:in:a,b,c", "note:empty", "note:present",
		"note:eq:num.gt", "note:eq:10", "created_at:gte:2026-09-11T00:00:00Z",
	} {
		t.Run(clause, func(t *testing.T) {
			got, err := ParseRowClauses([]string{clause}, testColumns(), "Sheet1")
			if err != nil {
				t.Fatalf("ParseRowClauses(%q) error = %v, want nil", clause, err)
			}
			if got[0].Hint != RowHintNone {
				t.Errorf("Hint = %q, want none — an unhinted clause stays text", got[0].Hint)
			}
		})
	}
}

// 3a refuses an unreadable operator before it resolves the column; the hint
// refusals join that grammar phase rather than sitting behind it.
func TestParseRowClauses_HintRefusalsPrecedeColumnResolution(t *testing.T) {
	_, err := ParseRowClauses([]string{"statuz:num.contains:x"}, testColumns(), "Sheet1")
	if !IsHintNotApplicableError(err) {
		t.Fatalf("error = %v, want HintNotApplicableError", err)
	}
	_, err = ParseRowClauses([]string{"statuz:num.gt:abc"}, testColumns(), "Sheet1")
	if !IsHintOperandError(err) {
		t.Fatalf("error = %v, want HintOperandError", err)
	}
}

func truncateForName(v string) string {
	if len(v) <= 24 {
		return v
	}
	return v[:24] + "…(" + strconv.Itoa(len(v)) + " chars)"
}

// The float ParseNum yields is deliberately not carried on the clause: a second
// representation beside Value can drift from it, and a driver calls
// ParseNum(c.Value) at bind time instead.
func TestRowClause_CarriesNoSecondRepresentationOfTheValue(t *testing.T) {
	want := []string{"Column", "Op", "Hint", "Value", "Values", "Pattern"}
	rt := reflect.TypeFor[RowClause]()
	got := make([]string, 0, rt.NumField())
	for i := range rt.NumField() {
		got = append(got, rt.Field(i).Name)
	}
	if !slices.Equal(got, want) {
		t.Errorf("RowClause fields = %v, want %v", got, want)
	}
}

func TestParseRowSort_EveryDirectionAndHint(t *testing.T) {
	tests := []struct {
		spec string
		want RowSort
	}{
		{"qty:asc", RowSort{Column: "qty"}},
		{"qty:desc", RowSort{Column: "qty", Desc: true}},
		{"qty:num.asc", RowSort{Column: "qty", Hint: RowHintNum}},
		{"qty:num.desc", RowSort{Column: "qty", Hint: RowHintNum, Desc: true}},
		{"created_at:date.asc", RowSort{Column: "created_at", Hint: RowHintDate}},
		{"created_at:date.desc", RowSort{Column: "created_at", Hint: RowHintDate, Desc: true}},
		{"Status:asc", RowSort{Column: "Status"}},
	}
	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			got, err := ParseRowSort([]string{tt.spec})
			if err != nil {
				t.Fatalf("ParseRowSort(%q) error = %v, want nil", tt.spec, err)
			}
			if got == nil {
				t.Fatalf("ParseRowSort(%q) = nil, want %+v", tt.spec, tt.want)
			}
			if *got != tt.want {
				t.Errorf("ParseRowSort(%q) = %+v, want %+v", tt.spec, *got, tt.want)
			}
		})
	}
}

// An absent or blank ?sort= is an unsorted read, not a refusal.
func TestParseRowSort_AbsentOrBlankIsUnsorted(t *testing.T) {
	for name, raw := range map[string][]string{
		"nil":         nil,
		"empty slice": {},
		"empty value": {""},
		"blank value": {"   "},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ParseRowSort(raw)
			if err != nil {
				t.Fatalf("ParseRowSort(%#v) error = %v, want nil", raw, err)
			}
			if got != nil {
				t.Errorf("ParseRowSort(%#v) = %+v, want nil — an unsorted read", raw, *got)
			}
		})
	}
}

// query.Get silently takes the first value, which is the silent-typo class the
// parameter allow-list exists to prevent, so a repeat is refused outright.
func TestParseRowSort_RefusesARepeatedParameter(t *testing.T) {
	for _, raw := range [][]string{
		{"qty:asc", "name:desc"},
		{"qty:asc", "qty:asc"},
		{"qty:asc", ""},
		{"", "qty:asc"},
		{"qty:asc", "name:desc", "id:asc"},
	} {
		t.Run(strings.Join(raw, "&"), func(t *testing.T) {
			got, err := ParseRowSort(raw)
			if got != nil {
				t.Errorf("ParseRowSort(%#v) = %+v, want nil", raw, *got)
			}
			if !IsInvalidSortError(err) {
				t.Fatalf("ParseRowSort(%#v) error = %v, want InvalidSortError", raw, err)
			}
			sortErr, _ := errors.AsType[*InvalidSortError](err)
			if !strings.Contains(sortErr.Reason, "given "+strconv.Itoa(len(raw))+" times") {
				t.Errorf("Reason = %q, want it to name the repeat", sortErr.Reason)
			}
		})
	}
}

func TestParseRowSort_RefusesAMalformedSpec(t *testing.T) {
	tests := []struct {
		spec       string
		wantReason string
	}{
		{"qty", "no direction"},
		{"asc", "no direction"},
		{":asc", "no column"},
		{"   :asc", "no column"},
		{"qty:", `unknown direction ""`},
		{"qty:ascending", `unknown direction "ascending"`},
		{"qty:up", `unknown direction "up"`},
		{"qty:num.", `unknown direction ""`},
		{"qty:num.up", `unknown direction "up"`},
		{"qty:foo.asc", `unknown direction "foo.asc"`},
		{"a:b:asc", `unknown direction "b:asc"`},
		{"qty:asc:extra", `unknown direction "asc:extra"`},
	}
	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			got, err := ParseRowSort([]string{tt.spec})
			if got != nil {
				t.Errorf("ParseRowSort(%q) = %+v, want nil", tt.spec, *got)
			}
			if !IsInvalidSortError(err) {
				t.Fatalf("ParseRowSort(%q) error = %v, want InvalidSortError", tt.spec, err)
			}
			sortErr, _ := errors.AsType[*InvalidSortError](err)
			if !strings.Contains(sortErr.Reason, tt.wantReason) {
				t.Errorf("Reason = %q, want it to contain %q", sortErr.Reason, tt.wantReason)
			}
			if sortErr.Sort != tt.spec {
				t.Errorf("Sort = %q, want the offending value %q", sortErr.Sort, tt.spec)
			}
		})
	}
}

// splitRowHint returns the token unchanged on an unknown prefix, so an unknown
// hint must land on the direction refusal rather than on a hint error.
func TestParseRowSort_AnUnknownHintPrefixIsADirectionRefusal(t *testing.T) {
	_, err := ParseRowSort([]string{"qty:foo.asc"})
	if !IsInvalidSortError(err) {
		t.Fatalf("error = %v, want InvalidSortError", err)
	}
	if IsHintNotApplicableError(err) || IsHintOperandError(err) {
		t.Error("an unknown hint prefix became a hint refusal")
	}
}

// ?sort= splits on the first colon only, so a header holding a colon is
// unsortable exactly as it is already unfilterable.
func TestParseRowSort_SplitsOnTheFirstColonOnly(t *testing.T) {
	got, err := ParseRowSort([]string{"a:b:asc"})
	if got != nil {
		t.Errorf("ParseRowSort = %+v, want nil — a colon'd header is unsortable", *got)
	}
	if !IsInvalidSortError(err) {
		t.Fatalf("error = %v, want InvalidSortError", err)
	}
	sortErr, _ := errors.AsType[*InvalidSortError](err)
	if strings.Contains(sortErr.Reason, "no column") {
		t.Error("the spec split on the last colon and read \"a:b\" as the column")
	}
}

// The direction vocabulary is case-sensitive, like the operator and hint vocabularies.
func TestParseRowSort_DirectionsAreCaseSensitive(t *testing.T) {
	for _, spec := range []string{"qty:ASC", "qty:Asc", "qty:DESC", "qty:Desc", "qty:NUM.asc", "qty:num.ASC"} {
		t.Run(spec, func(t *testing.T) {
			if _, err := ParseRowSort([]string{spec}); !IsInvalidSortError(err) {
				t.Fatalf("ParseRowSort(%q) error = %v, want InvalidSortError", spec, err)
			}
		})
	}
}

// The column is deliberately not resolved here: the tab's column list does not
// exist until the projection has been written, so SHT041 is the route's refusal.
func TestParseRowSort_DoesNotValidateTheColumn(t *testing.T) {
	got, err := ParseRowSort([]string{"nosuchcolumn:asc"})
	if err != nil {
		t.Fatalf("ParseRowSort() error = %v, want nil", err)
	}
	if got == nil || got.Column != "nosuchcolumn" {
		t.Fatalf("ParseRowSort() = %+v, want the column carried verbatim", got)
	}
}

// An optional parsed value is carried as a pointer, mirroring RowWindow.Cursor,
// so "is this a sorted read?" is one nil check at every later site.
func TestRowSort_IsCarriedAsAnOptionalPointer(t *testing.T) {
	field, ok := reflect.TypeFor[RowQuery]().FieldByName("Sort")
	if !ok {
		t.Fatal("RowQuery has no Sort field")
	}
	if want := reflect.TypeFor[*RowSort](); field.Type != want {
		t.Errorf("RowQuery.Sort is %s, want %s", field.Type, want)
	}
	fn := reflect.TypeOf(ParseRowSort)
	if fn.NumIn() != 1 || fn.In(0) != reflect.TypeFor[[]string]() {
		t.Errorf("ParseRowSort takes %s, want []string — a single string cannot represent a repeat", fn.In(0))
	}
	if fn.NumOut() != 2 || fn.Out(0) != reflect.TypeFor[*RowSort]() {
		t.Errorf("ParseRowSort returns %s, want *RowSort", fn.Out(0))
	}
}

// 0 falls back to the default rather than to an unbounded walk: a sorted walk is
// never unbounded, the same reasoning sheets.maxQueryRows uses.
func TestMaxSortPagesOf_ZeroAndNegativeFallBackToTheDefault(t *testing.T) {
	tests := []struct {
		configured int
		want       int
	}{
		{0, DefaultMaxSortPages},
		{-1, DefaultMaxSortPages},
		{1, 1},
		{5, 5},
		{DefaultMaxSortPages, DefaultMaxSortPages},
		{500, 500},
	}
	for _, tt := range tests {
		t.Run(strconv.Itoa(tt.configured), func(t *testing.T) {
			if got := maxSortPagesOf(tt.configured); got != tt.want {
				t.Errorf("maxSortPagesOf(%d) = %d, want %d", tt.configured, got, tt.want)
			}
		})
	}
	if DefaultMaxSortPages != 20 {
		t.Errorf("DefaultMaxSortPages = %d, want 20 — it pairs with sheets.maxSortPages", DefaultMaxSortPages)
	}
}

func TestSortedRowCursor_RoundTripsEveryField(t *testing.T) {
	sortValue := "42"
	for _, want := range []RowCursor{
		{Digest: "abc", RowIndex: 0, NullRank: 0, SortValue: &sortValue, Page: 1, Version: 2},
		{Digest: "abc", RowIndex: 17, NullRank: 1, SortValue: nil, Page: 3, Version: 2},
		{Digest: strings.Repeat("f", 64), RowIndex: 999999, NullRank: 0, SortValue: &sortValue, Page: 20, Version: 2},
	} {
		raw, err := EncodeSortedRowCursor(want)
		if err != nil {
			t.Fatalf("EncodeSortedRowCursor(%+v) error = %v, want nil", want, err)
		}
		if strings.ContainsAny(raw, "+/=") {
			t.Errorf("EncodeSortedRowCursor() = %q, want a raw URL-safe encoding", raw)
		}
		got, err := DecodeRowCursor(raw)
		if err != nil {
			t.Fatalf("DecodeRowCursor(%q) error = %v, want nil", raw, err)
		}
		if got.Digest != want.Digest || got.RowIndex != want.RowIndex ||
			got.NullRank != want.NullRank || got.Page != want.Page || got.Version != want.Version {
			t.Errorf("DecodeRowCursor() = %+v, want %+v", got, want)
		}
		if (got.SortValue == nil) != (want.SortValue == nil) {
			t.Fatalf("SortValue = %v, want %v", got.SortValue, want.SortValue)
		}
		if got.SortValue != nil && *got.SortValue != *want.SortValue {
			t.Errorf("SortValue = %q, want %q", *got.SortValue, *want.SortValue)
		}
	}
}

// An absent sort value on the wire as "" collapses the tie-break arm of a text
// or date walk, which drops every row after the first null.
func TestEncodeSortedRowCursor_AbsentSortValueIsJSONNull(t *testing.T) {
	raw, err := EncodeSortedRowCursor(RowCursor{Digest: "abc", RowIndex: 4, NullRank: 1, Page: 1})
	if err != nil {
		t.Fatalf("EncodeSortedRowCursor() error = %v, want nil", err)
	}
	body, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		t.Fatalf("DecodeString() error = %v, want nil", err)
	}
	if !strings.Contains(string(body), `"s":null`) {
		t.Errorf("envelope = %s, want it to carry \"s\":null", body)
	}
	if strings.Contains(string(body), `"s":""`) {
		t.Errorf("envelope = %s, want no empty-string sort value", body)
	}
	got, err := DecodeRowCursor(raw)
	if err != nil {
		t.Fatalf("DecodeRowCursor() error = %v, want nil", err)
	}
	if got.SortValue != nil {
		t.Errorf("SortValue = %q, want nil", *got.SortValue)
	}
}

// A blank cell is a real sort value under a text sort, so it must stay a
// non-nil pointer and never merge with the absent case.
func TestEncodeSortedRowCursor_EmptySortValueStaysNonNil(t *testing.T) {
	blank := ""
	raw, err := EncodeSortedRowCursor(RowCursor{Digest: "abc", RowIndex: 1, SortValue: &blank, Page: 1})
	if err != nil {
		t.Fatalf("EncodeSortedRowCursor() error = %v, want nil", err)
	}
	body, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		t.Fatalf("DecodeString() error = %v, want nil", err)
	}
	if !strings.Contains(string(body), `"s":""`) {
		t.Errorf("envelope = %s, want it to carry \"s\":\"\"", body)
	}
	got, err := DecodeRowCursor(raw)
	if err != nil {
		t.Fatalf("DecodeRowCursor() error = %v, want nil", err)
	}
	if got.SortValue == nil {
		t.Fatal("SortValue = nil, want a pointer to the empty string")
	}
	if *got.SortValue != "" {
		t.Errorf("SortValue = %q, want %q", *got.SortValue, "")
	}
}

func TestDecodeRowCursor_ReportsTheVersionItRead(t *testing.T) {
	unsorted, err := EncodeRowCursor(RowCursor{Digest: "abc", RowIndex: 7})
	if err != nil {
		t.Fatalf("EncodeRowCursor() error = %v, want nil", err)
	}
	got, err := DecodeRowCursor(unsorted)
	if err != nil {
		t.Fatalf("DecodeRowCursor() error = %v, want nil", err)
	}
	if got.Version != 1 {
		t.Errorf("Version = %d, want 1", got.Version)
	}
	sorted, err := EncodeSortedRowCursor(RowCursor{Digest: "abc", RowIndex: 7, Page: 1})
	if err != nil {
		t.Fatalf("EncodeSortedRowCursor() error = %v, want nil", err)
	}
	got, err = DecodeRowCursor(sorted)
	if err != nil {
		t.Fatalf("DecodeRowCursor() error = %v, want nil", err)
	}
	if got.Version != 2 {
		t.Errorf("Version = %d, want 2", got.Version)
	}
}

func TestDecodeRowCursor_MalformedSortedEnvelope(t *testing.T) {
	tests := []struct {
		name string
		body string
		raw  string
	}{
		{name: "version three", body: `{"v":3,"d":"a","r":1,"n":0,"s":null,"p":1}`},
		{name: "version zero", body: `{"v":0,"d":"a","r":1,"n":0,"s":null,"p":1}`},
		{name: "padded base64", raw: base64.StdEncoding.EncodeToString([]byte(`{"v":2,"d":"ab","r":1,"n":0,"s":null,"p":1}`))},
		{name: "not json", body: `{"v":2,`},
		{name: "digest missing", body: `{"v":2,"r":1,"n":0,"s":null,"p":1}`},
		{name: "row index missing", body: `{"v":2,"d":"a","n":0,"s":null,"p":1}`},
		{name: "null rank missing", body: `{"v":2,"d":"a","r":1,"s":null,"p":1}`},
		{name: "null rank two", body: `{"v":2,"d":"a","r":1,"n":2,"s":null,"p":1}`},
		{name: "null rank negative", body: `{"v":2,"d":"a","r":1,"n":-1,"s":null,"p":1}`},
		{name: "null rank not a number", body: `{"v":2,"d":"a","r":1,"n":"0","s":null,"p":1}`},
		{name: "page missing", body: `{"v":2,"d":"a","r":1,"n":0,"s":null}`},
		{name: "page negative", body: `{"v":2,"d":"a","r":1,"n":0,"s":null,"p":-1}`},
		{name: "sort value not a string", body: `{"v":2,"d":"a","r":1,"n":0,"s":42,"p":1}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := tt.raw
			if tt.body != "" {
				raw = base64.RawURLEncoding.EncodeToString([]byte(tt.body))
			}
			got, err := DecodeRowCursor(raw)
			if !IsInvalidCursorError(err) {
				t.Fatalf("DecodeRowCursor(%q) = %+v, %v, want InvalidCursorError", raw, got, err)
			}
		})
	}
}

func TestEncodeSortedRowCursor_RefusesAnUnusableCursor(t *testing.T) {
	for _, c := range []RowCursor{
		{Digest: "", RowIndex: 1, Page: 1},
		{Digest: "abc", RowIndex: -1, Page: 1},
		{Digest: "abc", RowIndex: 1, Page: -1},
		{Digest: "abc", RowIndex: 1, NullRank: 2, Page: 1},
		{Digest: "abc", RowIndex: 1, NullRank: -1, Page: 1},
	} {
		raw, err := EncodeSortedRowCursor(c)
		if err == nil {
			t.Fatalf("EncodeSortedRowCursor(%+v) = %q, want an error", c, raw)
		}
		if IsInvalidCursorError(err) {
			t.Errorf("EncodeSortedRowCursor(%+v) error = %v, want an internal failure, not a client refusal", c, err)
		}
	}
}

func TestCheckRowCursorVersion_PairsTheVersionWithTheSortedness(t *testing.T) {
	if err := CheckRowCursorVersion(nil, true); err != nil {
		t.Errorf("CheckRowCursorVersion(nil, true) error = %v, want nil", err)
	}
	if err := CheckRowCursorVersion(nil, false); err != nil {
		t.Errorf("CheckRowCursorVersion(nil, false) error = %v, want nil", err)
	}
	unsorted := &RowCursor{Digest: "a", RowIndex: 1, Version: 1}
	sorted := &RowCursor{Digest: "a", RowIndex: 1, Page: 1, Version: 2}
	if err := CheckRowCursorVersion(unsorted, false); err != nil {
		t.Errorf("CheckRowCursorVersion(v1, unsorted) error = %v, want nil", err)
	}
	if err := CheckRowCursorVersion(sorted, true); err != nil {
		t.Errorf("CheckRowCursorVersion(v2, sorted) error = %v, want nil", err)
	}
	onSorted := CheckRowCursorVersion(unsorted, true)
	if !IsInvalidCursorError(onSorted) {
		t.Fatalf("CheckRowCursorVersion(v1, sorted) error = %v, want InvalidCursorError", onSorted)
	}
	onUnsorted := CheckRowCursorVersion(sorted, false)
	if !IsInvalidCursorError(onUnsorted) {
		t.Fatalf("CheckRowCursorVersion(v2, unsorted) error = %v, want InvalidCursorError", onUnsorted)
	}
	if onSorted.Error() == onUnsorted.Error() {
		t.Errorf("both directions report %q, want a reason distinguishing them", onSorted)
	}
	var a, b *InvalidCursorError
	if !errors.As(onSorted, &a) || !errors.As(onUnsorted, &b) {
		t.Fatal("errors.As did not reach the InvalidCursorError")
	}
	if msg := a.ToAppError().Message(); !strings.Contains(msg, "unsorted read and this read is sorted") {
		t.Errorf("v1-on-sorted message = %q, want it to say which way round it is", msg)
	}
	if msg := b.ToAppError().Message(); !strings.Contains(msg, "sorted read and this read is unsorted") {
		t.Errorf("v2-on-unsorted message = %q, want it to say which way round it is", msg)
	}
}
