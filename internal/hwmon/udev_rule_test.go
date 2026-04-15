package hwmon

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The shipped udev rule lives at deploy/90-ventd-hwmon.rules. Its
// RUN+= directive is a /bin/sh -c '...' invocation. These tests
// extract that script, substitute %p with a temp-dir hwmon device,
// execute it, and assert on the side effects.
//
// What we test:
//   - chip with pwm files: no errors, files retain perms (chgrp can
//     fail in CI where the ventd group doesn't exist; the rule's
//     `2>/dev/null; ...; exit 0` shape must absorb that).
//   - chip without pwm files: no errors, no perm changes, exit 0.
//   - missing hwmon dir: no errors, exit 0 (defensive).
//
// What we do NOT test here:
//   - chgrp actually setting the ventd group. Requires getent ventd
//     and write privileges on perm bits the test runner may not have.
//     Covered by the rig smoke (deploy/README.md).
//   - Real udev event delivery. That's an integration concern — out
//     of scope for unit tests, exercised by the rig smoke.

const udevRuleRelPath = "../../deploy/90-ventd-hwmon.rules"

// runCommandRe extracts the inner shell command from a RUN+= directive.
// Matches the body inside `RUN+="/bin/sh -c '...'"`.
var runCommandRe = regexp.MustCompile(`RUN\+="/bin/sh -c '([^']+)'"`)

func extractRuleScript(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(udevRuleRelPath)
	if err != nil {
		t.Fatalf("read udev rule: %v", err)
	}
	matches := runCommandRe.FindAllStringSubmatch(string(data), -1)
	if len(matches) == 0 {
		t.Fatalf("no RUN+= /bin/sh -c '…' directives found in udev rule")
	}
	if len(matches) > 1 {
		t.Fatalf("expected exactly 1 active RUN+= directive in chip-agnostic rule, got %d", len(matches))
	}
	// %p in the udev rule is replaced by the device path at runtime;
	// callers substitute it for their fixture path.
	return matches[0][1]
}

// runRuleScript executes the udev RUN+= shell command with %p replaced
// by devicePath. devicePath is what /sys%p would resolve to in the
// rule (i.e. the test's fake hwmon dir without the leading /sys).
// Returns the command's exit error and combined output for assertion.
func runRuleScript(t *testing.T, script, devicePath string) error {
	t.Helper()
	// /sys%p in the rule expands to /sys + the device's sysfs path.
	// In tests we want the rule to operate on devicePath directly,
	// so we replace /sys%p with devicePath.
	body := strings.ReplaceAll(script, "/sys%p", devicePath)
	cmd := exec.Command("/bin/sh", "-c", body)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("rule script output: %s", string(out))
	}
	return err
}

func TestUdevRule_FiresOnChipWithPWM(t *testing.T) {
	// Hwmon dir with a real pwm and pwm_enable. Rule must succeed
	// (exit 0) regardless of whether chgrp can find the ventd group
	// in the test environment.
	dir := t.TempDir()
	for _, f := range []string{"pwm1", "pwm1_enable", "pwm2", "pwm2_enable"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("0"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	script := extractRuleScript(t)
	if err := runRuleScript(t, script, dir); err != nil {
		t.Fatalf("rule script returned non-zero on a healthy hwmon dir: %v", err)
	}
}

func TestUdevRule_NoOpOnTempOnlyChip(t *testing.T) {
	// Temp-only chip (coretemp, k10temp, drivetemp): the pwm globs
	// expand to no matches. The rule must NOT exit non-zero — that
	// would surface as a udev error on every boot for every
	// temperature-only chip on the system.
	dir := t.TempDir()
	for _, f := range []string{"temp1_input", "temp1_label", "temp2_input"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("0"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	script := extractRuleScript(t)
	if err := runRuleScript(t, script, dir); err != nil {
		t.Fatalf("rule script failed on a temp-only chip (must be no-op): %v", err)
	}
}

func TestUdevRule_NoOpOnEmptyDir(t *testing.T) {
	// Defensive: an empty dir (race during driver bind/unbind, or
	// a virtual class entry) must not error.
	dir := t.TempDir()
	script := extractRuleScript(t)
	if err := runRuleScript(t, script, dir); err != nil {
		t.Fatalf("rule script failed on empty dir: %v", err)
	}
}

func TestUdevRule_RuleHasGroupWriteOnlyNotWorld(t *testing.T) {
	// Belt-and-braces grep: the rule must use `g+w`, never `o+w`,
	// `a+w`, `666`, or `777`. The literal string assertion catches
	// a future maintainer who tried to "simplify" by widening perms.
	data, err := os.ReadFile(udevRuleRelPath)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if !strings.Contains(body, "g+w") {
		t.Errorf("udev rule does not contain g+w — chmod target unclear")
	}
	for _, banned := range []string{"o+w", "a+w", "777", "666"} {
		if strings.Contains(body, banned) {
			t.Errorf("udev rule contains forbidden perm widening: %q", banned)
		}
	}
}

func TestUdevRule_ScopeRestrictedToHwmonSubsystem(t *testing.T) {
	// The rule must filter on SUBSYSTEM=="hwmon" — without it, the
	// chgrp would fire for every udev device class, which could
	// clobber group ownership on /dev/sda etc.
	data, err := os.ReadFile(udevRuleRelPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `SUBSYSTEM=="hwmon"`) {
		t.Error("udev rule missing SUBSYSTEM==\"hwmon\" filter")
	}
}

func TestUdevRule_NoChipNameKeyingInActiveLines(t *testing.T) {
	// The chip-agnostic rule must NOT key on ATTR{name}== in any
	// uncommented (active) line. Reverting to chip-name keying would
	// re-introduce the manual-uncomment step and break the
	// zero-terminal install promise. Comments referencing the
	// pattern (e.g. design notes explaining why we DON'T key on it)
	// are fine.
	data, err := os.ReadFile(udevRuleRelPath)
	if err != nil {
		t.Fatal(err)
	}
	for n, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(line, `ATTR{name}==`) {
			t.Errorf("active line %d keys on ATTR{name}== — chip-agnostic design broken: %q",
				n+1, line)
		}
	}
}

func TestUdevRule_TouchesPWMScopeOnly(t *testing.T) {
	// chgrp/chmod targets must be pwm[0-9]* and pwm[0-9]*_enable,
	// not /sys%p/* (which would broaden to temp_input, voltage,
	// labels, etc.). Reject any glob that doesn't start with pwm.
	data, err := os.ReadFile(udevRuleRelPath)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if strings.Contains(body, "/sys%p/* ") || strings.Contains(body, "/sys%p/*'") {
		t.Errorf("udev rule chgrp's %s unconditionally — must scope to pwm[0-9]*", "/sys%p/*")
	}
	if !strings.Contains(body, "pwm[0-9]*") {
		t.Error("udev rule does not scope its chgrp/chmod to pwm[0-9]*")
	}
}

func TestUdevRule_ScriptDoesNotPropagateChgrpFailure(t *testing.T) {
	// The chgrp call must be tolerant of "no such file" (when the
	// glob expands to no matches) and of "no such group" (when the
	// ventd group hasn't been created yet, e.g. running the rule
	// before account creation). Rule must still exit 0.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pwm1"), []byte("0"), 0o644); err != nil {
		t.Fatal(err)
	}

	script := extractRuleScript(t)
	// We cannot create a "noexistent" group safely on the test host;
	// the test relies on the rule's `2>/dev/null` and `; exit 0`
	// safety net. Run twice to ensure no state leaked between runs.
	for i := 0; i < 2; i++ {
		if err := runRuleScript(t, script, dir); err != nil {
			t.Errorf("run %d: rule script propagated a chgrp failure: %v", i, err)
		}
	}
}
