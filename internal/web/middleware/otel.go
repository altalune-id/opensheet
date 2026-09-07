package middleware

import (
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// OTel wraps next in the otelhttp handler with span name "altalune.web".
func OTel(next http.Handler) http.Handler {
	return otelhttp.NewHandler(next, "altalune.web")
}
