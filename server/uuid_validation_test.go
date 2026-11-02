package main

import "testing"

// The MCP write tools validate every uuid they are handed. Rejecting the
// legacy hyphenated form made items imported in 2020 unaddressable even
// though Things Cloud still holds them under those ids.
func TestValidateUUIDAcceptsLegacyHyphenatedIDs(t *testing.T) {
	t.Parallel()

	valid := []string{
		"6Z4vn1NNerkFftMQYLX6Rp",               // Base58, as minted today
		"E1408FCA-A25C-4471-87F2-6F43B3E1D8DB", // legacy, uppercase
		"0A34678C-D341-4495-A613-635DF953AA2B", // legacy, contains 0 (not Base58)
		"34ea18ce-b88a-48ce-8bbc-bbeea3e22236", // legacy, lowercase
	}
	for _, id := range valid {
		if err := validateUUID("uuid", id); err != nil {
			t.Errorf("validateUUID(%q) = %v, want nil", id, err)
		}
	}

	invalid := []string{"", "not-a-uuid", "E1408FC-A25C-4471-87F2-6F43B3E1D8DB"}
	for _, id := range invalid {
		err := validateUUID("uuid", id)
		if err == nil {
			t.Errorf("validateUUID(%q) = nil, want an error", id)
			continue
		}
		if !isInvalidInput(err) {
			t.Errorf("validateUUID(%q) error must be an invalid-input error, got %v", id, err)
		}
	}
}

func TestValidateOptionalUUIDAcceptsLegacyHyphenatedIDs(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"", "none", "E1408FCA-A25C-4471-87F2-6F43B3E1D8DB"} {
		if err := validateOptionalUUID("project", id); err != nil {
			t.Errorf("validateOptionalUUID(%q) = %v, want nil", id, err)
		}
	}
}
