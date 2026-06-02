package web

import (
	"testing"

	"github.com/ventd/ventd/internal/doctor"
)

// detectorNames returns the Name() of every detector in the set.
func detectorNames(det []doctor.Detector) map[string]bool {
	out := make(map[string]bool, len(det))
	for _, d := range det {
		out[d.Name()] = true
	}
	return out
}

// TestDoctorDetectors_BaselineGating proves the wiring fix: the
// apparmor_profile_drift and dmi_fingerprint detectors are registered in the
// web doctor set ONLY when the daemon captured the matching startup baseline
// (SetDoctorBaselines). Without a baseline they stay absent (the prior state —
// fully-built detectors that were never constructed). Asserted by detector
// name so the test is deterministic regardless of the live /sys surfaces the
// detectors probe.
func TestDoctorDetectors_BaselineGating(t *testing.T) {
	t.Run("no baselines: both absent", func(t *testing.T) {
		srv, _, cancel := newHandlerHarness(t)
		defer cancel()
		names := detectorNames(srv.doctorDetectors())
		if names["apparmor_profile_drift"] {
			t.Error("apparmor_profile_drift wired without an AppArmor baseline")
		}
		if names["dmi_fingerprint"] {
			t.Error("dmi_fingerprint wired without a DMI baseline")
		}
		if names["kernel_update"] {
			t.Error("kernel_update wired without a kernel baseline")
		}
	})

	t.Run("baselines set: all present", func(t *testing.T) {
		srv, _, cancel := newHandlerHarness(t)
		defer cancel()
		srv.SetDoctorBaselines(DoctorBaselines{
			AppArmorMode: "enforce",
			HasDMI:       true,
			DMIMatched:   true,
			DMIBoardName: "asus-rog-strix-z790-e",
			LastKernel:   "6.8.0-49-generic",
		})
		names := detectorNames(srv.doctorDetectors())
		if !names["apparmor_profile_drift"] {
			t.Error("apparmor_profile_drift not wired despite an AppArmor baseline")
		}
		if !names["dmi_fingerprint"] {
			t.Error("dmi_fingerprint not wired despite a DMI baseline")
		}
		if !names["kernel_update"] {
			t.Error("kernel_update not wired despite a kernel baseline")
		}
	})

	t.Run("DMI seen but unmatched still wires the detector", func(t *testing.T) {
		// Board-not-in-catalog is a real Warning the detector must surface, so
		// HasDMI (not DMIMatched) is what gates wiring.
		srv, _, cancel := newHandlerHarness(t)
		defer cancel()
		srv.SetDoctorBaselines(DoctorBaselines{HasDMI: true, DMIMatched: false})
		if !detectorNames(srv.doctorDetectors())["dmi_fingerprint"] {
			t.Error("dmi_fingerprint must wire on HasDMI even when unmatched")
		}
	})
}
