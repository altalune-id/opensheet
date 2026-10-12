package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

const signatureScheme = "v1="

// Sign returns the R10 signature of body sent at timestamp, keyed by the full secret string.
func Sign(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return signatureScheme + hex.EncodeToString(mac.Sum(nil))
}
