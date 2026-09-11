package sheet_test

import (
	"database/sql"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"altalune.id/opensheet/internal/sheet"
)

type numGrammarCase struct {
	name string
	in   string
	want float64
	ok   bool
}

// numGrammarCases drives all three implementations of the num grammar: ParseNum, opensheet_num and the Postgres regex.
func numGrammarCases() []numGrammarCase {
	return []numGrammarCase{
		{name: "leading zeros", in: "003", want: 3, ok: true},
		{name: "negative", in: "-4", want: -4, ok: true},
		{name: "negative zero", in: "-0", want: 0, ok: true},
		{name: "zero", in: "0", want: 0, ok: true},
		{name: "fraction", in: "1.5", want: 1.5, ok: true},
		{name: "trailing zero fraction", in: "1.50", want: 1.5, ok: true},
		{name: "fifteen integer digits", in: "999999999999999", want: 999999999999999, ok: true},
		{name: "fifteen integer digits other", in: "123456789012345", want: 123456789012345, ok: true},
		{name: "one significant digit eleven chars", in: "0.000000001", want: 1e-9, ok: true},
		{name: "one significant digit seventeen chars", in: "0.000000000000001", want: 1e-15, ok: true},
		{name: "fifteen significant digits across the point", in: "-12345678901234.5", want: -12345678901234.5, ok: true},

		{name: "exponent lower", in: "1e3", ok: false},
		{name: "exponent upper", in: "1E3", ok: false},
		{name: "exponent signed", in: "1e-3", ok: false},
		{name: "nan", in: "NaN", ok: false},
		{name: "nan lower", in: "nan", ok: false},
		{name: "infinity", in: "Infinity", ok: false},
		{name: "negative infinity", in: "-Infinity", ok: false},
		{name: "inf", in: "inf", ok: false},
		{name: "letters", in: "abc", ok: false},
		{name: "digit then letters", in: "1abc", ok: false},
		{name: "partial parse", in: "3.5xyz", ok: false},
		{name: "padded both sides", in: " 7 ", ok: false},
		{name: "padded left", in: " 7", ok: false},
		{name: "padded right", in: "7 ", ok: false},
		{name: "empty", in: "", ok: false},
		{name: "bare leading point", in: ".5", ok: false},
		{name: "bare trailing point", in: "5.", ok: false},
		{name: "double sign", in: "--1", ok: false},
		{name: "two points", in: "1.2.3", ok: false},
		{name: "explicit plus", in: "+4", ok: false},
		{name: "thousands separator", in: "1,000", ok: false},
		{name: "currency", in: "$7", ok: false},
		{name: "parenthesised negative", in: "(7)", ok: false},
		{name: "newline", in: "7\n", ok: false},

		{name: "two to the fifty three", in: "9007199254740992", ok: false},
		{name: "two to the fifty three plus one", in: "9007199254740993", ok: false},
		{name: "sixteen digits", in: "1234567890123456", ok: false},
		{name: "sixteen digits last differs", in: "1234567890123457", ok: false},
		{name: "sixteen significant digits integer", in: "1000000000000000", ok: false},
		{name: "sixteen significant digits across the point", in: "123456789012345.1", ok: false},
		{name: "sixteen significant digits across the point nines", in: "999999999999999.9", ok: false},
		{name: "seventeen digits", in: "12345678901234567", ok: false},
		{name: "seventeen digits last differs", in: "12345678901234568", ok: false},
		{name: "sixteen thousand three hundred eighty six chars", in: "0." + strings.Repeat("9", 16384), ok: false},
		{name: "three hundred nine digits", in: strings.Repeat("9", 309), ok: false},
	}
}

type dateGrammarCase struct {
	name string
	in   string
	ok   bool
}

// dateGrammarCases drives all three implementations of the date grammar: MatchesDateShape, opensheet_date and the Postgres regex.
func dateGrammarCases() []dateGrammarCase {
	return []dateGrammarCase{
		{name: "day", in: "2026-01-02", ok: true},
		{name: "instant", in: "2026-01-02T00:00:00Z", ok: true},
		{name: "instant end of day", in: "2026-01-02T23:59:59Z", ok: true},
		{name: "impossible day passes the shape guard", in: "2026-02-31", ok: true},
		{name: "impossible month passes the shape guard", in: "2026-99-99", ok: true},
		{name: "impossible time passes the shape guard", in: "2026-01-02T25:99:99Z", ok: true},

		{name: "fractional seconds", in: "2026-01-02T00:00:00.5Z", ok: false},
		{name: "fractional seconds three places", in: "2026-01-02T00:00:00.000Z", ok: false},
		{name: "positive offset", in: "2026-01-02T10:00:00+07:00", ok: false},
		{name: "zero offset spelled out", in: "2026-01-02T00:00:00+00:00", ok: false},
		{name: "lowercase zulu", in: "2026-01-02T00:00:00z", ok: false},
		{name: "missing zulu", in: "2026-01-02T00:00:00", ok: false},
		{name: "space instead of T", in: "2026-01-02 00:00:00", ok: false},
		{name: "trailing junk after a day", in: "2026-01-02xyz", ok: false},
		{name: "leading junk before a day", in: "xyz2026-01-02", ok: false},
		{name: "leading junk before an instant", in: "xyz2026-01-02T00:00:00Z", ok: false},
		{name: "trailing junk after an instant", in: "2026-01-02T00:00:00Zxyz", ok: false},
		{name: "trailing newline", in: "2026-01-02\n", ok: false},
		{name: "unpadded month and day", in: "2026-1-2", ok: false},
		{name: "two digit year", in: "26-01-02", ok: false},
		{name: "slashes month first", in: "01/02/2026", ok: false},
		{name: "empty", in: "", ok: false},
		{name: "padded", in: " 2026-01-02 ", ok: false},
	}
}

func TestParseNum(t *testing.T) {
	for _, tc := range numGrammarCases() {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := sheet.ParseNum(tc.in)
			require.Equal(t, tc.ok, ok, "ParseNum(%q) acceptance", tc.in)
			assert.Equal(t, tc.want, got, "ParseNum(%q) value", tc.in)
		})
	}
}

// TestParseNum_RejectionIsAlwaysZero pins that ErrRange and ErrSyntax both map to a zero float, never to an infinity that would dominate a :desc sort.
func TestParseNum_RejectionIsAlwaysZero(t *testing.T) {
	for _, in := range []string{
		strings.Repeat("9", 309),
		strings.Repeat("9", 400),
		"-" + strings.Repeat("9", 309),
		"0." + strings.Repeat("9", 16384),
		"Infinity",
		"-Infinity",
		"1e400",
		"NaN",
	} {
		got, ok := sheet.ParseNum(in)
		require.False(t, ok, "ParseNum must reject a value outside the grammar")
		require.Equal(t, 0.0, got, "a rejected value must read back as zero, never as an infinity")
		require.False(t, math.IsInf(got, 0), "a rejected value must never read back as an infinity")
	}
}

// TestNumPattern_IsOnlyTheShapeHalf records that the exported pattern is necessary but not sufficient: a Postgres-side guard must carry the significance bound too.
func TestNumPattern_IsOnlyTheShapeHalf(t *testing.T) {
	shape := regexp.MustCompile(sheet.NumPattern)
	const crossRun = "123456789012345.1"
	require.True(t, shape.MatchString(crossRun), "each digit run is within the pattern's bound")
	_, ok := sheet.ParseNum(crossRun)
	require.False(t, ok, "16 significant digits spread across the point must still be refused")
	require.Equal(t, 15, sheet.NumSignificanceLimit)
}

func TestMatchesDateShape(t *testing.T) {
	for _, tc := range dateGrammarCases() {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.ok, sheet.MatchesDateShape(tc.in), "MatchesDateShape(%q)", tc.in)
		})
	}
}

func newTypedSQLite(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return sqlDB
}

// TestSQLiteNum_AgreesWithParseNum drives the registered function off the same table as ParseNum: the cell test and the operand test must be one grammar.
func TestSQLiteNum_AgreesWithParseNum(t *testing.T) {
	sqlDB := newTypedSQLite(t)
	for _, tc := range numGrammarCases() {
		t.Run(tc.name, func(t *testing.T) {
			var got sql.NullFloat64
			var kind string
			require.NoError(t, sqlDB.QueryRowContext(t.Context(),
				`SELECT opensheet_num(?), typeof(opensheet_num(?))`, tc.in, tc.in).Scan(&got, &kind))
			require.Equal(t, tc.ok, got.Valid, "opensheet_num(%q) acceptance", tc.in)
			assert.Equal(t, tc.want, got.Float64, "opensheet_num(%q) value", tc.in)
			if !tc.ok {
				assert.Equal(t, "null", kind)
				return
			}
			assert.Equal(t, "real", kind, "opensheet_num must yield REAL storage, or a comparison against a text bind inverts")
		})
	}
}

// TestSQLiteDate_AgreesWithMatchesDateShape pins that opensheet_date hands back the string, so the comparison stays textual.
func TestSQLiteDate_AgreesWithMatchesDateShape(t *testing.T) {
	sqlDB := newTypedSQLite(t)
	for _, tc := range dateGrammarCases() {
		t.Run(tc.name, func(t *testing.T) {
			var got sql.NullString
			var kind string
			require.NoError(t, sqlDB.QueryRowContext(t.Context(),
				`SELECT opensheet_date(?), typeof(opensheet_date(?))`, tc.in, tc.in).Scan(&got, &kind))
			require.Equal(t, tc.ok, got.Valid, "opensheet_date(%q) acceptance", tc.in)
			if !tc.ok {
				assert.Equal(t, "null", kind)
				return
			}
			assert.Equal(t, tc.in, got.String, "opensheet_date must return the cell verbatim")
			assert.Equal(t, "text", kind, "opensheet_date must yield TEXT storage so the comparison stays textual")
		})
	}
}

// TestSQLiteTypedFunctions_NonStringArgumentIsNull pins that a non-string cell is null rather than an error, or one such cell becomes a 500.
func TestSQLiteTypedFunctions_NonStringArgumentIsNull(t *testing.T) {
	sqlDB := newTypedSQLite(t)
	for _, arg := range []string{"1", "1.5", "NULL", "x'0102'"} {
		t.Run(arg, func(t *testing.T) {
			var num sql.NullFloat64
			var date sql.NullString
			require.NoError(t, sqlDB.QueryRowContext(t.Context(),
				`SELECT opensheet_num(`+arg+`), opensheet_date(`+arg+`)`).Scan(&num, &date))
			assert.False(t, num.Valid, "opensheet_num(%s) must be NULL, not an error", arg)
			assert.False(t, date.Valid, "opensheet_date(%s) must be NULL, not an error", arg)
		})
	}
}

// TestSQLiteNum_OperandBindsAsFloat pins the storage-class trap: a text bind makes every gt return nothing and every lt everything.
func TestSQLiteNum_OperandBindsAsFloat(t *testing.T) {
	sqlDB := newTypedSQLite(t)
	operand, ok := sheet.ParseNum("5")
	require.True(t, ok)

	var floatBind, textBind bool
	require.NoError(t, sqlDB.QueryRowContext(t.Context(),
		`SELECT opensheet_num('9') > ?, opensheet_num('9') > ?`, operand, "5").Scan(&floatBind, &textBind))
	assert.True(t, floatBind, "a float bind compares numerically")
	assert.False(t, textBind, "a text bind is the shipped defect this pins: REAL always sorts below TEXT")
}

// TestSQLiteNum_RoundTripsThroughStrconv pins that a cell's float renders back to a decimal Postgres reads as the same numeric.
func TestSQLiteNum_RoundTripsThroughStrconv(t *testing.T) {
	for _, tc := range numGrammarCases() {
		if !tc.ok {
			continue
		}
		got, ok := sheet.ParseNum(tc.in)
		require.True(t, ok)
		back, ok := sheet.ParseNum(strconv.FormatFloat(got, 'f', -1, 64))
		require.True(t, ok, "the rendered form of %q must re-enter the grammar", tc.in)
		require.Equal(t, got, back, "%q must round-trip", tc.in)
	}
}
