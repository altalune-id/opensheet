package webhook

import (
	"github.com/google/uuid"

	"altalune.id/opensheet/internal/platform/sealer"
)

const (
	SlotPrimary      = slotPrimary
	SlotSecondary    = slotSecondary
	SecretPrefix     = secretPrefix
	EventIDPrefix    = eventIDPrefix
	DeliveryIDPrefix = deliveryIDPrefix
)

func NewSecret() (string, error) { return newSecret() }

func SealSecret(sl sealer.Sealer, endpointID uuid.UUID, slot, secret string) ([]byte, error) {
	return sealSecret(sl, endpointID, slot, secret)
}

func OpenSecret(sl sealer.Sealer, endpointID uuid.UUID, slot string, sealed []byte) (string, error) {
	return openSecret(sl, endpointID, slot, sealed)
}

const ResponseDrainLimit = responseDrainLimit
