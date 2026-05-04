// Package web — hardware inventory endpoint composes monitor.Scan
// output with config aliases + curve coupling and a per-channel
// rolling history ring into the shape the redesigned Hardware page
// (web/hardware.{html,css,js}) consumes.
//
// The endpoint is GET /api/v1/hardware/inventory. Polled by the
// page at ~1.5 s; each call appends the current value to a per-
// sensor ring (cap 60) so the page can draw sparklines without
// keeping client-side state across reloads. The ring is
// in-process; daemon restart resets to empty.
package web

import (
	"net/http"
	"strings"
	"sync"

	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/monitor"
)

// historyCap is the per-sensor sample ring size. ~90 s at 1.5 s
// poll cadence; small enough that 200 sensors × 60 floats stays
// under 100 KiB in steady state.
const historyCap = 60

type sensorRing struct {
	values []float64
	head   int // next write index; wraps at historyCap
	full   bool
}

func (r *sensorRing) append(v float64) {
	if r.values == nil {
		r.values = make([]float64, historyCap)
	}
	r.values[r.head] = v
	r.head = (r.head + 1) % historyCap
	if r.head == 0 {
		r.full = true
	}
}

// snapshot returns the ring's contents in chronological order
// (oldest first). The pre-fill case (ring not yet full) returns
// only the populated prefix — no zero-padding so sparklines don't
// trace a phantom zero-line at first render.
func (r *sensorRing) snapshot() []float64 {
	if r.values == nil {
		return nil
	}
	if !r.full {
		out := make([]float64, r.head)
		copy(out, r.values[:r.head])
		return out
	}
	out := make([]float64, historyCap)
	copy(out, r.values[r.head:])
	copy(out[historyCap-r.head:], r.values[:r.head])
	return out
}

var (
	historyMu    sync.Mutex
	historyStore = map[string]*sensorRing{}
)

// scanFn is the monitor-scan injection point. nil-default falls
// through to monitor.Scan(); tests swap to a fixed []monitor.Device
// fixture so the handler is exercisable without a real /sys.
var scanFn = monitor.Scan

func recordHistory(key string, v float64) []float64 {
	historyMu.Lock()
	defer historyMu.Unlock()
	r, ok := historyStore[key]
	if !ok {
		r = &sensorRing{}
		historyStore[key] = r
	}
	r.append(v)
	return r.snapshot()
}

// resetHardwareInventoryHistoryForTest clears the per-process ring
// store between tests. Production code never calls this.
func resetHardwareInventoryHistoryForTest() {
	historyMu.Lock()
	defer historyMu.Unlock()
	historyStore = map[string]*sensorRing{}
}

// InventoryResponse is the wire shape returned by
// GET /api/v1/hardware/inventory. Fields match the contract
// agreed with the frontend port at PR-A time.
type InventoryResponse struct {
	Chips  []InventoryChip  `json:"chips"`
	Curves []InventoryCurve `json:"curves"`
}

type InventoryChip struct {
	ID      string            `json:"id"`
	Bus     string            `json:"bus"`    // "hwmon" | "nvml" | "acpi"
	Name    string            `json:"name"`   // friendly chip-family name
	Path    string            `json:"path"`   // sysfs / device path
	Model   string            `json:"model"`  // friendly + chip code line
	Status  string            `json:"status"` // "ok" | "offline"
	Sensors []InventorySensor `json:"sensors"`
}

type InventorySensor struct {
	ID       string           `json:"id"`              // stable unique key (sensor_path)
	Label    string           `json:"label"`           // raw driver label (e.g. "temp1", "fan1")
	Alias    string           `json:"alias,omitempty"` // config-supplied friendly name (e.g. "cpu_pkg"); empty if unconfigured
	Kind     string           `json:"kind"`            // "temp" | "fan" | "volt" | "power"
	Value    float64          `json:"value"`
	Unit     string           `json:"unit"`
	History  []float64        `json:"history"`            // chronological, ≤ historyCap entries, oldest first
	Position *config.Position `json:"position,omitempty"` // operator-supplied (x,y) for heatmap; nil → not on heatmap
	UsedBy   []string         `json:"used_by"`            // curve IDs that consume this sensor
}

type InventoryCurve struct {
	ID       string   `json:"id"`       // curve name (key)
	Name     string   `json:"name"`     // human label (same as ID today; reserved for future skin)
	Consumes []string `json:"consumes"` // sensor IDs (paths) — only those with a live matching alias
	Drives   []string `json:"drives"`   // fan IDs (PWM paths) — fans bound to this curve via Control
}

// handleHardwareInventory composes monitor.Scan() with the live
// config to produce the redesigned Hardware page's inventory feed.
// The handler is read-only and side-effects only the per-sensor
// history ring (an in-process append). Errors from config load
// are tolerated — an inventory with no aliases / no curves is
// still useful to the page.
func (s *Server) handleHardwareInventory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")

	devices := scanFn()
	cfg := s.cfg.Load()

	// Build alias-by-path indices for fast lookup. Sensors and fans
	// are keyed by their sysfs path, which is also the
	// monitor.Reading.SensorPath / config.Fan.PWMPath.
	sensorAliasByPath := make(map[string]string, len(cfg.Sensors))
	sensorPosByAlias := make(map[string]*config.Position, len(cfg.Sensors))
	for i := range cfg.Sensors {
		sensorAliasByPath[cfg.Sensors[i].Path] = cfg.Sensors[i].Name
		if cfg.Sensors[i].Position != nil {
			sensorPosByAlias[cfg.Sensors[i].Name] = cfg.Sensors[i].Position
		}
	}
	fanAliasByPath := make(map[string]string, len(cfg.Fans))
	fanPosByAlias := make(map[string]*config.Position, len(cfg.Fans))
	for i := range cfg.Fans {
		fanAliasByPath[cfg.Fans[i].PWMPath] = cfg.Fans[i].Name
		if cfg.Fans[i].Position != nil {
			fanPosByAlias[cfg.Fans[i].Name] = cfg.Fans[i].Position
		}
	}

	// Walk curves to build:
	//  • sensor-alias → curve IDs that consume it ("used_by")
	//  • curve ID → drives list (fan names) via config.Controls
	usedByAlias := make(map[string][]string)
	drivesByCurve := make(map[string][]string)
	for _, c := range cfg.Controls {
		drivesByCurve[c.Curve] = append(drivesByCurve[c.Curve], c.Fan)
	}
	curvesOut := make([]InventoryCurve, 0, len(cfg.Curves))
	for _, c := range cfg.Curves {
		var consumeAliases []string
		if c.Sensor != "" {
			consumeAliases = append(consumeAliases, c.Sensor)
		}
		consumeAliases = append(consumeAliases, c.Sources...)
		for _, alias := range consumeAliases {
			usedByAlias[alias] = append(usedByAlias[alias], c.Name)
		}
		curvesOut = append(curvesOut, InventoryCurve{
			ID:   c.Name,
			Name: c.Name,
			// Consumes / Drives are populated below once the live
			// alias→ID map is known. Placeholder slices for now.
			Consumes: consumeAliases,
			Drives:   drivesByCurve[c.Name],
		})
	}

	// Build chips from monitor scan.
	chipsOut := make([]InventoryChip, 0, len(devices))
	for _, d := range devices {
		bus := chipBus(d)
		sensorsOut := make([]InventorySensor, 0, len(d.Readings))
		for _, rd := range d.Readings {
			alias := sensorAliasByPath[rd.SensorPath]
			// Fans use PWMPath as alias key, which differs from the
			// fan's monitor SensorPath (which is fan*_input). Try
			// the fan-alias map by walking config.Fans for any whose
			// RPMPath or implicit fan*_input matches; cheaper to
			// just reuse the sensorAliasByPath when it hits, fall
			// back to fanAliasByPath comparing against the chip
			// directory + leaf — not worth the extra walk for v1.
			if alias == "" {
				alias = fanAliasByPath[rd.SensorPath]
			}
			pos := sensorPosByAlias[alias]
			if pos == nil {
				pos = fanPosByAlias[alias]
			}
			kind := readingKind(rd.Unit)
			usedBy := append([]string(nil), usedByAlias[alias]...)
			if usedBy == nil {
				usedBy = []string{}
			}
			history := recordHistory(rd.SensorPath, rd.Value)
			sensorsOut = append(sensorsOut, InventorySensor{
				ID:       rd.SensorPath,
				Label:    rd.Label,
				Alias:    alias,
				Kind:     kind,
				Value:    rd.Value,
				Unit:     rd.Unit,
				History:  history,
				Position: pos,
				UsedBy:   usedBy,
			})
		}
		chipsOut = append(chipsOut, InventoryChip{
			ID:      d.Path,
			Bus:     bus,
			Name:    d.Name,
			Path:    d.Path,
			Model:   d.Name + " · " + bus,
			Status:  "ok",
			Sensors: sensorsOut,
		})
	}

	// Resolve curve consumes/drives from alias names to live sensor /
	// fan IDs. A curve referencing a sensor that no live chip
	// produces (config drift across reboots) leaves the slot empty —
	// the frontend renders zero-edges rather than a stale phantom.
	aliasToSensorID := make(map[string]string, len(sensorAliasByPath))
	for _, ch := range chipsOut {
		for _, sn := range ch.Sensors {
			if sn.Alias != "" {
				aliasToSensorID[sn.Alias] = sn.ID
			}
		}
	}
	for i := range curvesOut {
		live := make([]string, 0, len(curvesOut[i].Consumes))
		for _, alias := range curvesOut[i].Consumes {
			if id, ok := aliasToSensorID[alias]; ok {
				live = append(live, id)
			}
		}
		curvesOut[i].Consumes = live
		// Drives reference fan PWMPath via fanAliasByPath inversion.
		fanIDs := make([]string, 0, len(curvesOut[i].Drives))
		for _, fanName := range curvesOut[i].Drives {
			for fanPath, alias := range fanAliasByPath {
				if alias == fanName {
					fanIDs = append(fanIDs, fanPath)
					break
				}
			}
		}
		curvesOut[i].Drives = fanIDs
	}

	s.writeJSON(r, w, InventoryResponse{
		Chips:  chipsOut,
		Curves: curvesOut,
	})
}

// chipBus classifies a monitor.Device into the bus enum the
// frontend uses. Mirrors devices.js's bus() inference but
// promoted to the backend so the page doesn't have to guess.
func chipBus(d monitor.Device) string {
	if strings.Contains(d.Path, "gpu") {
		return "nvml"
	}
	if len(d.Readings) > 0 && d.Readings[0].SensorType == "nvidia" {
		return "nvml"
	}
	if strings.HasPrefix(d.Path, "thermal") || strings.Contains(strings.ToLower(d.Name), "acpi") {
		return "acpi"
	}
	return "hwmon"
}

// readingKind maps the monitor.Reading.Unit field to the four-way
// enum the frontend expects. Empty / unknown units classify as
// "temp" by default — temperature is the most common reading type
// and the frontend renders unknown kinds as temp anyway.
func readingKind(unit string) string {
	switch unit {
	case "°C":
		return "temp"
	case "RPM":
		return "fan"
	case "V":
		return "volt"
	case "W":
		return "power"
	}
	// Fall back via a path-shape sniff for anything the unit table
	// doesn't cover — e.g. raw "in" without a scale that some
	// drivers leave bare.
	if strings.Contains(unit, "rpm") || strings.Contains(unit, "RPM") {
		return "fan"
	}
	return "temp"
}
