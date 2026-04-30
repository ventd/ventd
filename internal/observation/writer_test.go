package observation

import (
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/probe"
)

// TestWriter_FastClass_EmitsExactlyOneRecordPerTick verifies that each Append
// call emits exactly one Record, and that the Header's ChannelClassMap shows
// class=0 for a fast-class (hwmon) channel (RULE-OBS-RATE-01).
func TestWriter_FastClass_EmitsExactlyOneRecordPerTick(t *testing.T) {
	ch := &probe.ControllableChannel{PWMPath: "/sys/test/pwm1", Driver: "nct6775"}
	w, env := newTestWriter(t, []*probe.ControllableChannel{ch})
	id := ChannelID(ch.PWMPath)

	const n = 5
	for i := range n {
		if err := w.Append(&Record{Ts: int64(i + 1), ChannelID: id}); err != nil {
			t.Fatalf("Append[%d]: %v", i, err)
		}
	}

	payloads := collectPayloads(t, env.log)
	if len(payloads) != n+1 { // header + n records
		t.Fatalf("payload count: got %d, want %d (1 header + %d records)", len(payloads), n+1, n)
	}

	hdr, _, _ := UnmarshalPayload(payloads[0])
	if hdr == nil {
		t.Fatal("first payload is not a Header")
	}
	if hdr.ChannelClassMap[id] != 0 {
		t.Errorf("channel class: got %d, want 0 (fast)", hdr.ChannelClassMap[id])
	}
}

// TestWriter_SlowClass_EmitsExactlyOneRecordPerTick verifies that each Append
// call emits exactly one Record, and that the Header's ChannelClassMap shows
// class=1 for a slow-class (drivetemp) channel (RULE-OBS-RATE-02).
func TestWriter_SlowClass_EmitsExactlyOneRecordPerTick(t *testing.T) {
	ch := &probe.ControllableChannel{PWMPath: "/sys/test/pwm1", Driver: "drivetemp"}
	w, env := newTestWriter(t, []*probe.ControllableChannel{ch})
	id := ChannelID(ch.PWMPath)

	const n = 3
	for i := range n {
		if err := w.Append(&Record{Ts: int64(i + 1), ChannelID: id}); err != nil {
			t.Fatalf("Append[%d]: %v", i, err)
		}
	}

	payloads := collectPayloads(t, env.log)
	if len(payloads) != n+1 {
		t.Fatalf("payload count: got %d, want %d", len(payloads), n+1)
	}

	hdr, _, _ := UnmarshalPayload(payloads[0])
	if hdr == nil {
		t.Fatal("first payload is not a Header")
	}
	if hdr.ChannelClassMap[id] != 1 {
		t.Errorf("channel class: got %d, want 1 (slow)", hdr.ChannelClassMap[id])
	}
}

// TestWriter_ClassReadFromKV_NotRederivedPerTick verifies that channel class is
// loaded from KV at construction time and is not re-derived per tick.
// Mutating KV after construction must not change the Writer's classMap
// (RULE-OBS-RATE-03).
func TestWriter_ClassReadFromKV_NotRederivedPerTick(t *testing.T) {
	ch := &probe.ControllableChannel{PWMPath: "/sys/test/pwm1", Driver: "nct6775"}
	id := ChannelID(ch.PWMPath)

	// Pre-load class=1 into KV (overrides driver-inference result of 0).
	ml := &mockLogStore{}
	mkv := &mockKVStore{}
	_ = mkv.Set(kvNamespace, fmt.Sprintf("%s/%d", kvClassPrefix, id), "1")

	clock := func() time.Time { return testBaseTime }
	w, err := newWithClock(ml, mkv, []*probe.ControllableChannel{ch}, "", "v", slog.Default(), clock)
	if err != nil {
		t.Fatalf("newWithClock: %v", err)
	}

	// Overwrite KV with class=0 after construction.
	_ = mkv.Set(kvNamespace, fmt.Sprintf("%s/%d", kvClassPrefix, id), "0")

	if err := w.Append(&Record{ChannelID: id}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// Header must still show class=1 (the value loaded at construction).
	payloads := collectPayloads(t, ml)
	hdr, _, _ := UnmarshalPayload(payloads[0])
	if hdr == nil {
		t.Fatal("first payload is not a Header")
	}
	if hdr.ChannelClassMap[id] != 1 {
		t.Errorf("class was re-derived per tick; got %d, want 1 (from KV at construction)", hdr.ChannelClassMap[id])
	}
}

// TestWriter_New_RefusesConstructionWithExcludedField verifies that
// validateFieldExclusion rejects structs with msgpack field tags matching
// the §6.1 privacy exclusion list (RULE-OBS-PRIVACY-01).
func TestWriter_New_RefusesConstructionWithExcludedField(t *testing.T) {
	cases := []struct {
		name    string
		v       any
		wantErr bool
	}{
		// §6.1 excluded categories — one representative field per category.
		{"process_comm", struct {
			F string `msgpack:"comm"`
		}{}, true},
		{"pid", struct {
			F int `msgpack:"pid"`
		}{}, true},
		{"exe_path", struct {
			F string `msgpack:"exe"`
		}{}, true},
		{"cmdline", struct {
			F string `msgpack:"cmdline"`
		}{}, true},
		{"username", struct {
			F string `msgpack:"username"`
		}{}, true},
		{"hostname", struct {
			F string `msgpack:"hostname"`
		}{}, true},
		{"mac_addr", struct {
			F string `msgpack:"mac_addr"`
		}{}, true},
		{"ip_addr", struct {
			F string `msgpack:"ip_addr"`
		}{}, true},
		{"home_path", struct {
			F string `msgpack:"home"`
		}{}, true},
		{"nickname", struct {
			F string `msgpack:"nickname"`
		}{}, true},
		// Legitimate field names must not be rejected.
		{"ts", struct {
			F int64 `msgpack:"ts"`
		}{}, false},
		{"signature_label", struct {
			F string `msgpack:"signature_label"`
		}{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateFieldExclusion(tc.v)
			if tc.wantErr && err == nil {
				t.Errorf("expected error for excluded field %q, got nil", tc.name)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error for legitimate field %q: %v", tc.name, err)
			}
		})
	}
	// The real Record struct must always pass the exclusion check.
	if err := validateFieldExclusion(Record{}); err != nil {
		t.Errorf("Record struct failed exclusion check: %v", err)
	}
}

// TestWriter_SignatureLabel_AcceptedOpaque_NotTransformed verifies that the
// writer stores signature_label verbatim without hashing or transforming it
// (RULE-OBS-PRIVACY-03).
func TestWriter_SignatureLabel_AcceptedOpaque_NotTransformed(t *testing.T) {
	const label = "deadbeef01234567" // opaque SipHash-2-4 hex from R7
	w, env := newTestWriter(t, nil)

	if err := w.Append(&Record{Ts: 1, SignatureLabel: label}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	payloads := collectPayloads(t, env.log)
	if len(payloads) < 2 {
		t.Fatalf("expected header + record, got %d payloads", len(payloads))
	}
	_, rec, err := UnmarshalPayload(payloads[1])
	if err != nil {
		t.Fatalf("UnmarshalPayload: %v", err)
	}
	if rec == nil {
		t.Fatal("second payload is not a Record")
	}
	if rec.SignatureLabel != label {
		t.Errorf("SignatureLabel: got %q, want %q (must be stored verbatim)", rec.SignatureLabel, label)
	}
}

// TestWriter_ClassInferredFromDriver_WhenAbsentFromKV verifies that when no
// class entry exists in KV, the Writer infers class from the driver name:
// slowClassDrivers → class=1, all others → class=0 (RULE-OBS-RATE-04).
func TestWriter_ClassInferredFromDriver_WhenAbsentFromKV(t *testing.T) {
	cases := []struct {
		driver    string
		wantClass uint8
	}{
		{"drivetemp", 1},
		{"bmc", 1},
		{"ipmi", 1},
		{"nct6775", 0},
		{"it8790", 0},
		{"coretemp", 0},
		{"amdgpu", 0},
	}
	for _, tc := range cases {
		t.Run(tc.driver, func(t *testing.T) {
			ch := &probe.ControllableChannel{PWMPath: "/sys/test/pwm1", Driver: tc.driver}
			// Empty KV — no class pre-populated.
			w, env := newTestWriter(t, []*probe.ControllableChannel{ch})
			id := ChannelID(ch.PWMPath)

			if err := w.Append(&Record{Ts: 1, ChannelID: id}); err != nil {
				t.Fatalf("Append: %v", err)
			}

			payloads := collectPayloads(t, env.log)
			hdr, _, _ := UnmarshalPayload(payloads[0])
			if hdr == nil {
				t.Fatal("first payload is not a Header")
			}
			if hdr.ChannelClassMap[id] != tc.wantClass {
				t.Errorf("driver %q: class got %d, want %d",
					tc.driver, hdr.ChannelClassMap[id], tc.wantClass)
			}
		})
	}
}
