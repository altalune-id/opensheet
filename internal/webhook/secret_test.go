package webhook_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/opensheet/internal/platform/sealer"
	"altalune.id/opensheet/internal/webhook"
)

func newSealer(t *testing.T) sealer.Sealer {
	t.Helper()
	key, err := sealer.GenerateKey()
	require.NoError(t, err)
	sl, err := sealer.New(key)
	require.NoError(t, err)
	return sl
}

func TestNewSecret_Format(t *testing.T) {
	a, err := webhook.NewSecret()
	require.NoError(t, err)
	b, err := webhook.NewSecret()
	require.NoError(t, err)

	assert.NotEqual(t, a, b)
	require.True(t, strings.HasPrefix(a, "whsec_"), "got %q", a)
	assert.Equal(t, "whsec_", webhook.SecretPrefix)
	body := strings.TrimPrefix(a, "whsec_")
	assert.Len(t, body, 43)
	raw, err := base64.RawURLEncoding.DecodeString(body)
	require.NoError(t, err)
	assert.Len(t, raw, 32)
}

func TestSealSecret_RoundTrip(t *testing.T) {
	sl := newSealer(t)
	id := uuid.New()

	sealed, err := webhook.SealSecret(sl, id, webhook.SlotPrimary, "whsec_abc")
	require.NoError(t, err)
	assert.NotContains(t, string(sealed), "whsec_abc")

	got, err := webhook.OpenSecret(sl, id, webhook.SlotPrimary, sealed)
	require.NoError(t, err)
	assert.Equal(t, "whsec_abc", got)
}

func TestOpenSecret_BoundToSlotAndEndpoint(t *testing.T) {
	sl := newSealer(t)
	id := uuid.New()
	sealed, err := webhook.SealSecret(sl, id, webhook.SlotPrimary, "whsec_abc")
	require.NoError(t, err)

	_, err = webhook.OpenSecret(sl, id, webhook.SlotSecondary, sealed)
	assert.True(t, sealer.IsOpenFailedError(err), "a primary opened as secondary must fail: %v", err)

	_, err = webhook.OpenSecret(sl, uuid.New(), webhook.SlotPrimary, sealed)
	assert.True(t, sealer.IsOpenFailedError(err), "bytes copied to another endpoint must fail: %v", err)
}

func TestSecretAAD_IsTheWireFormat(t *testing.T) {
	sl := newSealer(t)
	id := uuid.New()
	sealed, err := sl.Seal([]byte("whsec_abc"), []byte("webhook:"+id.String()+":secondary"))
	require.NoError(t, err)

	got, err := webhook.OpenSecret(sl, id, webhook.SlotSecondary, sealed)
	require.NoError(t, err)
	assert.Equal(t, "whsec_abc", got)
}
