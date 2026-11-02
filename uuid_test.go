package thingscloud

import "testing"

func TestIsValidItemUUID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		id   string
		want bool
	}{
		// Base58, as Things mints today.
		{"base58", "6Z4vn1NNerkFftMQYLX6Rp", true},
		{"base58 short bound", "ABCDEFGHJKLMNPQRSTU", false},
		{"base58 min length", "ABCDEFGHJKLMNPQRSTUV", true},

		// Legacy hyphenated UUIDs. Things Cloud still holds these for items
		// imported around 2020 and the MCP tools must be able to address them.
		{"hyphenated uppercase", "E1408FCA-A25C-4471-87F2-6F43B3E1D8DB", true},
		{"hyphenated uppercase 2", "3A551656-32D9-45FB-98CE-05D467ED873C", true},
		// Contains 0 and O, which Base58 excludes.
		{"hyphenated with base58-illegal chars", "0A34678C-D341-4495-A613-635DF953AA2B", true},
		{"hyphenated lowercase", "34ea18ce-b88a-48ce-8bbc-bbeea3e22236", true},
		{"hyphenated mixed case", "CA562271-71f4-4D1F-910b-FB2FA146B1A8", true},

		// Malformed hyphenated forms stay invalid.
		{"wrong group lengths", "E1408FC-A25C-4471-87F2-6F43B3E1D8DB", false},
		{"too few groups", "E1408FCA-A25C-4471-6F43B3E1D8DB", false},
		{"non-hex", "G1408FCA-A25C-4471-87F2-6F43B3E1D8DB", false},
		{"no hyphens, hex-only, too long", "E1408FCAA25C447187F26F43B3E1D8DB2", false},

		{"empty", "", false},
		{"spaces", "E1408FCA A25C 4471 87F2 6F43B3E1D8DB", false},
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
	if IsHyphenatedUUID("6Z4vn1NNerkFftMQYLX6Rp") {
		t.Error("a Base58 UUID must not match the hyphenated form")
	}
}
