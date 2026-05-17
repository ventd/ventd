package conflicts

import (
	"os"
	"path/filepath"
	"strings"
)

// detectFDHolders walks /proc/*/fd looking for symlinks that resolve
// into hwmonRoot/*/pwm* paths. Any process holding an open fd to a PWM
// channel is an active writer; reporting it lets the wizard surface the
// race even when the daemon has no systemd unit and no comm match in
// the registry.
//
// This is the safety net for "unknown competitor": even if a hand-
// rolled daemon isn't in the registry, an open fd on /sys/class/hwmon/
// .../pwm1 is unambiguous evidence that *something* writes that path.
//
// procRoot is /proc in production, fixture root in tests. hwmonRoot is
// /sys/class/hwmon. The returned map is keyed by entry Name for entries
// whose Units / ProcPatterns / ConfigPaths matched; fd-holders for
// unknown processes are surfaced under the special "unknown" pseudo-
// entry so the wizard can still warn.
//
// The signal is best-effort: /proc/PID/fd readdir requires the same
// uid as the target process or CAP_SYS_PTRACE. ventd runs as root in
// production, so this is fine; in tests we use fixture symlinks.
func detectFDHolders(procRoot, hwmonRoot string, knownByCommName map[string]*Conflict) map[string]*Conflict {
	out := make(map[string]*Conflict)
	if procRoot == "" || hwmonRoot == "" {
		return out
	}

	dirs, err := os.ReadDir(procRoot)
	if err != nil {
		return out
	}

	for _, d := range dirs {
		if !d.IsDir() || !isAllDigits(d.Name()) {
			continue
		}
		fdDir := filepath.Join(procRoot, d.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		comm := readComm(procRoot, d.Name())

		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil {
				continue
			}
			if !isHwmonPWMPath(target, hwmonRoot) {
				continue
			}

			// Attribute the fd to a known entry if comm matches one
			// we already saw via the proc detector; otherwise emit
			// under "unknown".
			attribute := "unknown"
			if c, ok := knownByCommName[comm]; ok && c != nil {
				attribute = c.Entry.Name
			}
			label := d.Name() + ":" + comm + " -> " + target
			c, ok := out[attribute]
			if !ok {
				if attribute == "unknown" {
					c = &Conflict{
						Entry: Entry{
							Name:           "unknown_pwm_writer",
							Description:    "An unidentified process holds an open file descriptor on a PWM channel",
							Intrusiveness:  IntrusivenessMedium,
							ConflictReason: "ventd cannot take exclusive PWM control while another process holds an open fd on the same /sys path. The wizard cannot stop this daemon automatically because it does not appear in the known-competitor registry — investigate which package owns this process and either disable it or extend the registry.",
						},
					}
				} else {
					c = &Conflict{Entry: knownByCommName[comm].Entry}
				}
				out[attribute] = c
			}
			c.FDHolders = append(c.FDHolders, label)
		}
	}
	return out
}

// isHwmonPWMPath returns true when target points to a hwmonRoot/*/pwmN
// file. The check is path-prefix + filename-shape rather than glob so
// it handles arbitrary symlink resolution (target may have been
// resolved through /proc/self/root, snap mounts, etc.).
func isHwmonPWMPath(target, hwmonRoot string) bool {
	if !strings.HasPrefix(target, hwmonRoot) {
		return false
	}
	base := filepath.Base(target)
	// Match pwmN exactly — skip pwmN_enable, pwmN_mode, etc. Only the
	// bare pwmN file is a write target for fan-speed PWM.
	if !strings.HasPrefix(base, "pwm") {
		return false
	}
	suffix := base[3:]
	if suffix == "" {
		return false
	}
	for _, r := range suffix {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func readComm(procRoot, pid string) string {
	b, err := os.ReadFile(filepath.Join(procRoot, pid, "comm"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
