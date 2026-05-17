package conflicts

import "testing"

func TestRegistry_AllEntriesHaveNameAndReason(t *testing.T) {
	seen := make(map[string]struct{}, len(Registry))
	for i, e := range Registry {
		if e.Name == "" {
			t.Errorf("Registry[%d] has empty Name", i)
		}
		if e.ConflictReason == "" {
			t.Errorf("Registry[%d] (%s) has empty ConflictReason", i, e.Name)
		}
		if e.Intrusiveness < IntrusivenessLow || e.Intrusiveness > IntrusivenessHigh {
			t.Errorf("Registry[%d] (%s) has invalid Intrusiveness=%d", i, e.Name, e.Intrusiveness)
		}
		if _, dup := seen[e.Name]; dup {
			t.Errorf("Registry has duplicate Name %q", e.Name)
		}
		seen[e.Name] = struct{}{}
	}
}

func TestRegistry_EveryEntryHasAtLeastOneSignal(t *testing.T) {
	// An entry with no Units / ProcPatterns / ConfigPaths / ModprobeDropIns
	// is unreachable by the detector — wasted catalog space + a likely
	// editor error. Each entry must declare at least one signal.
	for _, e := range Registry {
		if len(e.Units) == 0 &&
			len(e.ProcPatterns) == 0 &&
			len(e.ConfigPaths) == 0 &&
			len(e.ModprobeDropIns) == 0 {
			t.Errorf("Registry entry %q has no detection signals (Units/ProcPatterns/ConfigPaths/ModprobeDropIns all empty)", e.Name)
		}
	}
}

func TestRegistry_VendorDaemonsWithUnitsHaveHighIntrusiveness(t *testing.T) {
	// Vendor daemons that run as a process (have Units or
	// ProcPatterns) own adjacent functionality (kbd backlight, GPU
	// switching, charge thresholds) so stopping them is always
	// high-cost. The headless auto-stop flag must NOT stop them
	// without explicit consent.
	//
	// Vendor-flagged entries that are purely modprobe drop-ins or
	// config-path markers (no running process) can be Medium —
	// "this hardware ships with a vendor-specific helper but
	// stopping it doesn't break anything that isn't already broken."
	for _, e := range Registry {
		if !e.Vendor {
			continue
		}
		if len(e.Units) == 0 && len(e.ProcPatterns) == 0 {
			continue
		}
		if e.Intrusiveness != IntrusivenessHigh {
			t.Errorf("vendor daemon entry %q has Intrusiveness=%d; vendor daemons with Units/ProcPatterns must be IntrusivenessHigh",
				e.Name, e.Intrusiveness)
		}
	}
}

func TestRegistry_CoreCompetitorsPresent(t *testing.T) {
	// Smoke test that the catalogue covers the seven daemons named in
	// the rework brief. Adding more entries shouldn't break this; removing
	// one should fail loudly.
	required := []string{
		"fancontrol", "thinkfan", "coolercontrol", "nbfc",
		"liquidctl", "i8kutils",
		"asusd", "system76-power",
	}
	have := make(map[string]struct{}, len(Registry))
	for _, e := range Registry {
		have[e.Name] = struct{}{}
	}
	for _, r := range required {
		if _, ok := have[r]; !ok {
			t.Errorf("required catalogue entry %q is missing from Registry", r)
		}
	}
}
