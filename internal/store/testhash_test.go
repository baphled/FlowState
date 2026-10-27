package store

import (
	"crypto/sha256"
	"encoding/hex"
)

func sha256Sum(b []byte) []byte {
	h := sha256.Sum256(b)
	return h[:]
}

func hexEncode(b []byte) string { return hex.EncodeToString(b) }
