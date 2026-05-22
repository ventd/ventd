package idle

import "testing"

// TestEvalBlockedProcesses_NilBaseline_RefusesOnAny asserts the
// pre-fix behaviour is preserved when caller doesn't wire baseline
// tracking. Any blocked process in the snap refuses with
// ReasonBlockedProcess + the process name.
func TestEvalBlockedProcesses_NilBaseline_RefusesOnAny(t *testing.T) {
	snap := map[string]int{"ffmpeg": 1}
	reason, refuse := evalBlockedProcesses(snap, nil)
	if !refuse {
		t.Fatal("expected refuse=true with nil baseline + non-empty snap")
	}
	if string(reason) != "blocked_process:ffmpeg" {
		t.Errorf("reason: got %q, want %q", reason, "blocked_process:ffmpeg")
	}
}

// TestEvalBlockedProcesses_EmptyBaseline_SeedsAndAdmits asserts the
// first-call seeding contract: caller wires an empty map, evaluator
// admits without enforcing the check and populates the baseline
// from the current snap.
func TestEvalBlockedProcesses_EmptyBaseline_SeedsAndAdmits(t *testing.T) {
	snap := map[string]int{"ffmpeg": 1, "rsync": 1}
	baseline := map[string]int{}
	reason, refuse := evalBlockedProcesses(snap, &baseline)

	if refuse {
		t.Fatalf("expected admit on first call with empty baseline; got refuse with reason %q", reason)
	}
	if len(baseline) != 2 {
		t.Errorf("baseline not seeded from snap: got %d entries, want 2", len(baseline))
	}
	if _, ok := baseline["ffmpeg"]; !ok {
		t.Error("baseline missing seeded ffmpeg")
	}
	if _, ok := baseline["rsync"]; !ok {
		t.Error("baseline missing seeded rsync")
	}
}

// TestEvalBlockedProcesses_SteadyStateProcess_Tolerated is the
// load-bearing homelab fix: a long-running media transcoder that
// was present in the previous baseline tick is treated as steady-
// state load, not as a fresh job. Admits.
func TestEvalBlockedProcesses_SteadyStateProcess_Tolerated(t *testing.T) {
	baseline := map[string]int{"ffmpeg": 1, "plex-transcoder": 1}
	snap := map[string]int{"ffmpeg": 1, "plex-transcoder": 1}
	reason, refuse := evalBlockedProcesses(snap, &baseline)

	if refuse {
		t.Fatalf("expected admit when all blocked processes are baseline-resident; "+
			"got refuse with reason %q", reason)
	}
}

// TestEvalBlockedProcesses_NewProcess_Refuses preserves the "one-off
// rsync still refuses" guarantee — a blocked process in the current
// snap but NOT in the baseline (i.e. started since the previous
// tick) is genuinely new transient work and refuses the probe.
func TestEvalBlockedProcesses_NewProcess_Refuses(t *testing.T) {
	baseline := map[string]int{"ffmpeg": 1}
	snap := map[string]int{"ffmpeg": 1, "rsync": 1}
	reason, refuse := evalBlockedProcesses(snap, &baseline)

	if !refuse {
		t.Fatal("expected refuse when snap has new blocked process not in baseline")
	}
	if string(reason) != "blocked_process:rsync" {
		t.Errorf("reason: got %q, want %q", reason, "blocked_process:rsync")
	}
	// After a refusal, the baseline must absorb the new process so
	// the same one doesn't keep triggering refusal forever — the
	// "rsync has been running for an hour, it's now steady state"
	// intuition the docstring promises.
	if _, ok := baseline["rsync"]; !ok {
		t.Error("baseline did not absorb the new process after refusal — "+
			"would cause permanent refusal on every subsequent tick")
	}
}

// TestEvalBlockedProcesses_ExitedProcess_DropsFromBaseline asserts the
// baseline tracks the current set of running blocked processes —
// when a process exits, it's dropped from the baseline so a future
// invocation of the same process counts as new.
func TestEvalBlockedProcesses_ExitedProcess_DropsFromBaseline(t *testing.T) {
	baseline := map[string]int{"ffmpeg": 1, "rsync": 1}
	// rsync has exited since the previous tick.
	snap := map[string]int{"ffmpeg": 1}
	_, refuse := evalBlockedProcesses(snap, &baseline)
	if refuse {
		t.Fatal("expected admit when snap is a subset of baseline")
	}
	if _, ok := baseline["rsync"]; ok {
		t.Error("baseline did not drop exited rsync — would mistakenly tolerate "+
			"a fresh rsync invocation later as if it were baseline-resident")
	}
	if _, ok := baseline["ffmpeg"]; !ok {
		t.Error("baseline dropped still-running ffmpeg")
	}
}
