package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ventd/ventd/internal/hwmon"
)

func TestRescan_DiffFanIdentities(t *testing.T) {
	cases := []struct {
		name        string
		prev        []string
		cur         []string
		wantNew     []string
		wantRemoved []string
	}{
		{
			name:        "no_change",
			prev:        []string{"hwmon0/pwm1", "hwmon1/pwm1"},
			cur:         []string{"hwmon0/pwm1", "hwmon1/pwm1"},
			wantNew:     nil,
			wantRemoved: nil,
		},
		{
			name:        "new_device_appears",
			prev:        []string{"hwmon0/pwm1"},
			cur:         []string{"hwmon0/pwm1", "hwmon3/pwm1", "hwmon3/pwm2"},
			wantNew:     []string{"hwmon3/pwm1", "hwmon3/pwm2"},
			wantRemoved: nil,
		},
		{
			name:        "device_removed",
			prev:        []string{"hwmon0/pwm1", "hwmon1/pwm1"},
			cur:         []string{"hwmon0/pwm1"},
			wantNew:     nil,
			wantRemoved: []string{"hwmon1/pwm1"},
		},
		{
			name:        "swap",
			prev:        []string{"hwmon0/pwm1"},
			cur:         []string{"hwmon4/pwm1"},
			wantNew:     []string{"hwmon4/pwm1"},
			wantRemoved: []string{"hwmon0/pwm1"},
		},
		{
			name:        "empty_both",
			prev:        nil,
			cur:         nil,
			wantNew:     nil,
			wantRemoved: nil,
		},
		{
			name:        "first_rescan_from_empty",
			prev:        nil,
			cur:         []string{"hwmon2/pwm1"},
			wantNew:     []string{"hwmon2/pwm1"},
			wantRemoved: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotNew, gotRemoved := diffFanIdentities(tc.prev, tc.cur)
			if !equalStrings(gotNew, tc.wantNew) {
				t.Errorf("new = %v, want %v", gotNew, tc.wantNew)
			}
			if !equalStrings(gotRemoved, tc.wantRemoved) {
				t.Errorf("removed = %v, want %v", gotRemoved, tc.wantRemoved)
			}
		})
	}
}

func TestRescan_FanIdentities_SortAcrossDevices(t *testing.T) {
	devs := []hwmonDebugDevice{
		{Dir: "hwmon10", Fans: []string{"pwm2", "pwm1"}},
		{Dir: "hwmon2", Fans: []string{"pwm1"}},
	}
	ids := fanIdentities(devs)
	want := []string{"hwmon10/pwm1", "hwmon10/pwm2", "hwmon2/pwm1"}
	if !equalStrings(ids, want) {
		t.Errorf("fanIdentities = %v, want %v (lexicographic so hwmon10 < hwmon2)", ids, want)
	}
}

func TestRescan_ToDebugDevices_BasenameOnly(t *testing.T) {
	devs := []hwmon.HwmonDevice{
		{
			Dir:          "/sys/class/hwmon/hwmon3",
			ChipName:     "nct6687",
			StableDevice: "/sys/devices/platform/nct6687.2592",
			Class:        hwmon.ClassPrimary,
			PWM: []hwmon.PWMChannel{
				{Path: "/sys/class/hwmon/hwmon3/pwm2"},
				{Path: "/sys/class/hwmon/hwmon3/pwm1"},
			},
			TempInputs: []string{
				"/sys/class/hwmon/hwmon3/temp2_input",
				"/sys/class/hwmon/hwmon3/temp1_input",
			},
		},
	}
	got := toDebugDevices(devs)
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1", len(got))
	}
	d := got[0]
	if d.Dir != "hwmon3" {
		t.Errorf("Dir = %q, want hwmon3 (basename only)", d.Dir)
	}
	if d.Chip != "nct6687" {
		t.Errorf("Chip = %q, want nct6687", d.Chip)
	}
	if d.Class != "primary" {
		t.Errorf("Class = %q, want primary", d.Class)
	}
	wantFans := []string{"pwm1", "pwm2"}
	if !equalStrings(d.Fans, wantFans) {
		t.Errorf("Fans = %v, want %v (sorted, basename only)", d.Fans, wantFans)
	}
	wantSensors := []string{"temp1_input", "temp2_input"}
	if !equalStrings(d.Sensors, wantSensors) {
		t.Errorf("Sensors = %v, want %v (sorted)", d.Sensors, wantSensors)
	}
}

func TestRescan_HandleHardwareRescan_MethodNotAllowed(t *testing.T) {
	h := newTestHarness(t)
	req := httptest.NewRequest(http.MethodGet, "/api/hardware/rescan", nil)
	w := httptest.NewRecorder()
	h.srv.handleHardwareRescan(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET status = %d, want 405", w.Code)
	}
}

func TestRescan_HandleHardwareRescan_NewDeviceDetected(t *testing.T) {
	h := newTestHarness(t)

	// First call: no prior snapshot, enumerator returns one chip with pwm1.
	prev := hwmonEnumerator
	hwmonEnumerator = func() []hwmon.HwmonDevice {
		return []hwmon.HwmonDevice{
			{Dir: "/sys/class/hwmon/hwmon0", ChipName: "nct6687", Class: hwmon.ClassPrimary,
				PWM: []hwmon.PWMChannel{{Path: "/sys/class/hwmon/hwmon0/pwm1"}}},
		}
	}
	t.Cleanup(func() { hwmonEnumerator = prev })

	resp := postRescan(t, h)
	// First-ever rescan: prev was empty, so every current fan shows up
	// under new_devices. That matches the intent — the operator is
	// explicitly asking "what's there?".
	if got := resp["new_devices"].([]interface{}); len(got) != 1 || got[0] != "hwmon0/pwm1" {
		t.Errorf("first rescan new_devices = %v, want [hwmon0/pwm1]", got)
	}

	// Second call with a new pwm: only the new one shows up.
	hwmonEnumerator = func() []hwmon.HwmonDevice {
		return []hwmon.HwmonDevice{
			{Dir: "/sys/class/hwmon/hwmon0", ChipName: "nct6687", Class: hwmon.ClassPrimary,
				PWM: []hwmon.PWMChannel{{Path: "/sys/class/hwmon/hwmon0/pwm1"}}},
			{Dir: "/sys/class/hwmon/hwmon3", ChipName: "it87", Class: hwmon.ClassPrimary,
				PWM: []hwmon.PWMChannel{{Path: "/sys/class/hwmon/hwmon3/pwm2"}}},
		}
	}
	resp = postRescan(t, h)
	newDevs := resp["new_devices"].([]interface{})
	if len(newDevs) != 1 || newDevs[0] != "hwmon3/pwm2" {
		t.Errorf("second rescan new_devices = %v, want [hwmon3/pwm2]", newDevs)
	}
	if removed := resp["removed_devices"].([]interface{}); len(removed) != 0 {
		t.Errorf("second rescan removed_devices = %v, want []", removed)
	}
}

func TestRescan_HandleHardwareRescan_DeviceRemoved(t *testing.T) {
	h := newTestHarness(t)
	prev := hwmonEnumerator
	// Seed current so the first POST sees a "previous" state.
	h.srv.rescan.current = []hwmonDebugDevice{
		{Dir: "hwmon0", Chip: "nct6687", Class: "primary", Fans: []string{"pwm1", "pwm2"}},
	}
	hwmonEnumerator = func() []hwmon.HwmonDevice {
		return []hwmon.HwmonDevice{
			{Dir: "/sys/class/hwmon/hwmon0", ChipName: "nct6687", Class: hwmon.ClassPrimary,
				PWM: []hwmon.PWMChannel{{Path: "/sys/class/hwmon/hwmon0/pwm1"}}},
		}
	}
	t.Cleanup(func() { hwmonEnumerator = prev })

	resp := postRescan(t, h)
	removed := resp["removed_devices"].([]interface{})
	if len(removed) != 1 || removed[0] != "hwmon0/pwm2" {
		t.Errorf("removed_devices = %v, want [hwmon0/pwm2]", removed)
	}
}

func TestHwmonDebug_MethodNotAllowed(t *testing.T) {
	h := newTestHarness(t)
	req := httptest.NewRequest(http.MethodPost, "/api/debug/hwmon", nil)
	w := httptest.NewRecorder()
	h.srv.handleHwmonDebug(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST status = %d, want 405", w.Code)
	}
}

func TestHwmonDebug_BeforeAndAfterAreNullUntilRescan(t *testing.T) {
	h := newTestHarness(t)
	prev := hwmonEnumerator
	hwmonEnumerator = func() []hwmon.HwmonDevice {
		return []hwmon.HwmonDevice{
			{Dir: "/sys/class/hwmon/hwmon0", ChipName: "nct6687", Class: hwmon.ClassPrimary,
				PWM: []hwmon.PWMChannel{{Path: "/sys/class/hwmon/hwmon0/pwm1"}}},
		}
	}
	t.Cleanup(func() { hwmonEnumerator = prev })

	req := httptest.NewRequest(http.MethodGet, "/api/debug/hwmon", nil)
	w := httptest.NewRecorder()
	h.srv.handleHwmonDebug(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["before"] != nil {
		t.Errorf("before = %v, want nil before any rescan", body["before"])
	}
	if body["after"] != nil {
		t.Errorf("after = %v, want nil before any rescan", body["after"])
	}
	if body["last_rescan_at"] != nil {
		t.Errorf("last_rescan_at = %v, want nil before any rescan", body["last_rescan_at"])
	}
	cur, ok := body["current"].([]interface{})
	if !ok || len(cur) != 1 {
		t.Errorf("current = %v, want one-element slice populated lazily", body["current"])
	}
}

func TestHwmonDebug_PopulatedAfterRescan(t *testing.T) {
	h := newTestHarness(t)
	prev := hwmonEnumerator
	hwmonEnumerator = func() []hwmon.HwmonDevice {
		return []hwmon.HwmonDevice{
			{Dir: "/sys/class/hwmon/hwmon0", ChipName: "nct6687", Class: hwmon.ClassPrimary,
				PWM: []hwmon.PWMChannel{{Path: "/sys/class/hwmon/hwmon0/pwm1"}}},
		}
	}
	t.Cleanup(func() { hwmonEnumerator = prev })

	_ = postRescan(t, h)

	req := httptest.NewRequest(http.MethodGet, "/api/debug/hwmon", nil)
	w := httptest.NewRecorder()
	h.srv.handleHwmonDebug(w, req)
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["last_rescan_at"] == nil {
		t.Errorf("last_rescan_at is nil after rescan")
	}
	if body["rescan_trigger"] != "api" {
		t.Errorf("rescan_trigger = %v, want api", body["rescan_trigger"])
	}
	if _, ok := body["after"].([]interface{}); !ok {
		t.Errorf("after missing after rescan: %v", body["after"])
	}
}

// --- plumbing --------------------------------------------------------------

func postRescan(t *testing.T, h *rescanHarness) map[string]interface{} {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/hardware/rescan", nil)
	w := httptest.NewRecorder()
	h.srv.handleHardwareRescan(w, req)
	if w.Code != http.StatusOK {
		b, _ := io.ReadAll(w.Body)
		t.Fatalf("POST status = %d, body = %s", w.Code, b)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return body
}

type rescanHarness struct {
	srv *Server
}

func newTestHarness(t *testing.T) *rescanHarness {
	t.Helper()
	return &rescanHarness{srv: newVersionTestServer(t)}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
