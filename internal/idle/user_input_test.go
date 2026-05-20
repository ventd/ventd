package idle

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReadIRQCounters_ReadsRealProcRoot is the regression test for the
// v1.0.0-and-earlier opportunistic-prober blocker. The pre-fix function
// joined procRoot with `"proc/interrupts"`, so production
// (`procRoot="/proc"`) tried to open `/proc/proc/interrupts` — every
// read failed, every opportunistic gate refused with
// ReasonProcInterruptsUnreadable, and the prober never ran on any real
// host. Unit tests masked the bug by injecting `IRQReader` overrides
// instead of exercising the real reader.
//
// This test stages an `interrupts` file under a `<tempdir>/proc` root
// — the same convention `writeProcFile` and the rest of the idle
// package use — and asserts that `ReadIRQCounters` opens it and
// parses successfully. With the bug present, the function would try
// `<tempdir>/proc/proc/interrupts` and return an error; with the fix,
// the IRQ map is populated.
func TestReadIRQCounters_ReadsRealProcRoot(t *testing.T) {
	dir := t.TempDir()
	procRoot := filepath.Join(dir, "proc")
	if err := os.MkdirAll(procRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	// One CPU header + a numeric IRQ row + a per-CPU keyboard IRQ row.
	// Trailing label column lets readIRQLabelFromInterrupts classify
	// without /sys/kernel/irq/<id>/actions in this fixture.
	content := `           CPU0       CPU1
  1:        42         0   IO-APIC    1-edge      i8042
  9:       100        80   IO-APIC    9-edge      mouse
`
	if err := os.WriteFile(filepath.Join(procRoot, "interrupts"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	counters, err := ReadIRQCounters(procRoot)
	if err != nil {
		t.Fatalf("ReadIRQCounters returned %v; with the pre-fix bug this errored every time", err)
	}
	if got, want := counters["1"], uint64(42); got != want {
		t.Errorf("IRQ 1 sum = %d, want %d (parsed both CPUs)", got, want)
	}
	if got, want := counters["9"], uint64(180); got != want {
		t.Errorf("IRQ 9 sum = %d, want %d", got, want)
	}
}

// TestReadIRQCounters_DefaultsToRealProc covers the empty/"/"
// procRoot fallback path. Operators running ventd inside a chroot
// or under a fakeroot occasionally pass an empty string here; the
// function must still hit /proc/interrupts directly rather than
// returning a "" or "/interrupts" path.
func TestReadIRQCounters_DefaultsToRealProc(t *testing.T) {
	// We can't assert successful parse against the real /proc on every
	// test runner (rootless containers may restrict it), but we can
	// assert that the function chooses the right path. Verify via
	// interruptsPath instead — exercising the lookup helper directly
	// keeps this test independent of the host's /proc visibility.
	got := interruptsPath("")
	if got != "/proc/interrupts" {
		t.Errorf("interruptsPath(\"\") = %q, want /proc/interrupts", got)
	}
	got = interruptsPath("/")
	if got != "/proc/interrupts" {
		t.Errorf("interruptsPath(\"/\") = %q, want /proc/interrupts", got)
	}
	got = interruptsPath("/proc")
	if got != "/proc/interrupts" {
		t.Errorf("interruptsPath(\"/proc\") = %q, want /proc/interrupts (production wiring)", got)
	}
	got = interruptsPath("/tmp/X/proc")
	if got != "/tmp/X/proc/interrupts" {
		t.Errorf("interruptsPath(\"/tmp/X/proc\") = %q, want /tmp/X/proc/interrupts (test-fixture convention)", got)
	}
}
