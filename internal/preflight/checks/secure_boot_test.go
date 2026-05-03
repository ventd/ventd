package checks

import (
	"context"
	"os"
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
		MOKPassword:  "ventd-test1",
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

	t.Run("RULE-PREFLIGHT-SB-08_signfile_check_uses_injected_probe", func(t *testing.T) {
		// The check MUST consult the SecureBootProbes.HasBinary
		// callback rather than hard-wiring exec.LookPath, so test
		// fixtures (and the live fallback to the kernel-headers
		// path) can resolve sign-file via paths PATH doesn't cover.
		// Caught on Phoenix's desktop where sign-file lives at
		// /usr/src/linux-headers-<release>/scripts/sign-file
		// (the canonical DKMS-hardcoded location).
		p := fakeProbes()
		p.HasBinary = func(name string) bool {
			// Simulate the live behaviour: PATH miss but
			// canonical-headers-path hit, surfaced as "yes" via
			// the probe.
			return name == "sign-file"
		}
		c := SecureBootChecks(*p)[0]
		tr, _ := c.Detect(context.Background())
		if tr {
			t.Fatal("signfile_missing triggered when probe says present")
		}
	})

	t.Run("RULE-PREFLIGHT-SB-10_enroll_pipes_password_to_mokutil_twice", func(t *testing.T) {
		// shim's MOK Manager firmware enforces a minimum password
		// length and rejects empty queues. mokutil --import reads
		// the password TWICE (with echo off) and only queues if both
		// reads match. The AutoFix MUST pipe the SecureBootProbes
		// MOKPassword to mokutil's stdin via two echo statements so
		// both reads see the same value. Caught on Phoenix's HIL
		// where firmware rejected the empty-stdin queue with
		// "unacceptable password length".
		p := fakeProbes()
		p.MOKKeyExists = func() bool { return true }
		p.MOKEnrolled = func(context.Context) (bool, error) { return false, nil }
		p.MOKPassword = "ventd-abcd"
		r := &recordingRunner{}
		p.Run = r.run

		c := SecureBootChecks(*p)[3] // mok_not_enrolled
		if err := c.AutoFix(context.Background()); err != nil {
			t.Fatalf("AutoFix: %v", err)
		}
		if len(r.commands) != 1 {
			t.Fatalf("commands: got %d, want 1", len(r.commands))
		}
		cmd := r.commands[0]
		if !strings.Contains(cmd, "mokutil --import") {
			t.Fatalf("missing mokutil --import: %s", cmd)
		}
		// Both echo statements must reference the password — two
		// reads, two echoes.
		if strings.Count(cmd, "ventd-abcd") != 2 {
			t.Fatalf("password not piped twice: %s", cmd)
		}
	})

	t.Run("RULE-PREFLIGHT-SB-11_password_format_is_ventd_4hex", func(t *testing.T) {
		// generateMOKPassword produces a "ventd-XXXX" string with
		// 4 hex chars. 10 chars total — long enough to clear shim's
		// minimum (we haven't seen one above 8 in the wild) and
		// short enough to type at firmware where keyboard layout
		// may be quirky and there's no copy-paste. Stable format
		// lets operators recognise the password as theirs (vs
		// random goop).
		got, err := generateMOKPassword()
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		if !strings.HasPrefix(got, "ventd-") {
			t.Fatalf("missing prefix: %s", got)
		}
		hex := strings.TrimPrefix(got, "ventd-")
		if len(hex) != 4 {
			t.Fatalf("hex suffix length: got %d, want 4", len(hex))
		}
		for _, c := range hex {
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				t.Fatalf("non-hex char in suffix: %s", got)
			}
		}
	})

	t.Run("RULE-PREFLIGHT-SB-12_mok_password_is_session_cached", func(t *testing.T) {
		// First loadOrGenerateMOKPassword call writes a fresh password
		// to disk; the second call reads the same value back. Lets
		// the operator re-run preflight (e.g. after firmware MOK
		// Manager rejected the password) without seeing a NEW one.
		dir := t.TempDir()
		path := dir + "/mok-session"
		first, err := loadOrGenerateMOKPassword(path)
		if err != nil {
			t.Fatalf("first call: %v", err)
		}
		if first == "" {
			t.Fatal("first call returned empty password")
		}
		second, err := loadOrGenerateMOKPassword(path)
		if err != nil {
			t.Fatalf("second call: %v", err)
		}
		if first != second {
			t.Errorf("session cache didn't stick: %q vs %q", first, second)
		}
	})

	t.Run("RULE-PREFLIGHT-SB-12_empty_path_disables_caching", func(t *testing.T) {
		// Empty path → no session cache → every call generates fresh.
		// Test environment for legacy invocations and unit tests that
		// don't want filesystem state.
		first, err := loadOrGenerateMOKPassword("")
		if err != nil {
			t.Fatalf("first: %v", err)
		}
		// Two consecutive generations should differ (4 random hex
		// chars per call → collision rate 1/65536, vanishingly
		// unlikely).
		second, err := loadOrGenerateMOKPassword("")
		if err != nil {
			t.Fatalf("second: %v", err)
		}
		if first == second {
			t.Errorf("empty path returned identical passwords twice — caching leaked: %q", first)
		}
	})

	t.Run("RULE-PREFLIGHT-SB-12_overpermissive_cache_is_rejected", func(t *testing.T) {
		// A pre-existing cache file with mode > 0600 indicates either
		// operator misuse or a write through a permissive umask. The
		// loader rejects rather than silently chmod'ing — fail noisy.
		dir := t.TempDir()
		path := dir + "/mok-session"
		if err := os.WriteFile(path, []byte("ventd-1234\n"), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
		_, err := loadOrGenerateMOKPassword(path)
		if err == nil {
			t.Fatal("expected error for over-permissive cache file, got nil")
		}
		if !strings.Contains(err.Error(), "0600") {
			t.Errorf("error doesn't mention required mode: %v", err)
		}
	})

	t.Run("RULE-PREFLIGHT-SB-12_empty_cache_file_regenerates", func(t *testing.T) {
		// Whitespace-only cache file is treated as missing — fall
		// through to fresh generation. Recovers from a partial write
		// or operator-cleared file rather than panicking.
		dir := t.TempDir()
		path := dir + "/mok-session"
		if err := os.WriteFile(path, []byte("   \n"), 0o600); err != nil {
			t.Fatalf("seed: %v", err)
		}
		pw, err := loadOrGenerateMOKPassword(path)
		if err != nil {
			t.Fatalf("regen: %v", err)
		}
		if !strings.HasPrefix(pw, "ventd-") {
			t.Errorf("regen returned unexpected shape: %q", pw)
		}
	})

	t.Run("RULE-PREFLIGHT-SB-12_cleanup_removes_cache_on_enrollment", func(t *testing.T) {
		// liveMOKEnrolledWithCleanup observes enrollment and removes
		// the session-cache file. The password is no longer needed
		// post-enrollment; leaving it on disk extends the leak
		// window for a secret that's served its purpose.
		dir := t.TempDir()
		path := dir + "/mok-session"
		if err := os.WriteFile(path, []byte("ventd-1234\n"), 0o600); err != nil {
			t.Fatalf("seed: %v", err)
		}

		stub := func(context.Context) (bool, error) { return true, nil }
		fn := makeMOKEnrolledWithCleanup(path, stub)
		enrolled, err := fn(context.Background())
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		if !enrolled {
			t.Fatal("expected enrolled=true")
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("cache file still present after enrollment: %v", err)
		}
	})

	t.Run("RULE-PREFLIGHT-SB-12_cleanup_keeps_cache_when_not_enrolled", func(t *testing.T) {
		// When MOK is NOT enrolled, the cache file MUST stay on disk.
		// The operator may still need to type the password at the
		// next firmware MOK Manager attempt.
		dir := t.TempDir()
		path := dir + "/mok-session"
		if err := os.WriteFile(path, []byte("ventd-1234\n"), 0o600); err != nil {
			t.Fatalf("seed: %v", err)
		}

		stub := func(context.Context) (bool, error) { return false, nil }
		fn := makeMOKEnrolledWithCleanup(path, stub)
		enrolled, _ := fn(context.Background())
		if enrolled {
			t.Fatal("expected enrolled=false")
		}
		if _, err := os.Stat(path); err != nil {
			t.Errorf("cache file removed despite not-enrolled: %v", err)
		}
	})

	t.Run("RULE-PREFLIGHT-SB-09_normaliseFingerprint_strips_colons_and_case", func(t *testing.T) {
		// The fingerprint matcher MUST handle openssl's
		// "SHA1 Fingerprint=AA:BB:..." output and mokutil's
		// "SHA1 Fingerprint: aa:bb:..." output uniformly. A
		// case-sensitive or colon-sensitive comparison would
		// produce false negatives on every machine because
		// openssl emits uppercase-with-equals and mokutil emits
		// lowercase-with-colon-after-Fingerprint.
		got := normaliseFingerprint("SHA1 Fingerprint=ED:9A:EB:D6:78:43:BE:7E\n")
		want := "ed9aebd67843be7e"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
		list := normaliseFingerprintList("SHA1 Fingerprint: ED:9A:EB:D6:78:43:BE:7E\n  Subject: CN=ventd")
		if !strings.Contains(list, want) {
			t.Fatalf("normalised list missing fingerprint: %s", list)
		}
	})
}
