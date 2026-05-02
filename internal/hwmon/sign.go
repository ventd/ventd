package hwmon

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// MOKKeyPair points at a private key + matching certificate file pair.
// Both fields are absolute paths on disk; the install pipeline opens them
// directly and passes them to sign-file as positional arguments.
type MOKKeyPair struct {
	Priv string
	Cert string
}

// ErrNoMOKKey is returned by LocateMOKKey when no key pair is present at
// any candidate path. Callers should treat this as "Secure Boot signing
// is not available" — the install pipeline either falls back to a
// not-enforcing path or refuses with ReasonSecureBootBlocks.
var ErrNoMOKKey = errors.New("hwmon: no MOK key pair found on disk")

// ErrSignFileMissing is returned by SignModuleFile when the kmod sign-file
// helper is not available on PATH and not at the canonical headers
// location. The preflight should have caught this already
// (ReasonSignFileMissing); this error guards the install pipeline against
// a race where the package was uninstalled between preflight and signing.
var ErrSignFileMissing = errors.New("hwmon: kmod sign-file helper not found")

// LocateMOKKey returns the first MOKKeyPair whose .priv and .cert files
// both exist. Search order is: shim-signed (Debian/Ubuntu canonical),
// /var/lib/ventd/mok/ (where ventd's MOK enroll handler writes), then
// /etc/ventd/mok/ as a fallback for operators who manage keys outside
// the daemon.
func LocateMOKKey() (MOKKeyPair, error) {
	for _, c := range mokKeyCandidates {
		if fileExists(c.priv) && fileExists(c.cert) {
			return MOKKeyPair{Priv: c.priv, Cert: c.cert}, nil
		}
	}
	return MOKKeyPair{}, ErrNoMOKKey
}

// SignFilePath resolves the path of the kmod sign-file helper. Tries
// PATH first (kmod package installs `sign-file` there on most distros),
// then the canonical headers-bundled location. Returns ErrSignFileMissing
// when neither resolves.
func SignFilePath(release string) (string, error) {
	if path, err := exec.LookPath("sign-file"); err == nil {
		return path, nil
	}
	// Some distros bundle sign-file alongside the kernel headers rather
	// than in the kmod package's PATH-visible bin. Probe the canonical
	// path before giving up.
	if release != "" {
		bundled := filepath.Join("/usr/src", "linux-headers-"+release, "scripts", "sign-file")
		if fileExists(bundled) {
			return bundled, nil
		}
		bundled2 := filepath.Join("/lib/modules", release, "build", "scripts", "sign-file")
		if fileExists(bundled2) {
			return bundled2, nil
		}
	}
	return "", ErrSignFileMissing
}

// SignModuleFile signs the .ko at modulePath with the provided MOK key
// pair using sign-file's SHA-256 digest. The signed module is written
// in-place — sign-file appends the signature to the existing file. The
// hash algorithm is hard-coded as sha256 because:
//
//   - it is the kernel's mandatory minimum for module signing,
//   - SHA-1 is being retired across the kernel's signing infrastructure,
//   - any operator who needs a different digest can patch this single
//     call site.
//
// release is used to resolve the sign-file binary when it is not on
// PATH; passing "" still works on systems where sign-file is on PATH.
func SignModuleFile(modulePath, release string, key MOKKeyPair) error {
	signFile, err := SignFilePath(release)
	if err != nil {
		return err
	}
	if !fileExists(modulePath) {
		return fmt.Errorf("hwmon: module file not found: %s", modulePath)
	}
	cmd := exec.Command(signFile, "sha256", key.Priv, key.Cert, modulePath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("hwmon: sign-file %s: %w (output: %s)",
			modulePath, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// SignBuildDirModules walks repoDir for *.ko files and signs each in
// place. Used as Step 2 of the install pipeline (per the architecture
// diagram in the v0.5.9 PR-D plan): we sign before copying into
// /lib/modules so the post-copy verification only needs to check that
// the file exists at the destination, not re-derive signing state.
//
// Returns the count of modules signed and the first error encountered.
// On error, modules signed before the error remain signed — the install
// pipeline's OnFailCleanup removes the entire build dir anyway.
func SignBuildDirModules(repoDir, release string, key MOKKeyPair) (int, error) {
	signed := 0
	err := filepath.Walk(repoDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".ko") {
			return nil
		}
		if err := SignModuleFile(path, release, key); err != nil {
			return err
		}
		signed++
		return nil
	})
	return signed, err
}
