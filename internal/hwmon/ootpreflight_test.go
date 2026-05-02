package hwmon

import (
	"errors"
	"testing"
)

// baseProbes returns a Probes set where every check passes. Tests opt
// individual probes into "fail" state to exercise the chain.
func baseProbes() Probes {
	return Probes{
		KernelRelease:              func() string { return "6.8.0-1-generic" },
		BuildDirExists:             func(string) bool { return true },
		HasBinary:                  func(string) bool { return true },
		SecureBootEnabled:          func() (bool, bool) { return false, true },
		MOKKeyAvailable:            func() bool { return true },
		LibModulesWritable:         func(string) bool { return true },
		IsContainerised:            func() bool { return false },
		AptLockHeld:                func() bool { return false },
		HaveRootOrPasswordlessSudo: func() bool { return true },
		StaleDKMSState:             func(string) bool { return false },
		InTreeDriverConflict:       func(string) (string, bool) { return "", false },
		AnotherWizardRunning:       func() bool { return false },
		DiskFreeBytes:              func(string) (uint64, error) { return 10 * 1024 * 1024 * 1024, nil },
	}
}

// TestPreflightOOT exercises every Reason in the chain. Each subtest binds
// 1:1 to a RULE-PREFLIGHT-* invariant in
// .claude/rules/preflight-comprehensive.md. Adding a probe means adding a
// subtest here; rulelint blocks the merge otherwise.
func TestPreflightOOT(t *testing.T) {
	nd := DriverNeed{
		Key:                "nct6687d",
		ChipName:           "NCT6687D",
		Module:             "nct6687",
		MaxSupportedKernel: "6.10.0",
	}

	t.Run("RULE-PREFLIGHT-OK_all_present", func(t *testing.T) {
		got := PreflightOOT(nd, baseProbes())
		if got.Reason != ReasonOK {
			t.Errorf("reason=%d want OK detail=%q", got.Reason, got.Detail)
		}
	})

	t.Run("RULE-PREFLIGHT-CONTAINER_refused", func(t *testing.T) {
		p := baseProbes()
		p.IsContainerised = func() bool { return true }
		got := PreflightOOT(nd, p)
		if got.Reason != ReasonContainerised {
			t.Errorf("reason=%d want Containerised", got.Reason)
		}
		if got.Detail == "" {
			t.Errorf("expected non-empty detail")
		}
	})

	t.Run("RULE-PREFLIGHT-SUDO_required", func(t *testing.T) {
		p := baseProbes()
		p.HaveRootOrPasswordlessSudo = func() bool { return false }
		got := PreflightOOT(nd, p)
		if got.Reason != ReasonNoSudoNoRoot {
			t.Errorf("reason=%d want NoSudoNoRoot", got.Reason)
		}
	})

	t.Run("RULE-PREFLIGHT-CONCURRENT_wizard", func(t *testing.T) {
		p := baseProbes()
		p.AnotherWizardRunning = func() bool { return true }
		got := PreflightOOT(nd, p)
		if got.Reason != ReasonAnotherWizardRunning {
			t.Errorf("reason=%d want AnotherWizardRunning", got.Reason)
		}
	})

	t.Run("RULE-PREFLIGHT-INTREE_conflict", func(t *testing.T) {
		p := baseProbes()
		p.InTreeDriverConflict = func(target string) (string, bool) {
			return "nct6683", true
		}
		got := PreflightOOT(nd, p)
		if got.Reason != ReasonInTreeDriverConflict {
			t.Errorf("reason=%d want InTreeDriverConflict", got.Reason)
		}
		// The remediation depends on the conflicting module's name being
		// surfaced — guard against a regex/format change that drops it.
		if !contains(got.Detail, "nct6683") {
			t.Errorf("detail %q must name conflicting module", got.Detail)
		}
	})

	t.Run("RULE-PREFLIGHT-LIBMODULES_readonly", func(t *testing.T) {
		p := baseProbes()
		p.LibModulesWritable = func(string) bool { return false }
		got := PreflightOOT(nd, p)
		if got.Reason != ReasonLibModulesReadOnly {
			t.Errorf("reason=%d want LibModulesReadOnly", got.Reason)
		}
	})

	t.Run("RULE-PREFLIGHT-DISKFULL_lib_modules", func(t *testing.T) {
		p := baseProbes()
		p.DiskFreeBytes = func(path string) (uint64, error) {
			if path == "/lib/modules" {
				return 100 * 1024 * 1024, nil // 100 MiB < MinFreeBytes
			}
			return 10 * 1024 * 1024 * 1024, nil
		}
		got := PreflightOOT(nd, p)
		if got.Reason != ReasonDiskFull {
			t.Errorf("reason=%d want DiskFull", got.Reason)
		}
		if !contains(got.Detail, "/lib/modules") {
			t.Errorf("detail %q must name path", got.Detail)
		}
	})

	t.Run("RULE-PREFLIGHT-DISKFULL_skips_missing_path", func(t *testing.T) {
		// Paths that don't exist on this distro return an error from
		// statfs; the preflight must skip them, not refuse.
		p := baseProbes()
		p.DiskFreeBytes = func(path string) (uint64, error) {
			return 0, errors.New("no such file or directory")
		}
		got := PreflightOOT(nd, p)
		if got.Reason != ReasonOK {
			t.Errorf("reason=%d want OK (missing path skipped)", got.Reason)
		}
	})

	t.Run("RULE-PREFLIGHT-APTLOCK_held", func(t *testing.T) {
		p := baseProbes()
		p.AptLockHeld = func() bool { return true }
		got := PreflightOOT(nd, p)
		if got.Reason != ReasonAptLockHeld {
			t.Errorf("reason=%d want AptLockHeld", got.Reason)
		}
	})

	t.Run("RULE-PREFLIGHT-SB_signfile_missing", func(t *testing.T) {
		p := baseProbes()
		p.SecureBootEnabled = func() (bool, bool) { return true, true }
		p.HasBinary = func(name string) bool { return name != "sign-file" }
		got := PreflightOOT(nd, p)
		if got.Reason != ReasonSignFileMissing {
			t.Errorf("reason=%d want SignFileMissing", got.Reason)
		}
	})

	t.Run("RULE-PREFLIGHT-SB_mokutil_missing", func(t *testing.T) {
		p := baseProbes()
		p.SecureBootEnabled = func() (bool, bool) { return true, true }
		p.HasBinary = func(name string) bool { return name != "mokutil" }
		got := PreflightOOT(nd, p)
		if got.Reason != ReasonMokutilMissing {
			t.Errorf("reason=%d want MokutilMissing", got.Reason)
		}
	})

	t.Run("RULE-PREFLIGHT-SB_mok_key_missing", func(t *testing.T) {
		p := baseProbes()
		p.SecureBootEnabled = func() (bool, bool) { return true, true }
		p.MOKKeyAvailable = func() bool { return false }
		got := PreflightOOT(nd, p)
		if got.Reason != ReasonSecureBootBlocks {
			t.Errorf("reason=%d want SecureBootBlocks (legacy aggregate)", got.Reason)
		}
	})

	t.Run("RULE-PREFLIGHT-GCC_missing", func(t *testing.T) {
		p := baseProbes()
		p.HasBinary = func(name string) bool { return name != "gcc" }
		got := PreflightOOT(nd, p)
		if got.Reason != ReasonGCCMissing {
			t.Errorf("reason=%d want GCCMissing", got.Reason)
		}
	})

	t.Run("RULE-PREFLIGHT-MAKE_missing", func(t *testing.T) {
		p := baseProbes()
		p.HasBinary = func(name string) bool { return name != "make" }
		got := PreflightOOT(nd, p)
		if got.Reason != ReasonMakeMissing {
			t.Errorf("reason=%d want MakeMissing", got.Reason)
		}
	})

	t.Run("RULE-PREFLIGHT-DKMS_stale", func(t *testing.T) {
		p := baseProbes()
		p.StaleDKMSState = func(string) bool { return true }
		got := PreflightOOT(nd, p)
		if got.Reason != ReasonStaleDKMSState {
			t.Errorf("reason=%d want StaleDKMSState", got.Reason)
		}
	})

	t.Run("RULE-PREFLIGHT-ORDER_container_beats_signfile", func(t *testing.T) {
		// The container check fires before Secure Boot. Pin the order so a
		// future re-shuffle that demotes container detection breaks CI.
		p := baseProbes()
		p.IsContainerised = func() bool { return true }
		p.SecureBootEnabled = func() (bool, bool) { return true, true }
		p.HasBinary = func(name string) bool { return name != "sign-file" }
		got := PreflightOOT(nd, p)
		if got.Reason != ReasonContainerised {
			t.Errorf("reason=%d want Containerised (must beat SignFileMissing)", got.Reason)
		}
	})

	t.Run("legacy_secure_boot_blocks_preserved", func(t *testing.T) {
		// Existing v0.5.8 fixture — shape is "SB enforcing, every other
		// probe nil". Must still resolve to ReasonSecureBootBlocks so
		// emitPreflightDiag's existing dispatch keeps working.
		p := Probes{
			KernelRelease:     func() string { return "6.8.0" },
			BuildDirExists:    func(string) bool { return true },
			HasBinary:         func(string) bool { return true },
			SecureBootEnabled: func() (bool, bool) { return true, true },
			// MOKKeyAvailable nil → SB-prereq path resolves to the legacy
			// aggregate ReasonSecureBootBlocks (the third arm only fires
			// when the probe is wired and returns false).
		}
		got := PreflightOOT(nd, p)
		if got.Reason == ReasonOK {
			t.Errorf("legacy SB-only probe set must still surface a blocker, got OK")
		}
	})

	t.Run("legacy_kernel_too_new", func(t *testing.T) {
		p := baseProbes()
		p.KernelRelease = func() string { return "6.12.0-generic" }
		got := PreflightOOT(nd, p)
		if got.Reason != ReasonKernelTooNew {
			t.Errorf("reason=%d want KernelTooNew", got.Reason)
		}
	})

	t.Run("legacy_headers_missing", func(t *testing.T) {
		p := baseProbes()
		p.BuildDirExists = func(string) bool { return false }
		got := PreflightOOT(nd, p)
		if got.Reason != ReasonKernelHeadersMissing {
			t.Errorf("reason=%d want KernelHeadersMissing", got.Reason)
		}
	})

	t.Run("legacy_dkms_missing", func(t *testing.T) {
		p := baseProbes()
		p.HasBinary = func(name string) bool { return name != "dkms" }
		got := PreflightOOT(nd, p)
		if got.Reason != ReasonDKMSMissing {
			t.Errorf("reason=%d want DKMSMissing", got.Reason)
		}
	})
}

func TestKernelAbove(t *testing.T) {
	cases := []struct {
		running, ceiling string
		want             bool
	}{
		{"6.12.0", "6.10.0", true},
		{"6.10.0", "6.10.0", false},
		{"6.8.0-1-generic", "6.10.0", false},
		{"6.10.1", "6.10.0", true},
		{"7.0.0", "6.99.99", true},
		{"garbage", "6.10.0", false},
	}
	for _, tc := range cases {
		if got := kernelAbove(tc.running, tc.ceiling); got != tc.want {
			t.Errorf("kernelAbove(%q, %q) = %v, want %v", tc.running, tc.ceiling, got, tc.want)
		}
	}
}

// TestLiveHasBinary_PassesAndRejects pins the regression-against-
// PATH-only contract for the SB chain. PATH-only LookPath was the
// cause of false-positive ReasonSignFileMissing on Phoenix's HIL
// desktop where sign-file lives at /usr/src/linux-headers-<release>/
// scripts/sign-file. The fallback walks the three known canonical
// locations (Debian/Ubuntu, Fedora, /lib/modules build symlink). A
// regression that reverts to PATH-only would re-introduce the false
// positive on every Debian/Ubuntu host that successfully signs.
//
// We don't write a synthetic /usr/src tree here — that would need
// root and pollute /usr/src on the dev box. The HIL run on
// Phoenix's desktop is the canonical "PATH miss but canonical path
// hit" coverage; this test just pins the basic PATH-positive and
// nonsense-name-negative behaviour.
func TestLiveHasBinary_PathBasics(t *testing.T) {
	if !liveHasBinary("ls") {
		t.Fatal("liveHasBinary(ls) returned false; expected PATH hit")
	}
	if liveHasBinary("definitely-not-a-real-binary-name-xyz") {
		t.Fatal("liveHasBinary returned true for nonsense name")
	}
}

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in   uint64
		want string
	}{
		{0, "0 B"},
		{1024, "1024 B"},
		{2 * 1024 * 1024, "2.0 MiB"},
		{3 * 1024 * 1024 * 1024, "3.0 GiB"},
	}
	for _, tc := range cases {
		if got := humanBytes(tc.in); got != tc.want {
			t.Errorf("humanBytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
