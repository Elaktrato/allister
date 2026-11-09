// Package ids generates identifiers shared by several packages.
package ids

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// NewManualID returns a random UUIDv4 string, the source id of manual entries.
func NewManualID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("manual id: %w", err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:], nil
}
