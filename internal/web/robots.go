package web

import (
	"net/http"

	"altalune.id/template/internal/platform/config"
)

const defaultRobotsBody = "User-agent: *\nDisallow: /\n"

// RobotsHandler serves /robots.txt with cfg.HTTP.RobotsTxt, or the deny-all default.
func RobotsHandler(cfg *config.HTTPConfig) http.Handler {
	body := defaultRobotsBody
	if cfg != nil && cfg.RobotsTxt != "" {
		body = cfg.RobotsTxt
	}
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(body))
	})
}
