package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ventd/ventd/internal/hwmon"
	"github.com/ventd/ventd/internal/probe"
	"github.com/ventd/ventd/internal/recovery"
)

// ProbedFan is one entry in the ProbePhase's enumeration of
// controllable fans visible on the host. Carries the minimum
// information ApplyPhase needs to build a default config.yaml plus
// enough identity for the wizard UI to label each fan distinctly.
type ProbedFan struct {
	// Index is a stable per-chip ordinal (the N in pwmN). Used in
	// the default user-facing label when LabelHint is empty.
	Index int `json:"index"`

	// PWMPath is the absolute sysfs path to the pwm<N> file.
	PWMPath string `json:"pwm_path"`

	// EnablePath is the absolute sysfs path to the pwm<N>_enable
	// file. Empty when no _enable sibling exists (read-only PWM —
	// the probe excludes these but the field stays for future
	// extensions).
	EnablePath string `json:"enable_path,omitempty"`

	// InitialEnable is the pwm<N>_enable byte the probe observed
	// before any phase wrote to the channel. ApplyPhase uses this
	// to restore firmware control when a channel is excluded from
	// the applied config (phantom polarity, verify reclassification,
	// monitor-only demotion). Zero when EnablePath is empty or the
	// read failed at probe time — apply treats zero as "do not
	// touch pwm_enable" rather than as a literal write target.
	InitialEnable byte `json:"initial_enable,omitempty"`

	// RPMPath is the absolute sysfs path to the fan<N>_input file
	// for the fan that THIS PWM drives. Best-effort: ProbePhase
	// pairs PWM N with fan N under the same hwmon root. Empty when
	// no matching fan input is visible (DC-only fan or
	// non-standard chip layout).
	RPMPath string `json:"rpm_path,omitempty"`

	// ChipName is the hwmon device's `name` file content (e.g.
	// "nct6687", "coretemp"). Used for fan labelling and to allow
	// the catalog (PR#C1) to look up vendor-specific defaults.
	ChipName string `json:"chip_name"`

	// LabelHint is the best-effort human-facing label. Either a
	// driver-supplied pwm<N>_label content, or a synthesised name
	// like "Chip Fan 1". Operators can override in the wizard UI.
	LabelHint string `json:"label_hint,omitempty"`
}

// ProbeArtifact is the structured result of the ProbePhase. Consumed
// by the ApplyPhase (which builds config.yaml) and the wizard UI
// (which lets the operator label/exclude fans).
type ProbeArtifact struct {
	Fans      []ProbedFan `json:"fans"`
	HwmonRoot string      `json:"hwmon_root"`
	// CPUTDPW is the host CPU package TDP in watts, read from Intel
	// RAPL constraint_0_power_limit_uw at probe time. 0 means
	// unknown (AMD CPUs without amd_energy RAPL, virtualised hosts,
	// kernels without intel-rapl support). ApplyPhase consumes this
	// to scale per-fan curve aggressiveness (#1280) — a 35W mini-PC
	// gets a gentler middle-band rise than a 250W HEDT chip across
	// the same temperature anchors.
	CPUTDPW int `json:"cpu_tdp_w,omitempty"`
	// MonitorChannels is the read-side classification of every
	// `fan*_input` file under HwmonRoot — distinct from Fans (which
	// is only the controllable PWM channels). The minipc HIL case:
	// 1 controllable PWM + 4 fan*_input zones → Fans has 1 entry,
	// MonitorChannels has 4 (1 real + 2 mirrors + 1 phantom). The
	// daemon's hardware-inventory endpoint filters non-real entries
	// out of the dashboard by default; operators reveal them via
	// the `?include_phantoms=1` query param backed by a Settings
	// toggle (#796).
	MonitorChannels []probe.MonitorChannel `json:"monitor_channels,omitempty"`
}

// ProbePhase enumerates controllable PWM channels under HwmonRoot and
// produces a ProbeArtifact. Side-effect-free.
//
// This is the v0.8.x orchestrator's equivalent of the legacy
// Manager.run Phase 4 (Hardware Probe). It produces a strictly
// smaller artifact than Phase 4's FanState[] — no DetectPhase /
// PolarityPhase / CalPhase, because those are owned by later phases.
// The minimum viable artifact: list of {PWMPath, RPMPath, ChipName}
// that ApplyPhase can turn into a usable config.yaml without further
// per-fan work.
//
// The phase succeeds with len(Fans)==0 when no controllable PWMs are
// visible. ApplyPhase (next) handles the empty-fanset case by
// configuring the daemon for monitor-only mode.
type ProbePhase struct {
	// PowercapRoot points at the /sys/class/powercap directory used
	// to read RAPL TDP. Empty falls through to /sys/class/powercap.
	// Tests inject a temp dir.
	PowercapRoot string
}

// Name identifies this phase in the checkpoint store and the wizard UI.
func (ProbePhase) Name() string { return "probe" }

// Execute walks rc.HwmonRoot for every pwm<N> with a sibling
// pwm<N>_enable and pairs each with the matching fan<N>_input file
// when present.
func (p ProbePhase) Execute(_ context.Context, rc *RunContext) Outcome {
	rc.Sink().Emit("info", "probe", "enumerating controllable PWM channels")

	root := rc.HwmonRoot
	if root == "" {
		root = "/sys/class/hwmon"
	}

	pwmPaths := hwmon.FindPWMPathsAt(root)
	art := ProbeArtifact{
		HwmonRoot: root,
		CPUTDPW:   readRAPLTDPW(p.PowercapRoot),
	}

	for _, p := range pwmPaths {
		enablePath := p + "_enable"
		if _, err := os.Stat(enablePath); err != nil {
			// Read-only PWM monitoring file (e.g. nct6683
			// loaded for an NCT6687D chip). Skip.
			continue
		}

		chipDir := filepath.Dir(p)
		fan := ProbedFan{
			Index:         pwmIndex(p),
			PWMPath:       p,
			EnablePath:    enablePath,
			InitialEnable: readProbeEnableByte(enablePath),
			RPMPath:       pairedFanInputPath(chipDir, p),
			ChipName:      readChipNameFromDir(chipDir),
		}
		fan.LabelHint = synthesiseLabel(chipDir, fan.Index, fan.ChipName)
		art.Fans = append(art.Fans, fan)
	}

	sort.Slice(art.Fans, func(i, j int) bool {
		if art.Fans[i].PWMPath != art.Fans[j].PWMPath {
			return art.Fans[i].PWMPath < art.Fans[j].PWMPath
		}
		return art.Fans[i].Index < art.Fans[j].Index
	})

	// Read-side classification of every fan*_input under the same
	// hwmon root (#796). Includes channels with no controllable PWM
	// surface — those are the dashboard "ghost fans" the user wants
	// hidden by default. Synchronous + bounded (default 500ms baseline
	// window) and degrades to nil when sysfs isn't walkable. The
	// pairedPWMs map carries the same-index pairing ProbePhase
	// already discovered above so the classifier can attach a
	// paired_pwm reference per channel.
	pairedPWMs := make(map[string]string, len(art.Fans))
	for _, f := range art.Fans {
		if f.RPMPath != "" {
			pairedPWMs[f.RPMPath] = f.PWMPath
		}
	}
	art.MonitorChannels = enumerateMonitorChannelsAt(root, pairedPWMs)

	rc.Log().Info("probe complete",
		"hwmon_root", root,
		"controllable_pwms", len(art.Fans),
		"monitor_channels", len(art.MonitorChannels))

	raw, err := EncodeArtifact(art)
	if err != nil {
		return Outcome{
			Status: StatusFailed,
			Class:  recovery.ClassUnknown,
			Detail: "encode artifact: " + err.Error(),
		}
	}
	return Outcome{Status: StatusSuccess, Artifact: raw}
}

// pwmIndex extracts the N from a pwm<N> path.
func pwmIndex(pwmPath string) int {
	base := filepath.Base(pwmPath)
	if !strings.HasPrefix(base, "pwm") {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimPrefix(base, "pwm"))
	if err != nil {
		return 0
	}
	return n
}

// pairedFanInputPath returns the sysfs path of the fan<N>_input file
// that corresponds to pwm<N>'s fan, when one is visible under the
// same hwmon chip dir. Best-effort: empty when no match.
func pairedFanInputPath(chipDir, pwmPath string) string {
	idx := pwmIndex(pwmPath)
	if idx <= 0 {
		return ""
	}
	candidate := filepath.Join(chipDir, "fan"+strconv.Itoa(idx)+"_input")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return ""
}

// readProbeEnableByte reads pwm<N>_enable at probe time so ApplyPhase
// has a deterministic restore target if the channel is later excluded
// from the applied config. Returns 0 on any read or parse failure;
// callers treat 0 as "do not touch pwm_enable" so the daemon's
// watchdog stays the authoritative restoration path in that case.
func readProbeEnableByte(enablePath string) byte {
	if enablePath == "" {
		return 0
	}
	b, err := os.ReadFile(enablePath)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0
	}
	if n < 0 || n > 255 {
		return 0
	}
	return byte(n)
}

// readRAPLTDPW reads the CPU package TDP in watts from Intel RAPL.
// Returns 0 when RAPL is unavailable (AMD CPUs without amd_energy,
// virtualised hosts, kernels without intel-rapl, sysfs unreadable).
// Tries the two layouts kernel versions have used for the
// intel-rapl:0 package node.
//
// powercapRoot is normally /sys/class/powercap; tests inject a
// fixture dir.
func readRAPLTDPW(powercapRoot string) int {
	if powercapRoot == "" {
		powercapRoot = "/sys/class/powercap"
	}
	for _, p := range []string{
		filepath.Join(powercapRoot, "intel-rapl", "intel-rapl:0", "constraint_0_power_limit_uw"),
		filepath.Join(powercapRoot, "intel-rapl:0", "constraint_0_power_limit_uw"),
	} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		uw, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
		if err == nil && uw > 0 {
			return int(uw / 1_000_000)
		}
	}
	return 0
}

// readChipNameFromDir reads the hwmon `name` file. Empty on read
// failure (the phase's overall result is still useful — chip name is
// labelling, not gating).
func readChipNameFromDir(chipDir string) string {
	b, err := os.ReadFile(filepath.Join(chipDir, "name"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// enumerateMonitorChannelsAt classifies every `fan*_input` file under
// an absolute hwmon root via probe.EnumerateMonitorChannels. The
// orchestrator works in absolute paths (rc.HwmonRoot is "/sys/class/
// hwmon" in production, a t.TempDir()/sys/class/hwmon in tests); the
// classifier's fs.FS API expects a relative path under a sys-rooted
// filesystem. This helper bridges by mounting os.DirFS at "/" and
// stripping the leading slash from hwmonAbsRoot. The TachReader uses
// the same hwmon.ReadRPMPath the live runtime does so test fixtures
// staged on real disk read back the staged RPM values without
// trapdoors. Returns nil silently when the root is unreadable —
// classification is best-effort; an inability to enumerate
// monitor channels is not an error worth failing ProbePhase over.
func enumerateMonitorChannelsAt(hwmonAbsRoot string, pairedPWMs map[string]string) []probe.MonitorChannel {
	if hwmonAbsRoot == "" {
		return nil
	}
	rel := strings.TrimPrefix(hwmonAbsRoot, "/")
	if rel == "" {
		return nil
	}
	sysFS := os.DirFS("/")
	read := func(p string) (int, error) { return hwmon.ReadRPMPath(p) }
	// sysAbsRoot="/" because sysFS is mounted at the filesystem root,
	// so chipDir is the absolute path with the leading slash stripped.
	// TachPath comes back as "/<chipDir>/<name>" — directly openable
	// by hwmon.ReadRPMPath in both production (`/sys/...`) and tests
	// (`/tmp/X/sys/...`).
	return probe.EnumerateMonitorChannels(sysFS, rel, "/", read, pairedPWMs)
}

// synthesiseLabel produces a best-effort human-facing fan label.
// Reads pwm<N>_label when the driver provides one (some Super-I/O
// chips do); falls back to "<chip-name-titlecase> Fan <N>".
func synthesiseLabel(chipDir string, index int, chipName string) string {
	labelPath := filepath.Join(chipDir, "pwm"+strconv.Itoa(index)+"_label")
	if b, err := os.ReadFile(labelPath); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			return s
		}
	}
	display := chipName
	if display == "" {
		display = "Fan"
	} else {
		// Title-case the first letter so "nct6687" becomes "Nct6687"
		// for the default label. Operators almost always relabel
		// anyway in the wizard UI; this is just the fallback.
		display = strings.ToUpper(display[:1]) + display[1:]
	}
	return display + " Fan " + strconv.Itoa(index)
}
