package mcp

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// DefaultChallengePrefix is the path the authorization server fetches to prove host control.
const DefaultChallengePrefix = "/.well-known/authalune-challenge/"

const (
	challengeCacheControl = "no-store"
	challengeMaxLen       = 512
)

// ChallengeTokenInvalidError reports a host-control token that cannot be one literal path segment.
type ChallengeTokenInvalidError struct {
	Token  string
	Reason string
}

func (e *ChallengeTokenInvalidError) Error() string {
	return "mcp: challenge token " + strconv.Quote(e.Token) + " is " + e.Reason
}

// IsChallengeTokenInvalidError reports whether err is a *ChallengeTokenInvalidError.
func IsChallengeTokenInvalidError(err error) bool {
	var target *ChallengeTokenInvalidError
	return errors.As(err, &target)
}

// ChallengePrefixInvalidError reports a challenge prefix that cannot be a literal ServeMux pattern.
type ChallengePrefixInvalidError struct {
	Prefix string
	Reason string
}

func (e *ChallengePrefixInvalidError) Error() string {
	return "mcp: challenge prefix " + strconv.Quote(e.Prefix) + " is " + e.Reason
}

// IsChallengePrefixInvalidError reports whether err is a *ChallengePrefixInvalidError.
func IsChallengePrefixInvalidError(err error) bool {
	var target *ChallengePrefixInvalidError
	return errors.As(err, &target)
}

// ChallengeRoutes returns the outer-mux routes proving host control, or nil when no token is set. SECURITY: the token is fixed in the registered pattern and echoed from configuration, never read back from the request path.
func ChallengeRoutes(basePath, prefix, token string) (map[string]http.Handler, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, nil
	}
	if prefix == "" {
		prefix = DefaultChallengePrefix
	}
	if err := validateChallengePrefix(prefix); err != nil {
		return nil, err
	}
	if err := validateChallengeToken(token); err != nil {
		return nil, err
	}

	body := []byte(token)
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", challengeCacheControl)
		_, _ = w.Write(body)
	})

	routes := map[string]http.Handler{"GET " + prefix + token: h}
	// NOTE: the proof is about host control, so the fetcher uses the domain root; the basePath
	// variant answers the same body for a deployment whose proxy forwards only the prefix.
	if p := strings.TrimRight(basePath, "/"); p != "" {
		routes["GET "+p+prefix+token] = h
	}
	return routes, nil
}

func validateChallengeToken(token string) error {
	if len(token) > challengeMaxLen {
		return &ChallengeTokenInvalidError{Token: token, Reason: "longer than " + strconv.Itoa(challengeMaxLen) + " bytes"}
	}
	if strings.ContainsFunc(token, unservableRune) || strings.Contains(token, "/") {
		return &ChallengeTokenInvalidError{Token: token, Reason: "not one literal path segment"}
	}
	return nil
}

func validateChallengePrefix(prefix string) error {
	if len(prefix) > challengeMaxLen {
		return &ChallengePrefixInvalidError{Prefix: prefix, Reason: "longer than " + strconv.Itoa(challengeMaxLen) + " bytes"}
	}
	if !strings.HasPrefix(prefix, "/") || !strings.HasSuffix(prefix, "/") {
		return &ChallengePrefixInvalidError{Prefix: prefix, Reason: "not a rooted path ending in a slash"}
	}
	if strings.ContainsFunc(prefix, unservableRune) {
		return &ChallengePrefixInvalidError{Prefix: prefix, Reason: "not a literal path"}
	}
	return nil
}

func unservableRune(r rune) bool {
	return r == '%' || r == '{' || r == '}' || r == '?' || r == '#' || r <= ' ' || r > '~'
}
