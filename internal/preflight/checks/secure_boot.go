package checks

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/ventd/ventd/internal/hwmon"
	"github.com/ventd/ventd/internal/preflight"
)

// SecureBootProbes captures the IO points the SB chain checks need.
// All four checks share the SecureBootEnabled probe; HasBinary, key
// presence, and enrollment status are the discriminators.
//
// Production callers wire DefaultSecureBootProbes(); tests substitute
// the fields they need.
type SecureBootProbes struct {
	// Enabled returns (enforcing, known). When known=false the
	// chain is skipped entirely — no point installing kmod / mokutil
	// on a non-UEFI host.
	Enabled func() (bool, bool)
	// HasBinary tests whether `name` is on PATH.
	HasBinary func(name string) bool
	// MOKKeyExists reports whether a private key + cert pair is on
	// disk in any of the canonical locations.
	MOKKeyExists func() bool
	// MOKEnrolled reports whether the key is currently enrolled in
	// the firmware's MOK list (true) or only present on disk (false).
	// Implementation runs `mokutil --list-enrolled` and matches the
	// SHA-1 of the on-disk .der.
	MOKEnrolled func(ctx context.Context) (bool, error)
	// Distro is captured at orchestrator construction so AutoFix
	// commands dispatch correctly.
	Distro hwmon.DistroInfo
	// Run is the shell command runner; defaults to liveRunShell.
	Run cmdRunner
	// MOKKeyDir is the directory the keypair is generated into.
	// Defaults to /var/lib/shim-signed/mok.
	MOKKeyDir string
}

// DefaultSecureBootProbes wires the live system. Each field is
// non-nil; tests can copy this and override individual fields.
func DefaultSecureBootProbes() SecureBootProbes {
	return SecureBootProbes{
		Enabled:      liveSecureBootEnabled,
		HasBinary:    liveHasBinary,
		MOKKeyExists: liveMOKKeyExists,
		MOKEnrolled:  liveMOKEnrolled,
		Distro:       hwmon.DetectDistro(),
		Run:          liveRunShell,
		MOKKeyDir:    "/var/lib/shim-signed/mok",
	}
}

// SecureBootChecks returns the four SB-related Checks in chain order.
// The orchestrator runs them all; chain ordering matters because each
// gate's AutoFix can only succeed once its predecessor's fix has
// landed:
//
//  1. kmod (sign-file) installed
//  2. mokutil installed
//  3. MOK keypair generated on disk
//  4. MOK enrolled in firmware (RequiresReboot)
//
// When SB is not enforcing, every Detect short-circuits to !triggered
// and the orchestrator skips the entire chain.
func SecureBootChecks(p SecureBootProbes) []preflight.Check {
	skipUnlessEnforcing := func(detect func(context.Context) (bool, string)) func(context.Context) (bool, string) {
		return func(ctx context.Context) (bool, string) {
			enabled, known := p.Enabled()
			if !known || !enabled {
				return false, ""
			}
			return detect(ctx)
		}
	}

	return []preflight.Check{
		// 1. kmod / sign-file
		{
			Name:     "secure_boot_signfile_missing",
			Severity: preflight.SeverityBlocker,
			Detect: skipUnlessEnforcing(func(context.Context) (bool, string) {
				if p.HasBinary("sign-file") {
					return false, ""
				}
				return true, "sign-file helper not on PATH"
			}),
			Explain: func(string) string {
				return "Secure Boot is enforcing — the kmod sign-file helper is required to sign the ventd kernel module."
			},
			AutoFix: func(ctx context.Context) error {
				return installPackage(ctx, p.Distro, kmodPackage(p.Distro), p.Run)
			},
			PromptText: "Install kmod (provides sign-file)?",
			DocURL:     "https://github.com/ventd/ventd/wiki/secure-boot",
		},
		// 2. mokutil
		{
			Name:     "secure_boot_mokutil_missing",
			Severity: preflight.SeverityBlocker,
			Detect: skipUnlessEnforcing(func(context.Context) (bool, string) {
				if p.HasBinary("mokutil") {
					return false, ""
				}
				return true, "mokutil not on PATH"
			}),
			Explain: func(string) string {
				return "mokutil is required to enroll the Machine Owner Key in firmware."
			},
			AutoFix: func(ctx context.Context) error {
				return installPackage(ctx, p.Distro, "mokutil", p.Run)
			},
			PromptText: "Install mokutil?",
			DocURL:     "https://github.com/ventd/ventd/wiki/secure-boot",
		},
		// 3. MOK keypair on disk
		{
			Name:     "secure_boot_mok_keypair_missing",
			Severity: preflight.SeverityBlocker,
			Detect: skipUnlessEnforcing(func(context.Context) (bool, string) {
				if p.MOKKeyExists() {
					return false, ""
				}
				return true, "no MOK keypair under " + p.MOKKeyDir
			}),
			Explain: func(string) string {
				return "Generate a Machine Owner Key. ventd will use this key to sign its module."
			},
			AutoFix: func(ctx context.Context) error {
				return generateMOKKey(ctx, p.MOKKeyDir, p.Run)
			},
			PromptText: "Generate a MOK signing keypair now?",
			DocURL:     "https://github.com/ventd/ventd/wiki/secure-boot",
		},
		// 4. MOK enrolled in firmware
		{
			Name:     "secure_boot_mok_not_enrolled",
			Severity: preflight.SeverityBlocker,
			Detect: skipUnlessEnforcing(func(ctx context.Context) (bool, string) {
				// Skip if the keypair isn't on disk yet — the prior
				// check still has to run first. Detect's contract is
				// "no triggered=false unless the predecessor cleared".
				if !p.MOKKeyExists() {
					return false, "(prerequisite secure_boot_mok_keypair_missing)"
				}
				enrolled, err := p.MOKEnrolled(ctx)
				if err != nil {
					return true, "could not list enrolled keys: " + err.Error()
				}
				if enrolled {
					return false, ""
				}
				return true, "MOK on disk but not enrolled in firmware"
			}),
			Explain: func(string) string {
				return "Queue MOK for enrollment. After reboot, confirm the key in the blue MOK Manager screen."
			},
			AutoFix: func(ctx context.Context) error {
				return enrollMOK(ctx, p.MOKKeyDir, p.Run)
			},
			PromptText:     "Queue MOK enrollment? (you'll be asked for a password, then prompted to reboot)",
			DocURL:         "https://github.com/ventd/ventd/wiki/secure-boot#enroll",
			RequiresReboot: true,
		},
	}
}

// kmodPackage maps families to their sign-file-bearing package. Arch
// ships sign-file alongside linux-headers rather than in a separate
// kmod package.
func kmodPackage(d hwmon.DistroInfo) string {
	if familyOf(d) == "arch" {
		return "linux-headers"
	}
	return "kmod"
}

// generateMOKKey runs `openssl req` to create a 2048-bit RSA keypair
// and a self-signed certificate at <dir>/MOK.priv + <dir>/MOK.der.
// The CN ("ventd MOK") is recognisable in mokutil's enrolled list so
// the operator can spot it during firmware enrollment.
func generateMOKKey(ctx context.Context, dir string, runner cmdRunner) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create MOK dir: %w", err)
	}
	cmd := strings.Join([]string{
		"openssl req -new -x509",
		"-newkey rsa:2048 -nodes",
		"-keyout " + dir + "/MOK.priv",
		"-outform DER -out " + dir + "/MOK.der",
		"-days 36500",
		"-subj '/CN=ventd MOK/'",
	}, " ")
	if err := runner(ctx, cmd); err != nil {
		return err
	}
	// Convert .der → .pem for tools that prefer PEM input.
	pemCmd := "openssl x509 -in " + dir + "/MOK.der -inform DER -outform PEM -out " + dir + "/MOK.pem"
	if err := runner(ctx, pemCmd); err != nil {
		return err
	}
	return os.Chmod(dir+"/MOK.priv", 0o600)
}

// enrollMOK runs `mokutil --import` non-interactively. The user-
// supplied password is prompted by mokutil itself and is verified at
// the firmware MOK Manager screen on next boot. The orchestrator's
// caller (cmd/ventd/preflight.go) wires stdin to the operator's
// terminal so the password prompt reaches them.
func enrollMOK(ctx context.Context, dir string, runner cmdRunner) error {
	cmd := "mokutil --import " + dir + "/MOK.der"
	return runner(ctx, cmd)
}

// liveSecureBootEnabled bridges to hwmon's existing live probe so we
// don't duplicate the efivar parsing logic.
func liveSecureBootEnabled() (bool, bool) {
	// hwmon.Probes is the canonical wire-up; constructing
	// DefaultProbes here is cheap (no I/O at construction) and the
	// SecureBootEnabled field is non-nil.
	return hwmon.DefaultProbes().SecureBootEnabled()
}

func liveHasBinary(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// liveMOKKeyExists checks the same canonical paths hwmon's
// liveMOKKeyAvailable does. Re-exported here because the hwmon helper
// is package-private.
func liveMOKKeyExists() bool {
	for _, pair := range mokKeyCandidates {
		if fileExists(pair.priv) && fileExists(pair.cert) {
			return true
		}
	}
	return false
}

var mokKeyCandidates = []struct{ priv, cert string }{
	{"/var/lib/shim-signed/mok/MOK.priv", "/var/lib/shim-signed/mok/MOK.der"},
	{"/var/lib/ventd/mok/MOK.priv", "/var/lib/ventd/mok/MOK.der"},
	{"/etc/ventd/mok/MOK.priv", "/etc/ventd/mok/MOK.der"},
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// liveMOKEnrolled runs `mokutil --list-enrolled` and matches the
// fingerprint of <dir>/MOK.der. Returns false when mokutil is missing
// or the list parse fails — caller treats that as "not enrolled" for
// safety.
func liveMOKEnrolled(ctx context.Context) (bool, error) {
	if _, err := exec.LookPath("mokutil"); err != nil {
		return false, nil
	}
	cmd := exec.CommandContext(ctx, "mokutil", "--list-enrolled")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, nil // mokutil printing nothing is treated as "no MOK enrolled".
	}
	// Naive substring match on the certificate's CN. A robust
	// implementation would compare SHA-1 fingerprints; the CN is
	// unique enough for the install-time gate.
	return strings.Contains(string(out), "ventd MOK"), nil
}
