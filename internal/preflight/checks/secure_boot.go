package checks

import (
	"context"
	"crypto/rand"
	"encoding/binary"
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
	// MOKPassword is the password we'll pipe to `mokutil --import`
	// AND display to the operator (they type it at firmware MOK
	// Manager). Generated at orchestrator construction so the
	// password is stable across the AutoFix call and the
	// walkthrough render. cmd/ventd/preflight.go reads this off
	// the Default()-constructed probes to surface in the boxed
	// walkthrough.
	MOKPassword string
	// MOKPasswordPath is the on-disk session-cache path. When set
	// (default `/var/lib/ventd/.mok-session-password`), the
	// password is persisted across multiple `ventd preflight`
	// invocations within the same install session — operators who
	// run preflight, type the wrong password at firmware MOK
	// Manager, and re-run preflight see the SAME password rather
	// than a freshly-generated one. The cache is removed when
	// MOKEnrolled detects the import landed.
	//
	// Empty string disables session caching (useful in tests +
	// the cmd/ventd/preflight legacy invocation).
	MOKPasswordPath string
}

// DefaultMOKPasswordPath is the canonical on-disk session-cache for
// the generated MOK enrollment password. Persisted across multiple
// `ventd preflight` invocations within the same install session so
// the operator sees the same password each time the chain re-runs.
// Removed by liveMOKEnrolledWithCleanup when enrollment is observed.
const DefaultMOKPasswordPath = "/var/lib/ventd/.mok-session-password"

// DefaultSecureBootProbes wires the live system. Each field is
// non-nil; tests can copy this and override individual fields.
//
// The MOKPassword field is loaded from DefaultMOKPasswordPath on
// first call (when the file exists), or freshly generated and saved
// otherwise. Subsequent invocations within the same install session
// see the same password — important for the rerun-after-firmware-
// rejection case where the operator typed wrong at MOK Manager and
// needs to retry with the same password they wrote down.
//
// A generation failure produces a fixed fallback ("ventd-changeme")
// — getting random bytes shouldn't fail outside a broken sandbox,
// but the install path mustn't abort over crypto/rand.
func DefaultSecureBootProbes() SecureBootProbes {
	pw, err := loadOrGenerateMOKPassword(DefaultMOKPasswordPath)
	if err != nil {
		pw = "ventd-changeme"
	}
	return SecureBootProbes{
		Enabled:         liveSecureBootEnabled,
		HasBinary:       liveHasBinary,
		MOKKeyExists:    liveMOKKeyExists,
		MOKEnrolled:     liveMOKEnrolledWithCleanup(DefaultMOKPasswordPath),
		Distro:          hwmon.DetectDistro(),
		Run:             liveRunShell,
		MOKKeyDir:       "/var/lib/shim-signed/mok",
		MOKPassword:     pw,
		MOKPasswordPath: DefaultMOKPasswordPath,
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
				return "Queue MOK for enrollment. After reboot, confirm the key in the blue MOK Manager screen with the password shown below."
			},
			AutoFix: func(ctx context.Context) error {
				return enrollMOK(ctx, p.MOKKeyDir, p.MOKPassword, p.Run)
			},
			PromptText:     "Queue MOK enrollment? (you'll be shown a one-time password to type at firmware boot)",
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

// enrollMOK queues the on-disk .der for next-boot firmware
// enrollment via `mokutil --import`. We pipe a generated password
// to mokutil's two password prompts and surface the same password
// to the operator via the returned MOKPassword field on the
// preflight Report — they need to type it at the firmware MOK
// Manager screen.
//
// Empty passwords don't work in practice: shim's MOK Manager
// enforces a minimum password length (caught on Phoenix's HIL
// desktop where firmware rejected the empty queue with
// "unacceptable password length"). We generate a short random
// suffix ("ventd-<4-hex>") rather than asking the operator to
// invent one — short enough to type at the firmware screen, long
// enough to satisfy any reasonable shim min-length check.
func enrollMOK(ctx context.Context, dir, password string, runner cmdRunner) error {
	// Echo the password twice into mokutil's stdin (mokutil reads
	// it twice, with echo off). Quote-escape the password just
	// enough to survive /bin/sh -c — generated passwords are
	// alphanumeric so this is belt-and-braces.
	safePass := strings.ReplaceAll(password, "'", `'\''`)
	cmd := "( echo '" + safePass + "'; echo '" + safePass + "' ) | mokutil --import " + dir + "/MOK.der"
	return runner(ctx, cmd)
}

// generateMOKPassword returns a short, memorable password the
// operator types at the firmware MOK Manager screen. Format:
// "ventd-XXXX" where XXXX is 4 hex chars from crypto/rand. 10
// chars total — long enough to clear shim's min-length check (we
// haven't seen one above 8 in the wild), short enough to type at
// firmware where the keyboard layout may be quirky and there's no
// copy-paste.
func generateMOKPassword() (string, error) {
	var buf [2]byte
	if _, err := cryptoRandRead(buf[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("ventd-%04x", binary.BigEndian.Uint16(buf[:])), nil
}

// loadOrGenerateMOKPassword reads an existing session password from
// `path` (mode-checked: must be ≤ 0600), or generates a fresh one
// and writes it atomically. Empty path disables the cache entirely
// and behaves like a bare generateMOKPassword call.
//
// The cache file is mode 0600 so only root can read it. A more-
// permissive existing file is rejected with an error rather than
// silently chmod'd — operator-edited password files are out of
// scope and should fail noisy.
//
// Cache file format: a single line containing the password,
// optionally trailed by a newline. Whitespace is trimmed. Empty
// content triggers regeneration as if the file did not exist.
func loadOrGenerateMOKPassword(path string) (string, error) {
	if path == "" {
		return generateMOKPassword()
	}
	if data, err := os.ReadFile(path); err == nil {
		st, statErr := os.Stat(path)
		if statErr == nil {
			mode := st.Mode().Perm()
			if mode&0o077 != 0 {
				return "", fmt.Errorf("mok session-password cache %s is mode %o, refusing (must be ≤ 0600)", path, mode)
			}
		}
		pw := strings.TrimSpace(string(data))
		if pw != "" {
			return pw, nil
		}
		// Empty / whitespace-only file → fall through to regenerate.
	}
	pw, err := generateMOKPassword()
	if err != nil {
		return "", err
	}
	// Best-effort save. Don't fail the whole preflight if /var/lib
	// is read-only or the parent dir doesn't exist — the operator
	// just doesn't get session-stable passwords on that host.
	if dir := strings.TrimSuffix(path, "/"+filepathBase(path)); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(pw+"\n"), 0o600); err == nil {
		_ = os.Rename(tmp, path)
	}
	return pw, nil
}

// filepathBase returns the trailing path component without dragging
// in path/filepath at the package level (the rest of secure_boot.go
// stays string-based so we keep the import set minimal).
func filepathBase(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// liveMOKEnrolledWithCleanup wraps liveMOKEnrolled with a side-
// effect: when enrollment is observed, the on-disk session-password
// cache at `path` is removed. The password is no longer needed
// post-enrollment; leaving it on disk extends the leak window for a
// secret that's already served its purpose.
//
// Empty path → behaves as bare liveMOKEnrolled (no cleanup).
// Removal failures are swallowed — the function's contract is
// "did the firmware enroll", not "did we tidy up".
func liveMOKEnrolledWithCleanup(path string) func(ctx context.Context) (bool, error) {
	return makeMOKEnrolledWithCleanup(path, liveMOKEnrolled)
}

// makeMOKEnrolledWithCleanup is the test-injection seam: production
// passes liveMOKEnrolled, tests pass a stub returning the desired
// outcome without monkey-patching package state.
func makeMOKEnrolledWithCleanup(path string, underlying func(ctx context.Context) (bool, error)) func(ctx context.Context) (bool, error) {
	if path == "" {
		return underlying
	}
	return func(ctx context.Context) (bool, error) {
		enrolled, err := underlying(ctx)
		if enrolled {
			_ = os.Remove(path)
		}
		return enrolled, err
	}
}

// cryptoRandRead is a var so tests can stub it; production points
// at crypto/rand.Read.
var cryptoRandRead = func(b []byte) (int, error) {
	return rand.Read(b)
}

// liveSecureBootEnabled bridges to hwmon's existing live probe so we
// don't duplicate the efivar parsing logic.
func liveSecureBootEnabled() (bool, bool) {
	// hwmon.Probes is the canonical wire-up; constructing
	// DefaultProbes here is cheap (no I/O at construction) and the
	// SecureBootEnabled field is non-nil.
	return hwmon.DefaultProbes().SecureBootEnabled()
}

// liveHasBinary tests whether `name` is reachable for execution. It
// checks PATH first; for "sign-file" specifically it falls back to
// the canonical kernel-headers location at
// /usr/src/linux-headers-<release>/scripts/sign-file (Debian/Ubuntu)
// and /usr/src/kernels/<release>/scripts/sign-file (Fedora). DKMS
// hardcodes that path so the helper "exists" for module-signing
// purposes even when not on PATH; without this fallback the
// orchestrator falsely flags hosts that successfully sign modules
// at install time.
func liveHasBinary(name string) bool {
	if _, err := exec.LookPath(name); err == nil {
		return true
	}
	if name == "sign-file" {
		release, err := exec.Command("uname", "-r").Output()
		if err == nil {
			r := strings.TrimSpace(string(release))
			for _, p := range []string{
				"/usr/src/linux-headers-" + r + "/scripts/sign-file",
				"/usr/src/kernels/" + r + "/scripts/sign-file",
				"/lib/modules/" + r + "/build/scripts/sign-file",
			} {
				if fileExists(p) {
					return true
				}
			}
		}
	}
	return false
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

// liveMOKEnrolled compares the SHA-1 fingerprint of the on-disk
// MOK.der (whichever of mokKeyCandidates exists first) against the
// list of enrolled fingerprints from `mokutil --list-enrolled`.
//
// Fingerprint comparison is the load-bearing approach because the
// ventd install pipeline may have regenerated the on-disk keypair
// (replacing what's enrolled with a new pair); a CN-string match
// would falsely report "enrolled" in that case and the next
// modprobe would fail with a key-rejected stamp. A fingerprint
// mismatch correctly reports the regenerated key as not yet
// enrolled, so the operator gets prompted to re-enroll.
//
// Returns (false, nil) when mokutil is missing, no MOK is on disk,
// or the openssl fingerprint extraction fails — every uncertain
// state collapses to "not enrolled" so the install path errs on
// the side of running the enrollment chain.
func liveMOKEnrolled(ctx context.Context) (bool, error) {
	if _, err := exec.LookPath("mokutil"); err != nil {
		return false, nil
	}
	if _, err := exec.LookPath("openssl"); err != nil {
		return false, nil
	}
	derPath := ""
	for _, pair := range mokKeyCandidates {
		if fileExists(pair.cert) {
			derPath = pair.cert
			break
		}
	}
	if derPath == "" {
		return false, nil
	}
	fpCmd := exec.CommandContext(ctx, "openssl", "x509",
		"-in", derPath, "-inform", "DER",
		"-noout", "-fingerprint", "-sha1")
	fpOut, err := fpCmd.Output()
	if err != nil {
		return false, nil
	}
	fp := normaliseFingerprint(string(fpOut))
	if fp == "" {
		return false, nil
	}
	enrolled, err := exec.CommandContext(ctx, "mokutil", "--list-enrolled").CombinedOutput()
	if err != nil {
		return false, nil
	}
	return strings.Contains(normaliseFingerprintList(string(enrolled)), fp), nil
}

// normaliseFingerprint extracts the hex digest from an openssl
// `-fingerprint -sha1` line ("SHA1 Fingerprint=AA:BB:..."), strips
// colons, and lowercases. Used for case-insensitive substring
// matching against mokutil's enrolled list which prints
// "SHA1 Fingerprint: aa:bb:..." (lowercase, with colons).
func normaliseFingerprint(s string) string {
	if i := strings.Index(s, "="); i >= 0 {
		s = s[i+1:]
	}
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.ReplaceAll(s, ":", "")
}

// normaliseFingerprintList strips colons and lowercases the entire
// mokutil --list-enrolled output so a substring search against a
// normalised fingerprint matches regardless of the colon / case
// formatting differences between openssl and mokutil.
func normaliseFingerprintList(s string) string {
	return strings.ReplaceAll(strings.ToLower(s), ":", "")
}
