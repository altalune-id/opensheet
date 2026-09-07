package render

import (
	"bytes"
	"encoding/json"
	"iter"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"google.golang.org/grpc/codes"

	"altalune.id/template/internal/apperror"
)

func TestDetect_FlagWins(t *testing.T) {
	t.Setenv(EnvVar, "text")
	root := &cobra.Command{Use: "root"}
	root.PersistentFlags().String("output", "", "")
	child := &cobra.Command{Use: "child"}
	root.AddCommand(child)
	if err := root.PersistentFlags().Set("output", "json"); err != nil {
		t.Fatal(err)
	}
	if got := Detect(child); got != FormatJSON {
		t.Errorf("want json, got %q", got)
	}
}

func TestDetect_EnvWhenNoFlag(t *testing.T) {
	t.Setenv(EnvVar, "ndjson")
	root := &cobra.Command{Use: "root"}
	root.PersistentFlags().String("output", "", "")
	child := &cobra.Command{Use: "child"}
	root.AddCommand(child)
	if got := Detect(child); got != FormatNDJSON {
		t.Errorf("want ndjson, got %q", got)
	}
}

func TestDetect_UnknownFallsBackToText(t *testing.T) {
	t.Setenv(EnvVar, "gibberish")
	root := &cobra.Command{Use: "root"}
	root.PersistentFlags().String("output", "", "")
	child := &cobra.Command{Use: "child"}
	root.AddCommand(child)
	if got := Detect(child); got != FormatText {
		t.Errorf("want text, got %q", got)
	}
}

func TestTable_WritesRows(t *testing.T) {
	buf := &bytes.Buffer{}
	if err := Table(buf, []string{"ID", "NAME"}, [][]string{{"1", "alice"}, {"2", "bob"}}); err != nil {
		t.Fatalf("Table: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "alice") || !strings.Contains(got, "bob") {
		t.Errorf("unexpected table body: %q", got)
	}
	if !strings.Contains(got, "ID") {
		t.Errorf("expected header row, got %q", got)
	}
}

func TestJSON_EnvelopesData(t *testing.T) {
	buf := &bytes.Buffer{}
	payload := []map[string]any{{"a": 1}, {"a": 2}}
	if err := JSON(buf, payload); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var got struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Data) != 2 {
		t.Errorf("data length = %d, want 2", len(got.Data))
	}
}

func TestNDJSON_OneObjectPerLine(t *testing.T) {
	buf := &bytes.Buffer{}
	seq := iter.Seq[any](func(yield func(any) bool) {
		if !yield(map[string]any{"n": 1}) {
			return
		}
		yield(map[string]any{"n": 2})
	})
	if err := NDJSON(buf, seq); err != nil {
		t.Fatalf("NDJSON: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Errorf("want 2 lines, got %d: %q", len(lines), buf.String())
	}
}

func TestError_TextFormat(t *testing.T) {
	buf := &bytes.Buffer{}
	ae := apperror.New("X_Y", "boom", codes.Internal)
	if err := Error(buf, FormatText, ae, 1); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "boom") {
		t.Errorf("expected error text, got %q", buf.String())
	}
}

func TestError_JSONFormat(t *testing.T) {
	buf := &bytes.Buffer{}
	ae := apperror.New("X_Y", "boom", codes.Internal)
	if err := Error(buf, FormatJSON, ae, 1); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Exit    int    `json:"exit"`
		} `json:"error"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Error.Code != "X_Y" || got.Error.Message != "boom" || got.Error.Exit != 1 {
		t.Errorf("unexpected envelope: %+v", got)
	}
}
