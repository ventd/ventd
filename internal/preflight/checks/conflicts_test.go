package checks

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/hwmon"
)

func okConflictProbes() *ConflictProbes {
	r := &recordingRunner{}
	return &ConflictProbes{
		InTreeDriverConflict: func(string) (string, bool) { return "", false },
		StaleDKMSState:       func(string) bool { return false },
		AnotherWizardRunning: func() bool { return false },
		UserspaceFanDaemon:   func() string { return "" },
		AptLockHeld:          func() bool { return false },
		DiskFreeBytes:        func(string) (uint64, error) { return 10 * 1024 * 1024 * 1024, nil },
		TargetModule:         "nct6687",
		BlacklistDropInPath:  "/etc/modprobe.d/ventd-blacklist.conf",
		Run:                  r.run,
	}
}

func TestConflictChecks(t *testing.T) {
	t.Run("RULE-PREFLIGHT-CONFL-01_in_tree_autofix_unbinds_and_blacklists", func(t *testing.T) {
		// The in-tree-conflict AutoFix MUST do both: modprobe -r
		// (clears immediate state) AND write to the blacklist
		// drop-in (prevents the conflict from coming back at next
		// boot). A modprobe-only fix would let the next reboot
		// reload the in-tree driver and fail the wizard again.
		p := okConflictProbes()
		p.InTreeDriverConflict = func(target string) (string, bool) {
			return "nct6683", true
		}
		r := &recordingRunner{}
		p.Run = r.run
		c := findByName(t, ConflictChecks(*p), "in_tree_driver_conflict")
		if err := c.AutoFix(context.Background()); err != nil {
			t.Fatalf("AutoFix: %v", err)
		}
		if len(r.commands) != 2 {
			t.Fatalf("commands: got %d, want 2 (modprobe -r + blacklist)", len(r.commands))
		}
		if !strings.HasPrefix(r.commands[0], "modprobe -r ") {
			t.Fatalf("first command not modprobe -r: %s", r.commands[0])
		}
		if !strings.Contains(r.commands[1], "blacklist nct6683") {
			t.Fatalf("blacklist line not written: %s", r.commands[1])
		}
		if !strings.Contains(r.commands[1], p.BlacklistDropInPath) {
			t.Fatalf("blacklist path not used: %s", r.commands[1])
		}
	})

	t.Run("RULE-PREFLIGHT-CONFL-02_stale_dkms_autofix_runs_remove_all", func(t *testing.T) {
		// `dkms remove --all` is required (not just `dkms remove
		// --version=X`) because we don't know which versions are
		// present without parsing dkms output. --all clears them
		// in one call.
		p := okConflictProbes()
		p.StaleDKMSState = func(string) bool { return true }
		r := &recordingRunner{}
		p.Run = r.run
		c := findByName(t, ConflictChecks(*p), "stale_dkms_state")
		if err := c.AutoFix(context.Background()); err != nil {
			t.Fatalf("AutoFix: %v", err)
		}
		if len(r.commands) != 1 || !strings.Contains(r.commands[0], "dkms remove --all") {
			t.Fatalf("got %v", r.commands)
		}
	})

	t.Run("RULE-PREFLIGHT-CONFL-03_userspace_daemon_stops_each_active", func(t *testing.T) {
		// Multiple competing daemons (rare but happens — fancontrol
		// + thinkfan on a Lenovo with both packages installed) MUST
		// each be stopped. A loop that returns after the first stop
		// would leave the second daemon competing for the same
		// hwmon paths.
		p := okConflictProbes()
		p.UserspaceFanDaemon = func() string { return "fancontrol, thinkfan" }
		r := &recordingRunner{}
		p.Run = r.run
		c := findByName(t, ConflictChecks(*p), "userspace_fan_daemon_active")
		if err := c.AutoFix(context.Background()); err != nil {
			t.Fatalf("AutoFix: %v", err)
		}
		if len(r.commands) != 2 {
			t.Fatalf("commands: got %d, want 2", len(r.commands))
		}
		for i, want := range []string{"fancontrol", "thinkfan"} {
			if !strings.Contains(r.commands[i], "systemctl disable --now "+want) {
				t.Errorf("commands[%d] = %q, want stop %s", i, r.commands[i], want)
			}
		}
	})

	t.Run("RULE-PREFLIGHT-CONFL-04_disk_full_skips_missing_paths", func(t *testing.T) {
		// On distros without /usr/src (some embedded ones), the
		// DiskFreeBytes probe returns ENOENT. The check MUST treat
		// that as skip (continue checking the other paths) rather
		// than fail with an error attributable to a missing path
		// the operator never had.
		p := okConflictProbes()
		p.DiskFreeBytes = func(path string) (uint64, error) {
			if path == "/usr/src" {
				return 0, errors.New("no such file")
			}
			return 10 * 1024 * 1024 * 1024, nil
		}
		c := findByName(t, ConflictChecks(*p), "disk_full")
		if tr, _ := c.Detect(context.Background()); tr {
			t.Fatalf("disk_full triggered on missing /usr/src")
		}
	})

	t.Run("RULE-PREFLIGHT-CONFL-05_disk_full_triggers_below_min_free", func(t *testing.T) {
		// Below 256 MiB on any critical path → blocker. The detail
		// string MUST include the offending path so the operator
		// knows which filesystem to free.
		p := okConflictProbes()
		p.DiskFreeBytes = func(path string) (uint64, error) {
			if path == "/lib/modules" {
				return 100 * 1024 * 1024, nil // 100 MiB < 256 MiB threshold
			}
			return 10 * 1024 * 1024 * 1024, nil
		}
		c := findByName(t, ConflictChecks(*p), "disk_full")
		tr, detail := c.Detect(context.Background())
		if !tr {
			t.Fatal("disk_full not triggered")
		}
		if !strings.Contains(detail, "/lib/modules") {
			t.Fatalf("detail missing path: %s", detail)
		}
	})

	t.Run("RULE-PREFLIGHT-CONFL-06_concurrent_install_no_autofix", func(t *testing.T) {
		// concurrent_install is docs-only — there is no safe
		// AutoFix because killing the other wizard PID could leave
		// the install in a half-state. The Check MUST have nil
		// AutoFix so the orchestrator surfaces it as docs-only.
		p := okConflictProbes()
		p.AnotherWizardRunning = func() bool { return true }
		c := findByName(t, ConflictChecks(*p), "concurrent_install")
		if c.AutoFix != nil {
			t.Fatalf("concurrent_install MUST be docs-only (nil AutoFix)")
		}
	})

	t.Run("RULE-PREFLIGHT-CONFL-07_in_tree_uses_distro_blacklist_path", func(t *testing.T) {
		// The blacklist drop-in path comes from DistroInfo so the
		// check writes to the path that THIS distro actually loads
		// at boot. A hard-coded /etc/modprobe.d/blacklist.conf would
		// land somewhere harmless on Alpine.
		p := okConflictProbes()
		p.InTreeDriverConflict = func(string) (string, bool) { return "nct6683", true }
		p.BlacklistDropInPath = "/custom/path/blacklist.conf"
		r := &recordingRunner{}
		p.Run = r.run
		c := findByName(t, ConflictChecks(*p), "in_tree_driver_conflict")
		if err := c.AutoFix(context.Background()); err != nil {
			t.Fatalf("AutoFix: %v", err)
		}
		// The second command writes to the blacklist path.
		if !strings.Contains(r.commands[1], "/custom/path/blacklist.conf") {
			t.Fatalf("custom path not honoured: %s", r.commands[1])
		}
	})

	t.Run("RULE-PREFLIGHT-CONFL-08_apt_lock_no_autofix", func(t *testing.T) {
		// apt_lock_held is docs-only — auto-fixing means waiting,
		// which is what the operator would do manually. The check
		// surfaces with a clear message asking the operator to wait
		// rather than offering to "force" the lock (which would
		// corrupt the package DB).
		p := okConflictProbes()
		p.AptLockHeld = func() bool { return true }
		c := findByName(t, ConflictChecks(*p), "apt_lock_held")
		if c.AutoFix != nil {
			t.Fatalf("apt_lock_held MUST be docs-only")
		}
	})
}

// Sanity: DefaultConflictProbes wires every field so a test that
// constructs Default() doesn't trip over a nil probe.
func TestDefaultConflictProbesNonNil(t *testing.T) {
	p := DefaultConflictProbes()
	if p.InTreeDriverConflict == nil || p.StaleDKMSState == nil ||
		p.AnotherWizardRunning == nil || p.UserspaceFanDaemon == nil ||
		p.AptLockHeld == nil || p.DiskFreeBytes == nil {
		t.Fatalf("DefaultConflictProbes left a probe nil: %+v", p)
	}
	if p.TargetModule == "" {
		t.Fatalf("default TargetModule empty")
	}
	// Distro-detected blacklist path may be the standard ventd one.
	d := hwmon.DetectDistro()
	if p.BlacklistDropInPath != d.BlacklistDropInPath() {
		t.Fatalf("blacklist path drift: %s vs %s", p.BlacklistDropInPath, d.BlacklistDropInPath())
	}
}
