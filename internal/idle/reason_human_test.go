package idle

import (
	"strings"
	"testing"
)

// TestReason_Human_KnownReasonsAreNonRaw asserts every defined Reason
// has a dedicated Human() string — i.e. none of the named codes fall
// through to the verbatim-pass-through path. The exhaustiveness is
// the user-facing contract: a new Reason added without a Human case
// would leak its raw sentinel onto the dashboard, which is the bug
// this method exists to prevent.
func TestReason_Human_KnownReasonsAreNonRaw(t *testing.T) {
	cases := []Reason{
		ReasonOK,
		ReasonOnBattery,
		ReasonInContainer,
		ReasonStorageMaintenance,
		ReasonBootWarmup,
		ReasonPostResumeWarmup,
		ReasonBlockedProcess,
		ReasonPSIPressure,
		ReasonCPUIdle,
		ReasonDiskActivity,
		ReasonNetActivity,
		ReasonGPUActivity,
		ReasonDurabilityInsufficient,
		ReasonRecentInputIRQ,
		ReasonActiveSSHSession,
		ReasonOpportunisticDisabled,
		ReasonOpportunisticBootWindow,
		ReasonProcInterruptsUnreadable,
	}
	for _, r := range cases {
		got := r.Human()
		if got == "" {
			t.Errorf("%q: Human() returned empty string", r)
		}
		if got == string(r) {
			t.Errorf("%q: Human() returned raw sentinel %q — needs a dedicated case", r, got)
		}
	}
}

// TestReason_Human_RecentInputIRQ_ScrubsIRQId asserts the
// recent_input_irq detail (e.g. "irq=1") does NOT leak into the
// user-facing string. The original UX complaint was the dashboard
// showing "recent_input_irq:irq=1" verbatim; the friendly text
// explains the WHAT (keyboard / mouse) without the implementation
// detail (which IRQ id).
func TestReason_Human_RecentInputIRQ_ScrubsIRQId(t *testing.T) {
	got := ReasonRecentInputIRQ.WithDetail("irq=1").Human()
	if strings.Contains(got, "irq=") {
		t.Errorf("Human() leaks IRQ id detail to user-facing string: %q", got)
	}
	if strings.Contains(got, "recent_input_irq") {
		t.Errorf("Human() leaks raw sentinel to user-facing string: %q", got)
	}
	if !strings.Contains(strings.ToLower(got), "keyboard") &&
		!strings.Contains(strings.ToLower(got), "mouse") &&
		!strings.Contains(strings.ToLower(got), "input") {
		t.Errorf("Human() doesn't explain WHAT was detected: %q", got)
	}
}

// TestReason_Human_BlockedProcess_FoldsProcessName asserts the
// process-name detail IS surfaced in the friendly string — it's
// actionable diagnostic ("stop rsync if you want a probe") in a
// way the IRQ id isn't.
func TestReason_Human_BlockedProcess_FoldsProcessName(t *testing.T) {
	got := ReasonBlockedProcess.WithDetail("rsync").Human()
	if !strings.Contains(got, "rsync") {
		t.Errorf("Human() drops process-name detail: %q", got)
	}
}

// TestReason_Human_BlockedProcess_NoDetail_StillReadable asserts the
// case where blocked_process is reported without a process name
// (older fixtures, or a refactor that drops the detail) doesn't
// produce a malformed string like "Blocking process running: ".
func TestReason_Human_BlockedProcess_NoDetail_StillReadable(t *testing.T) {
	got := ReasonBlockedProcess.Human()
	if strings.HasSuffix(got, ": ") || strings.HasSuffix(got, ":") {
		t.Errorf("Human() with empty detail produces dangling punctuation: %q", got)
	}
	if got == "" {
		t.Error("Human() returned empty string for ReasonBlockedProcess without detail")
	}
}

// TestReason_Human_UnknownPreservesRaw asserts the fallthrough
// contract: any unrecognised Reason returns its raw string so an
// operator still has SOMETHING to read in the worst case (rather
// than an empty cell or a generic "unknown" placeholder that loses
// information).
func TestReason_Human_UnknownPreservesRaw(t *testing.T) {
	raw := Reason("future_reason_not_yet_mapped")
	if got := raw.Human(); got != string(raw) {
		t.Errorf("Human() of unknown Reason: got %q, want raw %q", got, raw)
	}
}
