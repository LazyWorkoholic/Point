package protocol

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// newID mints an envelope id. The backend accepts any string and mints its own
// when one is missing, so the only requirement is uniqueness within this
// process's lifetime — and a UUID dependency is not worth 16 random bytes.
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is not a condition this app can continue past
		// meaningfully, but it is also not worth a panic in the transport:
		// a degenerate id still correlates, it just stops being unique.
		return fmt.Sprintf("point-%p", &b)
	}
	return hex.EncodeToString(b[:])
}
