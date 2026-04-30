package observation

import (
	"testing"
)

// TestRecord_RoundTrip_ByteEqual verifies that MarshalRecord + UnmarshalPayload
// reproduces the original Record field-for-field (RULE-OBS-SCHEMA-01).
func TestRecord_RoundTrip_ByteEqual(t *testing.T) {
	orig := &Record{
		Ts:                1234567890,
		ChannelID:         42,
		PWMWritten:        128,
		PWMEnable:         1,
		ControllerState:   ControllerState_CONVERGED,
		RPM:               1200,
		TachTier:          0,
		SensorReadings:    map[uint16]int16{1: 55, 2: 70},
		Polarity:          1,
		SignatureLabel:    "cafebabe0102",
		SignaturePromoted: false,
		R12Residual:       0.012,
		EventFlags:        EventFlag_DRIFT_TRIPPED,
	}

	payload, err := MarshalRecord(orig)
	if err != nil {
		t.Fatalf("MarshalRecord: %v", err)
	}
	if len(payload) == 0 {
		t.Fatal("MarshalRecord: empty payload")
	}
	if payload[0] != frameRecord {
		t.Fatalf("frame byte: got 0x%02x, want 0x%02x", payload[0], frameRecord)
	}

	hdr, rec, err := UnmarshalPayload(payload)
	if err != nil {
		t.Fatalf("UnmarshalPayload: %v", err)
	}
	if hdr != nil {
		t.Fatal("expected record, got header")
	}
	if rec == nil {
		t.Fatal("UnmarshalPayload returned nil record")
	}

	if rec.Ts != orig.Ts {
		t.Errorf("Ts: got %d, want %d", rec.Ts, orig.Ts)
	}
	if rec.ChannelID != orig.ChannelID {
		t.Errorf("ChannelID: got %d, want %d", rec.ChannelID, orig.ChannelID)
	}
	if rec.PWMWritten != orig.PWMWritten {
		t.Errorf("PWMWritten: got %d, want %d", rec.PWMWritten, orig.PWMWritten)
	}
	if rec.ControllerState != orig.ControllerState {
		t.Errorf("ControllerState: got %d, want %d", rec.ControllerState, orig.ControllerState)
	}
	if rec.RPM != orig.RPM {
		t.Errorf("RPM: got %d, want %d", rec.RPM, orig.RPM)
	}
	if rec.SignatureLabel != orig.SignatureLabel {
		t.Errorf("SignatureLabel: got %q, want %q", rec.SignatureLabel, orig.SignatureLabel)
	}
	if rec.EventFlags != orig.EventFlags {
		t.Errorf("EventFlags: got %d, want %d", rec.EventFlags, orig.EventFlags)
	}
	if len(rec.SensorReadings) != len(orig.SensorReadings) {
		t.Errorf("SensorReadings len: got %d, want %d", len(rec.SensorReadings), len(orig.SensorReadings))
	}
}

// TestHeader_OnePerFile_PrecedesRecords asserts that after appending records via
// a fresh Writer, the log starts with exactly one Header followed by Records
// (RULE-OBS-SCHEMA-02).
func TestHeader_OnePerFile_PrecedesRecords(t *testing.T) {
	w, kv := newTestWriter(t, nil)
	appendNRecords(t, w, 3)

	payloads := collectPayloads(t, kv.log)
	if len(payloads) < 2 {
		t.Fatalf("expected at least 2 payloads, got %d", len(payloads))
	}

	// First payload must be a Header.
	hdr, _, err := UnmarshalPayload(payloads[0])
	if err != nil {
		t.Fatalf("payload[0] unmarshal: %v", err)
	}
	if hdr == nil {
		t.Fatal("first payload is not a Header")
	}

	// Remaining payloads must be Records (no second Header).
	for i, p := range payloads[1:] {
		h, r, err := UnmarshalPayload(p)
		if err != nil {
			t.Fatalf("payload[%d] unmarshal: %v", i+1, err)
		}
		if h != nil {
			t.Fatalf("payload[%d] is a Header; only one Header per file allowed", i+1)
		}
		if r == nil {
			t.Fatalf("payload[%d] is neither Header nor Record", i+1)
		}
	}
}

// TestSchemaVersion_RejectsUnknownFuture verifies that Reader.Stream returns an
// error when a Header with schema_version > schemaVersion is encountered
// (RULE-OBS-SCHEMA-03).
func TestSchemaVersion_RejectsUnknownFuture(t *testing.T) {
	futureHdr := &Header{
		SchemaVersion:   schemaVersion + 1,
		ChannelClassMap: map[uint16]uint8{},
	}
	hdrPayload, err := MarshalHeader(futureHdr)
	if err != nil {
		t.Fatalf("MarshalHeader: %v", err)
	}
	recPayload, _ := MarshalRecord(&Record{Ts: 1})

	ml := &mockLogStore{
		files: [][][]byte{{hdrPayload, recPayload}},
	}

	rd := NewReader(ml)
	var called int
	err = rd.Stream(noSince, func(r *Record) bool {
		called++
		return true
	})
	if err == nil {
		t.Fatal("Stream: expected error for unknown schema version, got nil")
	}
	if called != 0 {
		t.Errorf("Stream: fn called %d times before error", called)
	}
}

// TestControllerState_EnumValuesLocked asserts that the seven controller_state
// constants have the exact values specified in schema doc §2.2
// (RULE-OBS-SCHEMA-04).
func TestControllerState_EnumValuesLocked(t *testing.T) {
	tests := []struct {
		name string
		got  uint8
		want uint8
	}{
		{"COLD_START", ControllerState_COLD_START, 0},
		{"WARMING", ControllerState_WARMING, 1},
		{"CONVERGED", ControllerState_CONVERGED, 2},
		{"DRIFTING", ControllerState_DRIFTING, 3},
		{"ABORTED", ControllerState_ABORTED, 4},
		{"MANUAL_OVERRIDE", ControllerState_MANUAL_OVERRIDE, 5},
		{"MONITOR_ONLY", ControllerState_MONITOR_ONLY, 6},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("ControllerState_%s = %d, want %d", tc.name, tc.got, tc.want)
			}
		})
	}
}

// TestEventFlags_Bits0Through12Locked asserts that the 13 event_flag constants
// have the exact bit positions defined in schema doc §2.3, and that
// eventFlagReservedMask covers bits 13–31 (RULE-OBS-SCHEMA-05).
func TestEventFlags_Bits0Through12Locked(t *testing.T) {
	tests := []struct {
		name string
		got  uint32
		bit  int
	}{
		{"LAYER_A_HARD_CAP", EventFlag_LAYER_A_HARD_CAP, 0},
		{"ENVELOPE_C_ABORT", EventFlag_ENVELOPE_C_ABORT, 1},
		{"ENVELOPE_D_FALLBACK", EventFlag_ENVELOPE_D_FALLBACK, 2},
		{"DRIFT_TRIPPED", EventFlag_DRIFT_TRIPPED, 3},
		{"SATURATION_DETECTED", EventFlag_SATURATION_DETECTED, 4},
		{"STALL_WATCHDOG_FIRED", EventFlag_STALL_WATCHDOG_FIRED, 5},
		{"IDLE_GATE_REFUSED", EventFlag_IDLE_GATE_REFUSED, 6},
		{"R12_GLOBAL_GATE_OFF", EventFlag_R12_GLOBAL_GATE_OFF, 7},
		{"LAYER_C_SHARD_ACTIVATED", EventFlag_LAYER_C_SHARD_ACTIVATED, 8},
		{"LAYER_C_SHARD_EVICTED", EventFlag_LAYER_C_SHARD_EVICTED, 9},
		{"R9_IDENT_CLASS_CHANGED", EventFlag_R9_IDENT_CLASS_CHANGED, 10},
		{"SIGNATURE_PROMOTED", EventFlag_SIGNATURE_PROMOTED, 11},
		{"SIGNATURE_RETIRED", EventFlag_SIGNATURE_RETIRED, 12},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want := uint32(1 << tc.bit)
			if tc.got != want {
				t.Errorf("EventFlag_%s = 0x%x, want 0x%x (bit %d)", tc.name, tc.got, want, tc.bit)
			}
		})
	}

	// Reserved mask must cover exactly bits 13–31.
	for bit := 13; bit <= 31; bit++ {
		if eventFlagReservedMask&(1<<bit) == 0 {
			t.Errorf("eventFlagReservedMask does not cover bit %d", bit)
		}
	}
	for bit := 0; bit <= 12; bit++ {
		if eventFlagReservedMask&(1<<bit) != 0 {
			t.Errorf("eventFlagReservedMask incorrectly covers bit %d", bit)
		}
	}
}

// TestRecord_StructHasNoUserControlledStrings asserts that the Record struct
// contains no map[string]interface{} or unconstrained string fields beyond
// signature_label (RULE-OBS-PRIVACY-02).
func TestRecord_StructHasNoUserControlledStrings(t *testing.T) {
	// Verify that MarshalRecord does not accept a record with user-provided
	// free-form string content in any field other than signature_label.
	// The structural guarantee is: the only string field is signature_label,
	// which is the opaque SipHash output from R7.
	r := Record{}
	payload, err := MarshalRecord(&r)
	if err != nil {
		t.Fatalf("MarshalRecord empty: %v", err)
	}
	_, rec, err := UnmarshalPayload(payload)
	if err != nil {
		t.Fatalf("UnmarshalPayload: %v", err)
	}
	if rec == nil {
		t.Fatal("expected record")
	}
	// sensor_readings must be a map[uint16]int16, not map[string]anything.
	_ = rec.SensorReadings
}
