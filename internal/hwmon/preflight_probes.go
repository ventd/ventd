package hwmon

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Live probe implementations consumed by DefaultProbes. Each is documented
// with the failure mode it detects; tests substitute a fixture rather than
// driving these against the live system.

// liveMOKKeyAvailable reports whether either the shim-managed MOK key pair
// or a ventd-generated key pair is present on disk and readable. The key
// must be paired (.priv + .der or .pem). The exact path doesn't matter to
// the preflight — we just need the install pipeline to be able to sign.
func liveMOKKeyAvailable() bool {
	for _, pair := range mokKeyCandidates {
		if fileExists(pair.priv) && fileExists(pair.cert) {
			return true
		}
	}
	return false
}

// mokKeyCandidates lists the canonical MOK key locations searched in
// priority order. The shim-signed package's path comes first because it's
// the standard Debian/Ubuntu location.
var mokKeyCandidates = []struct{ priv, cert string }{
	{"/var/lib/shim-signed/mok/MOK.priv", "/var/lib/shim-signed/mok/MOK.der"},
	{"/var/lib/ventd/mok/MOK.priv", "/var/lib/ventd/mok/MOK.der"},
	{"/etc/ventd/mok/MOK.priv", "/etc/ventd/mok/MOK.der"},
}

// liveLibModulesWritable reports whether the install path can create
// /lib/modules/<release>/extra and write to it. Probes by attempting to
// MkdirAll + create a sentinel file + remove it; any failure means
// read-only or permission denied.
func liveLibModulesWritable(release string) bool {
	if release == "" {
		return false
	}
	dir := "/lib/modules/" + release + "/extra"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false
	}
	sentinel := filepath.Join(dir, ".ventd-writable-test")
	f, err := os.Create(sentinel)
	if err != nil {
		return false
	}
	_ = f.Close()
	_ = os.Remove(sentinel)
	return true
}

// liveIsContainerised detects container environment via three independent
// signals (any one is sufficient): /.dockerenv, container keywords in
// /proc/1/cgroup, and systemd-detect-virt --container. Mirrors the
// detection scheme in internal/probe (RULE-PROBE-03) but lighter — the
// preflight only needs a yes/no, not a confidence count.
func liveIsContainerised() bool {
	if fileExists("/.dockerenv") {
		return true
	}
	if data, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		s := strings.ToLower(string(data))
		for _, k := range []string{"docker", "lxc", "kubepods", "garden", "containerd"} {
			if strings.Contains(s, k) {
				return true
			}
		}
	}
	if path, err := exec.LookPath("systemd-detect-virt"); err == nil {
		out, err := exec.Command(path, "--container").Output()
		if err == nil {
			s := strings.TrimSpace(strings.ToLower(string(out)))
			if s != "" && s != "none" {
				return true
			}
		}
	}
	return false
}

// liveAptLockHeld returns true when /var/lib/dpkg/lock-frontend is held.
// Checked via fcntl flock probe — opening the file and trying to take a
// non-blocking exclusive lock; EAGAIN means another process holds it.
// Returns false on non-apt systems (file absent).
func liveAptLockHeld() bool {
	for _, path := range []string{
		"/var/lib/dpkg/lock-frontend",
		"/var/lib/apt/lists/lock",
	} {
		f, err := os.OpenFile(path, os.O_RDONLY, 0)
		if err != nil {
			continue
		}
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		_ = f.Close()
		if err == nil {
			// We acquired the lock — nothing else holds it. Flock auto-
			// releases on Close, which we already did.
			continue
		}
		if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK) {
			return true
		}
	}
	return false
}

// liveHaveRootOrPasswordlessSudo returns true when euid==0 OR `sudo -n true`
// exits 0. Mirrors the elevation expectation of runLogDirRoot.
func liveHaveRootOrPasswordlessSudo() bool {
	if os.Geteuid() == 0 {
		return true
	}
	path, err := exec.LookPath("sudo")
	if err != nil {
		return false
	}
	cmd := exec.Command(path, "-n", "true")
	return cmd.Run() == nil
}

// liveStaleDKMSState reports whether dkms tracks the given module under any
// version. Uses `dkms status -m <module>` and checks for non-empty output.
// Returns false when dkms is not on PATH (no stale state to clean).
func liveStaleDKMSState(module string) bool {
	if module == "" {
		return false
	}
	if _, err := exec.LookPath("dkms"); err != nil {
		return false
	}
	out, err := exec.Command("dkms", "status", "-m", module).Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) != "" {
			return true
		}
	}
	return false
}

// inTreeConflicts maps an OOT module name to the in-tree driver that claims
// the same hardware. Adding entries is non-breaking — the table only grows
// when a new conflict surfaces.
var inTreeConflicts = map[string]string{
	"nct6687":  "nct6683", // OOT nct6687d (newer) conflicts with in-tree nct6683
	"nct6687d": "nct6683",
	"it8688e":  "it87", // OOT it87 fork sometimes shipped as it8688e
}

// liveInTreeDriverConflict returns the in-tree module currently loaded that
// would conflict with target, and true. Reads /proc/modules; returns "" +
// false when no conflict or when the table doesn't list target.
func liveInTreeDriverConflict(target string) (string, bool) {
	conflict, ok := inTreeConflicts[target]
	if !ok {
		return "", false
	}
	data, err := os.ReadFile("/proc/modules")
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 1 && fields[0] == conflict {
			return conflict, true
		}
	}
	return "", false
}

// wizardLockPath returns the canonical path of the wizard run lock. Root-
// mode uses /run; rootless uses $XDG_RUNTIME_DIR (falling back to /tmp
// when neither is available — preflight's job is detection, not policy).
// The internal/setup/lock.go writer agrees on the same path resolution.
func wizardLockPath() string {
	if os.Geteuid() == 0 {
		return "/run/ventd-wizard.lock"
	}
	if rt := os.Getenv("XDG_RUNTIME_DIR"); rt != "" {
		return filepath.Join(rt, "ventd-wizard.lock")
	}
	return "/tmp/ventd-wizard.lock"
}

// liveAnotherWizardRunning reads the wizard lock file and tests whether the
// PID it contains is alive via kill(pid, 0). Returns false on missing file,
// unreadable file, malformed PID, or stale (kill ESRCH).
func liveAnotherWizardRunning() bool {
	data, err := os.ReadFile(wizardLockPath())
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 1 {
		return false
	}
	if pid == os.Getpid() {
		return false // our own lock
	}
	// kill(pid, 0): 0 → process exists; ESRCH → stale.
	if err := syscall.Kill(pid, 0); err == nil {
		return true
	}
	return false
}

// liveDiskFreeBytes returns free bytes on the filesystem mounted at path,
// or 0 + error when statfs fails (typically ENOENT for paths that don't
// exist on this distro — caller treats that as "skip", not "fail").
func liveDiskFreeBytes(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	// Bavail = free blocks for unprivileged user. Multiplying by Bsize is
	// the standard df-equivalent computation.
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}

// fileExists is a small helper used by the live probes — present here so
// the existing ootpreflight.go file doesn't have to re-import os.Stat
// inline at every call site.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
