package orchestrator

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ventd/ventd/internal/recovery"
)

// InventoryArtifact is the structured result of the Inventory phase.
// It captures enough hardware identity for the upcoming CatalogMatch
// phase to look up a known-good profile, and enough hwmon topology for
// DriverPlan to decide whether any OOT modules need installing.
//
// Fields are JSON-tagged in snake_case so the on-disk checkpoint is
// human-readable when an operator inspects state.json directly.
type InventoryArtifact struct {
	BoardVendor   string   `json:"board_vendor"`
	BoardName     string   `json:"board_name"`
	HwmonDevices  []string `json:"hwmon_devices"`            // chip names from /sys/class/hwmon/hwmonN/name
	PWMChannels   int      `json:"pwm_channels"`             // count of pwmN files with a pwmN_enable sibling
	TempChannels  int      `json:"temp_channels"`            // count of tempN_input files
	KernelRelease string   `json:"kernel_release,omitempty"` // uname -r equivalent
}

// InventoryPhase is the first orchestrator phase: a side-effect-free
// hardware inventory. It produces the InventoryArtifact that downstream
// phases consume — never modifies the system.
//
// The phase NEVER fails on "missing hardware" — a host with zero hwmon
// devices is a legitimate state that the upcoming Driver Plan phase
// will resolve (by proposing a chip-appropriate module to load).
// Inventory only returns StatusFailed when the DMI/sysfs reads
// themselves error out unrecoverably.
type InventoryPhase struct{}

// Name identifies this phase in the checkpoint store and the wizard UI.
func (InventoryPhase) Name() string { return "inventory" }

// Execute walks rc.HwmonRoot and the DMI export to populate
// InventoryArtifact. All paths flow through rc so the phase is unit-
// testable with a fixture filesystem.
func (InventoryPhase) Execute(_ context.Context, rc *RunContext) Outcome {
	rc.Sink().Emit("info", "inventory", "scanning DMI and hwmon")

	art := InventoryArtifact{
		BoardVendor: readDMI(rc.HwmonRoot, "board_vendor"),
		BoardName:   readDMI(rc.HwmonRoot, "board_name"),
	}

	devices, pwm, temp, err := scanHwmonTree(rc.HwmonRoot)
	if err != nil {
		return Outcome{
			Status: StatusFailed,
			Class:  recovery.ClassUnknown,
			Detail: "failed to scan " + rc.HwmonRoot + ": " + err.Error(),
		}
	}
	art.HwmonDevices = devices
	art.PWMChannels = pwm
	art.TempChannels = temp
	art.KernelRelease = readKernelRelease(rc.ProcRoot)

	raw, err := EncodeArtifact(art)
	if err != nil {
		return Outcome{
			Status: StatusFailed,
			Class:  recovery.ClassUnknown,
			Detail: "encode artifact: " + err.Error(),
		}
	}

	rc.Log().Info("inventory complete",
		"board_vendor", art.BoardVendor,
		"board_name", art.BoardName,
		"hwmon_devices", len(art.HwmonDevices),
		"pwm_channels", art.PWMChannels,
		"temp_channels", art.TempChannels,
		"kernel", art.KernelRelease)

	return Outcome{
		Status:   StatusSuccess,
		Artifact: raw,
	}
}

// readDMI reads a single DMI field. hwmonRoot is used to derive the
// DMI root via the well-known sibling layout (/sys/class/hwmon and
// /sys/devices/virtual/dmi/id share /sys), so a test fixture can
// override DMI via the same RunContext.HwmonRoot it injects for hwmon.
//
// For the production /sys/class/hwmon root, this resolves to
// /sys/devices/virtual/dmi/id/<field>. Returns "" on any read error
// — DMI is a "nice to have," not a precondition.
func readDMI(hwmonRoot, field string) string {
	sysRoot := deriveSysRoot(hwmonRoot)
	path := filepath.Join(sysRoot, "devices", "virtual", "dmi", "id", field)
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// readKernelRelease reads uname -r equivalent from procRoot. Returns ""
// on any error — caller doesn't gate on it.
func readKernelRelease(procRoot string) string {
	if procRoot == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(procRoot, "sys", "kernel", "osrelease"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// deriveSysRoot maps a hwmonRoot like /sys/class/hwmon back to its
// parent /sys. For test fixtures rooted at <tmp>/sys/class/hwmon, this
// yields <tmp>/sys. Falls back to "/sys" when the input doesn't end in
// the expected suffix.
func deriveSysRoot(hwmonRoot string) string {
	const suffix = "/class/hwmon"
	if strings.HasSuffix(hwmonRoot, suffix) {
		return strings.TrimSuffix(hwmonRoot, suffix)
	}
	return "/sys"
}

// scanHwmonTree walks hwmonRoot/hwmon* directories, collecting chip
// names, controllable PWM channels, and temperature input channels.
// "Controllable" PWM means a pwmN file with a sibling pwmN_enable —
// read-only PWM monitoring values (e.g. nct6683 loaded for an NCT6687D
// chip) are excluded, matching the production countControllablePWM
// semantics in internal/hwmon.
func scanHwmonTree(hwmonRoot string) (devices []string, pwmCount, tempCount int, err error) {
	dirs, err := filepath.Glob(filepath.Join(hwmonRoot, "hwmon*"))
	if err != nil {
		return nil, 0, 0, err
	}
	for _, dir := range dirs {
		if name := readChipName(dir); name != "" {
			devices = append(devices, name)
		}
		pwmCount += countControllablePWM(dir)
		tempCount += countTempInputs(dir)
	}
	sort.Strings(devices)
	return devices, pwmCount, tempCount, nil
}

func readChipName(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "name"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func countControllablePWM(dir string) int {
	pwms, _ := filepath.Glob(filepath.Join(dir, "pwm[0-9]*"))
	n := 0
	for _, p := range pwms {
		// Skip pwm*_enable, pwm*_mode, etc. — only count the bare
		// pwmN file as a candidate, then require the _enable sibling.
		base := filepath.Base(p)
		if strings.ContainsAny(base[3:], "_") {
			continue
		}
		if _, err := os.Stat(p + "_enable"); err == nil {
			n++
		} else if !errors.Is(err, fs.ErrNotExist) {
			// A permission error on /sys is unusual; treat as
			// "not controllable" rather than failing the scan.
			continue
		}
	}
	return n
}

func countTempInputs(dir string) int {
	matches, _ := filepath.Glob(filepath.Join(dir, "temp[0-9]*_input"))
	return len(matches)
}
