package session

import (
	"encoding/json"
	"fmt"

	"altalune.id/opensheet/internal/platform/sealer"
)

// SECURITY: Principal.IDToken is a live IdP assertion, so the payload is sealed and AAD-bound to the sid.
func sealPrincipal(s sealer.Sealer, sid string, p Principal) ([]byte, error) {
	plain, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("session: marshal principal: %w", err)
	}
	blob, err := s.Seal(plain, []byte(sid))
	if err != nil {
		return nil, fmt.Errorf("session: seal principal: %w", err)
	}
	return blob, nil
}

func openPrincipal(s sealer.Sealer, sid string, blob []byte) (Principal, error) {
	plain, err := s.Open(blob, []byte(sid))
	if err != nil {
		return Principal{}, fmt.Errorf("session: open principal: %w", err)
	}
	var p Principal
	if uErr := json.Unmarshal(plain, &p); uErr != nil {
		return Principal{}, fmt.Errorf("session: unmarshal principal: %w", uErr)
	}
	return p, nil
}
