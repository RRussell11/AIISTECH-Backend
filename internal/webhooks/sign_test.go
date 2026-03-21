package webhooks

import (
	"testing"
)

// TestSignatureHeader_Deterministic verifies that SignatureHeader produces a
// stable, deterministic output for fixed inputs. The expected value was
// computed independently:
//
//	HMAC-SHA256("mysecret", "1711051200.{\"id\":\"evt-1\"}")
//	= sha256=<hexDigest>
func TestSignatureHeader_Deterministic(t *testing.T) {
	const (
		secret    = "mysecret"
		timestamp = "1711051200"
	)
	body := []byte(`{"id":"evt-1"}`)

	got := SignatureHeader(secret, timestamp, body)

	// Pre-computed expected value; must not change (algorithm stability test).
	const want = "sha256=b66ee784c4719e4cc120d22a4d3c4a0ac954d76587842bc17b7302738fee382e"
	if got != want {
		t.Errorf("SignatureHeader() = %q, want %q", got, want)
	}
}

// TestSignatureHeader_DifferentTimestamp verifies that changing the timestamp
// produces a different signature (timestamp is part of the signed message).
func TestSignatureHeader_DifferentTimestamp(t *testing.T) {
	body := []byte(`{"id":"evt-1"}`)
	sig1 := SignatureHeader("secret", "1000000000", body)
	sig2 := SignatureHeader("secret", "1000000001", body)

	if sig1 == sig2 {
		t.Error("different timestamps must produce different signatures")
	}
}

// TestSignatureHeader_DifferentBody verifies that changing the body produces
// a different signature.
func TestSignatureHeader_DifferentBody(t *testing.T) {
	sig1 := SignatureHeader("secret", "1000000000", []byte(`{"id":"evt-1"}`))
	sig2 := SignatureHeader("secret", "1000000000", []byte(`{"id":"evt-2"}`))

	if sig1 == sig2 {
		t.Error("different bodies must produce different signatures")
	}
}

// TestSignatureHeader_Format verifies the header value has the expected
// "sha256=<64 hex chars>" format.
func TestSignatureHeader_Format(t *testing.T) {
	got := SignatureHeader("k", "ts", []byte("body"))

	if len(got) != len("sha256=")+64 {
		t.Errorf("SignatureHeader() len = %d, want %d; value = %q", len(got), len("sha256=")+64, got)
	}
	if got[:7] != "sha256=" {
		t.Errorf("SignatureHeader() prefix = %q, want %q", got[:7], "sha256=")
	}
}
