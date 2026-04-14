package hwmon

import (
	"bytes"
	"testing"
)

// buildPayload constructs a kernel-shaped uevent: the first record is a
// header summary, followed by NUL-separated KEY=VALUE pairs, followed by a
// trailing NUL terminator the kernel always emits.
func buildPayload(header string, kv ...string) []byte {
	var b bytes.Buffer
	b.WriteString(header)
	for _, s := range kv {
		b.WriteByte(0)
		b.WriteString(s)
	}
	b.WriteByte(0)
	return b.Bytes()
}

func TestParseUevent_HwmonAdd(t *testing.T) {
	payload := buildPayload(
		"add@/devices/platform/nct6687.2608/hwmon/hwmon5",
		"ACTION=add",
		"DEVPATH=/devices/platform/nct6687.2608/hwmon/hwmon5",
		"SUBSYSTEM=hwmon",
		"SEQNUM=12345",
	)
	msg, ok := parseUevent(payload)
	if !ok {
		t.Fatalf("expected ok for hwmon add payload")
	}
	if msg.Action != UeventActionAdd {
		t.Fatalf("action = %q", msg.Action)
	}
	if msg.Subsystem != "hwmon" {
		t.Fatalf("subsystem = %q", msg.Subsystem)
	}
	if msg.DevPath != "/devices/platform/nct6687.2608/hwmon/hwmon5" {
		t.Fatalf("devpath = %q", msg.DevPath)
	}
}

func TestParseUevent_HwmonRemove(t *testing.T) {
	payload := buildPayload(
		"remove@/devices/platform/nct6687.2608/hwmon/hwmon5",
		"ACTION=remove",
		"DEVPATH=/devices/platform/nct6687.2608/hwmon/hwmon5",
		"SUBSYSTEM=hwmon",
	)
	msg, ok := parseUevent(payload)
	if !ok || msg.Action != UeventActionRemove {
		t.Fatalf("expected remove, got ok=%v action=%q", ok, msg.Action)
	}
}

func TestParseUevent_IgnoresNonHwmon(t *testing.T) {
	payload := buildPayload(
		"add@/devices/pci/...",
		"ACTION=add",
		"SUBSYSTEM=block",
	)
	if _, ok := parseUevent(payload); ok {
		t.Fatalf("expected non-hwmon payload to be rejected")
	}
}

func TestParseUevent_IgnoresUnwantedActions(t *testing.T) {
	payload := buildPayload(
		"change@/devices/platform/nct6687.2608/hwmon/hwmon5",
		"ACTION=change",
		"SUBSYSTEM=hwmon",
	)
	if _, ok := parseUevent(payload); ok {
		t.Fatalf("change action must be dropped (only add/remove interesting)")
	}
}

func TestParseUevent_Empty(t *testing.T) {
	if _, ok := parseUevent(nil); ok {
		t.Fatal("empty payload must be rejected")
	}
	if _, ok := parseUevent([]byte{0, 0, 0}); ok {
		t.Fatal("all-zero payload must be rejected")
	}
}

func TestParseUevent_MalformedRecordsTolerated(t *testing.T) {
	// A stray record with no '=' must be skipped, not crash, and parsing of
	// subsequent records must continue.
	payload := buildPayload(
		"add@/devices/foo/hwmon/hwmon9",
		"GARBAGE",
		"=EMPTYKEY",
		"ACTION=add",
		"SUBSYSTEM=hwmon",
		"DEVPATH=/devices/foo/hwmon/hwmon9",
	)
	msg, ok := parseUevent(payload)
	if !ok {
		t.Fatalf("expected ok despite malformed records")
	}
	if msg.Action != UeventActionAdd || msg.Subsystem != "hwmon" {
		t.Fatalf("parse lost fields: %+v", msg)
	}
}
