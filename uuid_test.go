package thingscloud

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Fixtures are synthetic: these tests check the *shape* of an identifier, not
// any particular item.
func TestIsValidItemUUID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		id   string
		want bool
	}{
		// Base58, as Things mints today.
		{"base58", "3KpQ7mWnXbTd2vZyRf9Jah", true},
		{"base58 below min length", "ABCDEFGHJKLMNPQRSTU", false},
		{"base58 at min length", "ABCDEFGHJKLMNPQRSTUV", true},
		{"base58 above max length", "3KpQ7mWnXbTd2vZyRf9Jah3KpQ7mWnXbT", false},

		// Legacy hyphenated UUIDs. Things Cloud still holds these for items
		// imported around 2020 and the MCP tools must be able to address them.
		{"hyphenated uppercase", "A1B2C3D4-E5F6-4789-8ABC-DEF012345678", true},
		// Contains 0, which the Base58 alphabet excludes.
		{"hyphenated with base58-illegal chars", "0123ABCD-4E5F-4A6B-8C7D-9E0F1A2B3C4D", true},
		{"hyphenated lowercase", "a1b2c3d4-e5f6-4789-8abc-def012345678", true},
		{"hyphenated mixed case", "A1b2C3d4-E5f6-4789-8aBc-DeF012345678", true},

		// Malformed hyphenated forms stay invalid.
		{"wrong group lengths", "A1B2C3D-E5F6-4789-8ABC-DEF012345678", false},
		{"too few groups", "A1B2C3D4-E5F6-4789-DEF012345678", false},
		{"too many groups", "A1B2C3D4-E5F6-4789-8ABC-DEF0-12345678", false},
		{"non-hex digit", "G1B2C3D4-E5F6-4789-8ABC-DEF012345678", false},

		{"empty", "", false},
		{"spaces instead of hyphens", "A1B2C3D4 E5F6 4789 8ABC DEF012345678", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsValidItemUUID(tc.id); got != tc.want {
				t.Errorf("IsValidItemUUID(%q) = %v, want %v", tc.id, got, tc.want)
			}
		})
	}
}

func TestIsHyphenatedUUIDRejectsBase58(t *testing.T) {
	t.Parallel()
	if IsHyphenatedUUID("3KpQ7mWnXbTd2vZyRf9Jah") {
		t.Error("a Base58 UUID must not match the hyphenated form")
	}
}

func TestIsBase58UUIDRejectsHyphenated(t *testing.T) {
	t.Parallel()
	if IsBase58UUID("A1B2C3D4-E5F6-4789-8ABC-DEF012345678") {
		t.Error("a hyphenated UUID must not match the Base58 form")
	}
}

// Base58 is a big-integer encoding, so a leading zero byte has no numeric
// weight and disappears unless encoded as a leading '1'. An encoder that drops
// it emits a short identifier that Things cannot decode to 16 bytes, which
// corrupts the sync history. It happens for about 1 UUID in 256.
func TestEncodeUUIDIsCanonicalForLeadingZeros(t *testing.T) {
	t.Parallel()

	mk := func(zeros int) uuid.UUID {
		var u uuid.UUID
		for i := range u {
			u[i] = byte(i + 1)
		}
		for i := 0; i < zeros; i++ {
			u[i] = 0
		}
		return u
	}

	for _, zeros := range []int{0, 1, 2, 5, 15, 16} {
		u := mk(zeros)
		enc := EncodeUUID(u)

		// Count the leading '1's directly; an all-zero UUID encodes as
		// nothing but '1's, so IndexFunc returns -1 and the count is len(enc).
		ones := strings.IndexFunc(enc, func(r rune) bool { return r != '1' })
		if ones < 0 {
			ones = len(enc)
		}
		if ones != zeros {
			t.Errorf("%d leading zero byte(s): encoding %q has %d leading '1's", zeros, enc, ones)
		}

		back, err := DecodeUUID(enc)
		if err != nil {
			t.Errorf("%d leading zero byte(s): %q does not decode: %v", zeros, enc, err)
			continue
		}
		if back != u {
			t.Errorf("%d leading zero byte(s): round-trip gave %v, want %v", zeros, back, u)
		}
		if !IsCanonicalBase58UUID(enc) {
			t.Errorf("%d leading zero byte(s): %q is not canonical", zeros, enc)
		}
	}
}

func TestDecodeUUIDRejectsShortEncodings(t *testing.T) {
	t.Parallel()

	// What the old encoder produced for a UUID with one leading zero byte:
	// the same digits, without the leading '1'.
	var u uuid.UUID
	for i := range u {
		u[i] = byte(i + 1)
	}
	u[0] = 0
	canonical := EncodeUUID(u)
	short := strings.TrimPrefix(canonical, "1")

	if short == canonical {
		t.Fatal("fixture did not have a leading 1")
	}
	if _, err := DecodeUUID(short); err == nil {
		t.Errorf("DecodeUUID(%q) succeeded; a 15-byte decode must be rejected", short)
	}
	if IsCanonicalBase58UUID(short) {
		t.Errorf("%q reported canonical", short)
	}

	for _, bad := range []string{"", "0OIl", "not base58!"} {
		if _, err := DecodeUUID(bad); err == nil {
			t.Errorf("DecodeUUID(%q) succeeded", bad)
		}
	}
}

func TestNewUUIDIsAlwaysCanonical(t *testing.T) {
	t.Parallel()
	for i := 0; i < 2000; i++ {
		id := NewUUID()
		if !IsCanonicalBase58UUID(id) {
			t.Fatalf("NewUUID produced a non-canonical identifier: %q", id)
		}
		if !IsValidItemUUID(id) {
			t.Fatalf("NewUUID produced an identifier the validator rejects: %q", id)
		}
	}
}
