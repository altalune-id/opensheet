package api

import (
	"fmt"
	"net/http"
)

const swaggerUIVersion = "5.17.14"

// docsHandler serves a self-contained Swagger UI page pointing at specURL. Assets load from unpkg.com; the spec URL is fetched same-origin so basic-auth credentials are reused across the same realm.
func docsHandler(specURL string) http.Handler {
	body := swaggerHTML(specURL)
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(body)
	})
}

func swaggerHTML(specURL string) []byte {
	const tmpl = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>altempl API docs</title>
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@%[1]s/swagger-ui.css">
  <link rel="icon" type="image/svg+xml" href="data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 16 16'><text y='14' font-size='14'>%%F0%%9F%%93%%98</text></svg>">
  <style>body{margin:0;background:#fafafa;}</style>
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@%[1]s/swagger-ui-bundle.js" crossorigin></script>
  <script src="https://unpkg.com/swagger-ui-dist@%[1]s/swagger-ui-standalone-preset.js" crossorigin></script>
  <script>
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
	return fmt.Appendf(nil, tmpl, swaggerUIVersion, specURL)
}
