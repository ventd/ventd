package hwdb

import (
	"testing"
)

// TestAnonymise_ForcesAnonymous verifies that Anonymise sets contributed_by to
// "anonymous" and verified to false unconditionally.
func TestAnonymise_ForcesAnonymous(t *testing.T) {
	t.Parallel()
	p := minimalProfile()
	p.ContributedBy = "some-user"
	p.Verified = true

	if err := Anonymise(p); err != nil {
		t.Fatalf("Anonymise: %v", err)
	}
	if p.ContributedBy != "anonymous" {
		t.Errorf("contributed_by = %q, want %q", p.ContributedBy, "anonymous")
	}
	if p.Verified {
		t.Error("verified should be false after Anonymise")
	}
}

// TestAnonymise_ClearsFanLabel verifies that Anonymise clears fan labels,
// preventing any user-set PII from appearing in the output.
func TestAnonymise_ClearsFanLabel(t *testing.T) {
	t.Parallel()
	p := minimalProfile()
	p.Hardware.Fans = []FanMeta{
		{ID: 1, Label: "my-custom-pc-hostname CPU Fan"},
	}

	if err := Anonymise(p); err != nil {
		t.Fatalf("Anonymise: %v", err)
	}
	for _, fan := range p.Hardware.Fans {
		if fan.Label != "" {
			t.Errorf("fan label = %q, want empty string after Anonymise", fan.Label)
		}
	}
}

// TestAnonymise_ClearsSensorTrustReason verifies that Anonymise clears sensor
// trust reasons, preventing any user-set text from appearing in the output.
func TestAnonymise_ClearsSensorTrustReason(t *testing.T) {
	t.Parallel()
	p := minimalProfile()
	p.SensorTrust = []SensorTrust{
		{Sensor: "coretemp", Trust: "high", Reason: "validated at /home/testuser/myconfig"},
	}

	if err := Anonymise(p); err != nil {
		t.Fatalf("Anonymise: %v", err)
	}
	for _, st := range p.SensorTrust {
		if st.Reason != "" {
			t.Errorf("sensor trust reason = %q, want empty string after Anonymise", st.Reason)
		}
	}
}

// TestAnonymise_PreservesVendorFields verifies that vendor-attributed
// identification fields (board names, pwm_control module) survive Anonymise.
func TestAnonymise_PreservesVendorFields(t *testing.T) {
	t.Parallel()
	p := minimalProfile()
	p.Fingerprint.DMISysVendor = "ASUSTeK COMPUTER INC."
	p.Fingerprint.DMIProductName = "PRIME Z690-A"
	p.Hardware.PWMControl = "nct6775"

	if err := Anonymise(p); err != nil {
		t.Fatalf("Anonymise: %v", err)
	}
	if p.Fingerprint.DMISysVendor == "" {
		t.Error("DMISysVendor was cleared; vendor-attributed fields must survive Anonymise")
	}
	if p.Hardware.PWMControl != "nct6775" {
		t.Errorf("hardware.pwm_control changed from nct6775 to %q", p.Hardware.PWMControl)
	}
}

// TestAnonymise_StrictDecodePassesForValidProfile verifies that the strict
// YAML round-trip inside Anonymise succeeds for a well-formed profile, and
// that the result is valid under the full schema pipeline.
// RULE-HWDB-CAPTURE-03.
func TestAnonymise_StrictDecodePassesForValidProfile(t *testing.T) {
	t.Parallel()
	p := minimalProfile()
	if err := Anonymise(p); err != nil {
		t.Fatalf("Anonymise on valid profile: %v", err)
	}
	if err := validateSingle(p); err != nil {
		t.Errorf("validateSingle after Anonymise: %v", err)
	}
}

// TestAnonymise_MultipleFansAllCleared verifies that all fan labels are cleared
// regardless of how many fans are present.
func TestAnonymise_MultipleFansAllCleared(t *testing.T) {
	t.Parallel()
	p := minimalProfile()
	p.Hardware.FanCount = 3
	p.Hardware.Fans = []FanMeta{
		{ID: 1, Label: "phoenix-pc CPU Fan"},
		{ID: 2, Label: "192.168.1.1 system fan"},
		{ID: 3, Label: "AA:BB:CC:DD:EE:FF exhaust"},
	}

	if err := Anonymise(p); err != nil {
		t.Fatalf("Anonymise: %v", err)
	}
	for i, fan := range p.Hardware.Fans {
		if fan.Label != "" {
			t.Errorf("fan[%d].label = %q, want empty after Anonymise", i, fan.Label)
		}
	}
}

// --- helpers ---

func minimalProfile() *Profile {
	return &Profile{
		ID:            "test-anon-board",
		SchemaVersion: 1,
		Fingerprint:   BoardFingerprint{DMIBoardVendor: "Test Vendor"},
		Hardware:      Hardware{PWMControl: "nct6775", FanCount: 1},
		ContributedBy: "anonymous",
		CapturedAt:    "2026-04-26",
		Verified:      false,
	}
}
