package checks

import (
	"context"
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/hwmon"
	"github.com/ventd/ventd/internal/preflight"
)

// fakeProbes returns a SecureBootProbes set where every check passes
// (SB enforcing, all binaries present, key on disk, key enrolled).
// Tests opt individual probes into "fail" state.
func fakeProbes() *SecureBootProbes {
	return &SecureBootProbes{
		Enabled:      func() (bool, bool) { return true, true },
		HasBinary:    func(string) bool { return true },
		MOKKeyExists: func() bool { return true },
		MOKEnrolled:  func(context.Context) (bool, error) { return true, nil },
		Distro:       hwmon.DistroInfo{ID: "ubuntu", IDLike: "debian"},
		MOKKeyDir:    "/tmp/test-mok",
	}
}

// recordingRunner captures the shell commands the AutoFix attempts to
// run so we can assert on the exact dispatch without invoking apt.
type recordingRunner struct{ commands []string }

func (r *recordingRunner) run(ctx context.Context, cmd string) error {
	r.commands = append(r.commands, cmd)
	return nil
}

// Each subtest binds 1:1 to a RULE-PREFLIGHT-SB-* invariant.

func TestSecureBootChecks(t *testing.T) {
	t.Run("RULE-PREFLIGHT-SB-01_chain_skipped_when_not_enforcing", func(t *testing.T) {
		// Non-UEFI / SB-disabled hosts MUST short-circuit every SB
		// Detect to !triggered. Without this, the chain would offer
		// to install kmod/mokutil and generate a key on a host that
		// will never need them.
		p := fakeProbes()
		p.Enabled = func() (bool, bool) { return false, true }
		// Force every other probe to claim missing — the gate
		// shouldn't ask.
		p.HasBinary = func(string) bool { return false }
		p.MOKKeyExists = func() bool { return false }
		for _, c := range SecureBootChecks(*p) {
			tr, _ := c.Detect(context.Background())
			if tr {
				t.Errorf("%s triggered when SB disabled", c.Name)
			}
		}
	})

	t.Run("RULE-PREFLIGHT-SB-02_signfile_missing_triggers_first", func(t *testing.T) {
		// When sign-file is absent, the first chain check fires.
		// Subsequent checks are independent (they each test their
		// own predicate) so they may fire too — the orchestrator
		// shows the operator the full picture and works through
		// them in chain order.
		p := fakeProbes()
		p.HasBinary = func(name string) bool { return name != "sign-file" }
		checks := SecureBootChecks(*p)
		tr, detail := checks[0].Detect(context.Background())
		if !tr {
			t.Fatalf("signfile_missing not triggered")
		}
		if !strings.Contains(detail, "sign-file") {
			t.Fatalf("detail missing 'sign-file': %s", detail)
		}
	})

	t.Run("RULE-PREFLIGHT-SB-03_signfile_autofix_dispatches_per_distro", func(t *testing.T) {
		// AutoFix MUST issue the distro-correct install command. On
		// Arch we install linux-headers (which bundles sign-file);
		// elsewhere we install kmod.
		cases := []struct {
			d        hwmon.DistroInfo
			wantSubs string
		}{
			{hwmon.DistroInfo{ID: "ubuntu", IDLike: "debian"}, "apt-get install -y --no-install-recommends kmod"},
			{hwmon.DistroInfo{ID: "fedora"}, "dnf install -y kmod"},
			{hwmon.DistroInfo{ID: "arch"}, "pacman -S --needed --noconfirm linux-headers"},
		}
		for _, c := range cases {
			r := &recordingRunner{}
			p := fakeProbes()
			p.Distro = c.d
			p.Run = r.run
			err := SecureBootChecks(*p)[0].AutoFix(context.Background())
			if err != nil {
				t.Fatalf("%s: %v", c.d.ID, err)
			}
			if len(r.commands) != 1 || !strings.Contains(r.commands[0], c.wantSubs) {
				t.Fatalf("%s: got commands %v, want substring %q", c.d.ID, r.commands, c.wantSubs)
			}
		}
	})

	t.Run("RULE-PREFLIGHT-SB-04_mok_keypair_check_uses_dir_field", func(t *testing.T) {
		// MOKKeyDir is the directory the keypair lands in. Without
		// it being honoured, the AutoFix would generate the key
		// somewhere mokutil --import doesn't know to look from. The
		// detail string must include the configured dir so an
		// operator running with a non-default dir can verify.
		p := fakeProbes()
		p.MOKKeyExists = func() bool { return false }
		p.MOKKeyDir = "/custom/mok/dir"
		_, detail := SecureBootChecks(*p)[2].Detect(context.Background())
		if !strings.Contains(detail, "/custom/mok/dir") {
			t.Fatalf("detail missing custom dir: %s", detail)
		}
	})

	t.Run("RULE-PREFLIGHT-SB-05_mok_enroll_requires_reboot", func(t *testing.T) {
		// The 4th check MUST set RequiresReboot=true. The
		// orchestrator reads this and surfaces a single reboot
		// prompt at the end of the chain — not one per AutoFix.
		p := fakeProbes()
		checks := SecureBootChecks(*p)
		enroll := checks[3]
		if enroll.Name != "secure_boot_mok_not_enrolled" {
			t.Fatalf("chain ordering changed: %s", enroll.Name)
		}
		if !enroll.RequiresReboot {
			t.Fatalf("RequiresReboot=false; mokutil --import only takes effect after reboot")
		}
	})

	t.Run("RULE-PREFLIGHT-SB-06_enroll_check_skips_when_keypair_absent", func(t *testing.T) {
		// The 4th check depends on the 3rd — without a keypair on
		// disk there's nothing to enroll. Detect MUST return
		// !triggered with a detail that names the prerequisite, so
		// the operator clears the predecessor first.
		p := fakeProbes()
		p.MOKKeyExists = func() bool { return false }
		tr, detail := SecureBootChecks(*p)[3].Detect(context.Background())
		if tr {
			t.Fatalf("enroll triggered without keypair; should defer to keypair_missing")
		}
		if !strings.Contains(detail, "prerequisite") {
			t.Fatalf("detail missing prerequisite hint: %s", detail)
		}
	})

	t.Run("RULE-PREFLIGHT-SB-07_all_four_severity_blocker", func(t *testing.T) {
		// Every SB check MUST be SeverityBlocker — none of them are
		// "warnings". A non-blocker SB issue would let the install
		// continue and the kernel module load to fail at modprobe
		// time, defeating the predict-not-react design.
		p := fakeProbes()
		for _, c := range SecureBootChecks(*p) {
			if c.Severity != preflight.SeverityBlocker {
				t.Errorf("%s: severity %v, want Blocker", c.Name, c.Severity)
			}
		}
	})
}
