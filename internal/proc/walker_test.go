package proc

import (
	"fmt"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/testfixture/fakeprocsys"
)

// writeFakePID stages a /proc/<pid>/{comm,stat,statm} triple under
// the fakeprocsys roots. Tests use this to build deterministic
// process snapshots.
func writeFakePID(t *testing.T, r fakeprocsys.Roots, pid int, comm string, ppid int, utime, stime, rssPages uint64) {
	t.Helper()
	rel := fmt.Sprintf("%d", pid)
	r.WriteFile(t, rel+"/comm", comm+"\n")
	// /proc/<pid>/stat format: PID (comm) state ppid pgrp ...
	// We need fields up to and including stime (field 15).
	r.WriteFile(t, rel+"/stat",
		fmt.Sprintf("%d (%s) S %d 0 0 0 -1 0 0 0 0 0 %d %d 0 0 0 0 0 0 0 0 0 0\n",
			pid, comm, ppid, utime, stime))
	r.WriteFile(t, rel+"/statm",
		fmt.Sprintf("100 %d 50 1 0 1 0\n", rssPages))
}

// TestProcWalker_ReadsCommUtimeRSSPPid asserts the walker pulls
// the four canonical fields from /proc/<pid>/{comm,stat,statm}.
// RULE-SIG-LIB-01 (the inputs the gate evaluates).
func TestProcWalker_ReadsCommUtimeRSSPPid(t *testing.T) {
	r := fakeprocsys.Idle(t)
	// Stage /proc/stat with one CPU line summing to 1000 jiffies.
	r.WriteFile(t, "stat", "cpu  500 0 300 200 0 0 0 0 0 0\ncpu0 500 0 300 200 0 0 0 0 0 0\n")
	writeFakePID(t, r, 1234, "chrome", 1, 100, 50, 5000)
	writeFakePID(t, r, 5678, "[kthreadd]", 0, 0, 0, 0)

	w := New(r.ProcRoot, 100, 4096)
	samples, err := w.Walk()
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(samples) < 2 {
		t.Fatalf("expected ≥2 samples, got %d", len(samples))
	}

	// First call seeds; assert fields.
	var chrome, kthread *ProcessSample
	for i := range samples {
		switch samples[i].Comm {
		case "chrome":
			chrome = &samples[i]
		case "[kthreadd]":
			kthread = &samples[i]
		}
	}
	if chrome == nil {
		t.Fatal("chrome sample missing")
	}
	if chrome.PPid != 1 {
		t.Errorf("chrome PPid: got %d, want 1", chrome.PPid)
	}
	if chrome.RSSBytes != 5000*4096 {
		t.Errorf("chrome RSSBytes: got %d, want %d", chrome.RSSBytes, 5000*4096)
	}
	if chrome.IsKThread {
		t.Error("chrome flagged as kthread")
	}

	if kthread == nil {
		t.Fatal("kthread sample missing")
	}
	if !kthread.IsKThread {
		t.Error("[kthreadd] not flagged as kthread")
	}
}

// TestProcWalker_FirstCallSeedsZeroCPU asserts that the first
// Walk() returns EWMACPU=0 (seed call) and the second Walk()
// returns a meaningful share.
func TestProcWalker_FirstCallSeedsZeroCPU(t *testing.T) {
	r := fakeprocsys.Idle(t)
	r.WriteFile(t, "stat", "cpu  100 0 100 800 0 0 0 0 0 0\ncpu0 100 0 100 800 0 0 0 0 0 0\n")
	writeFakePID(t, r, 1, "init", 0, 50, 50, 1000)

	w := New(r.ProcRoot, 100, 4096)
	w.now = func() time.Time { return time.Unix(1_000_000, 0) }

	samples1, err := w.Walk()
	if err != nil {
		t.Fatal(err)
	}
	if samples1[0].EWMACPU != 0 {
		t.Errorf("first Walk EWMACPU: got %f, want 0", samples1[0].EWMACPU)
	}

	// Advance both system and process jiffies.
	// System total goes 1000 → 1200 (delta 200). PID 1 utime+stime
	// goes 100 → 200 (delta 100). Share = 100/200 × 1 cpu = 0.5
	// of one core.
	r.WriteFile(t, "stat", "cpu  150 0 150 900 0 0 0 0 0 0\ncpu0 150 0 150 900 0 0 0 0 0 0\n")
	writeFakePID(t, r, 1, "init", 0, 100, 100, 1000)
	w.now = func() time.Time { return time.Unix(1_000_002, 0) }

	samples2, err := w.Walk()
	if err != nil {
		t.Fatal(err)
	}
	if samples2[0].EWMACPU == 0 {
		t.Errorf("second Walk EWMACPU should be >0 with rising jiffies")
	}
}

// TestProcWalker_ReadsZeroPID asserts that a malformed PID dir
// (e.g., empty comm) is silently skipped without erroring.
func TestProcWalker_SkipsMalformedPID(t *testing.T) {
	r := fakeprocsys.Idle(t)
	r.WriteFile(t, "stat", "cpu  100 0 100 800 0 0 0 0 0 0\n")
	// Valid PID.
	writeFakePID(t, r, 100, "valid", 1, 10, 10, 100)
	// Malformed PID — no comm file.
	r.WriteFile(t, "999/stat", "999 (orphan) S 1 0\n")

	w := New(r.ProcRoot, 100, 4096)
	samples, err := w.Walk()
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	for _, s := range samples {
		if s.PID == 999 {
			t.Errorf("malformed PID 999 not skipped")
		}
	}
}
