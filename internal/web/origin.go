package web

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// Origin returns raw's scheme://host[:port], lowercased and without the scheme's default port, or "" when raw is not an absolute URL.
func Origin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	scheme, host, port := strings.ToLower(u.Scheme), strings.ToLower(u.Hostname()), u.Port()
	if scheme == "https" && port == "443" || scheme == "http" && port == "80" {
		port = ""
	}
	if port != "" {
		return scheme + "://" + net.JoinHostPort(host, port)
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return scheme + "://" + host
}

// SecureCookie reports whether a cookie set on r is Secure: when configured, or whenever r arrived over TLS.
func SecureCookie(configured bool, r *http.Request) bool { return configured || r.TLS != nil }
