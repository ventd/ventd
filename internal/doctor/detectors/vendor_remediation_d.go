// SPDX-License-Identifier: GPL-3.0-or-later
package detectors

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/hwdb"
	"github.com/ventd/ventd/internal/recovery"
)

// VendorRemediationDetector recognises laptop / desktop families
// whose fan-control surface ventd can identify but cannot drive
// directly today (either no Linux mainline kernel path exists, or
// the existing path requires an out-of-tree kmod / userspace tool).
// One Info Fact is emitted per matched vendor family with a
// vendor-specific remediation pointer.
//
// Covers (as of v0.6.x):
//   - **Intel Mac** (`mbpfan` userspace; deprecated since Apple
//     Silicon)
//   - **Clevo / System76 / Tongfang** (proprietary EC; ventd's
//     `internal/hal/clevo` backend will cover most models once
//     T2.4 ships in a future cycle)
//   - **NZXT Kraken / Smart Device** (liquidctl userspace; ventd's
//     liquidtux-backed hwmon backend already drives these when the
//     liquidtux kernel module is loaded)
//
// HP Omen / Victus is intentionally NOT covered here — it has its
// own detector (`hp_omen_d.go`) with richer actionable detail
// (omen-fan + omen-fan-control patchset URLs). Keeping the two
// separate lets the doctor output surface omen-specific remediation
// without buried in a longer combined card.
//
// Severity is always OK (informational) — the hardware works
// per its firmware curve; ventd is not in a degraded state. The
// detector exists to bridge the operator's mental gap between
// "ventd didn't find a writable PWM" and "here is the upstream
// path your hardware uses".
type VendorRemediationDetector struct {
	// ReadDMIFn returns the live DMI tuple. Defaults to liveReadDMI
	// when nil; tests inject a stub.
	ReadDMIFn func() (hwdb.DMI, error)

	// ReadUSBVendorsFn returns the set of USB vendor IDs visible on
	// the host (lowercased hex, e.g. "1e71" for NZXT). Defaults to
	// scanUSBVendors when nil; tests inject a stub.
	ReadUSBVendorsFn func() map[string]struct{}
}

// NewVendorRemediationDetector constructs a detector with the
// production DMI + USB readers wired.
func NewVendorRemediationDetector() *VendorRemediationDetector {
	return &VendorRemediationDetector{
		ReadDMIFn:        liveReadDMI,
		ReadUSBVendorsFn: scanUSBVendors,
	}
}

// Name returns the stable detector ID.
func (d *VendorRemediationDetector) Name() string { return "vendor_remediation" }

// Probe inspects the live DMI + USB vendor set and emits one Fact
// per recognised family. Silent on hosts that don't match any
// entry. Graceful-degrade on DMI / USB read failure (no facts
// rather than a Warning — the system-wide DMI-fingerprint detector
// already covers that signal).
func (d *VendorRemediationDetector) Probe(ctx context.Context, deps doctor.Deps) ([]doctor.Fact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	read := d.ReadDMIFn
	if read == nil {
		read = liveReadDMI
	}
	readUSB := d.ReadUSBVendorsFn
	if readUSB == nil {
		readUSB = scanUSBVendors
	}
	dmi, _ := read()
	usbVendors := readUSB()

	now := timeNowFromDeps(deps)
	var facts []doctor.Fact

	if isAppleIntelMac(dmi) {
		facts = append(facts, doctor.Fact{
			Detector: d.Name(),
			Severity: doctor.SeverityOK,
			Class:    recovery.ClassUnknown,
			Title:    "Intel-era Apple Mac detected: mainline applesmc handles tach + temp; mbpfan is the canonical fan-control tool",
			Detail: fmt.Sprintf(
				"DMI matched an Intel-era Apple Mac (product=%q). The mainline `applesmc` "+
					"kernel module exposes hwmon temperatures and fan tach via "+
					"/sys/class/hwmon/*/name=applesmc, but does NOT accept duty-cycle writes — "+
					"Apple's SMC speaks RPM-target on `fan*_min` / `fan*_target` files instead. "+
					"The canonical userspace controller is `mbpfan` "+
					"(https://github.com/linux-on-mac/mbpfan), which writes RPM setpoints based "+
					"on a linear temp-to-RPM curve. ventd's hwmon backend can drive the same "+
					"`fan*_target` files via its RPM-target write path when the channel is "+
					"declared with `is_pump: true` semantics; an `apple-mac.yaml` board profile "+
					"set is a useful next step for first-class support. Apple Silicon Macs lost "+
					"the SMC entirely — there is no equivalent Linux path on M-series hardware.",
				strings.TrimSpace(dmi.ProductName),
			),
			EntityHash: doctor.HashEntity("vendor_remediation", "apple_intel:"+dmi.ProductName),
			Observed:   now,
		})
	}

	// Framework laptops are handled by the dedicated FrameworkStrategiesDetector
	// (framework_strategies_d.go), which is backed by the vendored fw-fanctrl
	// curve corpus and carries the correct cros_ec_hwmon kernel facts.

	if isClevoFamily(dmi) {
		facts = append(facts, doctor.Fact{
			Detector: d.Name(),
			Severity: doctor.SeverityOK,
			Class:    recovery.ClassUnknown,
			Title:    "Clevo / System76 / Tongfang laptop detected: proprietary EC, direct-port-I/O remediation available",
			Detail: fmt.Sprintf(
				"DMI matched a Clevo-family laptop (vendor=%q, product=%q). Mainline Linux "+
					"does not expose a hwmon path for the Clevo EC's fan registers — control "+
					"is via direct port-I/O on 0x68 / 0x6C (the ACPI EC ports). The community "+
					"reference implementation is `clevo-indicator` "+
					"(https://github.com/SkyLandTW/clevo-indicator) and the CLI fork "+
					"`clevo-fancontrol` (https://github.com/mmt050/clevo-fancontrol). "+
					"System76 hardware additionally has `system76-acpi-dkms` which exposes a "+
					"different ACPI path. ventd's `internal/ec/dev_port` transport already "+
					"speaks the OBF/IBF handshake these tools rely on; a dedicated "+
					"`internal/hal/clevo` backend over that transport is the planned "+
					"first-class path (T2.4 in the absorption roadmap).",
				strings.TrimSpace(dmi.SysVendor),
				strings.TrimSpace(dmi.ProductName),
			),
			EntityHash: doctor.HashEntity("vendor_remediation", "clevo:"+dmi.SysVendor+":"+dmi.ProductName),
			Observed:   now,
		})
	}

	// NZXT vendor ID (0x1e71) covers Kraken X/Z, Smart Device V1/V2,
	// Grid+ V3, RGB & Fan Controller. Userspace is the established
	// path via liquidctl; the in-tree liquidtux drivers expose the
	// Kraken X3/Z3 as hwmon, and ventd's existing hwmon backend
	// drives those nodes already once liquidtux is loaded.
	if _, ok := usbVendors["1e71"]; ok {
		facts = append(facts, doctor.Fact{
			Detector: d.Name(),
			Severity: doctor.SeverityOK,
			Class:    recovery.ClassUnknown,
			Title:    "NZXT device detected: liquidtux exposes Kraken X3/Z3 as hwmon; liquidctl is the userspace fallback",
			Detail: "A USB device with vendor 0x1e71 (NZXT) is connected. NZXT AIOs and " +
				"controllers fall into two integration camps: the Kraken X3 / Z3 series " +
				"(2020+) is exposed as hwmon by the in-tree `nzxt-kraken3` driver — when " +
				"that module is loaded, ventd's existing hwmon backend can drive the pump " +
				"+ fan channels directly. Older Kraken X1/X2 and the Smart Device V1/V2 " +
				"have no kernel driver; the canonical userspace controller is liquidctl " +
				"(https://github.com/liquidctl/liquidctl). Detection: " +
				"`ls /sys/class/hwmon/*/name | xargs -I{} cat {} | grep kraken` shows whether " +
				"the kernel side is active. If not, install the `liquidctl` package and add " +
				"`liquidctl initialize all` to a systemd unit alongside ventd. The companion " +
				"kernel module `liquidtux` (https://github.com/liquidctl/liquidtux) adds " +
				"hwmon support for further NZXT / Aquacomputer / Gigabyte devices not yet " +
				"in-tree.\n\n" +
				renderLiquidDeviceList("1e71"),
			EntityHash: doctor.HashEntity("vendor_remediation", "nzxt:1e71"),
			Observed:   now,
		})
	}

	// Aquacomputer vendor ID (0x0c70) — D5 Next, Octo, Quadro. All
	// covered by the in-tree `aquacomputer-d5next` driver (mainline
	// 5.18+); ventd's hwmon backend drives the pwm channels when the
	// module is loaded.
	if _, ok := usbVendors["0c70"]; ok {
		facts = append(facts, doctor.Fact{
			Detector: d.Name(),
			Severity: doctor.SeverityOK,
			Class:    recovery.ClassUnknown,
			Title:    "Aquacomputer device detected: in-tree aquacomputer-d5next exposes pwm + tach as hwmon",
			Detail: "A USB device with vendor 0x0c70 (Aquacomputer) is connected. " +
				"Mainline kernel 5.18+ ships the `aquacomputer-d5next` driver which " +
				"covers the D5 Next pump, Octo fan controller, and Quadro fan controller " +
				"as hwmon channels. Once the module is loaded, ventd's existing hwmon " +
				"backend drives them directly — no userspace tool required.\n\n" +
				renderLiquidDeviceList("0c70"),
			EntityHash: doctor.HashEntity("vendor_remediation", "aquacomputer:0c70"),
			Observed:   now,
		})
	}

	// Gigabyte AORUS Waterforce (vendor 0x1044). Covered by in-tree
	// `gigabyte-waterforce` driver in kernel 6.10+.
	if _, ok := usbVendors["1044"]; ok {
		facts = append(facts, doctor.Fact{
			Detector: d.Name(),
			Severity: doctor.SeverityOK,
			Class:    recovery.ClassUnknown,
			Title:    "Gigabyte AORUS Waterforce detected: in-tree gigabyte-waterforce exposes pump + fans as hwmon",
			Detail: "A USB device with vendor 0x1044 (Gigabyte) is connected. The " +
				"AORUS Waterforce X 240 / 280 / 360 AIOs are exposed as hwmon by the " +
				"in-tree `gigabyte-waterforce` driver (mainline 6.10+). Earlier kernels " +
				"need liquidctl userspace.\n\n" +
				renderLiquidDeviceList("1044"),
			EntityHash: doctor.HashEntity("vendor_remediation", "gigabyte:1044"),
			Observed:   now,
		})
	}

	// Corsair vendor ID (0x1b1c) — ventd already has a HAL backend
	// for Commander Core / ST, so surface only an "if your AIO isn't
	// detected, see liquidctl" remediation when no corsair channel
	// is enumerated. Conservative: emit on every Corsair USB
	// presence; operators who DO have channels see this card
	// alongside the working backend and can ignore.
	if _, ok := usbVendors["1b1c"]; ok {
		facts = append(facts, doctor.Fact{
			Detector: d.Name(),
			Severity: doctor.SeverityOK,
			Class:    recovery.ClassUnknown,
			Title:    "Corsair USB device detected: ventd's HAL backend covers Commander Core / ST; older Link / iCUE Pro need liquidctl",
			Detail: "A USB device with vendor 0x1b1c (Corsair) is connected. ventd's " +
				"`internal/hal/liquid/corsair` backend covers the Commander Core / Commander " +
				"ST / Commander Pro device families directly via hidraw — those should " +
				"appear in `ventd doctor` output as corsair channels under monitor or " +
				"control mode. Older Corsair AIOs (Hydro H80i v1, H100i v1, Link) and some " +
				"iCUE-only PSUs are NOT in ventd's backend and need the liquidctl " +
				"userspace controller (https://github.com/liquidctl/liquidctl).\n\n" +
				renderLiquidDeviceList("1b1c"),
			EntityHash: doctor.HashEntity("vendor_remediation", "corsair:1b1c"),
			Observed:   now,
		})
	}

	return facts, nil
}

// isAppleIntelMac matches DMI strings emitted by Intel-era Mac
// firmware. Apple Silicon Macs report "Apple Inc." vendor but
// can be distinguished by the absence of an x86 `product_name`
// (the M1/M2/M3 boot path doesn't populate the same fields). For
// safety we ALSO require the product_name to start with "MacBook",
// "iMac", "Macmini", or "MacPro" — Apple Silicon Macs use the
// same prefixes but the wrong CPU architecture; on aarch64 hosts
// applesmc isn't loaded, so the detector's downstream advice
// (about applesmc) is harmless even if the detection over-fires.
func isAppleIntelMac(dmi hwdb.DMI) bool {
	vendor := strings.TrimSpace(dmi.SysVendor)
	if vendor != "Apple Inc." {
		return false
	}
	product := strings.TrimSpace(dmi.ProductName)
	if product == "" {
		return false
	}
	for _, prefix := range []string{"MacBook", "iMac", "Macmini", "MacPro"} {
		if strings.HasPrefix(product, prefix) {
			return true
		}
	}
	return false
}

// isClevoFamily matches DMI strings emitted by Clevo / Tongfang
// ODMs and the System76 / Eluktronics / Origin PC / XMG brands
// that rebrand them. Clevo motherboards typically expose their
// own name as board_vendor ("Notebook" / "CLEVO") with the
// retailer brand in sys_vendor — match against either field.
//
// System76 ships its own custom hardware under "System76, Inc."
// sys_vendor; some models are Clevo rebrands (Bonobo, Oryx) and
// others (Lemur, Galago) are custom. We surface the card for
// the System76 brand uniformly so operators get the right
// userspace pointer regardless of the underlying hardware.
func isClevoFamily(dmi hwdb.DMI) bool {
	vendor := strings.TrimSpace(dmi.SysVendor)
	board := strings.TrimSpace(dmi.BoardVendor)
	hits := []string{
		"System76, Inc.",
		"System76",
		"Clevo",
		"CLEVO",
		"TongFang",
		"Eluktronics",
		"Origin PC",
		"SCHENKER",
		"XMG",
	}
	for _, h := range hits {
		if strings.EqualFold(vendor, h) || strings.EqualFold(board, h) {
			return true
		}
		if strings.Contains(strings.ToUpper(vendor), strings.ToUpper(h)) {
			return true
		}
	}
	return false
}

// renderLiquidDeviceList formats the LiquidDevice entries for the
// given vendor ID as a single multi-line string suitable for
// appending to a doctor Fact's Detail. Returns an empty string when
// no entries match — the caller's Detail then carries no trailing
// device list, which is the correct quiet-on-no-data shape.
func renderLiquidDeviceList(vid string) string {
	devs := LookupLiquidDeviceByVID(vid)
	if len(devs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("ventd has metadata for these vendor 0x")
	b.WriteString(vid)
	b.WriteString(" devices (PID → name + kernel driver / userspace tool):\n")
	for _, d := range devs {
		b.WriteString("  - 0x")
		b.WriteString(d.PID)
		b.WriteString(": ")
		b.WriteString(d.Name)
		b.WriteString(" — ")
		if d.KernelDriver != "" {
			b.WriteString("kernel driver: ")
			b.WriteString(d.KernelDriver)
			if d.UserspaceTool != "" {
				b.WriteString(" (fallback: ")
				b.WriteString(d.UserspaceTool)
				b.WriteString(")")
			}
		} else {
			b.WriteString("userspace: ")
			b.WriteString(d.UserspaceTool)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// scanUSBVendors walks /sys/bus/usb/devices and returns the set of
// idVendor values (lowercased hex strings). On any I/O error the
// function returns an empty set — the calling detector treats that
// as "no USB devices recognised" and skips the USB-driven facts,
// which is the correct quiet-on-no-data behaviour.
func scanUSBVendors() map[string]struct{} {
	out := make(map[string]struct{})
	entries, err := os.ReadDir("/sys/bus/usb/devices")
	if err != nil {
		return out
	}
	for _, e := range entries {
		path := "/sys/bus/usb/devices/" + e.Name() + "/idVendor"
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		v := strings.ToLower(strings.TrimSpace(string(data)))
		if v == "" {
			continue
		}
		out[v] = struct{}{}
	}
	return out
}
