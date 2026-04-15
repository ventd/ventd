package setup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"

	"github.com/ventd/ventd/internal/hwdiag"
)

// LoadModule shells out to `modprobe <name>` for a module in the fixed
// allowlist, then persists the module name to a file under modulesLoadDir so
// it survives reboot. Clears any hwdiag entries that advertised the module as
// missing so the UI reflects the remediated state on its next poll.
//
// Safety: the allowlist + regex gate below refuses anything the setup wizard
// didn't explicitly propose. Callers MUST pass the module name straight from
// the request JSON (or from a trusted hwdiag entry's Context["module"]); the
// allowlist is the choke-point that keeps the endpoint from turning into a
// generic "run arbitrary modprobe" RPC.
//
// Does NOT call back into the setup Manager's run() state machine — per the
// "client re-request" design the wizard state is left untouched. The UI's
// existing /api/hwdiag poller observes the cleared entries; the user re-runs
// setup explicitly to regenerate config with any newly-visible sensors.
func (m *Manager) LoadModule(ctx context.Context, name string) ([]string, error) {
	if err := validateModuleName(name); err != nil {
		return nil, err
	}
	if !allowedModules[name] {
		return nil, fmt.Errorf("module %q is not in the ventd remediation allowlist", name)
	}

	var log []string
	log = append(log, "Loading kernel module "+name+"...")

	out, err := modprobeCmd(ctx, name)
	if len(out) > 0 {
		log = append(log, string(out))
	}
	if err != nil {
		return log, modprobeUserFacingError(name, err)
	}
	log = append(log, "Module "+name+" loaded.")

	confPath := filepath.Join(modulesLoadDir, "ventd-"+name+".conf")
	if err := modulesLoadWrite(confPath, []byte(name+"\n")); err != nil {
		return log, fmt.Errorf("could not persist module %q so it loads on boot: %w", name, err)
	}
	log = append(log, "Wrote "+confPath+" so the module loads automatically on boot.")

	m.clearModuleDiagEntries(name)
	return log, nil
}

// clearModuleDiagEntries removes hwdiag entries whose remediation was the
// just-loaded module. Scoped to entries the setup package can emit: the
// hwmon CPU-module-missing entry, and any DMI candidate entries whose
// Context["module"] matches.
func (m *Manager) clearModuleDiagEntries(module string) {
	m.mu.Lock()
	store := m.diagStore
	m.mu.Unlock()
	if store == nil {
		return
	}
	snap := store.Snapshot(hwdiag.Filter{})
	for _, e := range snap.Entries {
		if mod, _ := e.Context["module"].(string); mod == module {
			store.Remove(e.ID)
		}
	}
	// Defensive: the CPU-module-missing entry stores the module name in
	// Context too, but clear by ID as well to cover any schema drift.
	store.Remove(hwdiag.IDHwmonCPUModuleMissing)
}

// moduleNameRE accepts the Linux kernel's own module-name charset: a-z, 0-9,
// and underscore, starting with a letter, at most 32 chars. This is stricter
// than kmod's official rules but covers every allowed name below, and it
// rejects shell metacharacters, path separators, and NUL before we hand the
// string to exec.CommandContext.
var moduleNameRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

func validateModuleName(name string) error {
	if name == "" {
		return errors.New("module name is required")
	}
	if !moduleNameRE.MatchString(name) {
		return fmt.Errorf("module name %q contains invalid characters", name)
	}
	return nil
}

// modprobeUserFacingError wraps a raw modprobe error with a plain-English
// explanation per usability.md. No stack traces, no sysfs paths, no Go error
// strings in the summary — the raw output is still available in the returned
// log slice for operators who want the detail.
func modprobeUserFacingError(name string, err error) error {
	return fmt.Errorf(
		"Could not load the %s module. Your kernel may be missing the driver — "+
			"try installing the linux-modules-extra package matching your running "+
			"kernel (on Debian/Ubuntu: linux-modules-extra-$(uname -r); on Fedora "+
			"it ships with the kernel package): %w",
		name, err,
	)
}

// allowedModules is the fixed set the /api/setup/load-module endpoint will
// accept. Adding a module here requires thinking about its blast radius —
// modprobe runs as root and loads kernel code. Do NOT populate this map from
// client input.
//
//   - coretemp, k10temp: Intel/AMD CPU thermal sensors; no fan control.
//   - nct6683, nct6687:  Nuvoton Super-I/O chips common on MSI boards.
//   - it87:              ITE Super-I/O chip common on Gigabyte/ASRock boards.
//   - drivetemp:         SATA drive temperature reporting via ATA SMART.
var allowedModules = map[string]bool{
	"coretemp":  true,
	"k10temp":   true,
	"nct6683":   true,
	"nct6687":   true,
	"it87":      true,
	"drivetemp": true,
}

// AllowedModule reports whether module is accepted by the load-module
// endpoint. Exported so the web layer can reject bad input before calling
// LoadModule (gives a cleaner 400 without spinning up any subprocess).
func AllowedModule(name string) bool {
	return allowedModules[name]
}

// modprobeCmd is the injection seam for LoadModule. Tests stub this to avoid
// actually shelling out. The default runs `modprobe -- <name>` with the
// supplied context so a cancelled request kills any in-flight invocation.
var modprobeCmd = defaultModprobeCmd

// modulesLoadWrite is the injection seam for the /etc/modules-load.d write.
// Tests stub this to divert writes into t.TempDir() so they can run without
// root. Default is os.WriteFile with 0644 perms.
var modulesLoadWrite = defaultModulesLoadWrite

// modulesLoadDir is the injection seam for the /etc/modules-load.d path.
// Tests point this at t.TempDir() via SetModulesLoadDir.
var modulesLoadDir = "/etc/modules-load.d"

// SetModprobeCmd overrides the modprobe invocation for tests. Pass nil to
// restore the default. Returns the previous impl so tests can restore it via
// t.Cleanup.
func SetModprobeCmd(fn func(ctx context.Context, module string) ([]byte, error)) func(ctx context.Context, module string) ([]byte, error) {
	prev := modprobeCmd
	if fn == nil {
		modprobeCmd = defaultModprobeCmd
	} else {
		modprobeCmd = fn
	}
	return prev
}

// SetModulesLoadWrite overrides the modules-load.d file writer for tests.
// Pass nil to restore the default. Returns the previous impl for cleanup.
func SetModulesLoadWrite(fn func(path string, data []byte) error) func(path string, data []byte) error {
	prev := modulesLoadWrite
	if fn == nil {
		modulesLoadWrite = defaultModulesLoadWrite
	} else {
		modulesLoadWrite = fn
	}
	return prev
}

// SetModulesLoadDir overrides the persistence directory for tests. Returns
// the previous path so tests can restore it via t.Cleanup.
func SetModulesLoadDir(dir string) string {
	prev := modulesLoadDir
	if dir == "" {
		modulesLoadDir = "/etc/modules-load.d"
	} else {
		modulesLoadDir = dir
	}
	return prev
}

func defaultModprobeCmd(ctx context.Context, name string) ([]byte, error) {
	// `--` terminates option parsing; a module name that slipped past the
	// regex still can't be interpreted as a flag.
	return exec.CommandContext(ctx, "modprobe", "--", name).CombinedOutput()
}

func defaultModulesLoadWrite(path string, data []byte) error {
	return os.WriteFile(path, data, 0644)
}
