package thingscloud

import "testing"

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
