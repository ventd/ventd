// SPDX-License-Identifier: GPL-3.0-or-later

package msiec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// FirmwarePinPath is the canonical location of ventd's msi-ec
// firmware pin file. Lives under /etc/modprobe.d so the option
// applies on every modprobe of msi-ec (boot, install pipeline,
// manual reload). The name is prefixed with `ventd-` so operators
// inspecting /etc/modprobe.d/ can tell at a glance which file the
// daemon manages.
const FirmwarePinPath = "/etc/modprobe.d/ventd-msiec-firmware-pin.conf"

// ErrUnsafeFirmwareString is returned when WriteFirmwarePin is called
// with a firmware string that does not match the MSI firmware-version
// shape (alphanumeric + a few separators). Refused at write time as
// defence in depth — the upstream catalogue should be the source of
// suggested values, but a confused wizard or operator-edit shouldn't
// be able to inject arbitrary shell or kernel-line content into the
// modprobe.d file.
var ErrUnsafeFirmwareString = errors.New("msiec: firmware string contains unsafe characters")

// safeFirmwareRe restricts firmware strings to the shape upstream
// msi-ec catalogues use: alphanumerics, dot, plus seven-or-more chars
// and at most 32. The pattern is strict on purpose: any deviation from
// this means the suggestion engine got something it didn't generate,
// which is exactly when the modprobe.d writer should refuse rather
// than rubber-stamp.
var safeFirmwareRe = regexp.MustCompile(`^[A-Za-z0-9.]{7,32}$`)

// ValidateFirmwareString reports whether fw is in the upstream-style
// shape WriteFirmwarePin will accept. Exposed so callers can validate
// operator-supplied strings before plumbing them through the wizard
// without having to mock the writer's filesystem dependency.
func ValidateFirmwareString(fw string) error {
	if !safeFirmwareRe.MatchString(fw) {
		return fmt.Errorf("%w: %q", ErrUnsafeFirmwareString, fw)
	}
	return nil
}

// WriteFirmwarePin writes a modprobe.d snippet that pins the upstream
// msi-ec driver's `firmware=<rev>` modparam. After this returns the
// caller is responsible for re-modprobing — typically:
//
//	modprobe -r msi-ec && modprobe msi-ec
//
// The path argument is the destination; pass FirmwarePinPath in
// production, t.TempDir() + "/ventd-msiec-firmware-pin.conf" in tests.
//
// The file's content is exactly:
//
//	# Managed by ventd — see issue #1168.
//	# Closest catalogued firmware for the EC's reported version.
//	options msi-ec firmware=<rev>
//
// Writes go through os.WriteFile with 0o644 — modprobe.d files are
// world-readable system configuration; no secrets live here, and a
// restrictive mode would block diag-collection from picking up the
// pin (which would obscure the pin's existence in support tickets).
func WriteFirmwarePin(path, firmware string) error {
	if err := ValidateFirmwareString(firmware); err != nil {
		return err
	}
	content := fmt.Sprintf(
		"# Managed by ventd — see issue #1168.\n"+
			"# Closest catalogued firmware for the EC's reported version.\n"+
			"options msi-ec firmware=%s\n",
		firmware,
	)
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("msiec: ensure %s: %w", dir, err)
		}
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("msiec: write %s: %w", path, err)
	}
	return nil
}

// RemoveFirmwarePin removes the ventd-managed firmware-pin file if it
// exists. Idempotent — returns nil whether the file existed or not.
// Used by the wizard when an operator chooses "Skip — file an issue
// upstream" so the next module load isn't constrained to a stale pin.
func RemoveFirmwarePin(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("msiec: remove %s: %w", path, err)
	}
	return nil
}
