package hwmon

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// candidate describes one module-load attempt.
type candidate struct {
	module  string
	options string // space-separated modprobe options; empty if none
}

// armProbeOrder is tried on ARM SBCs (Raspberry Pi, ODROID, etc.) where fans
// are driven by device-tree PWM controllers, not Super I/O chips.
var armProbeOrder = []candidate{
	{module: "pwm-fan"},
	{module: "gpio-fan"},
}

// iteChipForceIDs maps ITE chip names (as reported by sensors-detect) to the
// force_id value needed to make the in-kernel it87 driver recognise the chip.
// Only chips where the driver is capable but won't auto-detect are listed here.
// Chips fully unsupported by the in-kernel driver (IT8688E, IT8689E) need the
// out-of-tree it87 driver instead and are handled via knownDriverNeeds.
var iteChipForceIDs = map[string]string{
	"IT8625E": "force_id=0x8625",
	"IT8628E": "force_id=0x8628",
	"IT8655E": "force_id=0x8655",
	"IT8665E": "force_id=0x8665",
	"IT8686E": "force_id=0x8686",
	"IT8728F": "force_id=0x8728",
	"IT8771E": "force_id=0x8771",
	"IT8772E": "force_id=0x8772",
	"IT8790E": "force_id=0x8790",
	"IT8792E": "force_id=0x8792",
}

// DriverNeed describes a specific out-of-tree kernel driver that must be
// installed for fan control to work on this board.
type DriverNeed struct {
	// Key is a stable identifier used in API calls (e.g. "it8688e", "nct6687d").
	Key string `json:"key"`
	// ChipName is the human-readable chip name (e.g. "IT8688E").
	ChipName string `json:"chip_name"`
	// Explanation is plain English — never shows kernel/hwmon jargon.
	Explanation string `json:"explanation"`
	// RepoURL is the GitHub repo URL (without .git suffix) for downloading the driver source.
	RepoURL string `json:"repo_url"`
	// Branch is the default branch of the repo (e.g. "master", "main").
	Branch string `json:"branch"`
	// Module is the module name to load after installation.
	Module string `json:"module"`
	// MaxSupportedKernel is the last kernel release this driver is known to
	// build against. Empty means unbounded. Used by PreflightOOT to surface a
	// kernel-too-new diagnostic before the build fails.
	MaxSupportedKernel string `json:"max_supported_kernel,omitempty"`
	// DMITriggers lets Tier 3 propose this driver when no Primary/OpenLoop
	// hwmon device was found but the board's DMI identifiers match. Any
	// trigger matching is sufficient. Never causes auto-modprobe — a diagnostic
	// is surfaced and the user clicks to try loading.
	DMITriggers []DMITrigger `json:"-"`
}

// DMITrigger is a conjunction of substring matches against DMIInfo fields.
// Empty fields are wildcards (always match). A trigger with every field empty
// never matches, to avoid accidental blanket proposals. Matching is
// case-insensitive; needles are lowercased at compare time.
type DMITrigger struct {
	BoardVendorContains string
	BoardNameContains   string
	ProductContains     string
	SysVendorContains   string
}

// knownDriverNeeds maps chip detection keys to their DriverNeed definitions.
var knownDriverNeeds = map[string]DriverNeed{
	"it8688e": {
		Key:      "it8688e",
		ChipName: "IT8688E",
		Explanation: "Your board's fan controller chip (IT8688E) requires a driver " +
			"that isn't included in the standard Linux kernel. " +
			"Ventd can install it automatically — this is a one-time step.",
		RepoURL: "https://github.com/frankcrawford/it87",
		Branch:  "master",
		Module:  "it87",
		// Gigabyte AMD boards almost universally route fan headers through an
		// ITE Super I/O the in-kernel it87 driver does not recognise. Board-vendor
		// match is the only reliable signal when the chip exposes no hwmon entry
		// at all (the in-kernel driver simply refuses to bind).
		DMITriggers: []DMITrigger{
			{BoardVendorContains: "gigabyte"},
		},
	},
	"it8689e": {
		Key:      "it8689e",
		ChipName: "IT8689E",
		Explanation: "Your board's fan controller chip (IT8689E) requires a driver " +
			"that isn't included in the standard Linux kernel. " +
			"Ventd can install it automatically — this is a one-time step.",
		RepoURL: "https://github.com/frankcrawford/it87",
		Branch:  "master",
		Module:  "it87",
	},
	"nct6687d": {
		Key:      "nct6687d",
		ChipName: "NCT6687D",
		Explanation: "Your board's fan controller chip (NCT6687D) requires a driver " +
			"that isn't included in the standard Linux kernel. " +
			"Ventd can install it automatically — this is a one-time step.",
		RepoURL: "https://github.com/Fred78290/nct6687d",
		Branch:  "main",
		Module:  "nct6687", // module file is nct6687.ko, not nct6687d.ko
		// NCT6687D is the fan controller on MSI MAG and MPG series boards.
		// DMI board_vendor reports "Micro-Star International Co., Ltd." on most
		// MSI systems; MAG/MPG boards set board_name to include the series.
		// Narrow to MAG/MPG so MSI boards with a classic NCT6775 don't get
		// misrouted. Two triggers cover both DMI vendor spellings seen in the
		// wild.
		DMITriggers: []DMITrigger{
			{BoardVendorContains: "micro-star", BoardNameContains: "mag"},
			{BoardVendorContains: "micro-star", BoardNameContains: "mpg"},
			{BoardVendorContains: "msi", BoardNameContains: "mag"},
			{BoardVendorContains: "msi", BoardNameContains: "mpg"},
		},
	},
}

// HwmonDiagnostics summarises the current hwmon state for display in the
// setup wizard when no PWM channels are found.
type HwmonDiagnostics struct {
	BoardVendor  string       `json:"board_vendor"`
	BoardName    string       `json:"board_name"`
	HwmonDevices []string     `json:"hwmon_devices"` // chip names currently loaded
	PWMCount     int          `json:"pwm_count"`
	TempCount    int          `json:"temp_count"`
	DriverNeeds  []DriverNeed `json:"driver_needs,omitempty"` // out-of-tree drivers required
}

// sensorsDetectResult holds parsed output from sensors-detect.
type sensorsDetectResult struct {
	// modules are the driver modules recommended in the cut-here section.
	modules []candidate
	// detectedChips maps driver names to detected chip names from the summary.
	// The driver may be "to-be-written" for chips with no in-kernel support.
	detectedChips map[string]string
}

// AutoloadModules probes for a kernel module that exposes hwmon PWM channels
// and persists the winning module so the daemon works identically after
// reboots and crashes. Called before the setup wizard on every start.
//
// Probe sequence:
//  1. Fast path: PWM already visible → persist and return.
//  2. Install lm-sensors (if missing) and run sensors-detect --auto;
//     load the modules it recommends, including force_id for ITE chips.
//  3. Dynamic fallback: enumerate hwmon modules from the running kernel
//     (filtering out bus-specific non-fan drivers) and try each one.
//
// If no module produces PWM channels, Diagnose() will identify which
// out-of-tree driver is needed so the web UI can offer a one-click fix.
func AutoloadModules(logger *slog.Logger) {
	// 1. Fast path.
	if existing := findPWMPaths(); len(existing) > 0 {
		logger.Info("hwmon PWM channels already visible, skipping module probe",
			"count", len(existing), "example", existing[0])
		if module := moduleFromPath(existing[0]); module != "" {
			if err := persistModule(module, ""); err != nil {
				logger.Warn("could not persist hwmon module", "module", module, "err", err)
			}
		}
		return
	}

	logger.Info("no hwmon PWM channels found, probing kernel modules")

	// 2. Install lm-sensors and run sensors-detect --auto.
	installLmSensors(logger)
	sdResult := runSensorsDetect(logger)
	if len(sdResult.modules) > 0 {
		if done := tryModuleCandidates(logger, sdResult.modules); done {
			return
		}
		// Check if sensors-detect loaded a module with read-only PWM.
		if paths := findPWMPaths(); len(paths) > 0 && countControllablePWM(paths) == 0 {
			logger.Info("sensors-detect loaded a module but PWM is read-only — " +
				"setup wizard will check for required out-of-tree driver")
			return
		}
	}

	// If sensors-detect identified ITE chips that need force_id, try those next.
	if forceIDs := iteForceIDsFromDetection(sdResult.detectedChips, logger); len(forceIDs) > 0 {
		if done := tryModuleCandidates(logger, forceIDs); done {
			return
		}
	}

	// 3. Dynamic fallback: ARM SBCs use device-tree PWM controllers;
	// everything else gets the full kernel hwmon module scan.
	if runtime.GOARCH == "arm" || runtime.GOARCH == "arm64" {
		tryModuleCandidates(logger, armProbeOrder)
		return
	}

	candidates := enumerateHwmonCandidates(logger)
	if len(candidates) == 0 {
		logger.Warn("no hwmon module candidates found in kernel module directory")
	} else {
		logger.Info("scanning kernel hwmon modules for fan controller", "count", len(candidates))
		tryModuleCandidates(logger, candidates)
	}

	// 4. Nothing worked. Diagnose() will explain why and surface driver needs.
	logger.Warn("no hwmon module produced PWM channels — " +
		"setup wizard will check for required drivers")
}

// tryModuleCandidates loads each candidate module and checks whether it
// exposes controllable PWM channels. Returns true if a winner was found and
// persisted. Leaves the module loaded if it exposes read-only PWM (so that
// Diagnose can read the chip name for out-of-tree driver detection).
func tryModuleCandidates(logger *slog.Logger, candidates []candidate) bool {
	for _, c := range candidates {
		logger.Debug("trying module", "module", c.module, "options", c.options)

		// Unload it87 before each force_id attempt so the new ID takes effect.
		if c.module == "it87" && c.options != "" {
			_ = exec.Command("modprobe", "-r", "it87").Run()
		}

		args := []string{c.module}
		if c.options != "" {
			args = append(args, strings.Fields(c.options)...)
		}
		if out, err := exec.Command("modprobe", args...).CombinedOutput(); err != nil {
			logger.Debug("modprobe failed", "module", c.module, "options", c.options,
				"err", err, "output", strings.TrimSpace(string(out)))
			continue
		}

		var pwmPaths []string
		for i := 0; i < 3; i++ {
			time.Sleep(250 * time.Millisecond)
			pwmPaths = findPWMPaths()
			if len(pwmPaths) > 0 {
				break
			}
		}

		if len(pwmPaths) == 0 {
			// Module loaded but exposed no PWM files at all — not the right chip.
			_ = exec.Command("modprobe", "-r", c.module).Run()
			continue
		}
		if countControllablePWM(pwmPaths) == 0 {
			// PWM files exist but none are controllable (no pwm_enable).
			// Keep the module loaded so Diagnose() can read the hwmon chip name
			// and identify which out-of-tree driver is actually required.
			logger.Info("module exposes read-only PWM — need out-of-tree driver",
				"module", c.module)
			return false
		}

		logger.Info("hwmon module loaded successfully",
			"module", c.module, "options", c.options, "pwm_channels", countControllablePWM(pwmPaths))
		if err := persistModule(c.module, c.options); err != nil {
			logger.Warn("could not persist hwmon module", "module", c.module, "err", err)
		}
		return true
	}
	return false
}

// runSensorsDetect runs sensors-detect --auto and parses its output for
// module recommendations and detected chip names. Returns empty result on failure.
func runSensorsDetect(logger *slog.Logger) sensorsDetectResult {
	path, err := exec.LookPath("sensors-detect")
	if err != nil {
		return sensorsDetectResult{}
	}
	logger.Info("running sensors-detect to identify hardware")
	out, err := exec.Command(path, "--auto").CombinedOutput()
	if err != nil {
		logger.Warn("sensors-detect failed", "err", err,
			"output", strings.TrimSpace(string(out)))
		return sensorsDetectResult{}
	}
	output := string(out)
	logger.Debug("sensors-detect complete", "output", strings.TrimSpace(output))

	result := sensorsDetectResult{
		modules:       parseSensorsDetectModules(output),
		detectedChips: parseSensorsDetectChips(output),
	}
	if len(result.modules) > 0 {
		names := make([]string, len(result.modules))
		for i, m := range result.modules {
			names[i] = m.module
			if m.options != "" {
				names[i] += " " + m.options
			}
		}
		logger.Info("sensors-detect recommends modules", "modules", strings.Join(names, ", "))
	}
	if len(result.detectedChips) > 0 {
		for driver, chip := range result.detectedChips {
			logger.Debug("sensors-detect detected chip", "driver", driver, "chip", chip)
		}
	}
	return result
}

// parseSensorsDetectModules extracts module recommendations from the
// #----cut here---- section of sensors-detect output.
func parseSensorsDetectModules(output string) []candidate {
	optMap := make(map[string]string)
	var modules []string

	inSection := false
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "#----cut here----" {
			if !inSection {
				inSection = true
			} else {
				break // second marker ends the section
			}
			continue
		}
		if !inSection || line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// "options <module> <params>" lines set options for a module.
		if strings.HasPrefix(line, "options ") {
			parts := strings.SplitN(strings.TrimPrefix(line, "options "), " ", 2)
			if len(parts) == 2 {
				optMap[parts[0]] = parts[1]
			}
			continue
		}
		modules = append(modules, line)
	}

	out := make([]candidate, 0, len(modules))
	for _, m := range modules {
		out = append(out, candidate{module: m, options: optMap[m]})
	}
	return out
}

var (
	sdDriverRe = regexp.MustCompile(`^Driver ` + "`" + `([^']+)` + `':`)
	sdChipRe   = regexp.MustCompile(`Chip ` + "`" + `([^']+)` + `'`)
)

// parseSensorsDetectChips extracts the mapping of driver name → chip name
// from the sensors-detect summary section. Driver may be "to-be-written".
func parseSensorsDetectChips(output string) map[string]string {
	chips := make(map[string]string)
	var currentDriver string
	for _, line := range strings.Split(output, "\n") {
		if m := sdDriverRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			currentDriver = m[1]
		} else if currentDriver != "" {
			if m := sdChipRe.FindStringSubmatch(line); m != nil {
				chips[currentDriver] = m[1]
				currentDriver = ""
			}
		}
	}
	return chips
}

// iteForceIDsFromDetection builds force_id candidates for ITE chips that
// sensors-detect detected but whose in-kernel driver needs a force_id override.
// Chips fully unsupported by the in-kernel driver (IT8688E, IT8689E) are
// handled separately via knownDriverNeeds, not force_id.
func iteForceIDsFromDetection(chips map[string]string, logger *slog.Logger) []candidate {
	var candidates []candidate
	for driver, chipName := range chips {
		// sensors-detect lists ITE chips that need force_id under two driver values:
		//   "it87"         — chip is in the sensors-detect DB with a known driver
		//   "to-be-written" — chip is detected at the I/O port but has no driver yet
		//                    (the Perl source marks these as "# it87")
		// Both cases may need force_id if the in-kernel driver doesn't auto-detect.
		if driver != "it87" && driver != "to-be-written" {
			continue
		}
		fields := strings.Fields(chipName)
		if len(fields) == 0 {
			continue
		}
		// Extract bare chip name, e.g. "IT8625E" from "IT8625E Super I/O Sensors".
		bare := strings.ToUpper(fields[0])
		if !strings.HasPrefix(bare, "IT8") {
			continue // not an ITE chip
		}
		if opt, ok := iteChipForceIDs[bare]; ok {
			logger.Debug("will try it87 with force_id for detected ITE chip",
				"chip", bare, "force_id", opt)
			candidates = append(candidates, candidate{module: "it87", options: opt})
		}
	}
	return candidates
}

// enumerateHwmonCandidates returns module names from the running kernel's
// hwmon driver directory, filtered to modules that could be ISA/platform fan
// controllers. Modules with PCI, I2C, SPI, CPU, USB, HID, OF (device-tree),
// or ACPI bus-specific aliases are excluded — those target specific buses and
// won't find Super I/O fan controller hardware.
func enumerateHwmonCandidates(logger *slog.Logger) []candidate {
	release, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		logger.Debug("could not read kernel release", "err", err)
		return nil
	}
	dir := "/lib/modules/" + strings.TrimSpace(string(release)) + "/kernel/drivers/hwmon"

	entries, err := os.ReadDir(dir)
	if err != nil {
		logger.Debug("could not read hwmon module directory", "dir", dir, "err", err)
		return nil
	}

	var candidates []candidate
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		mod := koBasename(e.Name())
		if mod == "" {
			continue
		}
		aliasOut, _ := exec.Command("modinfo", "-F", "alias", mod).Output()
		if isBusSpecificModule(string(aliasOut)) {
			continue
		}
		candidates = append(candidates, candidate{module: mod})
	}
	logger.Debug("enumerated hwmon fan controller candidates", "count", len(candidates))
	return candidates
}

// koBasename strips .ko and compression suffixes from a kernel module filename.
// Returns "" if the name doesn't match any known ko suffix.
func koBasename(filename string) string {
	for _, sfx := range []string{".ko.zst", ".ko.xz", ".ko.gz", ".ko"} {
		if strings.HasSuffix(filename, sfx) {
			return strings.TrimSuffix(filename, sfx)
		}
	}
	return ""
}

// isBusSpecificModule reports whether modinfo alias output indicates the module
// targets a specific non-ISA bus (PCI, I2C, SPI, CPU, USB, HID, OF, ACPI).
// Such modules won't find Super I/O fan controller hardware.
func isBusSpecificModule(aliases string) bool {
	for _, line := range strings.Split(aliases, "\n") {
		line = strings.TrimSpace(line)
		for _, prefix := range []string{
			"pci:", "i2c:", "spi:", "cpu:", "usb:", "hid:", "mdio:", "of:", "acpi",
		} {
			if strings.HasPrefix(line, prefix) {
				return true
			}
		}
	}
	return false
}

// installLmSensors installs the lm-sensors package if sensors-detect is
// not already present. Logs but does not fail — it's best-effort.
func installLmSensors(logger *slog.Logger) {
	if _, err := exec.LookPath("sensors-detect"); err == nil {
		return // already installed
	}

	type pkgCmd struct {
		mgr  string
		args []string
	}
	candidates := []pkgCmd{
		{"apt-get", []string{"install", "-y", "lm-sensors"}},
		{"dnf", []string{"install", "-y", "lm_sensors"}},
		{"yum", []string{"install", "-y", "lm_sensors"}},
		{"pacman", []string{"-S", "--noconfirm", "lm_sensors"}},
		{"zypper", []string{"install", "-y", "sensors"}},
		{"apk", []string{"add", "--no-cache", "lm-sensors"}},
		{"xbps-install", []string{"-y", "lm_sensors"}},
	}

	for _, c := range candidates {
		if _, err := exec.LookPath(c.mgr); err != nil {
			continue
		}
		logger.Info("installing lm-sensors", "package_manager", c.mgr)
		args := append([]string{c.mgr}, c.args...)
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			logger.Warn("lm-sensors install failed", "err", err,
				"output", strings.TrimSpace(string(out)))
		}
		return
	}
}

// Diagnose returns a snapshot of the current hwmon state including any
// out-of-tree drivers that would be needed to gain fan control.
func Diagnose() HwmonDiagnostics {
	d := HwmonDiagnostics{
		BoardVendor: dmiRead("board_vendor"),
		BoardName:   dmiRead("board_name"),
		PWMCount:    countControllablePWM(findPWMPaths()),
	}

	dirs, _ := filepath.Glob("/sys/class/hwmon/hwmon*")
	for _, dir := range dirs {
		name, err := os.ReadFile(filepath.Join(dir, "name"))
		if err == nil {
			d.HwmonDevices = append(d.HwmonDevices, strings.TrimSpace(string(name)))
		}
		temps, _ := filepath.Glob(filepath.Join(dir, "temp*_input"))
		d.TempCount += len(temps)
	}

	if d.PWMCount == 0 {
		d.DriverNeeds = identifyDriverNeeds(d.BoardVendor, d.HwmonDevices)
	}

	return d
}

// countControllablePWM returns how many of the given PWM sysfs paths are
// actually controllable — i.e. they have a companion pwm_enable file.
// Some drivers (e.g. nct6683 loaded for an NCT6687D chip) expose pwmN files
// as read-only monitoring values without any pwm_enable; those are skipped.
func countControllablePWM(pwmPaths []string) int {
	n := 0
	for _, p := range pwmPaths {
		if _, err := os.Stat(p + "_enable"); err == nil {
			n++
		}
	}
	return n
}

// identifyDriverNeeds inspects the currently loaded hwmon chip names (and,
// as a fallback, the board vendor) to determine which out-of-tree drivers are
// required for fan control.
//
// Chip-name checks come first because they are precise. Vendor heuristics are
// a last resort: when IT8688E is present but has no in-kernel driver, no hwmon
// entry with "it8688" in its name will ever appear — only "gigabyte_wmi" or
// similar partial-support entries. In that case the vendor string is the only
// reliable signal.
func identifyDriverNeeds(boardVendor string, hwmonNames []string) []DriverNeed {
	hwmonSet := make(map[string]bool, len(hwmonNames))
	for _, n := range hwmonNames {
		hwmonSet[strings.ToLower(n)] = true
	}

	var needs []DriverNeed
	seen := make(map[string]bool)

	add := func(key string) {
		if !seen[key] {
			if nd, ok := knownDriverNeeds[key]; ok {
				needs = append(needs, nd)
				seen[key] = true
			}
		}
	}

	// NCT6687D: the in-kernel nct6683 driver partially supports it (read-only).
	// The out-of-tree nct6687d module provides full PWM control.
	if hwmonSet["nct6687"] {
		add("nct6687d")
	}

	// IT8688E / IT8689E: detected when an ITE chip is present but the in-kernel
	// it87 driver doesn't recognise it. The loaded module may partially expose
	// the chip as "it87" with no PWM, or the sysfs name may contain the chip ID.
	for name := range hwmonSet {
		if strings.Contains(name, "it8688") {
			add("it8688e")
		}
		if strings.Contains(name, "it8689") {
			add("it8689e")
		}
	}

	// Vendor fallback: IT8688E/IT8689E chips that have no in-kernel driver
	// produce no recognisable hwmon name — only the board vendor is detectable.
	//
	// Gigabyte AMD boards (Aorus, Gaming X, etc.) almost universally use
	// IT8688E for the Super I/O fan headers. The gigabyte_wmi presence gate
	// is dropped: on boards with two Super I/O chips (NCT6687D + IT8688E),
	// the WMI module may not be loaded yet, so the vendor string alone is the
	// reliable signal when no PWM channels have been found.
	//
	// Many ASUS boards use IT8688E for fan headers when asus_ec_sensors is
	// absent — asus_ec_sensors covers their newer embedded-controller-based
	// boards, but older headers fall through to the ITE Super I/O.
	//
	// MSI, ASRock, and Biostar also ship IT8688E on many boards (MAG Z490/Z590,
	// B550, X570, etc.). Extended here from the original Gigabyte/ASUS-only list
	// so that boards with two Super I/O chips get both drivers flagged.
	if !seen["it8688e"] && !seen["it8689e"] {
		vendor := strings.ToLower(boardVendor)
		isGigabyte := strings.Contains(vendor, "gigabyte")
		isASUS := strings.Contains(vendor, "asus") || strings.Contains(vendor, "asustek")
		isMSI := strings.Contains(vendor, "micro-star") || strings.Contains(vendor, "msi")
		isASRock := strings.Contains(vendor, "asrock")
		isBiostar := strings.Contains(vendor, "biostar")

		if isGigabyte {
			add("it8688e")
		}
		if isASUS && !hwmonSet["asus_ec"] && !hwmonSet["asus_ec_sensors"] {
			add("it8688e")
		}
		if isMSI || isASRock || isBiostar {
			add("it8688e")
		}
	}

	return needs
}

// FindPWMPaths is the exported accessor for findPWMPaths.
func FindPWMPaths() []string { return findPWMPaths() }

func findPWMPaths() []string {
	matches, err := filepath.Glob("/sys/class/hwmon/hwmon*/pwm[0-9]*")
	if err != nil {
		return nil
	}
	var pwm []string
	for _, p := range matches {
		base := filepath.Base(p)
		suffix := strings.TrimPrefix(base, "pwm")
		if suffix == "" || strings.ContainsAny(suffix, "_abcdefghijklmnopqrstuvwxyz") {
			continue
		}
		pwm = append(pwm, p)
	}
	return pwm
}

// moduleFromPath infers the kernel module that owns the given hwmon PWM path.
// It first tries the sysfs module symlink (accurate), then falls back to
// inferring from the hwmon chip name.
func moduleFromPath(pwmPath string) string {
	dir := filepath.Dir(pwmPath)

	// Prefer the sysfs symlink: hwmonN/device/driver/module -> <module-name>
	if modLink, err := filepath.EvalSymlinks(filepath.Join(dir, "device", "driver", "module")); err == nil {
		if base := filepath.Base(modLink); base != "" && base != "." {
			return base
		}
	}

	// Fallback: infer from the hwmon chip name file.
	data, err := os.ReadFile(filepath.Join(dir, "name"))
	if err != nil {
		return ""
	}
	name := strings.TrimSpace(string(data))
	switch {
	case strings.HasPrefix(name, "it8"):
		return "it87"
	case strings.HasPrefix(name, "nct677"):
		return "nct6775"
	case strings.HasPrefix(name, "nct678"):
		return "nct6683"
	case strings.HasPrefix(name, "nct6687"):
		return "nct6687d"
	case strings.HasPrefix(name, "w836"):
		return "w83627ehf"
	case strings.HasPrefix(name, "f718"):
		return "f71882fg"
	case name == "asus_ec":
		return "asus_ec_sensors"
	default:
		return ""
	}
}

// persistModule writes the winning module to boot-time configuration.
func persistModule(module, options string) error {
	const (
		loadDir     = "/etc/modules-load.d"
		modprobeDir = "/etc/modprobe.d"
		loadFile    = "/etc/modules-load.d/ventd.conf"
		optFile     = "/etc/modprobe.d/ventd.conf"
		etcModules  = "/etc/modules"
	)

	header := "# Written by ventd — do not edit manually\n"

	if fi, err := os.Stat(loadDir); err == nil && fi.IsDir() {
		if err := os.WriteFile(loadFile, []byte(header+module+"\n"), 0644); err != nil {
			return fmt.Errorf("write %s: %w", loadFile, err)
		}
	} else {
		if err := appendIfMissing(etcModules, module); err != nil {
			return fmt.Errorf("append to %s: %w", etcModules, err)
		}
	}

	if options != "" {
		if fi, err := os.Stat(modprobeDir); err == nil && fi.IsDir() {
			content := header + "options " + module + " " + options + "\n"
			if err := os.WriteFile(optFile, []byte(content), 0644); err != nil {
				return fmt.Errorf("write %s: %w", optFile, err)
			}
		}
	}

	return nil
}

// dmiRead reads a DMI sysfs field. Returns "" on any error.
func dmiRead(field string) string {
	data, err := os.ReadFile("/sys/class/dmi/id/" + field)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func appendIfMissing(file, line string) error {
	data, err := os.ReadFile(file)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, l := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(l) == line {
			return nil
		}
	}
	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = fmt.Fprintf(f, "%s\n", line)
	return err
}
