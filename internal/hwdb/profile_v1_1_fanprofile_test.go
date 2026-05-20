package hwdb

import "testing"

// TestLookupFanProfile_NilEntryReturnsFalse exercises the no-catalog
// path — smart_builders.go relies on this contract to short-circuit
// the resolveFanShape lookup without panicking when the matched
// board entry is nil. (#1283)
func TestLookupFanProfile_NilEntryReturnsFalse(t *testing.T) {
	if _, ok := LookupFanProfile(nil, "pwm1"); ok {
		t.Error("nil entry should return ok=false")
	}
}

// TestLookupFanProfile_CaseInsensitiveMatch verifies the channel
// match is case-insensitive (operators sometimes write "PWM1" in
// YAML; tools canonicalise to lowercase) and pins exact-match shape.
// (#1283)
func TestLookupFanProfile_CaseInsensitiveMatch(t *testing.T) {
	entry := &BoardCatalogEntry{
		FanProfiles: []FanProfile{
			{Channel: "pwm1", Class: "case_120_140", DiameterMM: 140, DefaultBladeCount: 7},
			{Channel: "pwm2", Class: "aio_pump", DiameterMM: 50, DefaultBladeCount: 11},
		},
	}
	tests := []struct {
		name    string
		ch      string
		wantOK  bool
		wantDia int
	}{
		{"exact_lower", "pwm1", true, 140},
		{"exact_upper", "PWM1", true, 140},
		{"second_entry", "pwm2", true, 50},
		{"miss", "pwm3", false, 0},
		{"empty_returns_false", "", false, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fp, ok := LookupFanProfile(entry, tc.ch)
			if ok != tc.wantOK {
				t.Errorf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && fp.DiameterMM != tc.wantDia {
				t.Errorf("DiameterMM = %d, want %d", fp.DiameterMM, tc.wantDia)
			}
		})
	}
}

// TestLookupFanProfile_NoEntriesReturnsFalse pins the empty-catalog
// path — a board catalog entry whose FanProfiles slice hasn't been
// populated yet (most rows pre-#1283) must return ok=false rather
// than match a zero-value profile. (#1283)
func TestLookupFanProfile_NoEntriesReturnsFalse(t *testing.T) {
	entry := &BoardCatalogEntry{}
	if _, ok := LookupFanProfile(entry, "pwm1"); ok {
		t.Error("empty FanProfiles slice should return ok=false")
	}
}
