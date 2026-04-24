package corsair

// corsairVID is Corsair's USB vendor ID.
const corsairVID = 0x1b1c

// deviceKind classifies the Commander device family.
type deviceKind int

const (
	kindCommanderCore   deviceKind = iota // Commander Core: pump ch0 + fans ch1-6
	kindCommanderCoreXT                   // Commander Core XT: all fans, no pump
	kindCommanderST                       // Commander ST: pump ch0 + fans ch1-6
)

// deviceEntry maps a PID to its family and pump presence.
type deviceEntry struct {
	pid     uint16
	kind    deviceKind
	hasPump bool
}

// knownDevices is the PID table for devices in spec-02 scope.
// Source: specs/spec-02-corsair-aio.md (PIDs) + specs/spec-02-amendment.md (narrowed family).
// Commander Pro (0x0c10) is excluded — different protocol, deferred to spec-02a.
// iCUE LINK System Hub — deferred to v0.4.1.
var knownDevices = []deviceEntry{
	// Commander Core — pump on channel 0, fans on channels 1-6.
	{pid: 0x0c1c, kind: kindCommanderCore, hasPump: true},
	{pid: 0x0c1e, kind: kindCommanderCore, hasPump: true},

	// Commander Core XT — all 6 channels are case fans; no pump.
	{pid: 0x0c20, kind: kindCommanderCoreXT, hasPump: false},
	{pid: 0x0c2a, kind: kindCommanderCoreXT, hasPump: false},

	// Commander ST — pump on channel 0, fans on channels 1-6.
	{pid: 0x0c32, kind: kindCommanderST, hasPump: true},
}

// lookupDevice returns the entry for pid, or (zero, false) if unknown.
func lookupDevice(pid uint16) (deviceEntry, bool) {
	for _, e := range knownDevices {
		if e.pid == pid {
			return e, true
		}
	}
	return deviceEntry{}, false
}

// allPIDs returns all PIDs in the known-device table (for Matcher construction).
func allPIDs() []uint16 {
	out := make([]uint16, len(knownDevices))
	for i, e := range knownDevices {
		out[i] = e.pid
	}
	return out
}
