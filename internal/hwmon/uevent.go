package hwmon

import (
	"bytes"
	"strings"
)

// UeventAction classifies kernel uevent messages we care about. Values match
// the ACTION= field emitted by the kernel; anything outside this set is
// filtered before reaching the watcher.
type UeventAction string

const (
	UeventActionAdd    UeventAction = "add"
	UeventActionRemove UeventAction = "remove"
)

// UeventMessage is the parsed form of a NETLINK_KOBJECT_UEVENT payload. Only
// the fields the watcher consumes are retained; everything else the kernel
// ships is ignored. Raw holds the NUL-separated original message for logging.
type UeventMessage struct {
	Action    UeventAction
	Subsystem string
	DevPath   string // kernel DEVPATH value — e.g. /devices/.../hwmon/hwmon3
	Raw       string
}

// parseUevent parses one netlink payload into a UeventMessage. Kernel uevent
// messages are NUL-separated; the first record is a header summary that we
// skip. Returns ok=false when the payload is empty, malformed, or not a
// SUBSYSTEM=hwmon add/remove — the watcher silently ignores those.
func parseUevent(payload []byte) (UeventMessage, bool) {
	if len(payload) == 0 {
		return UeventMessage{}, false
	}
	records := bytes.Split(payload, []byte{0})
	msg := UeventMessage{Raw: strings.ReplaceAll(string(payload), "\x00", " ")}
	// Records are KEY=VALUE pairs after the header (first record, e.g.
	// "add@/devices/..."). Empty trailing records from the NUL terminator are
	// skipped.
	for i, rec := range records {
		if i == 0 || len(rec) == 0 {
			continue
		}
		kv := string(rec)
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			continue
		}
		k, v := kv[:eq], kv[eq+1:]
		switch k {
		case "ACTION":
			msg.Action = UeventAction(v)
		case "SUBSYSTEM":
			msg.Subsystem = v
		case "DEVPATH":
			msg.DevPath = v
		}
	}
	if msg.Subsystem != "hwmon" {
		return UeventMessage{}, false
	}
	switch msg.Action {
	case UeventActionAdd, UeventActionRemove:
		return msg, true
	}
	return UeventMessage{}, false
}
