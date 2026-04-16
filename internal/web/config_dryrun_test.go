package web

import (
	"testing"

	"github.com/ventd/ventd/internal/config"
)

// TestDiffConfigs_NoChange asserts that two equal configs produce a
// Changed=false, empty-sections response. The UI relies on the
// Changed flag to short-circuit the modal and skip the save — an
// off-by-one in the walker would surface as a spurious modal on
// every Apply.
func TestDiffConfigs_NoChange(t *testing.T) {
	live := config.Empty()
	next := *live
	d := diffConfigs(live, &next)
	if d.Changed {
		t.Errorf("Changed=true on identical configs, sections=%v", d.Sections)
	}
	if len(d.Sections) != 0 {
		t.Errorf("want no sections, got %d: %v", len(d.Sections), d.Sections)
	}
}

// TestDiffConfigs_CurveFieldChange exercises the audit's reference
// case from the PR-2d spec: edit cpu_linear.max_temp from 80 to 75
// and max_pwm from 100 to 90. Shape of the Fields slice must match
// the order curve fields are walked so the UI renders a stable list.
func TestDiffConfigs_CurveFieldChange(t *testing.T) {
	live := config.Empty()
	live.Curves = []config.CurveConfig{
		{Name: "cpu_linear", Type: "linear", Sensor: "cpu_temp", MinTemp: 30, MaxTemp: 80, MinPWM: 80, MaxPWM: 100},
	}
	next := *live
	next.Curves = []config.CurveConfig{
		{Name: "cpu_linear", Type: "linear", Sensor: "cpu_temp", MinTemp: 30, MaxTemp: 75, MinPWM: 80, MaxPWM: 90},
	}
	d := diffConfigs(live, &next)
	if !d.Changed {
		t.Fatalf("Changed=false on modified curve")
	}
	if len(d.Sections) != 1 {
		t.Fatalf("want 1 section, got %d: %v", len(d.Sections), d.Sections)
	}
	got := d.Sections[0]
	if got.Section != "curves" || got.Kind != "modified" || got.Name != "cpu_linear" {
		t.Errorf("unexpected section header: %+v", got)
	}
	if len(got.Fields) != 2 {
		t.Fatalf("want 2 field diffs, got %d: %v", len(got.Fields), got.Fields)
	}
	if got.Fields[0].Name != "max_temp" || got.Fields[0].From != "80" || got.Fields[0].To != "75" {
		t.Errorf("max_temp diff wrong: %+v", got.Fields[0])
	}
	if got.Fields[1].Name != "max_pwm" || got.Fields[1].From != "100" || got.Fields[1].To != "90" {
		t.Errorf("max_pwm diff wrong: %+v", got.Fields[1])
	}
}

// TestDiffConfigs_MixSources verifies that changing the sources list
// on a mix curve is reported once as a single field diff rather than
// producing per-element drift. The UI wraps the slice in square
// brackets for display, matching the CurveConfig's mix-card output.
func TestDiffConfigs_MixSources(t *testing.T) {
	live := config.Empty()
	live.Curves = []config.CurveConfig{
		{Name: "mix", Type: "mix", Function: "max", Sources: []string{"a", "b"}},
	}
	next := *live
	next.Curves = []config.CurveConfig{
		{Name: "mix", Type: "mix", Function: "max", Sources: []string{"a"}},
	}
	d := diffConfigs(live, &next)
	if !d.Changed {
		t.Fatalf("Changed=false on mix-source change")
	}
	found := false
	for _, sec := range d.Sections {
		if sec.Section == "curves" && sec.Name == "mix" && sec.Kind == "modified" {
			for _, f := range sec.Fields {
				if f.Name == "sources" && f.From == "[a, b]" && f.To == "[a]" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Errorf("expected sources field diff [a, b]→[a], got: %+v", d.Sections)
	}
}

// TestDiffConfigs_AddRemoveRename covers the identity-bound diff
// shape: adding a curve reports added, removing one reports removed,
// and renaming one shows up as removed-old + added-new. The web UI
// relies on the removed/added pair for rename detection — showing a
// "rename" event would hide the fact that every downstream reference
// needs to be updated too.
func TestDiffConfigs_AddRemoveRename(t *testing.T) {
	live := config.Empty()
	live.Sensors = []config.Sensor{
		{Name: "cpu_temp", Type: "hwmon", Path: "/sys/class/hwmon/hwmon0/temp1_input"},
	}
	next := *live
	next.Sensors = []config.Sensor{
		{Name: "cpu_core", Type: "hwmon", Path: "/sys/class/hwmon/hwmon0/temp1_input"},
		{Name: "gpu_temp", Type: "nvidia", Path: "0", Metric: "temp"},
	}
	d := diffConfigs(live, &next)
	if !d.Changed {
		t.Fatalf("Changed=false after rename+add")
	}
	kinds := map[string]int{}
	names := map[string]bool{}
	for _, sec := range d.Sections {
		if sec.Section != "sensors" {
			continue
		}
		kinds[sec.Kind]++
		names[sec.Name] = true
	}
	if kinds["removed"] != 1 || kinds["added"] != 2 {
		t.Errorf("want 1 removed + 2 added, got %v (sections %+v)", kinds, d.Sections)
	}
	if !names["cpu_temp"] || !names["cpu_core"] || !names["gpu_temp"] {
		t.Errorf("missing expected sensor name in %v", names)
	}
}

// TestDiffConfigs_ControlManualPWM exercises the *uint8 distinction.
// Switching a control from curve mode to manual PWM should report
// manual_pwm: (curve mode) → 128, not be silently treated as a
// missing field.
func TestDiffConfigs_ControlManualPWM(t *testing.T) {
	live := config.Empty()
	live.Controls = []config.Control{{Fan: "cpu-fan", Curve: "cpu_linear"}}
	next := *live
	v := uint8(128)
	next.Controls = []config.Control{{Fan: "cpu-fan", Curve: "cpu_linear", ManualPWM: &v}}
	d := diffConfigs(live, &next)
	if !d.Changed {
		t.Fatalf("Changed=false on manual_pwm toggle")
	}
	var got DiffSection
	for _, s := range d.Sections {
		if s.Section == "controls" && s.Name == "cpu-fan" {
			got = s
			break
		}
	}
	if got.Kind != "modified" {
		t.Fatalf("want modified control, got %+v", got)
	}
	if len(got.Fields) != 1 {
		t.Fatalf("want 1 field diff, got %d: %+v", len(got.Fields), got.Fields)
	}
	if got.Fields[0].Name != "manual_pwm" || got.Fields[0].From != "(curve mode)" || got.Fields[0].To != "128" {
		t.Errorf("manual_pwm diff wrong: %+v", got.Fields[0])
	}
}

// TestDiffConfigs_HwmonDynamicRebind asserts that the gate flag is
// surfaced under its own section — a boolean flip from false to true
// is a load-bearing operational change and must render in the modal.
func TestDiffConfigs_HwmonDynamicRebind(t *testing.T) {
	live := config.Empty()
	next := *live
	next.Hwmon.DynamicRebind = true
	d := diffConfigs(live, &next)
	if !d.Changed || len(d.Sections) == 0 {
		t.Fatalf("no diff on dynamic_rebind flip")
	}
	var found bool
	for _, s := range d.Sections {
		if s.Section == "hwmon" {
			for _, f := range s.Fields {
				if f.Name == "dynamic_rebind" && f.From == "false" && f.To == "true" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Errorf("hwmon.dynamic_rebind flip not reported: %+v", d.Sections)
	}
}
