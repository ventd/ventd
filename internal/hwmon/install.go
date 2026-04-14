package hwmon

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ErrRebootRequired is returned by InstallDriver when the driver could not
// load due to an ACPI resource conflict. The fix (e.g. kernel boot parameter)
// has already been applied — the user just needs to reboot.
type ErrRebootRequired struct {
	// Message is shown verbatim in the web UI.
	Message string
}

func (e *ErrRebootRequired) Error() string { return e.Message }

// InstallDriver installs the out-of-tree kernel driver identified by chipKey
// (e.g. "it8688e", "nct6687d") and loads it via modprobe.
//
// logFn is called with each human-readable progress line so the caller can
// stream it to the web UI. It is called from a single goroutine.
//
// Returns nil on success (PWM paths visible after modprobe).
func InstallDriver(chipKey string, logFn func(string), logger *slog.Logger) error {
	nd, ok := knownDriverNeeds[chipKey]
	if !ok {
		return fmt.Errorf("unknown driver key: %s", chipKey)
	}

	log := func(msg string) {
		logger.Info("driver install: " + msg)
		logFn(msg)
	}

	// ── Step 1: ensure build tools are present ──────────────────────────────
	log("Checking build tools...")
	if err := ensureBuildTools(log); err != nil {
		return fmt.Errorf("build tools: %w", err)
	}

	// ── Step 2: download the driver source ─────────────────────────────────
	tmpDir, err := os.MkdirTemp("", "ventd-driver-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	repoDir := filepath.Join(tmpDir, "driver")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		return fmt.Errorf("create repo dir: %w", err)
	}

	log("Downloading driver source for " + nd.ChipName + "...")
	// Download a tarball instead of git clone — works without credentials
	// for any public GitHub repo and requires only curl or wget.
	branch := nd.Branch
	if branch == "" {
		branch = "master"
	}
	tarURL := nd.RepoURL + "/archive/refs/heads/" + branch + ".tar.gz"
	tarFile := filepath.Join(tmpDir, "driver.tar.gz")
	if err := downloadFile(tarURL, tarFile, log); err != nil {
		return fmt.Errorf("download driver source: %w", err)
	}
	log("Extracting source...")
	if err := runLogDir(tmpDir, log, "tar", "xzf", tarFile, "--strip-components=1", "-C", repoDir); err != nil {
		return fmt.Errorf("extract tarball: %w", err)
	}

	// ── Step 3: build ───────────────────────────────────────────────────────
	log("Building driver (this may take a minute)...")
	if err := runLogDir(repoDir, log, "make"); err != nil {
		return fmt.Errorf("make: %w", err)
	}

	// ── Step 4: install ─────────────────────────────────────────────────────
	log("Installing driver...")
	if err := runLogDir(repoDir, log, "make", "install"); err != nil {
		return fmt.Errorf("make install: %w", err)
	}

	// ── Step 5: register with DKMS (best-effort) ────────────────────────────
	registerDKMS(repoDir, nd, log, logger)

	// ── Step 6: update module index ─────────────────────────────────────────
	log("Updating module index...")
	_ = exec.Command("depmod", "-a").Run()

	// ── Step 7: load the module ─────────────────────────────────────────────
	log("Loading driver...")
	// Unload any previous failed attempt first.
	_ = exec.Command("modprobe", "-r", nd.Module).Run()
	if out, err := exec.Command("modprobe", nd.Module).CombinedOutput(); err != nil {
		outStr := strings.TrimSpace(string(out))
		if strings.Contains(outStr, "resource busy") {
			// ACPI has claimed the fan controller's I/O ports.
			// Auto-apply the standard fix and ask the user to reboot.
			log("ACPI resource conflict detected — updating boot configuration...")
			bootErr, manualInstr := addKernelParam("acpi_enforce_resources=lax", log)
			if bootErr != nil {
				logger.Warn("could not auto-patch bootloader", "err", bootErr)
				return &ErrRebootRequired{
					Message: "Your system firmware (ACPI) has reserved the " + nd.ChipName +
						" fan controller's hardware ports. Auto-patching the bootloader failed (" + bootErr.Error() + "). " +
						manualInstr + " Then reboot.",
				}
			}
			return &ErrRebootRequired{
				Message: "Your system firmware (ACPI) had reserved the " + nd.ChipName +
					" fan controller's hardware ports. We've updated your boot configuration to fix this. " +
					"Click Reboot Now to continue — setup will resume automatically after reboot.",
			}
		}
		return fmt.Errorf("modprobe %s: %w\n%s", nd.Module, err, outStr)
	}

	// ── Step 8: verify PWM channels appeared ────────────────────────────────
	var pwmPaths []string
	for i := 0; i < 6; i++ {
		time.Sleep(250 * time.Millisecond)
		pwmPaths = findPWMPaths()
		if countControllablePWM(pwmPaths) > 0 {
			break
		}
	}
	if countControllablePWM(pwmPaths) == 0 {
		return fmt.Errorf("driver installed but no controllable fan channels appeared — " +
			"your board may use a different chip variant")
	}

	log(fmt.Sprintf("Driver installed successfully — found %d fan controller channel(s).", countControllablePWM(pwmPaths)))

	// ── Step 9: persist ─────────────────────────────────────────────────────
	if err := persistModule(nd.Module, ""); err != nil {
		logger.Warn("could not persist module after install", "module", nd.Module, "err", err)
	}

	return nil
}

// registerDKMS registers the driver source with DKMS so the module rebuilds
// automatically on kernel updates. It is best-effort: failures are logged but
// never returned as errors. No-op when dkms is not installed.
//
// Both the it87 and nct6687d repos ship a dkms.conf; if the repo somehow
// lacks one, a minimal file is generated from the DriverNeed metadata.
func registerDKMS(repoDir string, nd DriverNeed, log func(string), logger *slog.Logger) {
	if _, err := exec.LookPath("dkms"); err != nil {
		return // DKMS not installed — skip silently
	}

	// Ensure dkms.conf is present; create a minimal one if the repo omits it.
	confPath := filepath.Join(repoDir, "dkms.conf")
	pkgName, pkgVersion := nd.Module, "1.0"
	if data, err := os.ReadFile(confPath); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if v, ok := strings.CutPrefix(line, "PACKAGE_NAME="); ok {
				pkgName = strings.Trim(v, `"`)
			}
			if v, ok := strings.CutPrefix(line, "PACKAGE_VERSION="); ok {
				pkgVersion = strings.Trim(v, `"`)
			}
		}
	} else {
		// Write a minimal dkms.conf so dkms add can proceed.
		conf := fmt.Sprintf(
			"PACKAGE_NAME=%q\nPACKAGE_VERSION=%q\n"+
				"BUILT_MODULE_NAME[0]=%q\n"+
				"DEST_MODULE_LOCATION[0]=\"/kernel/drivers/hwmon\"\n"+
				"MAKE[0]=\"make\"\nCLEAN=\"make clean\"\nAUTOINSTALL=\"yes\"\n",
			pkgName, pkgVersion, pkgName)
		if err := os.WriteFile(confPath, []byte(conf), 0644); err != nil {
			logger.Warn("DKMS: could not write dkms.conf", "err", err)
			return
		}
	}

	nameVer := pkgName + "/" + pkgVersion
	log("Registering with DKMS (" + nameVer + ") for automatic rebuild on kernel updates...")

	if err := runLogDir(repoDir, log, "dkms", "add", repoDir); err != nil {
		logger.Warn("DKMS add failed — module will not auto-rebuild on kernel update", "err", err)
		return
	}
	if err := runLogDir("", log, "dkms", "build", nameVer); err != nil {
		logger.Warn("DKMS build failed", "err", err)
		return
	}
	if err := runLogDir("", log, "dkms", "install", nameVer); err != nil {
		logger.Warn("DKMS install failed", "err", err)
	}
}

// ensureBuildTools installs make, gcc, and kernel headers if any are missing.
func ensureBuildTools(log func(string)) error {
	needed := []string{}
	for _, tool := range []string{"make", "gcc"} {
		if _, err := exec.LookPath(tool); err != nil {
			needed = append(needed, tool)
		}
	}
	if len(needed) > 0 {
		log("Installing build tools: " + strings.Join(needed, ", ") + "...")

		type pkgCmd struct {
			mgr     string
			pkgArgs func(tools []string) []string
		}
		managers := []pkgCmd{
			{"apt-get", func(t []string) []string {
				return append([]string{"install", "-y"}, buildPkgNames("apt", t)...)
			}},
			{"dnf", func(t []string) []string {
				return append([]string{"install", "-y"}, buildPkgNames("dnf", t)...)
			}},
			{"yum", func(t []string) []string {
				return append([]string{"install", "-y"}, buildPkgNames("dnf", t)...)
			}},
			{"pacman", func(t []string) []string {
				return append([]string{"-S", "--noconfirm"}, buildPkgNames("pacman", t)...)
			}},
			{"zypper", func(t []string) []string {
				return append([]string{"install", "-y"}, buildPkgNames("zypper", t)...)
			}},
			{"apk", func(t []string) []string {
				return append([]string{"add", "--no-cache"}, buildPkgNames("apk", t)...)
			}},
		}

		installed := false
		for _, m := range managers {
			if _, err := exec.LookPath(m.mgr); err != nil {
				continue
			}
			args := m.pkgArgs(needed)
			if out, err := exec.Command(m.mgr, args...).CombinedOutput(); err != nil {
				return fmt.Errorf("%s install failed: %w\n%s", m.mgr, err, strings.TrimSpace(string(out)))
			}
			installed = true
			break
		}
		if !installed {
			return fmt.Errorf("could not detect package manager to install build tools")
		}
	}

	// Always ensure kernel headers are present — they may be missing even if
	// make/gcc are installed (e.g. a fresh Proxmox VE install).
	return ensureKernelHeaders(log)
}

// ensureKernelHeaders installs kernel headers needed to build kernel modules.
// It is a no-op if the build symlink already exists.
func ensureKernelHeaders(log func(string)) error {
	uname, _ := exec.Command("uname", "-r").Output()
	kernelRelease := strings.TrimSpace(string(uname))

	// Fast path: if /lib/modules/<release>/build exists, headers are installed.
	buildDir := "/lib/modules/" + kernelRelease + "/build"
	if _, err := os.Stat(buildDir); err == nil {
		return nil
	}

	log("Installing kernel headers for " + kernelRelease + "...")

	// Determine the correct header package name.
	// Proxmox VE kernels (e.g. 6.x.y-z-pve) use pve-headers-<release>.
	// Standard Debian/Ubuntu use linux-headers-<release>.
	var aptHeaderPkg string
	if strings.HasSuffix(kernelRelease, "-pve") {
		aptHeaderPkg = "pve-headers-" + kernelRelease
	} else {
		aptHeaderPkg = "linux-headers-" + kernelRelease
	}

	// Kernel header package names vary by distro.
	type headerCmd struct {
		mgr  string
		args []string
	}

	// Try distro-specific approaches.
	cmds := []headerCmd{
		// Debian/Ubuntu/Proxmox VE
		{"apt-get", []string{"install", "-y", aptHeaderPkg, "build-essential"}},
		// Fedora/RHEL: kernel-devel
		{"dnf", []string{"install", "-y", "kernel-devel", "kernel-headers", "gcc", "make"}},
		{"yum", []string{"install", "-y", "kernel-devel", "kernel-headers", "gcc", "make"}},
		// Arch: linux-headers
		{"pacman", []string{"-S", "--noconfirm", "linux-headers", "base-devel"}},
		// openSUSE
		{"zypper", []string{"install", "-y", "kernel-devel", "gcc", "make"}},
		// Alpine
		{"apk", []string{"add", "--no-cache", "linux-headers", "gcc", "make", "musl-dev"}},
	}

	for _, c := range cmds {
		if _, err := exec.LookPath(c.mgr); err != nil {
			continue
		}
		out, err := exec.Command(c.mgr, c.args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("kernel headers install failed: %w\n%s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}

	return fmt.Errorf("could not detect package manager to install kernel headers")
}

// buildPkgNames maps tool names to package names per package manager.
func buildPkgNames(mgr string, tools []string) []string {
	pkgMap := map[string]map[string]string{
		"apt": {
			"git":  "git",
			"make": "build-essential",
			"gcc":  "build-essential",
		},
		"dnf": {
			"git":  "git",
			"make": "make",
			"gcc":  "gcc",
		},
		"pacman": {
			"git":  "git",
			"make": "base-devel",
			"gcc":  "base-devel",
		},
		"zypper": {
			"git":  "git",
			"make": "make",
			"gcc":  "gcc",
		},
		"apk": {
			"git":  "git",
			"make": "make",
			"gcc":  "gcc",
		},
	}

	seen := make(map[string]bool)
	var pkgs []string
	for _, tool := range tools {
		name := tool
		if m, ok := pkgMap[mgr]; ok {
			if pkg, ok := m[tool]; ok {
				name = pkg
			}
		}
		if !seen[name] {
			pkgs = append(pkgs, name)
			seen[name] = true
		}
	}
	return pkgs
}

// downloadFile downloads url to destPath using curl or wget.
func downloadFile(url, destPath string, logFn func(string)) error {
	if _, err := exec.LookPath("curl"); err == nil {
		return runLogDir("", logFn, "curl", "-fsSL", "-o", destPath, url)
	}
	if _, err := exec.LookPath("wget"); err == nil {
		return runLogDir("", logFn, "wget", "-q", "-O", destPath, url)
	}
	return fmt.Errorf("neither curl nor wget found — cannot download driver source")
}

// addKernelParam adds a kernel boot parameter to whatever bootloader this
// system uses. It tries GRUB first, then systemd-boot, and returns both an
// error (nil on success) and a manual-instruction string (used in the error
// message when auto-patching fails so the user knows exactly what to do).
func addKernelParam(param string, log func(string)) (err error, manualInstr string) {
	// Detect bootloader by examining known config paths.
	_, grubFileErr := os.Stat("/etc/default/grub")
	_, sdbootErr := os.Stat("/boot/loader/loader.conf")
	if sdbootErr != nil {
		// Also check EFI mount at /efi (common on Arch).
		_, sdbootErr = os.Stat("/efi/loader/loader.conf")
	}

	switch {
	case grubFileErr == nil:
		return addGRUBParam(param, log),
			"Add " + param + " to GRUB_CMDLINE_LINUX_DEFAULT in /etc/default/grub and run update-grub."

	case sdbootErr == nil:
		return addSystemdBootParam(param, log),
			"Add " + param + " to the options line of your active entry in /boot/loader/entries/ and run bootctl update."

	default:
		// Unknown bootloader — provide generic instructions.
		return fmt.Errorf("no supported bootloader config found (/etc/default/grub and /boot/loader/loader.conf both absent)"),
			"Add the kernel parameter " + param + " to your bootloader's kernel command line."
	}
}

// addGRUBParam inserts param into GRUB_CMDLINE_LINUX_DEFAULT (or
// GRUB_CMDLINE_LINUX as fallback) in /etc/default/grub, then regenerates
// the GRUB config using the distro's update command.
func addGRUBParam(param string, log func(string)) error {
	const grubFile = "/etc/default/grub"
	data, err := os.ReadFile(grubFile)
	if err != nil {
		return fmt.Errorf("read %s: %w", grubFile, err)
	}
	content := string(data)

	if strings.Contains(content, param) {
		log("Boot parameter already set, skipping GRUB update.")
		return nil // idempotent
	}

	// Find GRUB_CMDLINE_LINUX_DEFAULT="..." or GRUB_CMDLINE_LINUX="..."
	// and append the parameter before the closing quote.
	patched := false
	for _, key := range []string{`GRUB_CMDLINE_LINUX_DEFAULT="`, `GRUB_CMDLINE_LINUX="`} {
		idx := strings.Index(content, key)
		if idx == -1 {
			continue
		}
		start := idx + len(key)
		end := strings.Index(content[start:], `"`)
		if end == -1 {
			continue
		}
		end += start
		existing := content[start:end]
		var newVal string
		if existing == "" {
			newVal = param
		} else {
			newVal = existing + " " + param
		}
		content = content[:start] + newVal + content[end:]
		patched = true
		break
	}
	if !patched {
		// No GRUB_CMDLINE_ line found — append a new one.
		content += "\nGRUB_CMDLINE_LINUX_DEFAULT=\"" + param + "\"\n"
	}

	if err := os.WriteFile(grubFile, []byte(content), 0644); err != nil {
		return fmt.Errorf("write %s: %w", grubFile, err)
	}
	log("Wrote " + grubFile)

	// Run the distro's grub-update command.
	type grubCmd struct {
		bin  string
		args []string
	}
	candidates := []grubCmd{
		{"update-grub", nil},                                                   // Debian/Ubuntu/Proxmox
		{"grub2-mkconfig", []string{"-o", "/boot/grub2/grub.cfg"}},            // Fedora/RHEL BIOS
		{"grub2-mkconfig", []string{"-o", "/boot/efi/EFI/fedora/grub.cfg"}},   // Fedora EFI
		{"grub-mkconfig", []string{"-o", "/boot/grub/grub.cfg"}},              // Arch
	}
	for _, c := range candidates {
		if _, err := exec.LookPath(c.bin); err != nil {
			continue
		}
		log("Updating bootloader (" + c.bin + ")...")
		if err := runLog(log, c.bin, c.args...); err != nil {
			return fmt.Errorf("%s: %w", c.bin, err)
		}
		return nil
	}
	return fmt.Errorf("could not find update-grub or grub2-mkconfig — GRUB file written but not regenerated")
}

// addSystemdBootParam appends param to the options line of the active
// systemd-boot loader entry. The active entry is determined by reading
// loader.conf's "default" field. Falls back to editing all .conf entries
// under /boot/loader/entries/ if the default cannot be resolved.
func addSystemdBootParam(param string, log func(string)) error {
	// Find the loader base dir (/boot or /efi).
	loaderBase := "/boot"
	if _, err := os.Stat("/efi/loader/loader.conf"); err == nil {
		loaderBase = "/efi"
	}
	entriesDir := loaderBase + "/loader/entries"

	entries, err := filepath.Glob(entriesDir + "/*.conf")
	if err != nil || len(entries) == 0 {
		return fmt.Errorf("no loader entries found in %s", entriesDir)
	}

	// Try to identify the default entry from loader.conf.
	defaultEntry := ""
	if data, err := os.ReadFile(loaderBase + "/loader/loader.conf"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "default ") {
				token := strings.TrimSpace(strings.TrimPrefix(line, "default "))
				// token may be a glob like "arch-*" or a full filename.
				if !strings.HasSuffix(token, ".conf") {
					token += ".conf"
				}
				if matched, err := filepath.Glob(entriesDir + "/" + token); err == nil && len(matched) > 0 {
					defaultEntry = matched[0]
				}
				break
			}
		}
	}

	// If we couldn't resolve the default, patch all entries.
	targets := entries
	if defaultEntry != "" {
		targets = []string{defaultEntry}
	}

	patched := 0
	for _, entry := range targets {
		data, err := os.ReadFile(entry)
		if err != nil {
			continue
		}
		content := string(data)
		if strings.Contains(content, param) {
			log("Boot parameter already set in " + filepath.Base(entry) + ", skipping.")
			patched++
			continue
		}
		// Find the "options ..." line and append the parameter.
		lines := strings.Split(content, "\n")
		for i, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "options ") {
				lines[i] = strings.TrimRight(line, " \t") + " " + param
				patched++
				break
			}
		}
		newContent := strings.Join(lines, "\n")
		if err := os.WriteFile(entry, []byte(newContent), 0644); err != nil {
			return fmt.Errorf("write %s: %w", entry, err)
		}
		log("Updated loader entry: " + filepath.Base(entry))
	}

	if patched == 0 {
		return fmt.Errorf("no options line found in loader entries under %s", entriesDir)
	}
	return nil
}

// runLog runs a command and calls logFn for each output line.
func runLog(logFn func(string), name string, args ...string) error {
	return runLogDir("", logFn, append([]string{name}, args...)...)
}

// runLogDir runs a command in dir and calls logFn for each output line.
// GIT_TERMINAL_PROMPT=0 prevents git from trying to open /dev/tty when
// running without a controlling terminal (e.g. inside a systemd service).
func runLogDir(dir string, logFn func(string), nameAndArgs ...string) error {
	cmd := exec.Command(nameAndArgs[0], nameAndArgs[1:]...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			logFn(line)
		}
	}
	return err
}
