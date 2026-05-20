// Package detectors — stuck-fan diagnosis (#757). The umbrella UX bug
// behind #753 (watchdog miss), #754 (stiction), #755 (sentinel abort):
// fixing each one individually still leaves the class of failure cases
// (BIOS in wrong chip-mode, disconnected fan header, voltage-mode 3-pin
// fan on a PWM-mode chip channel) where ventd has the diagnostic data
// and chooses not to surface it. The detector reads live hwmon state,
// joins it to the persisted probe/calibrate/apply artifacts, and emits
// one Warning Fact per stalled channel with per-vendor BIOS guidance.
package detectors

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/hwdb"
	"github.com/ventd/ventd/internal/recovery"
)

// DefaultStuckFanStateFile is the orchestrator's terminal-artifact
// path read by the detector to lift persisted exclusion reasons
// (RPMDetect Uncontrollable, calibrate Phantom, polarity phantom)
// onto live hwmon state. Matches the path FileCalibrationArtifactLoader
// uses so the two detectors stay in lockstep on a custom StateDir.
const DefaultStuckFanStateFile = "/var/lib/ventd/setup/state.json"

// stuckFanMinimumPWM is the duty-cycle threshold above which a channel
// reporting RPM=0 is considered "stuck" rather than "idling at PWM=0
// on purpose". A fan at PWM=20 with RPM=0 might genuinely be below
// its stall threshold and that's working as intended; the same fan
// at PWM=70 with RPM=0 means something is wrong (BIOS interfering,
// header disconnected, or fan dead). 30 matches the operator-facing
// minSpinPctFloor in the curve generator — anything we'd actually
// write at runtime stays above this.
const stuckFanMinimumPWM = 30

// StuckFanArtifactLoader is the read surface the detector needs to
// join live hwmon readings to persisted probe/apply outcomes. The
// production implementation reads /var/lib/ventd/setup/state.json
// and decodes the ApplyPhase + ProbePhase outcomes; tests pass a
// stub returning canned exclusion reasons.
//
// Returning a nil map + nil error is the "no prior signal" case —
// the wizard hasn't run yet (fresh install). The detector still
// emits a Fact per stuck channel in that case; the exclusion label
// is just left blank in the Detail.
type StuckFanArtifactLoader interface {
	// LoadStuckFanReasons returns a map keyed by absolute pwm path
	// containing the persisted exclusion reason for each fan the
	// wizard chose not to drive. Reasons include the orchestrator's
	// "no_sensor_correlated" (RPMDetect Uncontrollable),
	// "polarity_phantom" (polarity probe), and "calibrate_phantom"
	// (calibrate sustained-spin check). A missing key means the
	// channel was admitted to the running config — its stuck state
	// is a runtime regression, not a wizard exclusion.
	LoadStuckFanReasons() (map[string]string, error)
}

// FileStuckFanArtifactLoader is the production loader. Reads the
// orchestrator's state.json once per Probe (cheap — <100 KB on a
// fully-populated host) and lifts the ApplyArtifact.Uncontrollable
// entries onto a path-keyed map. Falls back to ProbePhase outputs
// if ApplyPhase hasn't run yet.
type FileStuckFanArtifactLoader struct {
	// Path overrides DefaultStuckFanStateFile. Used by tests and by
	// installs with a non-standard StateDir.
	Path string
}

// LoadStuckFanReasons reads the orchestrator state.json and extracts
// per-pwm exclusion reasons. A missing file means the wizard has
// not yet run; returns (empty, nil) so the detector still emits
// live diagnostics for any sysfs channel that reads zero RPM.
func (l FileStuckFanArtifactLoader) LoadStuckFanReasons() (map[string]string, error) {
	path := l.Path
	if path == "" {
		path = DefaultStuckFanStateFile
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read state file: %w", err)
	}
	var envelope struct {
		Outcomes map[string]struct {
			Status   string          `json:"status"`
			Artifact json.RawMessage `json:"artifact"`
		} `json:"outcomes"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode envelope: %w", err)
	}
	out := map[string]string{}
	// ApplyPhase artifact carries Uncontrollable[] (#598).
	if apply, ok := envelope.Outcomes["apply"]; ok && len(apply.Artifact) > 0 {
		var art struct {
			Uncontrollable []struct {
				PWMPath string `json:"pwm_path"`
				Reason  string `json:"reason"`
			} `json:"uncontrollable"`
		}
		if err := json.Unmarshal(apply.Artifact, &art); err == nil {
			for _, u := range art.Uncontrollable {
				if u.PWMPath != "" {
					out[u.PWMPath] = u.Reason
				}
			}
		}
	}
	// CalibratePhase carries Phantom flag + PhantomReason per fan.
	// One of two reasons we exclude in apply: "calibrate_phantom".
	if calib, ok := envelope.Outcomes["calibrate"]; ok && len(calib.Artifact) > 0 {
		var art struct {
			Results []struct {
				PWMPath       string `json:"pwm_path"`
				Phantom       bool   `json:"phantom,omitempty"`
				PhantomReason string `json:"phantom_reason,omitempty"`
			} `json:"results"`
		}
		if err := json.Unmarshal(calib.Artifact, &art); err == nil {
			for _, r := range art.Results {
				if r.Phantom && out[r.PWMPath] == "" {
					reason := "calibrate_phantom"
					if r.PhantomReason != "" {
						reason = "calibrate_phantom:" + r.PhantomReason
					}
					out[r.PWMPath] = reason
				}
			}
		}
	}
	return out, nil
}

// StuckFanDetector emits one Warning Fact per hwmon PWM channel
// whose runtime state matches the "fan not spinning" pattern: the
// channel is enabled (pwm_enable in {1, 2}) AND the operator-
// observable PWM byte sits above the stiction floor AND the paired
// tach reads zero. The detector joins live sysfs state to the
// orchestrator's persisted exclusion reasons so the Detail tells
// the operator both WHAT happened (channel is stuck) and WHY
// ventd already excluded it from active control if applicable.
//
// Severity is Warning (not Blocker) — a stuck fan doesn't crash
// ventd, but it's a thermal-safety signal that needs operator eyes.
// Class is recovery.ClassUnknown — none of the canonical failure
// classes (driver_wont_bind, acpi_resource_conflict, etc.) map
// cleanly to "BIOS Smart Fan misconfigured" or "fan disconnected";
// the Title + Detail carry the actionable information directly.
type StuckFanDetector struct {
	// HwmonRoot is the live hwmon directory (default /sys/class/hwmon).
	HwmonRoot string

	// DMIFS is the filesystem used to read /sys/class/dmi/id/*. The
	// detector reads BoardVendor at Probe time to select per-vendor
	// BIOS guidance text. Default is os.DirFS("/").
	DMIFS fs.FS

	// Loader is the orchestrator-state reader. A nil loader is
	// equivalent to "no prior wizard run" — the detector still
	// emits live diagnostics for any sysfs channel reading zero
	// RPM, just without the wizard's exclusion-reason context.
	Loader StuckFanArtifactLoader
}

// NewStuckFanDetector constructs a detector with production defaults.
// A nil loader is tolerated; tests pass a stub or leave it nil to
// exercise the live-only path.
func NewStuckFanDetector(hwmonRoot string, loader StuckFanArtifactLoader) *StuckFanDetector {
	if hwmonRoot == "" {
		hwmonRoot = "/sys/class/hwmon"
	}
	return &StuckFanDetector{
		HwmonRoot: hwmonRoot,
		DMIFS:     os.DirFS("/"),
		Loader:    loader,
	}
}

// Name returns the stable detector ID.
func (d *StuckFanDetector) Name() string { return "stuck_fan_diagnosis" }

// Probe walks every pwm<N> file under HwmonRoot, classifies each
// channel by joining live state to the loader's exclusion map, and
// emits one Fact per stuck channel.
func (d *StuckFanDetector) Probe(ctx context.Context, deps doctor.Deps) ([]doctor.Fact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	exclusions := map[string]string{}
	if d.Loader != nil {
		got, err := d.Loader.LoadStuckFanReasons()
		if err != nil {
			// Loader I/O failure is not fatal — emit a single
			// detector-level Warning so the operator knows the
			// stuck-fan join surface is degraded.
			return []doctor.Fact{{
				Detector:   d.Name(),
				Severity:   doctor.SeverityWarning,
				Class:      recovery.ClassUnknown,
				Title:      "Cannot read wizard state file for stuck-fan diagnosis",
				Detail:     fmt.Sprintf("Reading %s failed: %v. Live hwmon scan continues, but the wizard's exclusion reasons (RPMDetect, calibrate-phantom) cannot be joined to the live state.", DefaultStuckFanStateFile, err),
				EntityHash: doctor.HashEntity("stuck_fan_state_unreadable"),
				Observed:   timeNowFromDeps(deps),
			}}, nil
		}
		exclusions = got
	}

	guidance := d.boardGuidance()

	// Walk hwmon for pwm<N> + pwm<N>_enable + fan<N>_input triples.
	candidates := walkStuckFanCandidates(d.HwmonRoot)
	if len(candidates) == 0 {
		return nil, nil
	}

	// Stable order keeps doctor output (and EntityHash-keyed
	// suppression) deterministic across runs.
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].PWMPath < candidates[j].PWMPath })

	now := timeNowFromDeps(deps)
	facts := make([]doctor.Fact, 0, len(candidates))
	for _, c := range candidates {
		if !c.IsStuck() {
			continue
		}
		reason := exclusions[c.PWMPath]
		classification := classifyStuck(c, reason)
		facts = append(facts, doctor.Fact{
			Detector:   d.Name(),
			Severity:   doctor.SeverityWarning,
			Class:      recovery.ClassUnknown,
			Title:      fmt.Sprintf("Fan not spinning: %s (%s)", c.LabelOrPath(), classification),
			Detail:     buildStuckFanDetail(c, classification, reason, guidance),
			EntityHash: doctor.HashEntity("stuck_fan_diagnosis", c.PWMPath),
			Observed:   now,
		})
	}
	return facts, nil
}

// boardGuidance returns BIOS-remediation text tailored to the host's
// motherboard family. Falls back to a generic message on unknown
// vendors. Read once per Probe — DMI is static across daemon lifetime.
func (d *StuckFanDetector) boardGuidance() string {
	fsys := d.DMIFS
	if fsys == nil {
		fsys = os.DirFS("/")
	}
	dmi, err := hwdb.ReadDMI(fsys)
	if err != nil {
		return genericFanNotSpinningGuidance
	}
	vendor := strings.ToLower(strings.TrimSpace(dmi.BoardVendor))
	if vendor == "" {
		vendor = strings.ToLower(strings.TrimSpace(dmi.SysVendor))
	}
	switch {
	case strings.Contains(vendor, "gigabyte"):
		return gigabyteFanNotSpinningGuidance
	case strings.Contains(vendor, "asus"):
		return asusFanNotSpinningGuidance
	case strings.Contains(vendor, "msi"), strings.Contains(vendor, "micro-star"):
		return msiFanNotSpinningGuidance
	case strings.Contains(vendor, "asrock"):
		return asrockFanNotSpinningGuidance
	case strings.Contains(vendor, "dell"):
		return dellFanNotSpinningGuidance
	case strings.Contains(vendor, "lenovo"):
		return lenovoFanNotSpinningGuidance
	case strings.Contains(vendor, "hp"), strings.Contains(vendor, "hewlett"):
		return hpFanNotSpinningGuidance
	}
	return genericFanNotSpinningGuidance
}

// Per-vendor BIOS guidance strings. Kept inline because the
// list is small (8 vendors covers > 95% of x86 desktop + laptop
// hosts) and adding a YAML-loaded catalog would be wider scope
// than #757 calls for. Each string is multi-line so doctor's
// text renderer can lay it out beneath the Title.
const (
	genericFanNotSpinningGuidance = "Check the BIOS Smart Fan / Fan Control screen for this header. The fan may be locked to BIOS-managed mode, or the header may be configured for the wrong drive type (PWM vs DC). Disconnect-then-reconnect the fan cable to rule out a loose connector. If the fan reads 0 RPM after a 60-second warm-up at PWM=70+, it is most likely physically disconnected, dead, or below its stall threshold."

	gigabyteFanNotSpinningGuidance = "Gigabyte BIOS: enter Setup → M.I.T. → Smart Fan 6 / Smart Fan 5 (varies by board). For this header, set Fan Control Mode to \"Auto\" or switch the drive mode (PWM ↔ Voltage) to match the fan's connector type — a 3-pin DC fan on a PWM-mode header reads 0 RPM at any duty cycle. Save & exit, then re-run the ventd setup wizard."

	asusFanNotSpinningGuidance = "ASUS BIOS: enter Setup → Monitor → Q-Fan Configuration. For this header, set the fan profile to \"Manual\" (so ventd's writes are honoured) and switch the fan tuning mode (PWM ↔ DC) to match the connector. ROG boards also have an AI Suite override that overrides BIOS until uninstalled — check that AI Suite / Armoury Crate is not running on the desktop side."

	msiFanNotSpinningGuidance = "MSI BIOS: enter Setup → HARDWARE MONITOR. For this header, set \"Smart Fan Mode\" to \"PWM\" or \"DC\" to match the connector and set the fan profile to allow third-party control. Click-BIOS 5 hides this under the OC tab on some boards. If MSI Center is installed on the OS side, uninstall it — its Mystic Light + fan service rewrites BIOS-configured curves on every boot."

	asrockFanNotSpinningGuidance = "ASRock BIOS: enter Setup → H/W Monitor → FAN-Tastic Tuning. Set the fan profile to \"Customize\" (so ventd writes pass through) and check the Fan Header drive mode (PWM ↔ DC) matches the connector. The ASRock Polychrome SDK on the OS side can fight ventd writes — uninstall if installed."

	dellFanNotSpinningGuidance = "Dell systems: Dell SMM-controlled fan headers don't honour direct PWM writes by default. ventd's dell-smm-hwmon-dkms driver enables the SMM whitelist when supported; if a fan is stuck after wizard, see the user_input drivers note in /var/lib/ventd/logs/. For laptops: many Dell BIOSes lock fan control entirely — the Latitude 7280 (EC SMM-private) is a confirmed unrecoverable case. The dell-smm-hwmon driver may be loaded read-only."

	lenovoFanNotSpinningGuidance = "Lenovo ThinkPad: ventd uses thinkpad_acpi's fan_control parameter. Ensure /etc/modprobe.d/thinkpad_acpi.conf has \"options thinkpad_acpi fan_control=1\" (the wizard wrote this) and reboot. Lenovo BIOS exposes no per-header configuration; if the fan is stuck after that, the host is likely on a BIOS version that refuses thinkpad_acpi writes — check Lenovo's release notes for fan-control regressions."

	hpFanNotSpinningGuidance = "HP gaming systems (Omen / Victus): mainline hp-wmi handles hotkeys only — fan control needs the omen-fan or omen-fan-control kmod the ventd installer offers to build. Non-gaming HP business laptops generally lack any fan-control surface; the host runs in monitor-only mode and the BIOS owns the curve."
)

// stuckFanCandidate is one hwmon pwm<N>/fan<N>_input pair the
// detector evaluated. Captured into a struct so the test fixture
// and the live walker share one shape.
type stuckFanCandidate struct {
	PWMPath    string
	EnablePath string
	RPMPath    string
	Label      string
	ChipName   string
	Enable     int // 0=unknown, 1=manual, 2=auto, 5=full-on, etc.
	PWM        int
	RPM        int
}

// IsStuck reports whether the candidate matches the "fan not
// spinning" pattern. enable=0 is uncommon and means the channel
// is fully disabled — not a stuck-fan case, ignore.
func (c stuckFanCandidate) IsStuck() bool {
	if c.Enable != 1 && c.Enable != 2 {
		return false
	}
	if c.PWM < stuckFanMinimumPWM {
		return false
	}
	return c.RPM == 0
}

// LabelOrPath returns the friendly label when one is set, otherwise
// the bare pwm path so the Title row stays human-scannable.
func (c stuckFanCandidate) LabelOrPath() string {
	if c.Label != "" {
		return c.Label
	}
	return c.PWMPath
}

// classifyStuck assigns one of the issue-text classification labels
// based on the live pwm_enable byte + persisted exclusion reason.
// The label appears in the Title and also feeds the slog-side
// emission gate.
func classifyStuck(c stuckFanCandidate, exclusionReason string) string {
	switch {
	case strings.HasPrefix(exclusionReason, "calibrate_phantom"):
		return "excluded:cal_aborted"
	case exclusionReason == "no_sensor_correlated":
		return "excluded:detect_failed"
	case exclusionReason == "polarity_phantom":
		return "phantom"
	case c.Enable == 1:
		return "mode_mismatch"
	case c.Enable == 2:
		return "disconnected_suspected"
	}
	return "stuck"
}

// buildStuckFanDetail composes the Detail body from the live state,
// the classification label, the exclusion reason (when present),
// and the per-vendor guidance text. Single string so the renderer
// can lay it out beneath the Title without per-Fact field-juggling.
func buildStuckFanDetail(c stuckFanCandidate, classification, exclusionReason, guidance string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "PWM path: %s (label: %s, chip: %s)\n", c.PWMPath, c.LabelOrPath(), c.ChipName)
	fmt.Fprintf(&b, "Live state: pwm_enable=%d, pwm=%d, rpm=%d.\n", c.Enable, c.PWM, c.RPM)
	fmt.Fprintf(&b, "Classification: %s.\n", classification)
	if exclusionReason != "" {
		fmt.Fprintf(&b, "Wizard excluded this channel from active control. Reason: %s.\n", exclusionReason)
	}
	switch classification {
	case "mode_mismatch":
		b.WriteString("ventd is the configured controller for this channel (pwm_enable=1) but the fan is reporting zero RPM at a duty cycle that should spin most fans. The most common cause is a BIOS chip-mode mismatch — the chip is in PWM mode but the connected fan is voltage-mode (3-pin), or vice versa. Switch the header's drive mode in BIOS to match the fan's connector type, then re-run setup.\n\n")
	case "disconnected_suspected":
		b.WriteString("BIOS auto-control is active on this channel (pwm_enable=2) but the fan reports zero RPM. The BIOS would have spun the fan up if it could see the tach signal, so the most likely cause is a physically disconnected fan, a dead fan, or a 3-pin DC fan whose tach wire was wired to a non-paired header.\n\n")
	case "excluded:detect_failed":
		b.WriteString("The wizard's RPM-detect sweep found no tach response to PWM writes on this channel. Possible causes: BIOS-controlled channel (firmware ignores ventd's writes), disconnected fan, or 3-pin DC fan on a PWM-only header. Verify in BIOS that this header is operator-controllable.\n\n")
	case "excluded:cal_aborted":
		b.WriteString("The wizard's calibration sweep aborted on this channel (sustained-spin check failed). The channel may be reachable but the fan is not — physically check that the fan is connected and spinning when commanded high.\n\n")
	case "phantom":
		b.WriteString("The wizard's polarity probe classified this channel as a phantom (no observable response to writes). Most laptops on this list also surface mirror tach zones for non-existent fans — see the dashboard's \"Show all sensors\" toggle if you want to inspect the raw inventory.\n\n")
	default:
		b.WriteString("The channel reports zero RPM at a duty cycle that should spin most fans. ventd has no further signal to classify the cause; the operator should investigate.\n\n")
	}
	b.WriteString("BIOS guidance for this host: ")
	b.WriteString(guidance)
	return b.String()
}

// walkStuckFanCandidates walks hwmonRoot and collects every
// (pwm<N>, pwm<N>_enable, fan<N>_input) triple where all three
// files are readable. Returns an empty slice on errors (the
// detector treats no candidates as "no signal, emit nothing");
// individual unreadable files cause that channel to be skipped.
func walkStuckFanCandidates(hwmonRoot string) []stuckFanCandidate {
	entries, err := os.ReadDir(hwmonRoot)
	if err != nil {
		return nil
	}
	var out []stuckFanCandidate
	for _, e := range entries {
		chipDir := filepath.Join(hwmonRoot, e.Name())
		chipName := readTrimmedFile(filepath.Join(chipDir, "name"))
		files, err := os.ReadDir(chipDir)
		if err != nil {
			continue
		}
		for _, f := range files {
			name := f.Name()
			if !strings.HasPrefix(name, "pwm") {
				continue
			}
			idxStr := strings.TrimPrefix(name, "pwm")
			if idxStr == "" {
				continue
			}
			if _, err := strconv.Atoi(idxStr); err != nil {
				continue // skip pwmN_enable, pwmN_mode, pwmN_label, …
			}
			pwmPath := filepath.Join(chipDir, name)
			enablePath := pwmPath + "_enable"
			rpmPath := filepath.Join(chipDir, "fan"+idxStr+"_input")
			if _, err := os.Stat(enablePath); err != nil {
				continue
			}
			labelPath := pwmPath + "_label"
			cand := stuckFanCandidate{
				PWMPath:    pwmPath,
				EnablePath: enablePath,
				RPMPath:    rpmPath,
				Label:      readTrimmedFile(labelPath),
				ChipName:   chipName,
				Enable:     readIntFile(enablePath),
				PWM:        readIntFile(pwmPath),
				RPM:        readIntFile(rpmPath),
			}
			out = append(out, cand)
		}
	}
	return out
}

// readTrimmedFile reads a sysfs file and returns its content trimmed
// of trailing whitespace. Empty string on any error.
func readTrimmedFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// readIntFile parses a sysfs file as a decimal integer. Returns 0
// on any read or parse error so the caller's classification logic
// can treat "unreadable" the same as "zero" for the RPM case (a
// missing fan*_input is functionally the same as RPM=0 for the
// stuck-fan check).
func readIntFile(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0
	}
	return n
}
