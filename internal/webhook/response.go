package webhook

import (
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	// MaxResponseBodyBytes bounds the receiver's response body kept per attempt.
	MaxResponseBodyBytes = 4 << 10
	// MaxResponseHeaderBytes bounds the total name and value bytes of the response headers kept per attempt.
	MaxResponseHeaderBytes = 4 << 10
	// MaxResponseHeaders bounds how many response headers are kept per attempt.
	MaxResponseHeaders = 50

	responseDrainLimit = 64 << 10
)

type receipt struct {
	status    int
	body      string
	truncated bool
	headers   []Header
}

func readReceipt(resp *http.Response) receipt {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBodyBytes+1))
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, responseDrainLimit))
	truncated := len(b) > MaxResponseBodyBytes
	if truncated {
		b = trimPartialRune(b[:MaxResponseBodyBytes])
	}
	return receipt{status: resp.StatusCode, body: string(b), truncated: truncated, headers: headerList(resp.Header)}
}

func headerList(h http.Header) []Header {
	out := []Header{}
	for _, name := range slices.Sorted(maps.Keys(h)) {
		if isCredentialHeader(name) {
			continue
		}
		for _, v := range h[name] {
			out = append(out, Header{Name: name, Value: v})
		}
	}
	return out
}

// BoundResponse cleans a's response body and headers to valid UTF-8 without NUL and applies the response caps; every Store runs it before writing.
func BoundResponse(a Attempt) Attempt {
	body, trimmed := storableText(a.ResponseBody, MaxResponseBodyBytes)
	a.ResponseBody = body
	a.ResponseTruncated = a.ResponseTruncated || trimmed
	a.ResponseHeaders = boundHeaders(a.ResponseHeaders)
	return a
}

// SECURITY: a response header whose lowercased name contains one of these may carry a credential and is never stored.
func credentialHeaderMarkers() []string {
	return []string{"auth", "cookie", "token", "secret", "key", "session", "password", "credential", "signature"}
}

func isCredentialHeader(name string) bool {
	lower := strings.ToLower(name)
	return slices.ContainsFunc(credentialHeaderMarkers(), func(m string) bool { return strings.Contains(lower, m) })
}

func boundHeaders(hs []Header) []Header {
	out := []Header{}
	size := 0
	for _, h := range hs {
		if len(out) >= MaxResponseHeaders {
			break
		}
		if isCredentialHeader(h.Name) {
			continue
		}
		name, _ := storableText(h.Name, MaxResponseHeaderBytes)
		value, _ := storableText(h.Value, MaxResponseHeaderBytes)
		if size+len(name)+len(value) > MaxResponseHeaderBytes {
			continue
		}
		size += len(name) + len(value)
		out = append(out, Header{Name: name, Value: value})
	}
	return out
}

func storableText(s string, limit int) (string, bool) {
	s = strings.ToValidUTF8(strings.ReplaceAll(s, "\x00", "�"), "�")
	if len(s) <= limit {
		return s, false
	}
	return string(trimPartialRune([]byte(s[:limit]))), true
}

func trimPartialRune(b []byte) []byte {
	for i := 1; i < utf8.UTFMax && i <= len(b); i++ {
		if !utf8.RuneStart(b[len(b)-i]) {
			continue
		}
		if utf8.FullRune(b[len(b)-i:]) {
			return b
		}
		return b[:len(b)-i]
	}
	return b
}
