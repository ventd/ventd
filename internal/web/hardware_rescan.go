package web

import (
	"net/http"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/ventd/ventd/internal/hwmon"
)

// Rescan feature — a UI-triggered "refresh the view" button paired with
// a debug endpoint that exposes the before/after snapshot of the most
// recent rescan.
//
// The Watcher in internal/hwmon runs a periodic rescan + uevent loop of
// its own that drives rebinds when the topology actually changes. This
// handler doesn't interrupt that loop: it re-enumerates via the same
// EnumerateDevices path and diffs against the most recently observed
// snapshot, so the UI can give the operator a push-button answer to
// "did my fan hub just plug in?" without waiting for the background
// tick. The diff is fan-level because that's what the operator sees in
// the sidebar — a new hwmonN chip with no pwm channels is interesting
// for diagnostics but not for "a fan appeared", so we key on
// "<hwmonN>/pwmM" identities.

type rescanState struct {
	mu      sync.Mutex
	lastAt  time.Time
	trigger string
	before  []hwmonDebugDevice
	after   []hwmonDebugDevice
	current []hwmonDebugDevice
}

// hwmonDebugDevice is the public shape for /api/hardware/rescan's
// "before"/"after" snapshots and /api/debug/hwmon's debug payload. The
// field set is a lossy projection of hwmon.HwmonDevice — just what the
// UI needs to render the sidebar and to recognise "a new fan appeared"
// at a glance. Raw pwm paths are kept so the toast copy can name
// "hwmonN/pwmM" without the UI having to assemble them.
type hwmonDebugDevice struct {
	Chip         string   `json:"chip"`
	Dir          string   `json:"dir"`
	StableDevice string   `json:"stable_device,omitempty"`
	Class        string   `json:"class"`
	Fans         []string `json:"fans"`
	Sensors      []string `json:"sensors"`
}

// hwmonEnumerator is the test seam for rescan. Production leaves it
// nil, which falls back to hwmon.EnumerateDevices(""). Tests inject a
// deterministic slice so the diff can be asserted without touching
// sysfs.
var hwmonEnumerator func() []hwmon.HwmonDevice

func enumerateForRescan() []hwmon.HwmonDevice {
	if hwmonEnumerator != nil {
		return hwmonEnumerator()
	}
	return hwmon.EnumerateDevices("")
}

// toDebugDevices projects a slice of hwmon.HwmonDevice into the public
// shape. Field order is deterministic so diff output stays stable
// across re-runs — the enumerator already sorts by hwmonN index, and
// we sort pwm/temp basenames lexicographically so pwm1 precedes pwm10.
func toDebugDevices(devs []hwmon.HwmonDevice) []hwmonDebugDevice {
	out := make([]hwmonDebugDevice, 0, len(devs))
	for _, d := range devs {
		fans := make([]string, 0, len(d.PWM))
		for _, p := range d.PWM {
			fans = append(fans, filepath.Base(p.Path))
		}
		sort.Strings(fans)
		sensors := make([]string, 0, len(d.TempInputs))
		for _, p := range d.TempInputs {
			sensors = append(sensors, filepath.Base(p))
		}
		sort.Strings(sensors)
		out = append(out, hwmonDebugDevice{
			Chip:         d.ChipName,
			Dir:          filepath.Base(d.Dir),
			StableDevice: d.StableDevice,
			Class:        string(d.Class),
			Fans:         fans,
			Sensors:      sensors,
		})
	}
	return out
}

// fanIdentities returns "<hwmonDir>/<pwmN>" strings for every pwm
// channel across every device. Used as the identity set for the
// rescan diff.
func fanIdentities(devs []hwmonDebugDevice) []string {
	var ids []string
	for _, d := range devs {
		for _, f := range d.Fans {
			ids = append(ids, d.Dir+"/"+f)
		}
	}
	sort.Strings(ids)
	return ids
}

// diffFanIdentities computes new and removed fan identities going from prev to
// cur. Both inputs are assumed to come from fanIdentities (sorted,
// deduplicated). Results are also sorted.
func diffFanIdentities(prev, cur []string) (newFans, removedFans []string) {
	prevSet := make(map[string]struct{}, len(prev))
	for _, p := range prev {
		prevSet[p] = struct{}{}
	}
	curSet := make(map[string]struct{}, len(cur))
	for _, c := range cur {
		curSet[c] = struct{}{}
	}
	for _, c := range cur {
		if _, ok := prevSet[c]; !ok {
			newFans = append(newFans, c)
		}
	}
	for _, p := range prev {
		if _, ok := curSet[p]; !ok {
			removedFans = append(removedFans, p)
		}
	}
	sort.Strings(newFans)
	sort.Strings(removedFans)
	return newFans, removedFans
}

// handleHardwareRescan POST /api/hardware/rescan
//
// Re-enumerates the hwmon tree, computes the fan-level diff against
// the snapshot stored from the previous rescan (or empty on first
// call), and stores before/after/current in the rescan state for
// /api/debug/hwmon.
func (s *Server) handleHardwareRescan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	start := time.Now()
	cur := toDebugDevices(enumerateForRescan())
	elapsed := time.Since(start)

	s.rescan.mu.Lock()
	prev := s.rescan.current
	s.rescan.before = prev
	s.rescan.after = cur
	s.rescan.current = cur
	s.rescan.lastAt = time.Now()
	s.rescan.trigger = "api"
	s.rescan.mu.Unlock()

	newFans, removedFans := diffFanIdentities(fanIdentities(prev), fanIdentities(cur))
	if newFans == nil {
		newFans = []string{}
	}
	if removedFans == nil {
		removedFans = []string{}
	}
	resp := map[string]interface{}{
		"new_devices":     newFans,
		"removed_devices": removedFans,
		"elapsed_ms":      elapsed.Milliseconds(),
	}
	s.writeJSON(r, w, resp)
}
