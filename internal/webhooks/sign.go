package webhooks

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// SignatureHeader computes the HMAC-SHA256 webhook signature header value used
// to authenticate outbound webhook deliveries (ADR-012 signing scheme).
//
// The signed message is:  <timestamp>.<rawBody>
//
// The returned string is the full header value:  sha256=<hexDigest>
//
// Receivers should compare the header value against their own computation of
// HMAC-SHA256(secret, "<timestamp>.<rawBody>") using a constant-time comparison
// to prevent timing attacks.
//
// Parameters:
//   - secret: the per-subscription HMAC secret (UTF-8 encoded).
//   - timestamp: the delivery timestamp string included in the request
//     (e.g. Unix seconds as a decimal string).
//   - rawBody: the exact bytes of the request body.
func SignatureHeader(secret, timestamp string, rawBody []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%s.", timestamp)
	mac.Write(rawBody) //nolint:errcheck // hash.Hash.Write never returns an error
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
