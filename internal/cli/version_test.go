package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestVersion_TextOutput(t *testing.T) {
	setSelfhostedEnv(t)
	root := NewRootCmd(stubServerBoot, stubClientBoot)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"--output", "text", "version"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(buf.String(), "altempl") {
		t.Errorf("expected altempl in output, got %q", buf.String())
	}
}

func TestVersion_JSONOutput(t *testing.T) {
	setSelfhostedEnv(t)
	root := NewRootCmd(stubServerBoot, stubClientBoot)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"--output", "json", "version"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body=%q", err, buf.String())
	}
	if got.Data == nil {
		t.Fatal("data missing")
	}
}
