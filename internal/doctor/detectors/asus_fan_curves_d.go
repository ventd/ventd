package detectors

import (
	"context"
	"fmt"
	"strings"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/hwdb"
	"github.com/ventd/ventd/internal/hwdb/asus"
	"github.com/ventd/ventd/internal/recovery"
)

// ASUSFanCurvesDetector surfaces, on an ASUS laptop, that ventd recognises the
// board and which g-helper-derived fan-curve presets are available. It is the
// operator-visible consumer of the vendored g-helper curve corpus
// (internal/hwdb/asus, spec-17 PR-3): it loads the corpus, matches the live
// DMI, and names the available performance-mode presets so an ASUS owner knows
// ventd can drive their fan (via the mainline asus-wmi custom-fan-curve hwmon,
// through the internal/hal/asuswmi CurveSink backend) and what proven curves
// they can adopt.
type ASUSFanCurvesDetector struct {
	// ReadDMIFn returns the live DMI tuple. Defaults to liveReadDMI when nil.
	ReadDMIFn func() (hwdb.DMI, error)
	// Catalog is the parsed g-helper corpus. nil → load via asus.LoadCatalog on
	// first Probe. Tests inject a fixture.
	Catalog *asus.Catalog
}

// NewASUSFanCurvesDetector constructs the detector with the production DMI
// reader. Pass a non-nil catalog to skip the embedded corpus (tests).
func NewASUSFanCurvesDetector(cat *asus.Catalog) *ASUSFanCurvesDetector {
	return &ASUSFanCurvesDetector{ReadDMIFn: liveReadDMI, Catalog: cat}
}

// Name returns the stable detector ID.
func (d *ASUSFanCurvesDetector) Name() string { return "asus_fan_curves" }

// Probe emits one Fact on an ASUS host naming the available g-helper curve
// presets, or nothing on a non-ASUS host. Graceful-degrade on a corpus-load or
// DMI-read failure (a Warning Fact, never a fatal error).
func (d *ASUSFanCurvesDetector) Probe(ctx context.Context, deps doctor.Deps) ([]doctor.Fact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	readDMI := d.ReadDMIFn
	if readDMI == nil {
		readDMI = liveReadDMI
	}
	dmi, err := readDMI()
	if err != nil {
		// Can't read DMI → can't tell if this is an ASUS host. Stay quiet
		// rather than emit a card that may not apply.
		return nil, nil
	}
	if !asus.IsASUS(dmi) {
		return nil, nil
	}

	if d.Catalog == nil {
		cat, err := asus.LoadCatalog()
		if err != nil {
			return []doctor.Fact{{
				Detector:   d.Name(),
				Severity:   doctor.SeverityWarning,
				Class:      recovery.ClassUnknown,
				Title:      "ASUS fan-curve preset corpus failed to load",
				Detail:     fmt.Sprintf("internal/hwdb/asus.LoadCatalog: %v", err),
				EntityHash: doctor.HashEntity("asus_fan_curves", "corpus_load_err"),
				Observed:   timeNowFromDeps(deps),
			}}, nil
		}
		d.Catalog = cat
	}

	entry, ok := asus.Match(d.Catalog, dmi)
	if !ok || entry == nil {
		return nil, nil
	}

	modes := d.Catalog.Modes()
	// Summarise each preset's aggressiveness by its CPU peak duty so the card is
	// concrete (read from the corpus, not hard-coded).
	summaries := make([]string, 0, len(modes))
	for _, m := range modes {
		if p, found := d.Catalog.Mode(m); found {
			summaries = append(summaries, fmt.Sprintf("%s (CPU peaks ~%d%%)", m, p.PeakDuty("cpu")))
		}
	}

	return []doctor.Fact{{
		Detector: d.Name(),
		Severity: doctor.SeverityOK,
		Class:    recovery.ClassUnknown,
		Title:    fmt.Sprintf("ASUS laptop detected — %d g-helper fan-curve presets available", len(modes)),
		Detail: fmt.Sprintf(
			"DMI matched an ASUS machine (vendor=%q, product=%q). On kernel 6.4+ the mainline "+
				"`asus-wmi` driver exposes an eight-point custom fan curve as the hwmon device "+
				"`asus_custom_fan_curve` (pwm1=CPU, pwm2=GPU); ventd's asuswmi backend programs that "+
				"curve directly (the firmware then runs the fan loop). ventd vendors the default "+
				"fan-curve presets from g-helper (https://github.com/seerge/g-helper) as reference "+
				"curves you can adopt in your ventd config: %s. These are g-helper's model-agnostic "+
				"fallback curves (ASUS keeps per-model curves in the BIOS); ventd talks to the kernel "+
				"asus-wmi sysfs directly and never shells out to g-helper or asusctl.",
			strings.TrimSpace(dmi.SysVendor),
			strings.TrimSpace(dmi.ProductName),
			strings.Join(summaries, ", "),
		),
		EntityHash: doctor.HashEntity("asus_fan_curves", "asus:"+dmi.ProductName),
		Observed:   timeNowFromDeps(deps),
	}}, nil
}
