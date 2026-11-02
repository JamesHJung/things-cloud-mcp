package thingscloud

import "strings"

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
// UUID, e.g. "E1408FCA-A25C-4471-87F2-6F43B3E1D8DB".
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
