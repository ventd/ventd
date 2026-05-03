package hwmon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// allowedModprobeOptions is the closed-set allowlist of (module, option)
// pairs that the /api/hwdiag/modprobe-options-write endpoint will write.
// Adding a new entry here is a deliberate review-gated act: a typo or a
// caller-controlled string MUST NOT reach modprobe.d.
//
// Stage 1A — ThinkPad fan_control. The kernel's thinkpad_acpi driver
// loads with fan writes disabled by default. RULE-WIZARD-RECOVERY-10
// classifies the failure; this writer applies the fix.
//
// Future Stage 1B/1C entries (it87 ignore_resource_conflict=1 for
// kernel ≥ 6.2; it87 force_id=0xNNNN per detected chip ID) extend
// this map in their own PRs alongside catalog rows.
var allowedModprobeOptions = map[string]map[string]bool{
	"thinkpad_acpi": {
		"fan_control=1": true,
	},
}

// IsAllowedModprobeOption reports whether (module, options) is an
// approved entry. Both arguments are matched literally — case- and
// whitespace-sensitive — so a caller-supplied string with a stray space
// or trailing semicolon does not silently match.
func IsAllowedModprobeOption(module, options string) bool {
	if module == "" || options == "" {
		return false
	}
	opts, ok := allowedModprobeOptions[module]
	if !ok {
		return false
	}
	return opts[options]
}

// ModprobeOptionsDropInPath returns the canonical drop-in path for
// `options <module> ...` entries written by ventd. One file per module
// keeps the ventd-owned fragments distinct from operator-authored
// /etc/modprobe.d entries.
func ModprobeOptionsDropInPath(module string) string {
	return filepath.Join("/etc/modprobe.d", "ventd-"+module+".conf")
}

// WriteModprobeOptionsDropIn writes (or rewrites) a one-line
// `options <module> <options>` drop-in. Idempotent: re-writing the
// same content is a no-op.
//
// The caller MUST gate via IsAllowedModprobeOption — this writer
// trusts its inputs because the endpoint layer is the single
// authorised entry point.
func WriteModprobeOptionsDropIn(path, module, options string) error {
	if module == "" {
		return fmt.Errorf("module name is empty")
	}
	if options == "" {
		return fmt.Errorf("options string is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	want := "options " + module + " " + options + "\n"
	existing, _ := os.ReadFile(path)
	if string(existing) == want {
		return nil
	}
	return os.WriteFile(path, []byte(want), 0o644)
}

// ReloadModule runs `modprobe -r <module>` then `modprobe <module>`,
// returning the combined log lines via logFn so the operator UI can
// render the modprobe stderr inline.
//
// A failure to remove the module (EBUSY when in use) is logged and
// the function continues — the new options take effect on the next
// load anyway. A failure to load AFTER a successful remove is fatal.
func ReloadModule(module string, logFn func(string)) error {
	if _, err := exec.LookPath("modprobe"); err != nil {
		return fmt.Errorf("modprobe not on PATH: %w", err)
	}
	rmOut, rmErr := exec.Command("modprobe", "-r", module).CombinedOutput()
	if rmErr != nil {
		logFn(fmt.Sprintf("modprobe -r %s: %s (continuing)", module, strings.TrimSpace(string(rmOut))))
	} else {
		logFn(fmt.Sprintf("modprobe -r %s: ok", module))
	}
	loadOut, loadErr := exec.Command("modprobe", module).CombinedOutput()
	if loadErr != nil {
		logFn(fmt.Sprintf("modprobe %s: %s", module, strings.TrimSpace(string(loadOut))))
		return fmt.Errorf("modprobe %s: %w", module, loadErr)
	}
	logFn(fmt.Sprintf("modprobe %s: ok", module))
	return nil
}
