package observation

import (
	"testing"
)

// TestSchemaV2_WriterNeverEmitsV1 asserts that the writer at v0.5.5+
// always emits schema_version=2 in the file header — never v1, even
// transiently or under error paths. Catches the bug class where a
// writer accidentally emits v1 records on legacy systems.
//
// Audit gap #4 (schema v1↔v2 bidirectional). RULE-OPP-OBS-01.
func TestSchemaV2_WriterNeverEmitsV1(t *testing.T) {
	w, env := newTestWriter(t, nil)
	appendNRecords(t, w, 5)

	payloads := collectPayloads(t, env.log)
	if len(payloads) == 0 {
		t.Fatal("no payloads emitted")
	}
	// First payload is the file header.
	hdr, _, err := UnmarshalPayload(payloads[0])
	if err != nil {
		t.Fatalf("UnmarshalPayload header: %v", err)
	}
	if hdr == nil {
		t.Fatal("first payload not a header")
	}
	if hdr.SchemaVersion != schemaVersion {
		t.Errorf("writer emitted schema_version=%d, expected %d (current)", hdr.SchemaVersion, schemaVersion)
	}
	if hdr.SchemaVersion < schemaVersion {
		t.Errorf("writer regressed: emitted older schema v%d while v%d is current", hdr.SchemaVersion, schemaVersion)
	}
}

// TestSchemaV1_ReaderRejectsCorruptedV1AsV2 asserts that a v1 file
// header read by a v2 reader is treated as a valid v1 file (forward-
// compat per RULE-OPP-OBS-01), but a corrupt header — e.g., one
// claiming v1 but with a v2-only event-flag bit set in the records
// — is accepted by the reader (because event-flag bits beyond the
// known set are ignored, never silently corrupted as a v2-flag).
//
// This catches the bug class where a reader silently corrupts v1
// records by treating an unknown event-flag bit as a v2 flag.
func TestSchemaV1_ReaderToleratesUnknownBits(t *testing.T) {
	// Build a v1 header.
	v1Hdr := &Header{
		SchemaVersion:   schemaV1Min,
		ChannelClassMap: map[uint16]uint8{1: 0},
	}
	hdrPayload, err := MarshalHeader(v1Hdr)
	if err != nil {
		t.Fatalf("MarshalHeader: %v", err)
	}
	// Record with a high bit set (bit 13 = v2-only OPPORTUNISTIC_PROBE
	// according to v0.5.5 schema). A "well-behaved" v1 writer
	// wouldn't emit this — but a corrupt or future writer might.
	rec := &Record{
		Ts:         42,
		ChannelID:  1,
		PWMWritten: 100,
		EventFlags: EventFlag_OPPORTUNISTIC_PROBE,
	}
	recPayload, err := MarshalRecord(rec)
	if err != nil {
		t.Fatalf("MarshalRecord: %v", err)
	}

	store := &mockLogStore{
		files: [][][]byte{{hdrPayload, recPayload}},
	}
	rd := NewReader(store)

	var seenFlags uint32
	err = rd.Stream(noSince, func(r *Record) bool {
		seenFlags = r.EventFlags
		return true
	})
	if err != nil {
		t.Fatalf("Stream: unexpected error: %v", err)
	}
	// The reader returns the bits as-is; downstream consumers
	// know which bits they understand. The reader does NOT
	// silently mask or transform bits.
	if seenFlags != EventFlag_OPPORTUNISTIC_PROBE {
		t.Errorf("Stream: event flags mutated by reader: got 0x%x want 0x%x",
			seenFlags, EventFlag_OPPORTUNISTIC_PROBE)
	}
}

// TestSchemaVersion_BoundsAreSane is a meta-invariant: the v2
// schema is the current emit version, the v1 floor is 1, and the
// gap between them is exactly the v0.5.5 bump (1 step). Catches
// accidental schema-version arithmetic errors.
func TestSchemaVersion_BoundsAreSane(t *testing.T) {
	if schemaV1Min != 1 {
		t.Errorf("schemaV1Min should be 1 (forward-compat floor), got %d", schemaV1Min)
	}
	if schemaVersion != 2 {
		t.Errorf("schemaVersion should be 2 (v0.5.5 bump), got %d", schemaVersion)
	}
	if schemaVersion < schemaV1Min {
		t.Errorf("schemaVersion %d < schemaV1Min %d", schemaVersion, schemaV1Min)
	}
}
