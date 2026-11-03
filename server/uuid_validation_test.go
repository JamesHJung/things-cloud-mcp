package main

import "testing"

// The MCP write tools validate every uuid they are handed. Rejecting the
// legacy hyphenated form made items imported in 2020 unaddressable even
// though Things Cloud still holds them under those ids.
func TestValidateUUIDAcceptsLegacyHyphenatedIDs(t *testing.T) {
	t.Parallel()

	valid := []string{
		"3KpQ7mWnXbTd2vZyRf9Jah",               // Base58, as minted today
		"A1B2C3D4-E5F6-4789-8ABC-DEF012345678", // legacy, uppercase
		"0123ABCD-4E5F-4A6B-8C7D-9E0F1A2B3C4D", // legacy, contains 0 (not Base58)
		"a1b2c3d4-e5f6-4789-8abc-def012345678", // legacy, lowercase
	}
	for _, id := range valid {
		if err := validateUUID("uuid", id); err != nil {
			t.Errorf("validateUUID(%q) = %v, want nil", id, err)
		}
	}

	invalid := []string{"", "not-a-uuid", "A1B2C3D-E5F6-4789-8ABC-DEF012345678"}
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

	for _, id := range []string{"", "none", "A1B2C3D4-E5F6-4789-8ABC-DEF012345678"} {
		if err := validateOptionalUUID("project", id); err != nil {
			t.Errorf("validateOptionalUUID(%q) = %v, want nil", id, err)
		}
	}
}
