package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

const sessionFileVersion = 1

type sessionFile struct {
	Version  int                `json:"version"`
	Profiles map[string]profile `json:"profiles"`
}

// NOTE: an empty URL marks a local sign-in, used only by in-process commands; a remote profile whose Token is nil has expired.
type profile struct {
	URL       string        `json:"url,omitempty"`
	ExpiresAt time.Time     `json:"expiresAt,omitzero"`
	Principal principalView `json:"principal"`
	Token     *profileToken `json:"token,omitempty"`
}

type profileToken struct {
	Issuer   string   `json:"issuer"`
	ClientID string   `json:"clientId"`
	Resource string   `json:"resource"`
	Access   string   `json:"access"`            // SECURITY: never printed
	Refresh  string   `json:"refresh,omitempty"` // SECURITY: never printed
	Scopes   []string `json:"scopes,omitempty"`
}

type principalView struct {
	Source          string   `json:"source,omitempty"`
	UserID          string   `json:"userId,omitempty"`
	Email           string   `json:"email,omitempty"`
	Name            string   `json:"name,omitempty"`
	ActiveOrgID     string   `json:"activeOrgId,omitempty"`
	ActiveOrgSlug   string   `json:"activeOrgSlug,omitempty"`
	ActiveProjectID string   `json:"activeProjectId,omitempty"`
	Scopes          []string `json:"scopes,omitempty"`
}

// NOTE: an older format loads as empty, so its people sign in again; a newer one is an error, so an older binary never wipes it.
func loadSessionFile(path string) (*sessionFile, error) {
	empty := &sessionFile{Version: sessionFileVersion, Profiles: map[string]profile{}}
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return nil, &SessionFileError{Path: path, Err: err}
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, &SessionFileError{Path: path, Err: fmt.Errorf("mode %o lets others read its tokens; run chmod 600 %s", info.Mode().Perm(), path)}
	}
	b, err := os.ReadFile(path) //nolint:gosec // operator-chosen path (session.path).
	if err != nil {
		return nil, &SessionFileError{Path: path, Err: err}
	}
	var f sessionFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, &SessionFileError{Path: path, Err: err}
	}
	if f.Version > sessionFileVersion {
		return nil, &SessionFileError{Path: path, Err: fmt.Errorf("format version %d is newer than this binary reads (%d); upgrade opensheet", f.Version, sessionFileVersion)}
	}
	if f.Version < sessionFileVersion {
		return empty, nil
	}
	if f.Profiles == nil {
		f.Profiles = map[string]profile{}
	}
	return &f, nil
}

// NOTE: written to a fresh 0600 temp file then renamed, so a crash never leaves a half-written file.
func saveSessionFile(path string, f *sessionFile) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return &SessionFileError{Path: path, Err: err}
	}
	f.Version = sessionFileVersion
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return &SessionFileError{Path: path, Err: err}
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return &SessionFileError{Path: path, Err: err}
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return &SessionFileError{Path: path, Err: err}
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return &SessionFileError{Path: path, Err: err}
	}
	if err := tmp.Close(); err != nil {
		return &SessionFileError{Path: path, Err: err}
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return &SessionFileError{Path: path, Err: err}
	}
	return nil
}

type profileStore struct{ path string }

func (s profileStore) load(name string) (*profile, error) {
	f, err := loadSessionFile(s.path)
	if err != nil {
		return nil, err
	}
	p, ok := f.Profiles[name]
	if !ok {
		return nil, nil
	}
	return &p, nil
}

// SECURITY: every write holds the session file lock, so concurrent processes never lose a rotated refresh token.
func (s profileStore) edit(ctx context.Context, fn func(profiles map[string]profile) (changed bool, err error)) error {
	unlock, err := lockSessionFile(ctx, s.path)
	if err != nil {
		return &SessionFileError{Path: s.path, Err: err}
	}
	defer unlock()
	f, err := loadSessionFile(s.path)
	if err != nil {
		return err
	}
	changed, fnErr := fn(f.Profiles)
	if !changed {
		return fnErr
	}
	if err := saveSessionFile(s.path, f); err != nil {
		return errors.Join(fnErr, err)
	}
	return fnErr
}

func (s profileStore) put(ctx context.Context, name string, p profile) error {
	return s.edit(ctx, func(profiles map[string]profile) (bool, error) {
		profiles[name] = p
		return true, nil
	})
}
