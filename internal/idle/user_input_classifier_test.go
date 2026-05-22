package idle

import "testing"

// TestInputIRQClassifierKeywords_NoUSBHostControllers is a regression
// guard for the "every USB activity refused the gate" bug.
// /sys/kernel/irq/<id>/actions lists the IRQ handler driver (the
// HCI driver) regardless of which USB devices are attached, so
// matching xhci_hcd / ehci_hcd / uhci_hcd / ohci_hcd refused on any
// USB activity — USB storage, audio DACs, UPS heartbeats, autosuspend
// wake-ups, dongles, etc. None of those are human input. Verified
// on a Plex/Jellyfin homelab where IRQ 131 (xhci_hcd, no HID
// device) refused every other opportunistic gate evaluation with
// `recent_input_irq:irq=131`.
//
// The classifier MUST NOT contain HCI driver names; per-device
// input drivers (i8042, hid, usbhid, kbd, mouse, synaptics, elan)
// are the right granularity.
func TestInputIRQClassifierKeywords_NoUSBHostControllers(t *testing.T) {
	banned := []string{"xhci_hcd", "ehci_hcd", "uhci_hcd", "ohci_hcd"}
	for _, b := range banned {
		for _, kw := range inputIRQClassifierKeywords {
			if kw == b {
				t.Errorf("inputIRQClassifierKeywords contains %q — USB host controllers "+
					"trigger on ALL USB activity, not just input, and cause the "+
					"opportunistic gate to refuse on background USB work "+
					"(reintroduces the proxmox `recent_input_irq:irq=131` bug)", b)
			}
		}
	}
}

// TestInputIRQClassifierKeywords_StillHasRealInputDrivers asserts the
// remaining classifier entries actually cover input — accidentally
// emptying the list would silently disable RULE-OPP-IDLE-02 input-
// activity refusal, which would let probes fire mid-keystroke.
func TestInputIRQClassifierKeywords_StillHasRealInputDrivers(t *testing.T) {
	required := []string{"i8042", "hid", "usbhid", "kbd", "mouse"}
	have := make(map[string]bool, len(inputIRQClassifierKeywords))
	for _, kw := range inputIRQClassifierKeywords {
		have[kw] = true
	}
	for _, r := range required {
		if !have[r] {
			t.Errorf("inputIRQClassifierKeywords missing %q — real input devices "+
				"would no longer refuse the gate", r)
		}
	}
}
