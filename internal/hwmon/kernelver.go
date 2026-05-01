package hwmon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// ErrKernelVersionUnknown is returned when every detection source returns
// empty. The install path treats this as fatal — proceeding with an empty
// version would build `apt-get install -y linux-headers-` (no version) and
// hang waiting for an interactive package selection (#769).
var ErrKernelVersionUnknown = errors.New("could not detect kernel version: tried /proc/sys/kernel/osrelease, uname(1), and uname(2)")

// kernelHeadersInstallTimeout caps how long we wait for the kernel-headers
// install command. Five minutes is well above the normal apt-get / dnf /
// pacman / zypper happy-path duration on a fresh install (typically
// 30–90 seconds even on a constrained connection) and below the systemd
// service-watchdog window. dpkg-lock contention or a stalled mirror past
// this window are the operator's problem, not the wizard's to wait on.
const kernelHeadersInstallTimeout = 5 * time.Minute

// kernelVersionPattern matches release strings produced by uname -r. It
// rejects empty input, shell-injection-shaped strings, and anything that
// would expand into a malformed package name. Examples that match:
//
//	6.8.0-111-generic
//	6.14.0-rc1
//	6.5.13-pve
//	6.1.0-13-amd64
//	6.8.0-1010+something_distro
var kernelVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+([-+][a-zA-Z0-9._+-]+)?$`)

// validKernelVersion reports whether v is plausible enough to substitute
// into a package name.
func validKernelVersion(v string) bool {
	return kernelVersionPattern.MatchString(v)
}

// detectKernelVersion returns the running kernel's release string with a
// three-source fallback chain so a single failure mode (AppArmor masking
// /usr/bin/uname, systemd PrivatePath hiding /proc/sys/kernel/osrelease,
// $PATH stripped under the daemon's hardened unit) does not produce an
// empty value the install path silently swallows.
//
//  1. /proc/sys/kernel/osrelease — direct file read; works under most
//     systemd hardening since /proc is rarely masked.
//  2. uname -r via PATH — the historical default; covered by AppArmor
//     and PrivatePath= in some configurations.
//  3. uname(2) syscall via golang.org/x/sys/unix — bypasses both
//     /proc and PATH entirely.
//
// procRoot is "/" in production; tests inject a tempdir for the /proc fixture.
func detectKernelVersion(procRoot string) (string, error) {
	if v := readProcOSRelease(procRoot); v != "" {
		return v, nil
	}
	if v, err := exec.Command("uname", "-r").Output(); err == nil {
		if s := strings.TrimSpace(string(v)); s != "" {
			return s, nil
		}
	}
	var u unix.Utsname
	if err := unix.Uname(&u); err == nil {
		s := strings.TrimRight(string(u.Release[:]), "\x00")
		if s = strings.TrimSpace(s); s != "" {
			return s, nil
		}
	}
	return "", ErrKernelVersionUnknown
}

// readProcOSRelease reads <procRoot>/proc/sys/kernel/osrelease and returns
// the trimmed value. An I/O error or an empty file returns the empty string
// — the caller falls through to the next source.
func readProcOSRelease(procRoot string) string {
	if procRoot == "" {
		procRoot = "/"
	}
	b, err := os.ReadFile(procRoot + "/proc/sys/kernel/osrelease")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// runInstallWithTimeout runs `mgr args...` under a kernelHeadersInstallTimeout
// context. On deadline-exceeded the command is killed and a clear error is
// returned identifying the timeout — the wizard's "Could not install kernel
// headers — install timed out" is a much better failure surface than the
// daemon hanging silently on `apt-get install -y linux-headers-` (no
// version) or a dpkg lock that won't clear (#769).
func runInstallWithTimeout(mgr string, args []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), kernelHeadersInstallTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, mgr, args...).CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return out, fmt.Errorf("%s install timed out after %s — check for a stuck dpkg lock, a stalled mirror, or run `sudo %s %s` by hand to see the prompt: %w",
			mgr, kernelHeadersInstallTimeout, mgr, strings.Join(args, " "), context.DeadlineExceeded)
	}
	return out, err
}
