// SPDX-License-Identifier: GPL-3.0-or-later
package detectors

import (
	"context"
	"fmt"
	"strings"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/hwdb"
	"github.com/ventd/ventd/internal/recovery"
)

// HPOmenDetector recognises the HP Omen / Victus gaming-laptop family
// via DMI and emits an Info-severity card pointing operators at the
// out-of-tree omen-fan / omen-fan-control userspace tools. Mainline
// `hp-wmi` handles hotkeys + rfkill only — it does not expose
// fan-control sysfs. The board catalog entries
// (`hp-omen-16-2024-intel`, `hp-omen-15-2023-amd`, `hp-victus-16-2024`
// in `catalog/boards/hp.yaml`) mark these models `overrides.unsupported:
// true`, so the matcher already refuses calibration. This detector
// adds the operator-facing breadcrumb that the matcher's silent skip
// is intentional and there is an actionable remediation path —
// distinct from the generic `ec_locked_laptop` card (which only
// names the platform_profile choices).
//
// Trigger: DMI sys_vendor begins with "HP" / "Hewlett-Packard" AND
// product_name contains "OMEN" or "Victus" (case-insensitive). The
// detector does NOT gate on platform_profile presence — some Omens
// expose it and some don't, and the operator-facing recommendation
// is the same regardless.
//
// Severity: Info. The hardware works; the fan curve is firmware-
// managed. Operators choosing performance over acoustics have a
// userspace path (omen-fan / omen-fan-control); the kernel-side
// patch is tracked upstream.
type HPOmenDetector struct {
	// ReadDMIFn returns the live DMI tuple. Defaults to liveReadDMI
	// when nil (production); tests inject a stub.
	ReadDMIFn func() (hwdb.DMI, error)
}

// NewHPOmenDetector constructs a detector with the production DMI
// reader. Pass a stub via the exported field for tests.
func NewHPOmenDetector() *HPOmenDetector {
	return &HPOmenDetector{ReadDMIFn: liveReadDMI}
}

// Name returns the stable detector ID.
func (d *HPOmenDetector) Name() string { return "hp_omen" }

// Probe reads DMI and emits one Info Fact when the live host matches
// the Omen / Victus family. Quiet on every non-match including DMI
// read failure — the lack of a card on a non-HP host is the correct
// signal that the detector ran without firing.
func (d *HPOmenDetector) Probe(ctx context.Context, deps doctor.Deps) ([]doctor.Fact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	read := d.ReadDMIFn
	if read == nil {
		read = liveReadDMI
	}
	dmi, err := read()
	if err != nil {
		// Graceful-degrade per RULE-DOCTOR-04: DMI read failure on a
		// detector that is purely informational is a no-op, not a
		// Warning card. The DMI-fingerprint detector
		// (RULE-DOCTOR-DETECTOR-DMIFINGERPRINT) already surfaces
		// DMI-read failures system-wide; we don't duplicate that.
		return nil, nil
	}
	if !isHPOmenFamily(dmi) {
		return nil, nil
	}
	product := strings.TrimSpace(dmi.ProductName)
	family := "OMEN"
	if strings.Contains(strings.ToLower(product), "victus") {
		family = "Victus"
	}
	return []doctor.Fact{{
		Detector: d.Name(),
		Severity: doctor.SeverityOK,
		Class:    recovery.ClassUnknown,
		Title:    fmt.Sprintf("HP %s detected: fan control requires out-of-tree kmod patch", family),
		Detail: fmt.Sprintf(
			"DMI matched the HP %s gaming-laptop family (product=%q). Mainline `hp-wmi` "+
				"handles hotkeys and rfkill only — it does not expose a fan-control sysfs "+
				"path, and ventd's HAL backends therefore cannot drive the EC. Two "+
				"upstream-community options exist: (1) the `omen-fan` Python userspace "+
				"controller at https://github.com/alou-S/omen-fan, which talks to the EC "+
				"directly via /dev/ports + an installed kernel module; (2) the "+
				"`omen-fan-control` kmod patchset at https://github.com/arfelious/omen-fan-control "+
				"which adds the missing pwm sysfs nodes to `hp-wmi`. ventd will pick up "+
				"the latter automatically (no config changes) once the patches land in "+
				"mainline. Until then, the operator-facing path is firmware fan profiles "+
				"via /sys/firmware/acpi/platform_profile (low-power / balanced / "+
				"performance), if your BIOS exposes it.",
			family,
			product,
		),
		EntityHash: doctor.HashEntity("hp_omen", product),
		Observed:   timeNowFromDeps(deps),
	}}, nil
}

// isHPOmenFamily returns true when the DMI tuple matches the HP Omen
// or Victus gaming-laptop family. Match is case-insensitive on
// product_name to absorb the casing variation HP ships across BIOS
// revisions ("OMEN by HP Laptop 16-wf...", "OMEN BY HP LAPTOP ...").
// sys_vendor is checked against both "HP" and "Hewlett-Packard"
// since older BIOS revisions used the long form.
func isHPOmenFamily(dmi hwdb.DMI) bool {
	vendor := strings.ToUpper(strings.TrimSpace(dmi.SysVendor))
	if vendor != "HP" && !strings.HasPrefix(vendor, "HEWLETT") {
		return false
	}
	product := strings.ToLower(strings.TrimSpace(dmi.ProductName))
	if product == "" {
		return false
	}
	return strings.Contains(product, "omen") || strings.Contains(product, "victus")
}
