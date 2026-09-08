package credential

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"altalune.id/opensheet/internal/platform/session"
	"altalune.id/opensheet/nanoid"
)

// StateMaxAge bounds how far a connect state's IssuedAt may sit from now, in either direction.
const StateMaxAge = 10 * time.Minute

const stateNonceLen = 24

// SECURITY: reasons are fixed phrases; nothing an attacker supplies is echoed back through them.
const (
	reasonSignature = "signature does not verify"
	reasonEncoding  = "payload is not base64url"
	reasonPayload   = "payload is not a connect state"
	reasonScope     = "payload names no org, project or user"
	reasonStale     = "issued outside the freshness window"
	reasonReturnTo  = "returnTo is not a rooted relative path"
	reasonNoCode    = "callback carried no authorization code"
)

type state struct {
	OrgID     uuid.UUID `json:"o"`
	ProjectID uuid.UUID `json:"p"`
	UserID    uuid.UUID `json:"u"`
	Nonce     string    `json:"n"`
	IssuedAt  time.Time `json:"i"`
	ReturnTo  string    `json:"r,omitzero"`
}

func newState(orgID, projectID, userID uuid.UUID, returnTo string, issuedAt time.Time) (state, error) {
	safe, err := safeReturnTo(returnTo)
	if err != nil {
		return state{}, err
	}
	nonce, err := nanoid.New(stateNonceLen)
	if err != nil {
		return state{}, fmt.Errorf("credential: connect state: nonce: %w", err)
	}
	return state{
		OrgID:     orgID,
		ProjectID: projectID,
		UserID:    userID,
		Nonce:     nonce,
		IssuedAt:  issuedAt.UTC(),
		ReturnTo:  safe,
	}, nil
}

// NOTE: session.Sign is this repo's HMAC-SHA256 signer over an opaque value; the name is session-flavoured but the primitive is exactly what OAuth state needs.
func encodeState(secret []byte, s state) (string, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return "", fmt.Errorf("credential: connect state: marshal: %w", err)
	}
	return session.Sign(secret, base64.RawURLEncoding.EncodeToString(raw)), nil
}

// SECURITY: session.Verify compares the HMAC with hmac.Equal, and it runs before any field of the payload is read.
func decodeState(secret []byte, raw string, now time.Time) (state, error) {
	value, err := session.Verify(secret, strings.TrimSpace(raw))
	if err != nil {
		return state{}, &StateInvalidError{Reason: reasonSignature}
	}
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return state{}, &StateInvalidError{Reason: reasonEncoding}
	}
	var s state
	if err := json.Unmarshal(payload, &s); err != nil {
		return state{}, &StateInvalidError{Reason: reasonPayload}
	}
	if s.OrgID == uuid.Nil || s.ProjectID == uuid.Nil || s.UserID == uuid.Nil || s.Nonce == "" {
		return state{}, &StateInvalidError{Reason: reasonScope}
	}
	if age := now.Sub(s.IssuedAt); age > StateMaxAge || age < -StateMaxAge {
		return state{}, &StateInvalidError{Reason: reasonStale}
	}
	if _, err := safeReturnTo(s.ReturnTo); err != nil {
		return state{}, err
	}
	return s, nil
}

// SECURITY: an absolute or protocol-relative returnTo would become a trusted open redirect the moment the callback honours it.
func safeReturnTo(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if !strings.HasPrefix(raw, "/") ||
		strings.HasPrefix(raw, "//") ||
		strings.ContainsAny(raw, "\\\r\n") {
		return "", &StateInvalidError{Reason: reasonReturnTo}
	}
	return raw, nil
}
