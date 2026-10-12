package boot

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

func TestWebhookHTTPClient_OtelDisabled(t *testing.T) {
	c := webhookHTTPClient()
	_, wrapped := c.Transport.(*otelhttp.Transport)
	require.False(t, wrapped, "webhook dispatch client must not wrap otelhttp: url.full would leak the endpoint URL, including any query-string token, into traces")
	require.Equal(t, webhookTimeout, c.Timeout, "the dispatch client must bound each delivery by webhookTimeout")
}
