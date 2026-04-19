// Package fakeipmi provides a deterministic IPMI device fixture for unit tests.
// It emulates vendor-specific BMC responses (Supermicro, Dell, HPE) in memory
// without requiring a real /dev/ipmi0 device.
//
// Full ioctl transport requires a backend seam — see DevicePath for details.
package fakeipmi

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Options configures the behaviour of the fake IPMI device.
type Options struct {
	// Vendor selects canned responses: "supermicro", "dell", or "hpe".
	// Defaults to "supermicro" when empty.
	Vendor string

	// SensorResponses overrides per-sensor-number responses for Get Sensor
	// Reading (netfn=0x04 cmd=0x2D). Key: sensor number byte.
	// Value: complete response (completion code followed by data bytes).
	SensorResponses map[byte][]byte

	// SDRResponses overrides per-record-ID responses for Get SDR
	// (netfn=0x0A cmd=0x23). Key: SDR record ID byte.
	// Value: complete response.
	SDRResponses map[byte][]byte

	// Latency adds artificial per-call latency, useful for timeout tests.
	// Applied before BusyCount and vendor dispatch.
	Latency time.Duration

	// FailOn maps zero-based ioctl call sequence numbers to errors.
	// When Respond is called for the Nth time (0-indexed) and N is a key,
	// Respond returns that error instead of a BMC response.
	FailOn map[uint64]error

	// BusyCount causes the first BusyCount calls to Respond to return
	// completion code 0xC3 (IPMI_CC_NODE_BUSY) before returning the real
	// response. Models BMC temporary-busy backoff.
	BusyCount int
}

// Fake is a deterministic IPMI device fixture. It emulates the vendor-specific
// BMC response logic exercised by internal/hal/ipmi. All state is guarded by
// an internal mutex so concurrent test goroutines are safe.
//
// To connect a Fake to the IPMI backend, pass DevicePath() to
// NewBackend(WithDevicePath(f.DevicePath())).  The backend does not yet expose
// that option; see the T-IPMI-01a PR CONCERNS for the required change.
type Fake struct {
	t    *testing.T
	dir  string
	opts Options

	mu     sync.Mutex
	busyN  int
	reqSeq uint64
}

// New creates a Fake IPMI device fixture registered with t.Cleanup().
// All resources (temp directory, placeholder device file) are released when the
// test ends. opts may be nil; a nil opts is equivalent to &Options{}.
func New(t *testing.T, opts *Options) *Fake {
	t.Helper()
	if opts == nil {
		opts = &Options{}
	}
	dir := t.TempDir()

	// Create a placeholder file at the device path. A real char-device cannot
	// be created from userspace without root; the placeholder reserves the path
	// for when the backend seam (WithDevicePath option) lands in T-IPMI-01b.
	devPath := filepath.Join(dir, "ipmi0")
	if err := os.WriteFile(devPath, nil, 0600); err != nil {
		t.Fatalf("fakeipmi: create device placeholder %s: %v", devPath, err)
	}

	f := &Fake{
		t:     t,
		dir:   dir,
		opts:  *opts,
		busyN: opts.BusyCount,
	}
	t.Cleanup(func() {
		// t.TempDir() owns dir cleanup; nothing else to release.
	})
	return f
}

// DevicePath returns the filesystem path callers should hand to the IPMI
// backend as the device file (e.g. NewBackend(WithDevicePath(f.DevicePath()))).
//
// Currently returns a regular-file placeholder; actual ioctl dispatch requires
// NewBackend to accept a configurable device path — see the T-IPMI-01a PR
// CONCERNS section for the exact change needed.
func (f *Fake) DevicePath() string {
	return filepath.Join(f.dir, "ipmi0")
}

// Respond simulates one IPMI request/response round-trip.
//
// Order of operations:
//  1. Latency (sleep if opts.Latency > 0)
//  2. FailOn check (returns error for matching call sequence number)
//  3. BusyCount (returns 0xC3 while counter > 0, then decrements)
//  4. Vendor-specific dispatch (Supermicro / Dell / HPE)
//
// Respond is the fixture's primary test surface. T-IPMI-01b will drive it
// indirectly via the ioctl bridge once the backend seam exists.
func (f *Fake) Respond(netfn, cmd byte, data []byte) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	seq := f.reqSeq
	f.reqSeq++

	if f.opts.Latency > 0 {
		time.Sleep(f.opts.Latency)
	}

	if err, ok := f.opts.FailOn[seq]; ok {
		return nil, err
	}

	if f.busyN > 0 {
		f.busyN--
		return []byte{0xC3}, nil // IPMI_CC_NODE_BUSY
	}

	vendor := f.opts.Vendor
	if vendor == "" {
		vendor = "supermicro"
	}

	switch vendor {
	case "supermicro":
		return f.supermicroResponse(netfn, cmd, data)
	case "dell":
		return f.dellResponse(netfn, cmd, data)
	case "hpe":
		return f.hpeResponse(netfn, cmd, data)
	default:
		return nil, fmt.Errorf("fakeipmi: unknown vendor %q", vendor)
	}
}

// supermicroResponse returns canned Supermicro BMC responses.
//
// Supported commands:
//   - Get Sensor Reading (netfn=0x04 cmd=0x2D): returns [CC=0x00, raw=0x60, status=0xC0]
//   - Set Fan Control   (netfn=0x30 cmd=0x70, param=0x66): returns [CC=0x00]
//   - All others: returns [CC=0xC1] (Command Not Available)
func (f *Fake) supermicroResponse(netfn, cmd byte, data []byte) ([]byte, error) {
	if netfn == 0x04 && cmd == 0x2D {
		if len(data) > 0 {
			if resp, ok := f.opts.SensorResponses[data[0]]; ok {
				return resp, nil
			}
		}
		// CC=0x00 raw_reading=0x60 status=scanning|event-enabled
		return []byte{0x00, 0x60, 0xC0}, nil
	}
	if netfn == 0x30 && cmd == 0x70 {
		return []byte{0x00}, nil
	}
	return []byte{0xC1}, nil
}

// dellResponse returns canned Dell iDRAC BMC responses.
//
// Supported commands:
//   - Get Sensor Reading (netfn=0x04 cmd=0x2D): returns [CC=0x00, raw=0x50, status=0xC0]
//   - Set Fan Control   (netfn=0x30 cmd=0x30): returns [CC=0x00]
//   - All others: returns [CC=0xC1]
func (f *Fake) dellResponse(netfn, cmd byte, data []byte) ([]byte, error) {
	if netfn == 0x04 && cmd == 0x2D {
		if len(data) > 0 {
			if resp, ok := f.opts.SensorResponses[data[0]]; ok {
				return resp, nil
			}
		}
		return []byte{0x00, 0x50, 0xC0}, nil
	}
	if netfn == 0x30 && cmd == 0x30 {
		return []byte{0x00}, nil
	}
	return []byte{0xC1}, nil
}

// hpeResponse returns canned HPE iLO BMC responses.
//
// Reads (netfn != 0x30) succeed; writes (netfn == 0x30) return 0xC1 because
// iLO Advanced licence is required for fan control.
//
// Supported read commands:
//   - Get Sensor Reading (netfn=0x04 cmd=0x2D): returns [CC=0x00, raw=0x40, status=0xC0]
//   - All other reads: returns [CC=0xC1]
func (f *Fake) hpeResponse(netfn, cmd byte, data []byte) ([]byte, error) {
	if netfn == 0x30 {
		return []byte{0xC1}, nil // iLO Advanced required for writes
	}
	if netfn == 0x04 && cmd == 0x2D {
		if len(data) > 0 {
			if resp, ok := f.opts.SensorResponses[data[0]]; ok {
				return resp, nil
			}
		}
		return []byte{0x00, 0x40, 0xC0}, nil
	}
	return []byte{0xC1}, nil
}
