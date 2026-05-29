package detectors

import (
	"context"
	"fmt"
	"strings"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/hwdb"
	"github.com/ventd/ventd/internal/hwdb/framework"
	"github.com/ventd/ventd/internal/recovery"
)

// FrameworkStrategiesDetector surfaces, on a Framework laptop, that ventd
// recognises the board and which Framework-tuned fan-curve presets are
// available. It is the operator-visible consumer of the vendored fw-fanctrl
// corpus (internal/hwdb/framework, spec-17 PR-2): it loads the corpus, matches
// the live DMI, and names the strategies + the default curve so a Framework
// owner knows ventd can drive their fan (via the mainline cros_ec_hwmon hwmon
// pwm) and what proven curves they can adopt.
//
// Supersedes the generic Framework card the vendor_remediation detector used
// to emit — this one is backed by the actual curve corpus and carries the
// correct cros_ec_hwmon kernel facts (writable pwm since 6.18, not the old
// "cros_ec_fan 6.7+" text).
type FrameworkStrategiesDetector struct {
	// ReadDMIFn returns the live DMI tuple. Defaults to liveReadDMI when nil.
	ReadDMIFn func() (hwdb.DMI, error)
	// Catalog is the parsed fw-fanctrl corpus. nil → load via
	// framework.LoadCatalog on first Probe. Tests inject a fixture.
	Catalog *framework.Catalog
}

// NewFrameworkStrategiesDetector constructs the detector with the production
// DMI reader. Pass a non-nil catalog to skip the embedded corpus (tests).
func NewFrameworkStrategiesDetector(cat *framework.Catalog) *FrameworkStrategiesDetector {
	return &FrameworkStrategiesDetector{ReadDMIFn: liveReadDMI, Catalog: cat}
}

// Name returns the stable detector ID.
func (d *FrameworkStrategiesDetector) Name() string { return "framework_strategies" }

// Probe emits one Fact on a Framework host naming the available fw-fanctrl
// curve presets, or nothing on a non-Framework host. Graceful-degrade on a
// corpus-load or DMI-read failure (a Warning Fact, never a fatal error).
func (d *FrameworkStrategiesDetector) Probe(ctx context.Context, deps doctor.Deps) ([]doctor.Fact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	readDMI := d.ReadDMIFn
	if readDMI == nil {
		readDMI = liveReadDMI
	}
	dmi, err := readDMI()
	if err != nil {
		// Can't read DMI → can't tell if this is a Framework host. Stay quiet
		// rather than emit a card that may not apply.
		return nil, nil
	}
	if !framework.IsFramework(dmi) {
		return nil, nil
	}

	if d.Catalog == nil {
		cat, err := framework.LoadCatalog()
		if err != nil {
			return []doctor.Fact{{
				Detector:   d.Name(),
				Severity:   doctor.SeverityWarning,
				Class:      recovery.ClassUnknown,
				Title:      "Framework fan-curve preset corpus failed to load",
				Detail:     fmt.Sprintf("internal/hwdb/framework.LoadCatalog: %v", err),
				EntityHash: doctor.HashEntity("framework_strategies", "corpus_load_err"),
				Observed:   timeNowFromDeps(deps),
			}}, nil
		}
		d.Catalog = cat
	}

	entry, ok := framework.Match(d.Catalog, dmi)
	if !ok || entry == nil {
		return nil, nil
	}

	strategies := strings.Join(entry.Config.StrategyNames(), ", ")
	dischargeNote := ""
	if amd := d.Catalog.Lookup(framework.SourceAMD); amd != nil {
		dischargeNote = " A battery-aware variant (the fw-fanctrl-AMD fork) switches to a quieter " +
			"curve on discharge via `strategyOnDischarging`; ventd applies its own battery gate instead."
	}

	return []doctor.Fact{{
		Detector: d.Name(),
		Severity: doctor.SeverityOK,
		Class:    recovery.ClassUnknown,
		Title:    fmt.Sprintf("Framework laptop detected — %d fw-fanctrl curve presets available (default: %q)", len(entry.Config.Strategies), entry.Config.DefaultStrategy),
		Detail: fmt.Sprintf(
			"DMI matched a Framework laptop (product=%q). The mainline `cros_ec_hwmon` kernel "+
				"driver exposes the EC fan as hwmon (name=cros_ec): read-only sensors since kernel "+
				"6.15, and writable `pwm1` duty since kernel 6.18 — when present, ventd's hwmon "+
				"backend drives it directly (no out-of-tree module needed). On older kernels, or to "+
				"tune curves, the community tool `fw-fanctrl` (https://github.com/TamtamHero/fw-fanctrl) "+
				"drives the EC via `ectool`. ventd vendors fw-fanctrl's curve presets as reference "+
				"curves you can adopt in your ventd config: %s (upstream default: %q).%s",
			strings.TrimSpace(dmi.ProductName),
			strategies,
			entry.Config.DefaultStrategy,
			dischargeNote,
		),
		EntityHash: doctor.HashEntity("framework_strategies", "framework:"+dmi.ProductName),
		Observed:   timeNowFromDeps(deps),
	}}, nil
}
