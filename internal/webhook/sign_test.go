package webhook_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"altalune.id/opensheet/internal/webhook"
)

func TestSign_FixedVector(t *testing.T) {
	got := webhook.Sign("whsec_test", "1758153600", []byte(`{"a":1}`))
	assert.Equal(t, "v1=5d7a59cb9a5399806655f45f56c0139fbffdf766435435b0d7004e9d9ae115b1", got)
}

func TestSign_CoversTimestampAndBody(t *testing.T) {
	base := webhook.Sign("whsec_test", "1758153600", []byte(`{"a":1}`))
	assert.NotEqual(t, base, webhook.Sign("whsec_test", "1758153601", []byte(`{"a":1}`)))
	assert.NotEqual(t, base, webhook.Sign("whsec_test", "1758153600", []byte(`{"a":2}`)))
	assert.NotEqual(t, base, webhook.Sign("whsec_other", "1758153600", []byte(`{"a":1}`)))
}
