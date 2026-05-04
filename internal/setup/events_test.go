package setup

import (
	"testing"
	"time"
)

// TestEvents_CursorPollAndRing exercises the activity-feed event ring:
// emit > 0 events, observe them via EventsSince(0), advance the cursor,
// emit more, observe only the new ones; then overflow the ring and
// verify the oldest entries get dropped (FIFO behaviour, bounded
// memory). This is the contract the SSE handler in
// internal/web/setup_events_sse.go depends on.
func TestEvents_CursorPollAndRing(t *testing.T) {
	m := newTestManager(t)

	// Start: empty ring, EventsSince should return nothing and bounce
	// the cursor back unchanged.
	events, cursor := m.EventsSince(0)
	if len(events) != 0 {
		t.Fatalf("fresh manager: expected 0 events, got %d", len(events))
	}
	if cursor != 0 {
		t.Fatalf("fresh manager: expected cursor 0, got %d", cursor)
	}

	// Emit a small batch.
	m.EmitEvent("info", "kmod.scan", "probing super-io space 0x290–0x2ff")
	time.Sleep(2 * time.Millisecond)
	m.EmitEvent("ok", "kmod.scan", `matched "NCT6798D" (rev 0xC2)`)
	time.Sleep(2 * time.Millisecond)
	m.EmitEvent("warn", "fan.scan", "pwm9: no tach pulses")

	events, cursor = m.EventsSince(0)
	if len(events) != 3 {
		t.Fatalf("after 3 emits: expected 3 events, got %d", len(events))
	}
	if cursor <= 0 {
		t.Errorf("expected cursor > 0 after emits, got %d", cursor)
	}
	if events[0].Tag != "kmod.scan" || events[0].Level != "info" {
		t.Errorf("event[0] tag/level mismatch: got %q/%q", events[0].Tag, events[0].Level)
	}
	if events[2].Level != "warn" {
		t.Errorf("event[2] level: got %q want warn", events[2].Level)
	}

	// Advance cursor — second poll should return zero.
	more, cursor2 := m.EventsSince(cursor)
	if len(more) != 0 {
		t.Errorf("EventsSince(latest) should return empty, got %d events", len(more))
	}
	if cursor2 != cursor {
		t.Errorf("cursor should not advance with no new events: got %d want %d", cursor2, cursor)
	}

	// Emit one more — only that one comes back on the next poll.
	m.EmitEvent("ok", "phase.scanning_fans", "Detecting fan controllers...")
	more, _ = m.EventsSince(cursor)
	if len(more) != 1 {
		t.Errorf("after 1 more emit, expected 1 new event, got %d", len(more))
	}

	// Overflow: emit more than maxEventsRingSize and verify the ring
	// stays bounded and drops the oldest entries.
	for i := 0; i < maxEventsRingSize+50; i++ {
		m.EmitEvent("info", "burst", "filler")
	}
	all, _ := m.EventsSince(0)
	if got := len(all); got > maxEventsRingSize {
		t.Errorf("ring overflowed cap: got %d entries, max %d", got, maxEventsRingSize)
	}
	if got := len(all); got != maxEventsRingSize {
		t.Errorf("ring should be exactly full after overflow: got %d, want %d", got, maxEventsRingSize)
	}
	// Oldest entries (the original 4) should be gone — the first
	// retained event should be a "burst" filler, not "kmod.scan".
	if all[0].Tag != "burst" {
		t.Errorf("oldest retained event tag: got %q want burst (oldest 4 should have been dropped)", all[0].Tag)
	}
}

// TestEvents_SetPhaseEmitsPhaseEvent pins the wiring contract that
// every setPhase call drops a "phase.<id>" event into the ring. The
// SSE feed depends on this for the calibration UI's phase-narrator
// row to update without polling /api/v1/setup/status.
func TestEvents_SetPhaseEmitsPhaseEvent(t *testing.T) {
	m := newTestManager(t)

	m.setPhase("detecting", "Scanning your system for hardware...")

	events, _ := m.EventsSince(0)
	if len(events) != 1 {
		t.Fatalf("setPhase should emit exactly 1 event, got %d", len(events))
	}
	got := events[0]
	if got.Tag != "phase.detecting" {
		t.Errorf("tag = %q, want phase.detecting", got.Tag)
	}
	if got.Level != "ok" {
		t.Errorf("level = %q, want ok", got.Level)
	}
	if got.Text != "Scanning your system for hardware..." {
		t.Errorf("text = %q, want the phase msg verbatim", got.Text)
	}
	if got.TS <= 0 {
		t.Errorf("ts = %d, want > 0", got.TS)
	}
}
