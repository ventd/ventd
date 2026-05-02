package detectors

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/recovery"
)

// PowerSupplyFS is the read-only filesystem surface
// BatteryTransitionDetector needs. Production wires the live
// /sys/class/power_supply/ tree; tests inject testing/fstest.MapFS
// rooted at a synthetic path.
type PowerSupplyFS interface {
	// ReadFile returns the bytes of name. Same contract as
	// os.ReadFile — wraps os.ErrNotExist on absence.
	ReadFile(name string) ([]byte, error)

	// ReadDir returns one entry per direct child of name. Used to
	// walk power_supply/ for AC*/online and BAT*/status entries.
	ReadDir(name string) ([]os.DirEntry, error)
}

// liveAcSupplyFS reads from the real /sys.
type liveAcSupplyFS struct{}

func (liveAcSupplyFS) ReadFile(name string) ([]byte, error) { return os.ReadFile(name) }
func (liveAcSupplyFS) ReadDir(name string) ([]os.DirEntry, error) {
	return os.ReadDir(name)
}

// powerSupplyRoot is the /sys path. Tests override via the FS
// implementation rather than mutating this constant.
const powerSupplyRoot = "/sys/class/power_supply"

// BatteryTransitionDetector detects the laptop-unplugged-mid-run
// case: AC adapter goes offline AND a battery reports Discharging
// while ventd is running. Calibration on battery is a hard-refusal
// per RULE-IDLE-02; the runtime case is an operational warning
// (sustained battery drain + active fan control = battery wear +
// possible thermal throttling) but not an outright refuse.
//
// Surfaces as a Warning. Resolves automatically on next probe when
// AC is restored.
type BatteryTransitionDetector struct {
	// FS is the power_supply filesystem reader. Defaults to the
	// real /sys when nil.
	FS PowerSupplyFS
}

// NewBatteryTransitionDetector constructs a detector. fs nil means
// "use the real /sys/class/power_supply".
func NewBatteryTransitionDetector(fs PowerSupplyFS) *BatteryTransitionDetector {
	if fs == nil {
		fs = liveAcSupplyFS{}
	}
	return &BatteryTransitionDetector{FS: fs}
}

// Name returns the stable detector ID.
func (d *BatteryTransitionDetector) Name() string { return "battery_transition" }

// Probe walks /sys/class/power_supply and emits a single Fact when
// (a) at least one AC* supply reports online=0, AND (b) at least
// one BAT* supply reports status=Discharging. Both conditions must
// hold — a desktop with no AC monitoring still reports online=0 on
// the empty AC slot but never has a BAT, so the AND-gate avoids
// false positives there.
func (d *BatteryTransitionDetector) Probe(ctx context.Context, deps doctor.Deps) ([]doctor.Fact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	entries, err := d.FS.ReadDir(powerSupplyRoot)
	if err != nil {
		// /sys/class/power_supply absent → no battery hardware (or
		// the daemon is in a sandbox). Not a fan-control issue;
		// emit nothing.
		return nil, nil
	}

	acOffline := false
	batDischarging := false
	var batName string

	for _, e := range entries {
		name := e.Name()
		switch {
		case strings.HasPrefix(name, "AC"), strings.HasPrefix(name, "ADP"):
			online, ok := readAcOnline(d.FS, name)
			if ok && !online {
				acOffline = true
			}
		case strings.HasPrefix(name, "BAT"):
			status, ok := readBatteryStatus(d.FS, name)
			if ok && strings.EqualFold(status, "Discharging") {
				batDischarging = true
				if batName == "" {
					batName = name
				}
			}
		}
	}

	if !acOffline || !batDischarging {
		return nil, nil
	}

	now := timeNowFromDeps(deps)
	return []doctor.Fact{{
		Detector: d.Name(),
		Severity: doctor.SeverityWarning,
		Class:    recovery.ClassUnknown,
		Title:    "Running on battery — calibration refused; fan control continues",
		Detail: fmt.Sprintf(
			"AC adapter offline AND battery %s reports Discharging. RULE-IDLE-02 hard-refuses any new Envelope C calibration sweep on battery. The current control loop continues (firmware still owns the safety floor), but sustained battery drain at high fan speeds will accelerate battery wear and may trigger thermal throttling. Resolves automatically when AC is restored.",
			batName,
		),
		EntityHash: doctor.HashEntity("battery_transition", batName),
		Observed:   now,
	}}, nil
}

// readAcOnline reads <root>/<name>/online and returns (true, true)
// for "1", (false, true) for "0", (false, false) on any read error
// or unexpected content.
func readAcOnline(fs PowerSupplyFS, name string) (bool, bool) {
	raw, err := fs.ReadFile(filepath.Join(powerSupplyRoot, name, "online"))
	if err != nil {
		return false, false
	}
	val := strings.TrimSpace(string(raw))
	switch val {
	case "1":
		return true, true
	case "0":
		return false, true
	}
	return false, false
}

// readBatteryStatus reads <root>/<name>/status. Possible values:
// Charging / Discharging / Full / Unknown / Not charging. The detector
// only fires on Discharging.
func readBatteryStatus(fs PowerSupplyFS, name string) (string, bool) {
	raw, err := fs.ReadFile(filepath.Join(powerSupplyRoot, name, "status"))
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(raw)), true
}
