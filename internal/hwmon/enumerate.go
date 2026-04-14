package hwmon

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// DefaultHwmonRoot is the standard sysfs hwmon class directory. Tests pass an
// alternate root into EnumerateDevices to classify against a synthetic tree.
const DefaultHwmonRoot = "/sys/class/hwmon"

// CapabilityClass names the fan-control shape of an hwmon device. It is a
// string type so diagnostic JSON surfaces human-readable values directly
// instead of opaque integer codes.
type CapabilityClass string

const (
	// ClassPrimary — the device exposes at least one pwmN + pwmN_enable + a
	// matching fanN_input. These are the devices setup should try to control.
	// NOTE: ClassPrimary means "candidate for writability test"; the classifier
	// only checks file existence, not that writes succeed. Setup must still run
	// its writability probe before treating a channel as controllable.
	ClassPrimary CapabilityClass = "primary"

	// ClassOpenLoop — pwmN + pwmN_enable present but no fanN_input for that
	// index. Writing PWM works but RPM cannot be read back for calibration.
	ClassOpenLoop CapabilityClass = "open-loop"

	// ClassReadOnly — fanN_input present but no controllable pwmN_enable for
	// any index. Typical of BIOS-managed headers or partial-support drivers
	// (e.g. nct6683 loaded against an NCT6687D chip). Surfaces as a
	// "BIOS-managed" badge; not usable for control, but the RPM read tells
	// the UI the fan exists.
	ClassReadOnly CapabilityClass = "readonly"

	// ClassNoFans — neither controllable PWM nor fanN_input. These devices are
	// temperature-only sensors and are filtered out of the primary candidate
	// list.
	ClassNoFans CapabilityClass = "nofans"

	// ClassSkipNVIDIA — hwmon entry belongs to an NVIDIA GPU. Fan control goes
	// through NVML, not sysfs; the capability pass records it only so tests
	// can assert consistent handling.
	ClassSkipNVIDIA CapabilityClass = "skip-nvidia"
)

// RPMTarget describes an fan*_target channel — an RPM setpoint controller
// (pre-RDNA AMD GPUs). Kept separate from PWM channels because the write
// semantics differ (RPM integer, not 0–255 duty cycle).
type RPMTarget struct {
	Path     string // full path to fanN_target
	Index    string // channel number as a string ("1", "2", …)
	InputPath string // companion fanN_input
}

// PWMChannel describes one pwmN + pwmN_enable pair on a device. EnableExists
// reflects file presence only; the file may still reject writes (some drivers
// lock pwm_enable to automatic mode).
type PWMChannel struct {
	Path        string // full path to pwmN
	EnablePath  string // full path to pwmN_enable (empty string if missing)
	Index       string // channel number as a string ("1", "2", …)
	FanInput    string // companion fanN_input (empty if no RPM readback)
}

// HwmonDevice is one /sys/class/hwmon/hwmonN entry with its classified
// capabilities. Discovery is read-only: every field is derived from
// os.Stat / os.ReadFile. Writability testing lives in setup.
type HwmonDevice struct {
	Dir          string          // e.g. /sys/class/hwmon/hwmon3
	ChipName     string          // contents of hwmonN/name, trimmed
	StableDevice string          // StableDevice(Dir) — hwmonX-renumbering-safe
	Class        CapabilityClass // capability bucket
	PWM          []PWMChannel    // pwmN channels found (any class)
	RPMTargets   []RPMTarget     // fanN_target channels found
	FanInputs    []string        // fanN_input paths found (any class)
	TempInputs   []string        // tempN_input paths found
}

var pwmChannelRe = regexp.MustCompile(`^pwm(\d+)$`)
var fanInputRe = regexp.MustCompile(`^fan(\d+)_input$`)
var fanTargetRe = regexp.MustCompile(`^fan(\d+)_target$`)
var tempInputRe = regexp.MustCompile(`^temp(\d+)_input$`)

// EnumerateDevices walks root (normally DefaultHwmonRoot), inspects every
// hwmonN entry, and returns one HwmonDevice per directory with its capability
// class set. The returned slice is sorted by directory name numerically, so
// hwmon2 precedes hwmon10.
//
// The function never touches hardware state — no writes, no module loads, no
// mutation of pwm_enable. Safe to call on every daemon start and during
// periodic rescan without side effects.
func EnumerateDevices(root string) []HwmonDevice {
	if root == "" {
		root = DefaultHwmonRoot
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}

	var devices []HwmonDevice
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "hwmon") {
			continue
		}
		dir := filepath.Join(root, e.Name())
		devices = append(devices, classifyDevice(dir))
	}

	sort.Slice(devices, func(i, j int) bool {
		return hwmonIndex(devices[i].Dir) < hwmonIndex(devices[j].Dir)
	})
	return devices
}

// classifyDevice inspects one hwmonN directory and builds its HwmonDevice.
// Classification rules (in order):
//  1. chip name == "nvidia"                                 → ClassSkipNVIDIA
//  2. any pwmN with pwmN_enable AND matching fanN_input     → ClassPrimary
//  3. any pwmN with pwmN_enable, no fanN_input at that idx  → ClassOpenLoop
//  4. fanN_input present, no controllable pwmN_enable       → ClassReadOnly
//  5. none of the above                                     → ClassNoFans
//
// fanN_target controllers (pre-RDNA AMD) follow the same rules via their
// companion pwmN_enable — when pwm controllability is present for the same
// index, the device is ClassPrimary; when it is not, the _target is reported
// but the device is ClassOpenLoop or ClassReadOnly depending on fanN_input.
func classifyDevice(dir string) HwmonDevice {
	dev := HwmonDevice{
		Dir:          dir,
		ChipName:     strings.TrimSpace(readFileOrEmpty(filepath.Join(dir, "name"))),
		StableDevice: StableDevice(dir),
	}

	if dev.ChipName == "nvidia" {
		dev.Class = ClassSkipNVIDIA
		return dev
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		dev.Class = ClassNoFans
		return dev
	}

	pwmIdx := map[string]bool{}       // channel nums with pwmN file
	pwmEnableIdx := map[string]bool{} // channel nums with pwmN_enable file
	fanInputIdx := map[string]bool{}  // channel nums with fanN_input

	for _, e := range entries {
		name := e.Name()
		switch {
		case pwmChannelRe.MatchString(name):
			idx := pwmChannelRe.FindStringSubmatch(name)[1]
			pwmIdx[idx] = true
		case strings.HasPrefix(name, "pwm") && strings.HasSuffix(name, "_enable"):
			mid := strings.TrimSuffix(strings.TrimPrefix(name, "pwm"), "_enable")
			if _, err := strconv.Atoi(mid); err == nil {
				pwmEnableIdx[mid] = true
			}
		case fanInputRe.MatchString(name):
			idx := fanInputRe.FindStringSubmatch(name)[1]
			fanInputIdx[idx] = true
			dev.FanInputs = append(dev.FanInputs, filepath.Join(dir, name))
		case fanTargetRe.MatchString(name):
			idx := fanTargetRe.FindStringSubmatch(name)[1]
			dev.RPMTargets = append(dev.RPMTargets, RPMTarget{
				Path:      filepath.Join(dir, name),
				Index:     idx,
				InputPath: filepath.Join(dir, "fan"+idx+"_input"),
			})
		case tempInputRe.MatchString(name):
			dev.TempInputs = append(dev.TempInputs, filepath.Join(dir, name))
		}
	}

	for idx := range pwmIdx {
		ch := PWMChannel{
			Path:  filepath.Join(dir, "pwm"+idx),
			Index: idx,
		}
		if pwmEnableIdx[idx] {
			ch.EnablePath = filepath.Join(dir, "pwm"+idx+"_enable")
		}
		if fanInputIdx[idx] {
			ch.FanInput = filepath.Join(dir, "fan"+idx+"_input")
		}
		dev.PWM = append(dev.PWM, ch)
	}
	sort.Slice(dev.PWM, func(i, j int) bool { return numLess(dev.PWM[i].Index, dev.PWM[j].Index) })
	sort.Slice(dev.RPMTargets, func(i, j int) bool {
		return numLess(dev.RPMTargets[i].Index, dev.RPMTargets[j].Index)
	})
	sort.Strings(dev.FanInputs)
	sort.Strings(dev.TempInputs)

	controllablePrimary := false
	controllableOpenLoop := false
	for idx := range pwmIdx {
		if !pwmEnableIdx[idx] {
			continue
		}
		if fanInputIdx[idx] {
			controllablePrimary = true
		} else {
			controllableOpenLoop = true
		}
	}

	switch {
	case controllablePrimary:
		dev.Class = ClassPrimary
	case controllableOpenLoop:
		dev.Class = ClassOpenLoop
	case len(dev.FanInputs) > 0 || len(dev.RPMTargets) > 0:
		dev.Class = ClassReadOnly
	default:
		dev.Class = ClassNoFans
	}
	return dev
}

// readFileOrEmpty returns the file contents or "" on any read error.
func readFileOrEmpty(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

// hwmonIndex extracts the integer N from .../hwmonN for numeric sort.
// Returns a very large value on parse failure so malformed entries sort last.
func hwmonIndex(dir string) int {
	base := filepath.Base(dir)
	n, err := strconv.Atoi(strings.TrimPrefix(base, "hwmon"))
	if err != nil {
		return 1 << 30
	}
	return n
}

func numLess(a, b string) bool {
	ai, _ := strconv.Atoi(a)
	bi, _ := strconv.Atoi(b)
	return ai < bi
}
