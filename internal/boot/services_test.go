package boot_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/boot"
	"altalune.id/opensheet/internal/platform/sealer"
)

const testEncryptionKey = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

func TestBuildServices_WiresAllFourModules(t *testing.T) {
	cfg := newSmokeCfg(t)
	srv, err := boot.BootServer(t.Context(), cfg)
	if err != nil {
		t.Fatalf("BootServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	if srv.Credentials == nil {
		t.Error("credential service must be wired")
	}
	if srv.Spreadsheets == nil {
		t.Error("spreadsheet service must be wired")
	}
	if srv.Sheets == nil {
		t.Error("sheet service must be wired")
	}
	if srv.APIKeys == nil {
		t.Error("apikey service must be wired")
	}
	if srv.Read == nil {
		t.Error("sheet read workflow must be wired")
	}
	if srv.Connect == nil {
		t.Error("credential connect workflow must be wired")
	}
}

func TestBuildServices_AuthnChainHasBothAuthenticators(t *testing.T) {
	cfg := newSmokeCfg(t)
	srv, err := boot.BootServer(t.Context(), cfg)
	if err != nil {
		t.Fatalf("BootServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	if srv.API == nil {
		t.Fatal("API server must be wired")
	}
	if got := len(srv.API.Authn); got != 2 {
		t.Fatalf("authn chain length = %d, want 2", got)
	}
	for i, a := range srv.API.Authn {
		if a == nil {
			t.Errorf("authn chain link %d is nil", i)
		}
	}
}

func TestBuildServices_RegistersTheAPIKeyUsageWorker(t *testing.T) {
	cfg := newSmokeCfg(t)
	srv, err := boot.BootServer(t.Context(), cfg)
	if err != nil {
		t.Fatalf("BootServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	for _, w := range srv.Supervisor.Workers() {
		if w.Name() == "apikey-lastused" {
			return
		}
	}
	t.Error("the apikey usage worker must be registered on the supervisor")
}

func TestBootstrap_SealerDisabledWithoutAKey(t *testing.T) {
	cfg := newSmokeCfg(t)
	cfg.Security.EncryptionKey = ""
	srv, err := boot.BootServer(t.Context(), cfg)
	if err != nil {
		t.Fatalf("BootServer must still boot without an encryption key: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	if srv.Platform.Sealer == nil {
		t.Fatal("Sealer must never be nil")
	}
	if _, err := srv.Platform.Sealer.Seal([]byte("secret"), nil); !sealer.IsUnavailableError(err) {
		t.Errorf("Seal error = %v, want *UnavailableError", err)
	}
}

func TestBootstrap_ValidEncryptionKeyYieldsAWorkingSealer(t *testing.T) {
	cfg := newSmokeCfg(t)
	cfg.Security.EncryptionKey = testEncryptionKey
	srv, err := boot.BootServer(t.Context(), cfg)
	if err != nil {
		t.Fatalf("BootServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	aad := []byte("org/project/credential")
	ct, err := srv.Platform.Sealer.Seal([]byte("secret"), aad)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	pt, err := srv.Platform.Sealer.Open(ct, aad)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if string(pt) != "secret" {
		t.Errorf("Open = %q, want %q", pt, "secret")
	}
}

func TestBootstrap_InvalidEncryptionKeyIsABootError(t *testing.T) {
	for name, key := range map[string]string{
		"not hex or base64": "not-a-key!!",
		"too short":         "00112233",
	} {
		t.Run(name, func(t *testing.T) {
			cfg := newSmokeCfg(t)
			cfg.Security.EncryptionKey = key
			srv, err := boot.BootServer(t.Context(), cfg)
			if err == nil {
				_ = srv.Close()
				t.Fatal("expected a boot error, got nil")
			}
			if !strings.Contains(err.Error(), "encryptionKey") {
				t.Errorf("error = %v, want it to name security.encryptionKey", err)
			}
		})
	}
}

func TestBootstrap_GoogleConnectDisabledWithoutAnOAuthClient(t *testing.T) {
	cfg := newSmokeCfg(t)
	cfg.Google.OAuth.ClientID = "id-without-a-secret"
	srv, err := boot.BootServer(t.Context(), cfg)
	if err != nil {
		t.Fatalf("a half-configured google client must not fail the boot: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	id := uuid.Must(uuid.NewV7())
	if _, err := srv.Connect.Start(t.Context(), id, id, id, ""); err == nil {
		t.Error("Start must refuse when no google client is configured")
	}
}
