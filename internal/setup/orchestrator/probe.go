package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ventd/ventd/internal/hwmon"
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
type ProbePhase struct{}

// Name identifies this phase in the checkpoint store and the wizard UI.
func (ProbePhase) Name() string { return "probe" }

// Execute walks rc.HwmonRoot for every pwm<N> with a sibling
// pwm<N>_enable and pairs each with the matching fan<N>_input file
// when present.
func (ProbePhase) Execute(_ context.Context, rc *RunContext) Outcome {
	rc.Sink().Emit("info", "probe", "enumerating controllable PWM channels")

	root := rc.HwmonRoot
	if root == "" {
		root = "/sys/class/hwmon"
	}

	pwmPaths := hwmon.FindPWMPathsAt(root)
	art := ProbeArtifact{HwmonRoot: root}

	for _, p := range pwmPaths {
		enablePath := p + "_enable"
		if _, err := os.Stat(enablePath); err != nil {
			// Read-only PWM monitoring file (e.g. nct6683
			// loaded for an NCT6687D chip). Skip.
			continue
		}

		chipDir := filepath.Dir(p)
		fan := ProbedFan{
			Index:      pwmIndex(p),
			PWMPath:    p,
			EnablePath: enablePath,
			RPMPath:    pairedFanInputPath(chipDir, p),
			ChipName:   readChipNameFromDir(chipDir),
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

	rc.Log().Info("probe complete",
		"hwmon_root", root,
		"controllable_pwms", len(art.Fans))

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
