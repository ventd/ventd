package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/monitor"
	"github.com/ventd/ventd/internal/probe"
)

// stubScan returns a fixed device fixture so the handler is
// exercisable without a real /sys mount.
func stubScan(devices []monitor.Device) func() []monitor.Device {
	return func() []monitor.Device { return devices }
}

func resetInventoryFixtures(t *testing.T) {
	t.Helper()
	prev := scanFn
	t.Cleanup(func() { scanFn = prev })
	resetHardwareInventoryHistoryForTest()
	t.Cleanup(resetHardwareInventoryHistoryForTest)
}

// TestHardwareInventory_EmptyConfig_NoChips covers the cold-start
// path: empty config + empty scan returns a well-formed empty
// envelope rather than nil slices the frontend would have to
// branch on.
func TestHardwareInventory_EmptyConfig_NoChips(t *testing.T) {
	resetInventoryFixtures(t)
	scanFn = stubScan(nil)

	srv := newVersionTestServer(t)
	w := httptest.NewRecorder()
	srv.handleHardwareInventory(w, httptest.NewRequest(http.MethodGet, "/api/v1/hardware/inventory", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", w.Code)
	}
	var got InventoryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Chips == nil {
		t.Errorf("chips: nil, want []")
	}
	if got.Curves == nil {
		t.Errorf("curves: nil, want []")
	}
	if len(got.Chips) != 0 {
		t.Errorf("chips: %d, want 0", len(got.Chips))
	}
}

// TestHardwareInventory_AliasMappingFromConfig pins that a config
// Sensor whose Path matches a live Reading.SensorPath surfaces as
// the sensor's alias on the wire — the load-bearing handle the
// Hardware page uses to render "→ cpu_pkg" next to the raw label.
func TestHardwareInventory_AliasMappingFromConfig(t *testing.T) {
	resetInventoryFixtures(t)
	scanFn = stubScan([]monitor.Device{{
		Name: "Intel CPU",
		Path: "hwmon4",
		Readings: []monitor.Reading{{
			Label:      "Package",
			Value:      62.5,
			Unit:       "°C",
			SensorType: "hwmon",
			SensorPath: "/sys/class/hwmon/hwmon4/temp1_input",
		}},
	}})

	srv := newVersionTestServer(t)
	cfg := config.Empty()
	cfg.Sensors = []config.Sensor{{Name: "cpu_pkg", Type: "hwmon", Path: "/sys/class/hwmon/hwmon4/temp1_input"}}
	pinCfg(srv, cfg)

	w := httptest.NewRecorder()
	srv.handleHardwareInventory(w, httptest.NewRequest(http.MethodGet, "/api/v1/hardware/inventory", nil))
	var got InventoryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Chips) != 1 || len(got.Chips[0].Sensors) != 1 {
		t.Fatalf("got %d chips %d sensors, want 1/1", len(got.Chips), len(got.Chips[0].Sensors))
	}
	s := got.Chips[0].Sensors[0]
	if s.Alias != "cpu_pkg" {
		t.Errorf("alias = %q, want cpu_pkg", s.Alias)
	}
	if s.Kind != "temp" {
		t.Errorf("kind = %q, want temp", s.Kind)
	}
}

// TestHardwareInventory_UsedByPopulatedFromCurves pins that a
// curve referencing a sensor by alias surfaces in that sensor's
// used_by[] — the Hardware page's coupling map driver.
func TestHardwareInventory_UsedByPopulatedFromCurves(t *testing.T) {
	resetInventoryFixtures(t)
	scanFn = stubScan([]monitor.Device{{
		Name: "Intel CPU", Path: "hwmon4",
		Readings: []monitor.Reading{{
			Label: "Package", Value: 62.5, Unit: "°C",
			SensorType: "hwmon", SensorPath: "/sys/class/hwmon/hwmon4/temp1_input",
		}},
	}})

	srv := newVersionTestServer(t)
	cfg := config.Empty()
	cfg.Sensors = []config.Sensor{{Name: "cpu_pkg", Type: "hwmon", Path: "/sys/class/hwmon/hwmon4/temp1_input"}}
	cfg.Fans = []config.Fan{{Name: "cpu_fan", Type: "hwmon", PWMPath: "/sys/class/hwmon/hwmon2/pwm1"}}
	cfg.Curves = []config.CurveConfig{{Name: "cpu_curve", Type: "linear", Sensor: "cpu_pkg"}}
	cfg.Controls = []config.Control{{Fan: "cpu_fan", Curve: "cpu_curve"}}
	pinCfg(srv, cfg)

	w := httptest.NewRecorder()
	srv.handleHardwareInventory(w, httptest.NewRequest(http.MethodGet, "/api/v1/hardware/inventory", nil))
	var got InventoryResponse
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if len(got.Chips[0].Sensors) != 1 {
		t.Fatalf("sensors %d, want 1", len(got.Chips[0].Sensors))
	}
	usedBy := got.Chips[0].Sensors[0].UsedBy
	if len(usedBy) != 1 || usedBy[0] != "cpu_curve" {
		t.Errorf("used_by = %v, want [cpu_curve]", usedBy)
	}
	if len(got.Curves) != 1 {
		t.Fatalf("curves %d, want 1", len(got.Curves))
	}
	c := got.Curves[0]
	if c.ID != "cpu_curve" {
		t.Errorf("curve id = %q, want cpu_curve", c.ID)
	}
	// Consumes resolves alias → live sensor ID.
	if len(c.Consumes) != 1 || c.Consumes[0] != "/sys/class/hwmon/hwmon4/temp1_input" {
		t.Errorf("consumes = %v, want [/sys/class/hwmon/hwmon4/temp1_input]", c.Consumes)
	}
	// Drives resolves fan alias → live PWM path.
	if len(c.Drives) != 1 || c.Drives[0] != "/sys/class/hwmon/hwmon2/pwm1" {
		t.Errorf("drives = %v, want [/sys/class/hwmon/hwmon2/pwm1]", c.Drives)
	}
}

// TestHardwareInventory_HistoryRingAccumulates pins that the
// per-sensor ring grows on each successive call — sparklines
// require chronological history, oldest-first.
func TestHardwareInventory_HistoryRingAccumulates(t *testing.T) {
	resetInventoryFixtures(t)
	values := []float64{50, 51, 52, 53}
	idx := 0
	scanFn = func() []monitor.Device {
		v := values[idx%len(values)]
		idx++
		return []monitor.Device{{
			Name: "Intel CPU", Path: "hwmon4",
			Readings: []monitor.Reading{{
				Label: "Package", Value: v, Unit: "°C",
				SensorType: "hwmon", SensorPath: "/sys/class/hwmon/hwmon4/temp1_input",
			}},
		}}
	}

	srv := newVersionTestServer(t)
	for i := 0; i < 4; i++ {
		w := httptest.NewRecorder()
		srv.handleHardwareInventory(w, httptest.NewRequest(http.MethodGet, "/api/v1/hardware/inventory", nil))
	}

	// Final response carries the chronological history.
	w := httptest.NewRecorder()
	scanFn = stubScan([]monitor.Device{{
		Name: "Intel CPU", Path: "hwmon4",
		Readings: []monitor.Reading{{
			Label: "Package", Value: 99, Unit: "°C",
			SensorType: "hwmon", SensorPath: "/sys/class/hwmon/hwmon4/temp1_input",
		}},
	}})
	srv.handleHardwareInventory(w, httptest.NewRequest(http.MethodGet, "/api/v1/hardware/inventory", nil))
	var got InventoryResponse
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	hist := got.Chips[0].Sensors[0].History
	want := []float64{50, 51, 52, 53, 99}
	if len(hist) != len(want) {
		t.Fatalf("history len = %d (%v), want %d (%v)", len(hist), hist, len(want), want)
	}
	for i, v := range want {
		if hist[i] != v {
			t.Errorf("history[%d] = %v, want %v (full=%v)", i, hist[i], v, hist)
		}
	}
}

// TestHardwareInventory_PositionPropagated pins that an
// operator-supplied Position on the config Sensor surfaces on the
// inventory wire — the Heatmap view's load-bearing input.
func TestHardwareInventory_PositionPropagated(t *testing.T) {
	resetInventoryFixtures(t)
	scanFn = stubScan([]monitor.Device{{
		Name: "Intel CPU", Path: "hwmon4",
		Readings: []monitor.Reading{{
			Label: "Package", Value: 60, Unit: "°C",
			SensorType: "hwmon", SensorPath: "/sys/class/hwmon/hwmon4/temp1_input",
		}},
	}})

	srv := newVersionTestServer(t)
	cfg := config.Empty()
	cfg.Sensors = []config.Sensor{{
		Name: "cpu_pkg", Type: "hwmon",
		Path:     "/sys/class/hwmon/hwmon4/temp1_input",
		Position: &config.Position{X: 0.32, Y: 0.30},
	}}
	pinCfg(srv, cfg)

	w := httptest.NewRecorder()
	srv.handleHardwareInventory(w, httptest.NewRequest(http.MethodGet, "/api/v1/hardware/inventory", nil))
	var got InventoryResponse
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	pos := got.Chips[0].Sensors[0].Position
	if pos == nil {
		t.Fatalf("position: nil, want {0.32,0.30}")
	}
	if pos.X != 0.32 || pos.Y != 0.30 {
		t.Errorf("position = %+v, want {0.32,0.30}", *pos)
	}
}

// pinCfg swaps the live config in a test Server. Mirrors
// liveCfg.Store(...) used in the test-server constructor.
func pinCfg(srv *Server, cfg *config.Config) {
	(*atomic.Pointer[config.Config])(srv.cfg).Store(cfg)
}

// pinMonitorChannels swaps the orchestrator-state loader for the
// duration of the test so the inventory handler sees a deterministic
// classification set without touching /var/lib/ventd. Reverts on
// t.Cleanup.
func pinMonitorChannels(t *testing.T, chs []probe.MonitorChannel) {
	t.Helper()
	prev := loadMonitorChannelsFn
	t.Cleanup(func() { loadMonitorChannelsFn = prev })
	loadMonitorChannelsFn = func() ([]probe.MonitorChannel, error) { return chs, nil }
}

// TestHardwareInventory_HidesPhantomTachsByDefault covers the
// minipc HIL case from #796: a chip exposes 4 fan*_input zones
// (1 real + 2 mirrors + 1 phantom) plus a temp sensor. By default
// the inventory returns the real fan + the temp sensor (mirror /
// phantom rows dropped). With `?include_phantoms=1` the full set
// is returned — the operator opted in via Settings.
func TestHardwareInventory_HidesPhantomTachsByDefault(t *testing.T) {
	resetInventoryFixtures(t)

	fan1 := "/sys/class/hwmon/hwmon3/fan1_input" // real
	fan2 := "/sys/class/hwmon/hwmon3/fan2_input" // mirror of fan1
	fan3 := "/sys/class/hwmon/hwmon3/fan3_input" // mirror of fan1
	fan4 := "/sys/class/hwmon/hwmon3/fan4_input" // phantom (all-zero, no PWM)
	temp1 := "/sys/class/hwmon/hwmon3/temp1_input"

	scanFn = stubScan([]monitor.Device{{
		Name: "Intel EC", Path: "hwmon3",
		Readings: []monitor.Reading{
			{Label: "Package", Value: 55.0, Unit: "°C", SensorType: "hwmon", SensorPath: temp1},
			{Label: "fan1", Value: 1500, Unit: "RPM", SensorType: "hwmon", SensorPath: fan1},
			{Label: "fan2", Value: 1500, Unit: "RPM", SensorType: "hwmon", SensorPath: fan2},
			{Label: "fan3", Value: 1500, Unit: "RPM", SensorType: "hwmon", SensorPath: fan3},
			{Label: "fan4", Value: 0, Unit: "RPM", SensorType: "hwmon", SensorPath: fan4},
		},
	}})

	pinMonitorChannels(t, []probe.MonitorChannel{
		{TachPath: fan1, Visibility: probe.VisibilityReal},
		{TachPath: fan2, Visibility: probe.VisibilityMirror, MirrorOf: fan1},
		{TachPath: fan3, Visibility: probe.VisibilityMirror, MirrorOf: fan1},
		{TachPath: fan4, Visibility: probe.VisibilityPhantom},
	})

	srv := newVersionTestServer(t)
	pinCfg(srv, config.Empty())

	// Default request — phantoms hidden.
	w := httptest.NewRecorder()
	srv.handleHardwareInventory(w, httptest.NewRequest(http.MethodGet, "/api/v1/hardware/inventory", nil))
	var got InventoryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Chips) != 1 {
		t.Fatalf("got %d chips, want 1", len(got.Chips))
	}
	defaultIDs := map[string]bool{}
	for _, s := range got.Chips[0].Sensors {
		defaultIDs[s.ID] = true
	}
	if !defaultIDs[temp1] {
		t.Errorf("default view should keep temp1 (non-fan readings unaffected by the filter)")
	}
	if !defaultIDs[fan1] {
		t.Errorf("default view should keep fan1 (real channel)")
	}
	for _, p := range []string{fan2, fan3, fan4} {
		if defaultIDs[p] {
			t.Errorf("default view leaked phantom/mirror %q — should be hidden until include_phantoms=1", p)
		}
	}

	// Opt-in request — every channel surfaces.
	w = httptest.NewRecorder()
	srv.handleHardwareInventory(w, httptest.NewRequest(http.MethodGet, "/api/v1/hardware/inventory?include_phantoms=1", nil))
	var got2 InventoryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got2.Chips) != 1 {
		t.Fatalf("got %d chips on include_phantoms=1, want 1", len(got2.Chips))
	}
	if len(got2.Chips[0].Sensors) != 5 {
		t.Errorf("include_phantoms=1 should surface all 5 readings (1 temp + 4 fans); got %d",
			len(got2.Chips[0].Sensors))
	}
}

// TestHardwareInventory_PhantomFilterNoOpWithoutClassifications
// pins the pre-wizard fallback: when LoadMonitorChannels returns
// an empty slice (state.json missing or no Probe / Apply outcome
// yet), the filter degrades to a no-op. Without this, a fresh
// install would hide every fan because the visByTach map is empty
// and the strict "vis == real" check would drop everything.
func TestHardwareInventory_PhantomFilterNoOpWithoutClassifications(t *testing.T) {
	resetInventoryFixtures(t)

	fan1 := "/sys/class/hwmon/hwmon0/fan1_input"
	fan2 := "/sys/class/hwmon/hwmon0/fan2_input"
	scanFn = stubScan([]monitor.Device{{
		Name: "Super-I/O", Path: "hwmon0",
		Readings: []monitor.Reading{
			{Label: "fan1", Value: 1100, Unit: "RPM", SensorType: "hwmon", SensorPath: fan1},
			{Label: "fan2", Value: 1200, Unit: "RPM", SensorType: "hwmon", SensorPath: fan2},
		},
	}})

	pinMonitorChannels(t, nil) // no classifications yet — first boot.

	srv := newVersionTestServer(t)
	pinCfg(srv, config.Empty())

	w := httptest.NewRecorder()
	srv.handleHardwareInventory(w, httptest.NewRequest(http.MethodGet, "/api/v1/hardware/inventory", nil))
	var got InventoryResponse
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if len(got.Chips) != 1 || len(got.Chips[0].Sensors) != 2 {
		t.Fatalf("pre-wizard inventory should pass every reading through; got %d chips / %d sensors",
			len(got.Chips), len(got.Chips[0].Sensors))
	}
}

// TestHardwareInventory_NVMLAliasKeyedByMetric pins the regression on
// Phoenix's RTX 4090: when the only configured GPU sensor is
// {name: gpu_temp, type: nvidia, path: "0", metric: temp}, ONLY the
// temp reading should resolve to alias "gpu_temp"; fan_pct, power, util
// and clock readings on the same gpu0 must NOT inherit it. Pre-fix,
// every metric on a GPU shared the gpuIdx path and hit the same
// sensorAliasByPath["0"] entry, producing a wall of cells all labelled
// "gpu_temp" on the Hardware page (B8 from the v0.5.26 bug-floor probe).
func TestHardwareInventory_NVMLAliasKeyedByMetric(t *testing.T) {
	resetInventoryFixtures(t)
	scanFn = stubScan([]monitor.Device{{
		Name: "NVIDIA GeForce RTX 4090", Path: "gpu0",
		Readings: []monitor.Reading{
			{Label: "Temperature", Value: 29, Unit: "°C", SensorType: "nvidia", SensorPath: "0", Metric: "temp"},
			{Label: "Fan Speed", Value: 30, Unit: "%", SensorType: "nvidia", SensorPath: "0", Metric: "fan_pct"},
			{Label: "Power", Value: 26.5, Unit: "W", SensorType: "nvidia", SensorPath: "0", Metric: "power"},
			{Label: "GPU Clock", Value: 210, Unit: "MHz", SensorType: "nvidia", SensorPath: "0", Metric: "clock_gpu"},
		},
	}})

	srv := newVersionTestServer(t)
	cfg := config.Empty()
	cfg.Sensors = []config.Sensor{{
		Name: "gpu_temp", Type: "nvidia",
		Path: "0", Metric: "temp",
	}}
	pinCfg(srv, cfg)

	w := httptest.NewRecorder()
	srv.handleHardwareInventory(w, httptest.NewRequest(http.MethodGet, "/api/v1/hardware/inventory", nil))
	var got InventoryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Chips) != 1 || len(got.Chips[0].Sensors) != 4 {
		t.Fatalf("got %d chips %d sensors, want 1/4", len(got.Chips), len(got.Chips[0].Sensors))
	}
	want := map[string]string{
		"gpu0:temp":      "gpu_temp", // the configured one — alias matches
		"gpu0:fan_pct":   "",         // NOT inherited
		"gpu0:power":     "",
		"gpu0:clock_gpu": "",
	}
	for _, s := range got.Chips[0].Sensors {
		w, ok := want[s.ID]
		if !ok {
			t.Errorf("unexpected sensor id %q", s.ID)
			continue
		}
		if s.Alias != w {
			t.Errorf("sensor %s: alias = %q, want %q", s.ID, s.Alias, w)
		}
	}
}

// TestHardwareInventory_NVMLAliasDefaultMetricIsTemp pins the legacy
// behaviour: an NVML config without an explicit metric defaults to
// "temp" (per the Sensor struct doc). A v1 config like
// {name: gpu_temp, type: nvidia, path: "0"} (no metric field) must still
// resolve the gpu0:temp reading. Fan/power/etc. must still NOT inherit.
func TestHardwareInventory_NVMLAliasDefaultMetricIsTemp(t *testing.T) {
	resetInventoryFixtures(t)
	scanFn = stubScan([]monitor.Device{{
		Name: "NVIDIA GeForce RTX 4090", Path: "gpu0",
		Readings: []monitor.Reading{
			{Label: "Temperature", Value: 29, Unit: "°C", SensorType: "nvidia", SensorPath: "0", Metric: "temp"},
			{Label: "Fan Speed", Value: 30, Unit: "%", SensorType: "nvidia", SensorPath: "0", Metric: "fan_pct"},
		},
	}})

	srv := newVersionTestServer(t)
	cfg := config.Empty()
	cfg.Sensors = []config.Sensor{{
		Name: "gpu_temp", Type: "nvidia", Path: "0", // Metric omitted
	}}
	pinCfg(srv, cfg)

	w := httptest.NewRecorder()
	srv.handleHardwareInventory(w, httptest.NewRequest(http.MethodGet, "/api/v1/hardware/inventory", nil))
	var got InventoryResponse
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if len(got.Chips) != 1 || len(got.Chips[0].Sensors) != 2 {
		t.Fatalf("got %d chips %d sensors, want 1/2", len(got.Chips), len(got.Chips[0].Sensors))
	}
	for _, s := range got.Chips[0].Sensors {
		switch s.ID {
		case "gpu0:temp":
			if s.Alias != "gpu_temp" {
				t.Errorf("gpu0:temp alias = %q, want gpu_temp (default-metric resolves)", s.Alias)
			}
		case "gpu0:fan_pct":
			if s.Alias != "" {
				t.Errorf("gpu0:fan_pct alias = %q, want empty (different metric, must not inherit)", s.Alias)
			}
		}
	}
}
