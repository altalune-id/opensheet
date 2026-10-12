package web

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// TestVendorMatchesPinnedDigest fails on any embedded vendored byte scripts/ui-vendor.sh did not verify.
func TestVendorMatchesPinnedDigest(t *testing.T) {
	pinned := map[string]string{
		"static/basecoat.css":    "8123677adb9bba43be3298e1543bcc5fc763e8cda3d32dc74c806046a3537ca0",
		"static/easymde.min.css": "6eee36340432776d682e7372ec4a7eb29be4fdb3ffada32c0bfa5e31a5ef34a2",
		"static/easymde.min.js":  "2c06bddfd0c89176db08ccf9d42e2beaa9b4f1a4ff8fb2ec0bf1bed25ce08e05",
		"static/htmx.min.js":     "e484d9171a9db30a39c8f16e3d709d4137f3211c659f8e6125816635033d593f",
		"static/hx-csp.min.js":   "279f659b9ec8658e3d47230cc545b0bb4e3efa4c2d780454d54db104a5257b7d",
	}
	for name, want := range pinned {
		b, err := staticFS.ReadFile(name)
		if err != nil {
			t.Errorf("read %s: %v — run `make ui-vendor`", name, err)
			continue
		}
		sum := sha256.Sum256(b)
		if got := hex.EncodeToString(sum[:]); got != want {
			t.Errorf("vendored %s sha256 = %s, want %s — re-run scripts/ui-vendor.sh and bump both pins together", name, got, want)
		}
	}
}

// TestCompiledCSSIsEmbedded fails when the committed Tailwind output is missing from the binary.
func TestCompiledCSSIsEmbedded(t *testing.T) {
	const minBytes = 5000
	b, err := staticFS.ReadFile("static/app.css")
	if err != nil {
		t.Fatalf("read static/app.css: %v — run `make ui-vendor` and commit the result", err)
	}
	if len(b) < minBytes {
		t.Errorf("static/app.css is %d bytes, want >= %d — the Tailwind compile produced an empty sheet", len(b), minBytes)
	}
}
