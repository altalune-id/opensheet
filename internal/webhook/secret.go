package webhook

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/platform/sealer"
)

const (
	secretPrefix  = "whsec_"
	secretBytes   = 32
	slotPrimary   = "primary"
	slotSecondary = "secondary"
)

func newSecret() (string, error) {
	b := make([]byte, secretBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("webhook.newSecret: %w", err)
	}
	return secretPrefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// SECURITY: the AAD binds a sealed secret to its endpoint and slot, so bytes copied elsewhere never open.
func secretAAD(endpointID uuid.UUID, slot string) []byte {
	return []byte("webhook:" + endpointID.String() + ":" + slot)
}

func sealSecret(sl sealer.Sealer, endpointID uuid.UUID, slot, secret string) ([]byte, error) {
	sealed, err := sl.Seal([]byte(secret), secretAAD(endpointID, slot))
	if err != nil {
		return nil, fmt.Errorf("webhook.sealSecret: %w", err)
	}
	return sealed, nil
}

func openSecret(sl sealer.Sealer, endpointID uuid.UUID, slot string, sealed []byte) (string, error) {
	plain, err := sl.Open(sealed, secretAAD(endpointID, slot))
	if err != nil {
		return "", fmt.Errorf("webhook.openSecret: %w", err)
	}
	return string(plain), nil
}
