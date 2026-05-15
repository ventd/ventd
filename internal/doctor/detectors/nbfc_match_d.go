package detectors

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/hwdb"
	"github.com/ventd/ventd/internal/hwdb/nbfc"
	"github.com/ventd/ventd/internal/recovery"
)

// NBFCMatchDetector surfaces whether the live DMI matches a config in
// the vendored upstream nbfc-linux catalogue. The detector is the
// operator-visible bridge between "this laptop has a known EC fan-
// control recipe" and "ventd hasn't shipped the EC backend yet" —
// closing the diagnostic gap that `RULE-DOCTOR-DETECTOR-ECLOCKEDLAPTOP`
// currently fills with a generic "fan control owned by the EC" card.
//
// Trigger condition: ventd is running monitor-only on this host
// (`ControllableChannelCount == 0`) AND the DMI matches a catalogue
// entry. The detector emits exactly one Fact (RULE-NBFC-DOCTOR-01):
//
//   - Severity OK + control-mode detail when the match is register-
//     only or ACPI — these are within the v0.8.0 spec-09 scope.
//   - Severity Warning when the match's control mode is Lua-driven —
//     refused in v0.8.0 (0 catalogue entries today; the slot exists
//     for forward-compat).
//   - Severity Warning + contribution-invite when there is no match —
//     operators on uncatalogued laptops see the upstream-contribution
//     pathway.
//
// Skipped silently when there ARE controllable channels — smart-mode
// applies to that case via other detectors.
type NBFCMatchDetector struct {
	// ControllableChannelCount is len(probe.ProbeResult.ControllableChannels)
	// at daemon start. Zero = monitor-only, the trigger condition.
	ControllableChannelCount int

	// ReadDMIFn returns the live DMI tuple. Defaults to liveReadDMI
	// when nil; tests inject a stub.
	ReadDMIFn func() (hwdb.DMI, error)

	// Catalog is the parsed nbfc catalogue. nil → load via
	// nbfc.LoadCatalog on first Probe. Tests inject a small fixture.
	Catalog *nbfc.Catalog
}

// NewNBFCMatchDetector constructs a detector with the production
// default DMI reader. Pass a non-nil catalog to skip the embedded
// catalogue (tests, alternate-source operators).
func NewNBFCMatchDetector(controllableChannels int, cat *nbfc.Catalog) *NBFCMatchDetector {
	return &NBFCMatchDetector{
		ControllableChannelCount: controllableChannels,
		ReadDMIFn:                liveReadDMI,
		Catalog:                  cat,
	}
}

// Name returns the stable detector ID.
func (d *NBFCMatchDetector) Name() string { return "nbfc_match" }

// Probe inspects the live DMI and emits a single Fact per trigger
// branch. Graceful-degrade on DMI read error (RULE-DOCTOR-04) — log
// nothing fatal; emit a Warning Fact identifying the read failure.
func (d *NBFCMatchDetector) Probe(ctx context.Context, deps doctor.Deps) ([]doctor.Fact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Only fire on monitor-only hosts. Desktops + workstations + any
	// host with a writable PWM go through smart-mode; this detector
	// is the laptop-EC surface.
	if d.ControllableChannelCount > 0 {
		return nil, nil
	}

	if d.Catalog == nil {
		cat, err := nbfc.LoadCatalog()
		if err != nil {
			// Catalogue parse failure shouldn't crash doctor; surface
			// as a Warning so the daemon journal carries an actionable
			// breadcrumb.
			return []doctor.Fact{{
				Detector:   d.Name(),
				Severity:   doctor.SeverityWarning,
				Class:      recovery.ClassUnknown,
				Title:      "NBFC catalogue failed to load",
				Detail:     fmt.Sprintf("internal/hwdb/nbfc.LoadCatalog: %v", err),
				EntityHash: doctor.HashEntity("nbfc_match", "catalog_load_err"),
				Observed:   timeNowFromDeps(deps),
			}}, nil
		}
		d.Catalog = cat
	}

	dmi, err := d.ReadDMIFn()
	if err != nil {
		return []doctor.Fact{{
			Detector:   d.Name(),
			Severity:   doctor.SeverityWarning,
			Class:      recovery.ClassUnknown,
			Title:      "DMI read failed; cannot consult NBFC catalogue",
			Detail:     fmt.Sprintf("hwdb.ReadDMI: %v — rerun as root for full /sys/class/dmi/id read access", err),
			EntityHash: doctor.HashEntity("nbfc_match", "dmi_err"),
			Observed:   timeNowFromDeps(deps),
		}}, nil
	}

	entry, tier := nbfc.Match(d.Catalog, dmi)
	if entry == nil {
		// No match. Invite an upstream contribution.
		return []doctor.Fact{{
			Detector: d.Name(),
			Severity: doctor.SeverityWarning,
			Class:    recovery.ClassUnknown,
			Title:    fmt.Sprintf("This laptop is not in the NBFC catalogue (%d known models)", d.Catalog.Size()),
			Detail: fmt.Sprintf(
				"DMI product_name=%q, board=%q, vendor=%q has no upstream nbfc-linux config — "+
					"fan control via the EC will not work in v0.8.0+. The upstream catalogue "+
					"accepts community contributions; the HOWTO is at "+
					"https://github.com/nbfc-linux/nbfc-linux/blob/main/doc/Configuration%%20HowTo.md "+
					"(an `ec_probe` walk of the embedded controller's register space is the "+
					"main contribution artefact). Once upstream accepts, ventd picks up the new "+
					"model on the next `make sync-nbfc-configs` (see internal/hwdb/nbfc/UPSTREAM).",
				strings.TrimSpace(dmi.ProductName),
				strings.TrimSpace(dmi.BoardName),
				strings.TrimSpace(dmi.SysVendor),
			),
			EntityHash: doctor.HashEntity("nbfc_match", "no_match:"+dmi.ProductName),
			Observed:   timeNowFromDeps(deps),
		}}, nil
	}

	// Matched. Surface the upstream model name + control-mode + tier.
	// Severity is OK for register/ACPI (spec-09 v0.8.0 scope) and
	// Warning for Lua (refused — operator should expect monitor-only).
	severity := doctor.SeverityOK
	statusVerb := "deferred to v0.8.0 (spec-09)"
	if entry.Mode == nbfc.ControlModeLua {
		severity = doctor.SeverityWarning
		statusVerb = "unsupported (Lua-driven configs are refused; v0.8.0 supports register + ACPI only)"
	}
	tierNote := ""
	if tier != nbfc.MatchExact {
		tierNote = fmt.Sprintf(" — match was %s, not exact (live ProductName=%q vs upstream NotebookModel=%q)", tier, dmi.ProductName, entry.Config.NotebookModel)
	}
	return []doctor.Fact{{
		Detector: d.Name(),
		Severity: severity,
		Class:    recovery.ClassUnknown,
		Title:    fmt.Sprintf("NBFC config matched: %s (%s)", entry.Config.NotebookModel, entry.Mode),
		Detail: fmt.Sprintf(
			"ventd is currently in monitor-only mode (no writable PWM channels were found on this host). "+
				"The upstream nbfc-linux catalogue contains a configuration for this exact hardware: "+
				"%q (source: %s, control mode: %s%s). Fan control via the embedded controller is %s. "+
				"Until then, the only operator-facing fan control is manual via /sys/firmware/acpi/platform_profile "+
				"if your laptop exposes it.",
			entry.Config.NotebookModel,
			entry.Filename,
			entry.Mode,
			tierNote,
			statusVerb,
		),
		EntityHash: doctor.HashEntity("nbfc_match", entry.Config.NotebookModel),
		Observed:   timeNowFromDeps(deps),
	}}, nil
}

// liveReadDMI is the production DMI reader. Reads from os.DirFS("/")
// via hwdb.ReadDMI — same code path the daemon's hwdb matcher uses,
// so the doctor surface is parity-aligned (RULE-DOCTOR-05).
func liveReadDMI() (hwdb.DMI, error) {
	return hwdb.ReadDMI(os.DirFS("/"))
}
