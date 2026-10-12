package controlplane

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
)

const swaggerUIVersion = "5.17.14"

// NOTE: this page carries its own CSP, overriding the app policy, because its assets and inline bootstrap are third-party and would otherwise be blocked.
func docsHandler(specURL string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nonce := docsNonce()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Security-Policy", docsCSP(nonce))
		w.Header().Del("Content-Security-Policy-Report-Only")
		_, _ = w.Write(swaggerHTML(specURL, nonce))
	})
}

func docsCSP(nonce string) string {
	return "default-src 'self'; " +
		"script-src 'self' 'nonce-" + nonce + "' https://unpkg.com; " +
		"style-src 'self' 'unsafe-inline' https://unpkg.com; " +
		"img-src 'self' data:; font-src 'self' data:; connect-src 'self'; " +
		"object-src 'none'; base-uri 'self'; frame-ancestors 'none'"
}

func docsNonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawStdEncoding.EncodeToString(b)
}

func swaggerHTML(specURL, nonce string) []byte {
	const tmpl = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>opensheet API docs</title>
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@%[1]s/swagger-ui.css">
  <link rel="icon" type="image/svg+xml" href="data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 16 16'><text y='14' font-size='14'>%%F0%%9F%%93%%98</text></svg>">
  <style>body{margin:0;background:#fafafa;}</style>
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@%[1]s/swagger-ui-bundle.js" nonce="%[3]s" crossorigin></script>
  <script src="https://unpkg.com/swagger-ui-dist@%[1]s/swagger-ui-standalone-preset.js" nonce="%[3]s" crossorigin></script>
  <script nonce="%[3]s">
    window.addEventListener('load', function () {
      window.ui = SwaggerUIBundle({
        url: %[2]q,
        dom_id: '#swagger-ui',
        deepLinking: true,
        presets: [SwaggerUIBundle.presets.apis, SwaggerUIStandalonePreset],
        plugins: [SwaggerUIBundle.plugins.DownloadUrl],
        layout: 'StandaloneLayout',
        docExpansion: 'list',
        defaultModelsExpandDepth: 0,
        withCredentials: true
      });
    });
  </script>
</body>
</html>
`
	return fmt.Appendf(nil, tmpl, swaggerUIVersion, specURL, nonce)
}
