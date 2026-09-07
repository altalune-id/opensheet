package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"altalune.id/template/internal/boot"
	"altalune.id/template/internal/platform/config"
	"altalune.id/template/internal/platform/session"
)

func bootServerEcho(_ context.Context, cfg *config.Config, _ ...boot.Option) (*boot.Server, error) {
	return &boot.Server{Cfg: cfg}, nil
}

func TestWhoami_NoSession(t *testing.T) {
	setSelfhostedEnv(t)

	root := NewRootCmd(bootServerEcho, stubClientBoot)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"auth", "whoami"})
	err := root.ExecuteContext(context.Background())
	if !errors.Is(err, ErrNotSignedIn) {
		t.Fatalf("got %v, want ErrNotSignedIn", err)
	}
}

func TestWhoami_WithSession(t *testing.T) {
	sessPath := setSelfhostedEnv(t)
	uid := uuid.New()
	if err := saveSessionFile(sessPath, &sessionFile{
		Principal: session.Principal{UserID: uid, Email: "who@here", Source: session.SourceGenesis},
		IssuedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	root := NewRootCmd(bootServerEcho, stubClientBoot)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"--output", "text", "auth", "whoami"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("whoami: %v", err)
	}
	if !strings.Contains(buf.String(), "who@here") {
		t.Errorf("expected email in output, got %q", buf.String())
	}
}

func TestWhoami_JSONOutput(t *testing.T) {
	sessPath := setSelfhostedEnv(t)
	if err := saveSessionFile(sessPath, &sessionFile{
		Principal: session.Principal{UserID: uuid.New(), Email: "j@son", Source: session.SourceGenesis},
		IssuedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	root := NewRootCmd(bootServerEcho, stubClientBoot)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"--output", "json", "auth", "whoami"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("whoami json: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `"data"`) || !strings.Contains(out, "j@son") {
		t.Errorf("expected JSON envelope with email, got %q", out)
	}
}

func TestLogout_RemovesSession(t *testing.T) {
	sessPath := setSelfhostedEnv(t)
	if err := saveSessionFile(sessPath, &sessionFile{
		Principal: session.Principal{UserID: uuid.New(), Email: "z@z", Source: session.SourceGenesis},
	}); err != nil {
		t.Fatal(err)
	}

	root := NewRootCmd(bootServerEcho, stubClientBoot)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"auth", "logout"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if s, _ := loadSessionFile(sessPath); s != nil {
		t.Fatal("session file should be gone")
	}
}
