package thingscloud

import (
	"crypto/sha1"
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

// Legacy identifier migration
//
// Things Cloud moved from UUID-style identifiers to Base58 ones and wrote no
// mapping records into the history. The current identifier of a pre-migration
// object is derived by hashing the old one, and that derivation is the only
// link between the two generations:
//
//	Base58( SHA1(legacyID)[:16] )
//
// Legacy-kind history items (Task3, Task4, Area2, Tag3, ...) key objects by the
// old identifier; current-kind items (Task6, Task7, Area3, Tag4, ...) key the
// same object by the derived one. A replay that takes each item's identifier at
// face value therefore splits one object across two keys, and each half ends up
// holding an arbitrary subset of the truth — one with the title and an old
// status, the other with a later status and no title.
//
// Credit for the derivation goes to arthursoares/things-cloud-sdk.

// EncodeLegacyIdentifier derives the current Base58 identifier for an object
// created before Things Cloud's identifier migration.
//
// legacyID must be the identifier text exactly as the history stores it:
// hashing is byte-exact, so normalising it (lowercasing a UUID, trimming) gives
// a different and wrong identifier. The empty string is returned unchanged — a
// malformed item should keep an obviously bogus key rather than acquire a
// plausible-looking derived one. SHA-1 mirrors what Things itself does; it is
// not being used as a security primitive.
func EncodeLegacyIdentifier(legacyID string) string {
	if legacyID == "" {
		return ""
	}

	input := []byte(legacyID)
	if prefix, suffix, ok := splitLegacyRecurrenceIdentifier(legacyID); ok {
		// A repeating instance has a composite <uuid>-YYYYMMDD identifier and
		// hashes in two steps: the uuid prefix alone, then its raw digest
		// followed by the date text.
		prefixSum := sha1.Sum([]byte(prefix))
		input = make([]byte, 0, len(prefixSum)+len(suffix))
		input = append(input, prefixSum[:]...)
		input = append(input, suffix...)
	}

	sum := sha1.Sum(input)
	var u uuid.UUID
	copy(u[:], sum[:len(u)])
	return EncodeUUID(u)
}

// splitLegacyRecurrenceIdentifier recognises a repeating instance's composite
// <uuid>-YYYYMMDD identifier, returning the uuid prefix and the date suffix.
func splitLegacyRecurrenceIdentifier(id string) (string, []byte, bool) {
	const uuidLength = 36
	const dateSuffixLength = len("-YYYYMMDD")

	if len(id) != uuidLength+dateSuffixLength {
		return "", nil, false
	}
	prefix := id[:uuidLength]
	if _, err := uuid.Parse(prefix); err != nil || id[uuidLength] != '-' {
		return "", nil, false
	}
	for i := uuidLength + 1; i < len(id); i++ {
		if id[i] < '0' || id[i] > '9' {
			return "", nil, false
		}
	}
	return prefix, []byte(id[uuidLength:]), true
}

// IsLegacyIdentifier reports whether id predates Things Cloud's identifier
// migration, and so keys its object by a value that must be derived.
//
// The test is whether the identifier is expressible in Base58 at all. Every
// current identifier is, by construction; every pre-migration one observed in a
// real history is not, because all of them contain a hyphen:
//
//	14B75398-06A8-43C3-8E13-AF9D5148EA86            a task, area or tag
//	14B75398-06A8-43C3-8E13-AF9D5148EA86-20261102   a repeating instance
//	ACTIONGROUP-019730CD-41A7-4B89-89E1-A33C3C49AAB5 a heading
//	CC-Things-Tag-Errand                            a built-in tag
//
// Testing the character set rather than enumerating those shapes matters: the
// heading and built-in-tag forms were found only by noticing that references to
// them had stopped resolving, and there is no reason to assume the list is
// complete.
//
// It also deliberately does not ask whether the identifier is *canonical*
// Base58. A short, non-canonical one — which this server generated itself
// before the leading-zero bug was fixed — is current, not legacy, and deriving
// it would relocate an object that merely has a malformed key.
func IsLegacyIdentifier(id string) bool {
	if id == "" {
		return false
	}
	for i := 0; i < len(id); i++ {
		if !strings.ContainsRune(base58Alphabet, rune(id[i])) {
			return true
		}
	}
	return false
}
