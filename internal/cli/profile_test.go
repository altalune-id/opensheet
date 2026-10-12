package cli

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"altalune.id/opensheet/internal/platform/config"
)

func TestURLPrecedence(t *testing.T) {
	// --url > OPENSHEET_URL > saved profile > http.baseURL from config.
	tests := []struct{ name, flag, env, profile, config, want string }{
		{"flag wins", "https://flag.example", "https://env.example", "https://prof.example", "https://cfg.example", "https://flag.example"},
		{"env over profile", "", "https://env.example", "https://prof.example", "https://cfg.example", "https://env.example"},
		{"profile over config", "", "", "https://prof.example", "https://cfg.example", "https://prof.example"},
		{"config is the floor", "", "", "", "https://cfg.example", "https://cfg.example"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			sessionPath := filepath.Join(dir, "session.json")
			if tt.profile != "" {
				sf := &sessionFile{Profiles: map[string]profile{tt.profile: {URL: tt.profile}}}
				if err := saveSessionFile(sessionPath, sf); err != nil {
					t.Fatal(err)
				}
			}
			if tt.env != "" {
				t.Setenv("OPENSHEET_URL", tt.env)
			}

			cfg := &config.Config{
				Session: config.SessionConfig{Path: sessionPath},
				HTTP:    config.HTTPConfig{BaseURL: tt.config},
			}

			root := NewRootCmd(stubServerBoot, stubClientBoot)
			args := []string{"version"}
			if tt.flag != "" {
				args = []string{"--url", tt.flag, "version"}
			}
			root.SetArgs(args)
			if err := root.ParseFlags(args); err != nil {
				t.Fatal(err)
			}
			root.SetContext(ctxWithConfig(context.Background(), cfg))

			if got := resolveURL(root); got != tt.want {
				t.Errorf("resolveURL = %q, want %q", got, tt.want)
			}
		})
	}
}

func profileFor(url string) profile {
	return profile{URL: url, Org: "acme", Project: "site", Device: "device-for-" + url}
}

func legacyProfileWithoutURL() profile {
	return profile{Device: "legacy-device-key"}
}

// SECURITY: a saved credential belongs to the host that issued it.
func TestSavedCredentialNotSentToAnotherHost(t *testing.T) {
	_, err := credentialFor("https://b.example", profileFor("https://a.example"), "" /* explicit token */)
	if err == nil {
		t.Fatal("a saved credential was offered to a different host")
	}
	if !IsHostMismatchError(err) {
		t.Fatalf("err = %v, want a HostMismatchError", err)
	}
}

func TestExplicitTokenAlwaysWins(t *testing.T) {
	// An explicit --token is unambiguous operator intent and goes to whatever --url names.
	got, err := credentialFor("https://b.example", profileFor("https://a.example"), "explicit-token")
	if err != nil {
		t.Fatalf("explicit token rejected: %v", err)
	}
	if got != "explicit-token" {
		t.Fatalf("credential = %q, want the explicit token", got)
	}
}

func TestURLLessProfileIsAdopted(t *testing.T) {
	// NOTE: a profile with no URL is adopted for the configured baseURL, never treated as a mismatch.
	p := legacyProfileWithoutURL()
	got, err := credentialFor("https://cfg.example", p, "")
	if err != nil {
		t.Fatalf("legacy profile rejected: %v", err)
	}
	if got == "" {
		t.Fatal("legacy profile yielded no credential")
	}
}

func TestLoadProfile_AdoptsAndRewritesLegacyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
	if err := saveSessionFile(path, &sessionFile{IssuedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}

	got, err := loadProfile(path, "https://cfg.example")
	if err != nil {
		t.Fatalf("loadProfile: %v", err)
	}
	if got.URL != "https://cfg.example" {
		t.Errorf("URL = %q, want the adopted config URL", got.URL)
	}

	sf, err := loadSessionFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := sf.Profiles["https://cfg.example"]; !ok {
		t.Fatal("adoption was not persisted to the session file")
	}
}

func TestSaveProfile_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")

	if err := saveProfile(path, profile{URL: "https://a.example", Device: "dev-1"}); err != nil {
		t.Fatalf("saveProfile: %v", err)
	}
	got, err := loadProfile(path, "https://a.example")
	if err != nil {
		t.Fatalf("loadProfile: %v", err)
	}
	if got.Device != "dev-1" {
		t.Errorf("Device = %q, want dev-1", got.Device)
	}
}
