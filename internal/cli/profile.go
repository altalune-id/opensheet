package cli

import (
	"errors"
	"fmt"
)

// SECURITY: profiles are keyed by URL because a device credential issued by one instance must never be offered to another.
type profile struct {
	URL     string `json:"url"`
	Org     string `json:"org,omitempty"`
	Project string `json:"project,omitempty"`
	Device  string `json:"device,omitempty"`
}

// HostMismatchError reports a saved profile's URL not matching the URL requested. SECURITY: a saved credential is bound to the host that issued it.
type HostMismatchError struct {
	Saved  string
	Wanted string
}

func (e *HostMismatchError) Error() string {
	return fmt.Sprintf("cli: saved credential for %q is not sent to %q", e.Saved, e.Wanted)
}

// IsHostMismatchError reports whether err is a HostMismatchError.
func IsHostMismatchError(err error) bool {
	var target *HostMismatchError
	return errors.As(err, &target)
}

// SECURITY: a saved credential is offered only when its URL matches, or when a pre-URL-keying profile is adopted.
func credentialFor(url string, p profile, explicitToken string) (string, error) {
	if explicitToken != "" {
		return explicitToken, nil
	}
	if p.URL != "" && p.URL != url {
		return "", &HostMismatchError{Saved: p.URL, Wanted: url}
	}
	return p.Device, nil
}

// NOTE: a legacy session file with no profiles map is adopted for url, never treated as a mismatch.
func loadProfile(path, url string) (profile, error) {
	sf, err := loadSessionFile(path)
	if err != nil {
		return profile{}, err
	}
	if sf == nil {
		return profile{}, nil
	}
	if p, ok := sf.Profiles[url]; ok {
		return p, nil
	}
	if len(sf.Profiles) > 0 {
		return profile{}, nil
	}
	adopted := profile{URL: url}
	sf.Profiles = map[string]profile{url: adopted}
	if err := saveSessionFile(path, sf); err != nil {
		return profile{}, err
	}
	return adopted, nil
}

func saveProfile(path string, p profile) error {
	sf, err := loadSessionFile(path)
	if err != nil {
		return err
	}
	if sf == nil {
		sf = &sessionFile{}
	}
	if sf.Profiles == nil {
		sf.Profiles = make(map[string]profile)
	}
	sf.Profiles[p.URL] = p
	return saveSessionFile(path, sf)
}
