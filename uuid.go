package thingscloud

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/google/uuid"
)

// base58Alphabet is the Bitcoin/Flickr alphabet Things uses for the UUIDs it
// generates: no 0, O, I or l.
const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// IsBase58UUID reports whether id looks like a Base58 UUID of the kind Things
// generates for items created by recent versions of the app.
func IsBase58UUID(id string) bool {
	if len(id) < 20 || len(id) > 32 {
		return false
	}
	for i := 0; i < len(id); i++ {
		if !strings.ContainsRune(base58Alphabet, rune(id[i])) {
			return false
		}
	}
	return true
}

// IsHyphenatedUUID reports whether id is a canonical 8-4-4-4-12 hyphenated
// UUID, e.g. "A1B2C3D4-E5F6-4789-8ABC-DEF012345678".
//
// Things Cloud still holds identifiers in this older form for items imported
// around 2020, and continues to reference them in payloads, so they have to be
// accepted even though the app no longer mints them. Both cases are allowed:
// Things stores these uppercase, but they also appear lowercase in
// mail-to-inbox command payloads.
func IsHyphenatedUUID(id string) bool {
	groups := [...]int{8, 4, 4, 4, 12}
	parts := strings.Split(id, "-")
	if len(parts) != len(groups) {
		return false
	}
	for i, part := range parts {
		if len(part) != groups[i] {
			return false
		}
		for j := 0; j < len(part); j++ {
			if !isHexDigit(part[j]) {
				return false
			}
		}
	}
	return true
}

func isHexDigit(c byte) bool {
	switch {
	case c >= '0' && c <= '9',
		c >= 'a' && c <= 'f',
		c >= 'A' && c <= 'F':
		return true
	}
	return false
}

// IsValidItemUUID reports whether id is an identifier Things Cloud may hold
// for an item: either a Base58 UUID or a legacy hyphenated one.
//
// Validation has to accept both. Rejecting the hyphenated form made every
// item imported in 2020 unaddressable through the MCP tools even though the
// server accepts operations on it.
func IsValidItemUUID(id string) bool {
	return IsBase58UUID(id) || IsHyphenatedUUID(id)
}

// Canonical Base58 identifiers
//
// Things.app decodes identifiers with BSIdentifierFromBase58String and expects
// exactly 16 bytes back. A string that decodes to fewer bytes is not merely
// rejected — it is written into the sync history, where it corrupts that
// history and can crash the client.
//
// Base58 is a big-integer encoding, so leading zero *bytes* carry no numeric
// weight and vanish unless encoded explicitly. Each must be emitted as a
// leading '1'. An encoder that skips this produces a short identifier for
// roughly 1 UUID in 256 — the proportion with a leading zero byte — which is
// rare enough to pass casual testing and frequent enough to happen.

// EncodeUUID encodes a UUID as a canonical Base58 identifier, with one leading
// '1' per leading zero byte, matching what Things.app itself emits.
func EncodeUUID(u uuid.UUID) string {
	zeros := 0
	for zeros < len(u) && u[zeros] == 0 {
		zeros++
	}

	n := new(big.Int).SetBytes(u[:])
	base := big.NewInt(58)
	mod := new(big.Int)
	var encoded []byte
	for n.Sign() > 0 {
		n.DivMod(n, base, mod)
		encoded = append(encoded, base58Alphabet[mod.Int64()])
	}
	for i := 0; i < zeros; i++ {
		encoded = append(encoded, '1')
	}
	for i, j := 0, len(encoded)-1; i < j; i, j = i+1, j-1 {
		encoded[i], encoded[j] = encoded[j], encoded[i]
	}
	return string(encoded)
}

// NewUUID returns a fresh canonical Base58 identifier.
func NewUUID() string {
	return EncodeUUID(uuid.New())
}

// DecodeUUID decodes a canonical Base58 identifier back into a UUID, rejecting
// anything that is not an encoding of exactly 16 bytes.
func DecodeUUID(s string) (uuid.UUID, error) {
	var u uuid.UUID
	if s == "" {
		return u, fmt.Errorf("thingscloud: empty identifier")
	}

	n := big.NewInt(0)
	base := big.NewInt(58)
	zeros := 0
	for i := 0; i < len(s); i++ {
		idx := strings.IndexByte(base58Alphabet, s[i])
		if idx < 0 {
			return u, fmt.Errorf("thingscloud: %q is not Base58", s)
		}
		if s[i] == '1' && n.Sign() == 0 {
			zeros++
			continue
		}
		n.Mul(n, base)
		n.Add(n, big.NewInt(int64(idx)))
	}

	bs := n.Bytes()
	if zeros+len(bs) != len(u) {
		return u, fmt.Errorf("thingscloud: %q decodes to %d bytes, want %d", s, zeros+len(bs), len(u))
	}
	copy(u[zeros:], bs)
	return u, nil
}

// IsCanonicalBase58UUID reports whether id round-trips as a canonical Base58
// encoding of 16 bytes — the standard a *newly generated* identifier must meet.
//
// Deliberately stricter than IsValidItemUUID: identifiers Things Cloud already
// holds include legacy hyphenated ones that fail this test and must still be
// addressable. Only generation has to be canonical.
func IsCanonicalBase58UUID(id string) bool {
	u, err := DecodeUUID(id)
	if err != nil {
		return false
	}
	return EncodeUUID(u) == id
}
