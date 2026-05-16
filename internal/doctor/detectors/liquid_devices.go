// SPDX-License-Identifier: GPL-3.0-or-later
package detectors

// LiquidDevice describes a single USB device the operator-facing
// `vendor_remediation` detector can name when it sees the matching
// VID:PID on the host. The table is a vendored subset of the
// liquidctl device list, scoped to devices where ventd has a
// concrete recommendation (kernel driver name when available,
// userspace tool name otherwise).
//
// Data source: liquidctl's `liquidctl/driver/*.py` device tables
// (GPL-3.0+). Only metadata is vendored — no Python code is copied.
// Each entry pins:
//   - VID + PID (lowercase hex strings)
//   - Marketing name as shipped by the vendor
//   - Kernel driver name (if any) that exposes hwmon for this device
//   - Userspace tool fallback when no kernel driver exists
type LiquidDevice struct {
	VID            string // lowercased hex, e.g. "1e71"
	PID            string // lowercased hex, e.g. "2007"
	Name           string // marketing name
	KernelDriver   string // empty when no in-tree driver exists
	UserspaceTool  string // canonical userspace fallback
}

// liquidDeviceCatalog is the curated table consumed by the
// vendor_remediation detector. Indexed by VID and lookups walk the
// PID set for that VID — the slice shape (vs map[string]map[string])
// is preserved so the test fixture can iterate the table to assert
// every entry parses cleanly.
//
// Subset scope: NZXT Kraken X3 / Z3 (liquidtux in-tree hwmon),
// NZXT Smart Device V1/V2 (liquidctl userspace), Aquacomputer
// D5 Next + Quadro (in-tree aquacomputer-d5next), Corsair Commander
// Core / ST (ventd's own corsair backend). The list is intentionally
// short — operators with un-listed devices see the vendor-level card
// from the detector's fallback branch.
var liquidDeviceCatalog = []LiquidDevice{
	// NZXT (vendor 1e71)
	{VID: "1e71", PID: "2007", Name: "NZXT Kraken X53 / X63 / X73 (X3 series)", KernelDriver: "nzxt-kraken3", UserspaceTool: "liquidctl"},
	{VID: "1e71", PID: "3008", Name: "NZXT Kraken Z53 / Z63 / Z73 (Z3 series)", KernelDriver: "nzxt-kraken3", UserspaceTool: "liquidctl"},
	{VID: "1e71", PID: "1714", Name: "NZXT Smart Device V1", KernelDriver: "", UserspaceTool: "liquidctl"},
	{VID: "1e71", PID: "2006", Name: "NZXT Smart Device V2", KernelDriver: "nzxt-smart2", UserspaceTool: "liquidctl"},
	{VID: "1e71", PID: "200d", Name: "NZXT RGB & Fan Controller", KernelDriver: "nzxt-smart2", UserspaceTool: "liquidctl"},
	{VID: "1e71", PID: "2009", Name: "NZXT H1 V2 fan / pump controller", KernelDriver: "", UserspaceTool: "liquidctl"},
	{VID: "1e71", PID: "1715", Name: "NZXT Grid+ V3", KernelDriver: "", UserspaceTool: "liquidctl"},

	// Aquacomputer (vendor 0c70)
	{VID: "0c70", PID: "f00e", Name: "Aquacomputer D5 Next pump", KernelDriver: "aquacomputer-d5next", UserspaceTool: "liquidctl"},
	{VID: "0c70", PID: "f00d", Name: "Aquacomputer Octo fan controller", KernelDriver: "aquacomputer-d5next", UserspaceTool: "liquidctl"},
	{VID: "0c70", PID: "f0b6", Name: "Aquacomputer Quadro fan controller", KernelDriver: "aquacomputer-d5next", UserspaceTool: "liquidctl"},

	// Corsair (vendor 1b1c) — ventd's own corsair HAL backend covers these
	{VID: "1b1c", PID: "0c10", Name: "Corsair Commander Pro", KernelDriver: "corsair-cpro", UserspaceTool: "liquidctl"},
	{VID: "1b1c", PID: "0c1c", Name: "Corsair Commander Core (legacy)", KernelDriver: "", UserspaceTool: "ventd internal/hal/liquid/corsair"},
	{VID: "1b1c", PID: "0c1f", Name: "Corsair Commander ST", KernelDriver: "", UserspaceTool: "ventd internal/hal/liquid/corsair"},
	{VID: "1b1c", PID: "0c1b", Name: "Corsair Commander Core XT", KernelDriver: "", UserspaceTool: "ventd internal/hal/liquid/corsair"},

	// Gigabyte (vendor 1044) — Waterforce series
	{VID: "1044", PID: "7a01", Name: "Gigabyte AORUS Waterforce X 240/280/360", KernelDriver: "gigabyte-waterforce", UserspaceTool: "liquidctl"},
}

// LookupLiquidDeviceByVID returns the liquid-device entries with the
// matching vendor ID. Used by vendor_remediation to enrich its
// USB-vendor cards with specific device names when the PID is also
// recognised.
func LookupLiquidDeviceByVID(vid string) []LiquidDevice {
	out := make([]LiquidDevice, 0, 4)
	for _, d := range liquidDeviceCatalog {
		if d.VID == vid {
			out = append(out, d)
		}
	}
	return out
}

// LookupLiquidDevice returns the exact-match entry for a VID:PID
// pair, or nil when no entry matches. The lookup is linear over
// the catalog — the table is small (< 32 entries) and the call
// site (vendor_remediation Probe) fires at doctor-tick cadence
// (60 s default), so a more elaborate map index is not warranted.
func LookupLiquidDevice(vid, pid string) *LiquidDevice {
	for i := range liquidDeviceCatalog {
		if liquidDeviceCatalog[i].VID == vid && liquidDeviceCatalog[i].PID == pid {
			return &liquidDeviceCatalog[i]
		}
	}
	return nil
}
