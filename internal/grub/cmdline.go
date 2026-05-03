// Package grub adds kernel command-line parameters to GRUB
// idempotently via a drop-in file under /etc/default/grub.d/. The
// drop-in approach (rather than editing /etc/default/grub directly)
// keeps ventd's changes auditable + revertable: a single file the
// operator can rm to undo.
//
// The package is intentionally small — one job, no surprise edits.
// The host's bootloader regen tool (update-grub on Debian/Ubuntu,
// grub2-mkconfig on Fedora/RHEL, update-bootloader on SUSE) is
// invoked after the drop-in is written so the change takes effect
// at next boot.
package grub

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ventd/ventd/internal/iox"
)

// DropInPath is the canonical location for ventd's kernel cmdline
// additions. /etc/default/grub.d is read by every modern grub
// install; the .cfg suffix matches the convention used by other
// packages (e.g. cloud-init's drop-ins under the same directory).
const DropInPath = "/etc/default/grub.d/ventd-cmdline.cfg"

// dropInPath is the live path the package writes to. Production
// initialises it to DropInPath; tests swap via setDropInPath.
var dropInPath = DropInPath

// AddParam idempotently appends `param` to the kernel cmdline via
// a drop-in. If the drop-in already contains `param`, returns nil
// without rewriting (no spurious update-grub invocation).
//
// The drop-in writes a line of the form:
//
//	GRUB_CMDLINE_LINUX_DEFAULT="$GRUB_CMDLINE_LINUX_DEFAULT param1 param2 ..."
//
// using shell parameter expansion so the drop-in stacks on top of
// whatever the main /etc/default/grub set, rather than replacing
// it. Multiple AddParam calls with different params accumulate.
//
// regenerate, when non-nil, is invoked after a successful write to
// rebuild the bootloader configuration. The default
// (DefaultRegenerator) dispatches to the per-distro tool. Callers
// can pass a fake for tests.
func AddParam(param string, regenerate func() error) error {
	if !validParam(param) {
		return fmt.Errorf("grub: refusing to add invalid kernel param %q", param)
	}
	existing, err := os.ReadFile(dropInPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("grub: read %s: %w", DropInPath, err)
	}
	current := parseDropIn(string(existing))
	if contains(current, param) {
		return nil // idempotent no-op
	}
	current = append(current, param)
	if err := writeDropIn(current); err != nil {
		return err
	}
	if regenerate != nil {
		return regenerate()
	}
	return nil
}

// HasParam reports whether the drop-in currently lists the param.
// Used by callers that want to short-circuit before triggering the
// reboot prompt — if the param was added in a prior run, the
// reboot is needed, not a re-write.
func HasParam(param string) bool {
	data, err := os.ReadFile(dropInPath)
	if err != nil {
		return false
	}
	return contains(parseDropIn(string(data)), param)
}

// parseDropIn extracts the params from a drop-in's
// GRUB_CMDLINE_LINUX_DEFAULT="$GRUB_CMDLINE_LINUX_DEFAULT a b c"
// line. Returns nil for empty or unrecognised content. We don't try
// to handle every shell escape — drop-ins ventd writes follow a
// strict format, so this parses the format we wrote, not arbitrary
// shell.
func parseDropIn(content string) []string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "GRUB_CMDLINE_LINUX_DEFAULT=") {
			continue
		}
		val := strings.TrimPrefix(line, "GRUB_CMDLINE_LINUX_DEFAULT=")
		val = strings.Trim(val, `"'`)
		val = strings.TrimPrefix(val, "$GRUB_CMDLINE_LINUX_DEFAULT")
		val = strings.TrimSpace(val)
		if val == "" {
			return nil
		}
		return strings.Fields(val)
	}
	return nil
}

// writeDropIn renders the current params back as a drop-in. The
// directory is created with 0755 (matches /etc/default/ default);
// the file is 0644 (system-readable but root-writable).
func writeDropIn(params []string) error {
	if err := os.MkdirAll(filepath.Dir(dropInPath), 0o755); err != nil {
		return fmt.Errorf("grub: mkdir %s: %w", filepath.Dir(dropInPath), err)
	}
	body := "# Managed by ventd — do not edit manually.\n" +
		"# Adds kernel command-line parameters via drop-in. Remove this file\n" +
		"# to revert ventd's additions.\n" +
		"GRUB_CMDLINE_LINUX_DEFAULT=\"$GRUB_CMDLINE_LINUX_DEFAULT " +
		strings.Join(params, " ") + "\"\n"
	if err := iox.WriteFile(dropInPath, []byte(body), 0o644); err != nil {
		return fmt.Errorf("grub: persist drop-in: %w", err)
	}
	return nil
}

// validParam allows alphanumerics, dot, dash, underscore, equals.
// Refuses anything that could shell-escape (quotes, semicolons,
// backticks). The kernel cmdline syntax doesn't need anything
// fancier; restricting now means a future bug that lets caller-
// controlled input reach AddParam can't trigger shell injection
// when the drop-in is sourced by /etc/default/grub.
func validParam(p string) bool {
	if p == "" || len(p) > 64 {
		return false
	}
	for _, r := range p {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '.', r == '-', r == '_', r == '=':
			continue
		}
		return false
	}
	return true
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// DefaultRegenerator runs the per-distro bootloader-rebuild tool.
// Tries update-grub first (Debian/Ubuntu), then grub2-mkconfig
// (Fedora/RHEL/Arch), then update-bootloader (SUSE). The first
// tool found on PATH is used; if none is found, returns an error
// naming each one tried so the operator knows which to install.
func DefaultRegenerator() error {
	if path, err := exec.LookPath("update-grub"); err == nil {
		return runRegen(path)
	}
	if path, err := exec.LookPath("grub2-mkconfig"); err == nil {
		// Fedora/RHEL/Arch convention: write to the canonical
		// grub.cfg location. Most distros wire grub2-mkconfig as
		// a wrapper that picks the right output path; we still
		// pass -o explicitly to be sure.
		return runRegenOut(path, "/boot/grub2/grub.cfg")
	}
	if path, err := exec.LookPath("update-bootloader"); err == nil {
		return runRegen(path)
	}
	return fmt.Errorf("grub: no bootloader rebuild tool found (looked for update-grub, grub2-mkconfig, update-bootloader)")
}

func runRegen(tool string) error {
	out, err := exec.Command(tool).CombinedOutput()
	if err != nil {
		return fmt.Errorf("grub: %s: %w (output: %s)", tool, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func runRegenOut(tool, outPath string) error {
	out, err := exec.Command(tool, "-o", outPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("grub: %s -o %s: %w (output: %s)", tool, outPath, err, strings.TrimSpace(string(out)))
	}
	return nil
}
