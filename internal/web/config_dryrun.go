package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/ventd/ventd/internal/config"
)

// ConfigDiff is the response shape for POST /api/config/dryrun. The
// web UI renders Sections as a modal before POSTing to /api/config;
// Changed == false short-circuits the modal and skips the save.
//
// Why a structured diff rather than a unified diff of the YAML/JSON:
// fan-controller configs have a lot of tiny numeric fields (min_pwm,
// max_pwm, min_temp, max_temp, …) that read as noise in a line diff.
// A semantic diff pairs items by identity, so a user who renamed one
// curve doesn't also see spurious drift in the fields of unrelated
// curves.
type ConfigDiff struct {
	Changed  bool          `json:"changed"`
	Sections []DiffSection `json:"sections"`
}

// DiffSection describes one added / removed / modified item. Name is
// the identifier (curve name, fan name, sensor name, control.fan) and
// is empty for scalar sections like "web" or "hwmon".
type DiffSection struct {
	Section string      `json:"section"`
	Kind    string      `json:"kind"` // "added" | "removed" | "modified"
	Name    string      `json:"name,omitempty"`
	Fields  []DiffField `json:"fields,omitempty"`
}

// DiffField is a single field change inside a modified section. From
// and To are stringified for display; the UI does not need to parse
// them. Empty string represents a zero value (e.g. unset sensor).
type DiffField struct {
	Name string `json:"name"`
	From string `json:"from"`
	To   string `json:"to"`
}

// handleConfigDryrun accepts a candidate config and returns the diff
// against the live config without persisting anything. Used by the
// dashboard's Apply flow to show a confirmation modal before committing
// a change — fan-controller configs touch physical hardware and the
// user should see exactly what they're about to commit.
//
// Body limits and validation mirror /api/config PUT: the same 1 MiB
// cap, the same JSON-decode error surface. Dryrun does NOT run the
// config validator; a diff of an invalid config is still useful ("you
// just introduced a min_pwm:0 without allow_stop"), and the actual
// save will reject it anyway via config.Save.
func (s *Server) handleConfigDryrun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limitBody(w, r, defaultMaxBody)
	var incoming config.Config
	if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
		if isMaxBytesErr(err) {
			http.Error(w, "config too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	live := s.cfg.Load()
	diff := diffConfigs(live, &incoming)
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(r, w, diff)
}

// diffConfigs computes the semantic diff between the live config and
// a candidate. Ordering is deterministic (section order + alphabetical
// by name within each section) so the UI renders a stable list across
// repeat calls.
func diffConfigs(live, next *config.Config) ConfigDiff {
	out := ConfigDiff{}

	// Scalar / top-level fields the UI surfaces. The password hash is
	// intentionally excluded — even a "changed"/"unchanged" signal
	// would leak timing data, and the UI doesn't expose it for edit.
	if live.Version != next.Version {
		out.Sections = append(out.Sections, scalarDiff("version", "", "version",
			fmt.Sprintf("%d", live.Version), fmt.Sprintf("%d", next.Version)))
	}
	if live.PollInterval.Duration != next.PollInterval.Duration {
		out.Sections = append(out.Sections, scalarDiff("poll_interval", "", "poll_interval",
			live.PollInterval.String(), next.PollInterval.String()))
	}
	if live.Hwmon.DynamicRebind != next.Hwmon.DynamicRebind {
		out.Sections = append(out.Sections, scalarDiff("hwmon", "", "dynamic_rebind",
			boolStr(live.Hwmon.DynamicRebind), boolStr(next.Hwmon.DynamicRebind)))
	}
	if live.Web.Listen != next.Web.Listen {
		out.Sections = append(out.Sections, scalarDiff("web", "", "listen",
			live.Web.Listen, next.Web.Listen))
	}

	out.Sections = append(out.Sections, diffSensors(live.Sensors, next.Sensors)...)
	out.Sections = append(out.Sections, diffFans(live.Fans, next.Fans)...)
	out.Sections = append(out.Sections, diffCurves(live.Curves, next.Curves)...)
	out.Sections = append(out.Sections, diffControls(live.Controls, next.Controls)...)

	out.Changed = len(out.Sections) > 0
	return out
}

func scalarDiff(section, name, field, from, to string) DiffSection {
	return DiffSection{
		Section: section,
		Kind:    "modified",
		Name:    name,
		Fields:  []DiffField{{Name: field, From: from, To: to}},
	}
}

// pairByKey splits two item sets into added / removed / both-present.
// Caller supplies a key function; duplicate keys are merged down to
// the first occurrence on each side (a truly duplicate-keyed config
// is already rejected by config.Save, so dryrun doesn't need to
// surface it separately).
type pairResult[T any] struct {
	added   []T
	removed []T
	paired  []pair[T]
}
type pair[T any] struct{ from, to T }

func pairByKey[T any](live, next []T, key func(T) string) pairResult[T] {
	liveByKey := make(map[string]T, len(live))
	for _, it := range live {
		k := key(it)
		if _, ok := liveByKey[k]; !ok {
			liveByKey[k] = it
		}
	}
	var res pairResult[T]
	seen := make(map[string]bool, len(next))
	for _, it := range next {
		k := key(it)
		if seen[k] {
			continue
		}
		seen[k] = true
		if prev, ok := liveByKey[k]; ok {
			res.paired = append(res.paired, pair[T]{from: prev, to: it})
		} else {
			res.added = append(res.added, it)
		}
	}
	for k, it := range liveByKey {
		if !seen[k] {
			res.removed = append(res.removed, it)
		}
	}
	return res
}

func diffSensors(live, next []config.Sensor) []DiffSection {
	key := func(s config.Sensor) string { return s.Name }
	res := pairByKey(live, next, key)
	var out []DiffSection
	for _, s := range res.added {
		out = append(out, DiffSection{Section: "sensors", Kind: "added", Name: s.Name})
	}
	for _, s := range res.removed {
		out = append(out, DiffSection{Section: "sensors", Kind: "removed", Name: s.Name})
	}
	for _, p := range res.paired {
		fields := diffSensorFields(p.from, p.to)
		if len(fields) == 0 {
			continue
		}
		out = append(out, DiffSection{Section: "sensors", Kind: "modified", Name: p.to.Name, Fields: fields})
	}
	return out
}

func diffSensorFields(a, b config.Sensor) []DiffField {
	var f []DiffField
	if a.Type != b.Type {
		f = append(f, DiffField{Name: "type", From: a.Type, To: b.Type})
	}
	if a.Path != b.Path {
		f = append(f, DiffField{Name: "path", From: a.Path, To: b.Path})
	}
	if a.Metric != b.Metric {
		f = append(f, DiffField{Name: "metric", From: a.Metric, To: b.Metric})
	}
	if a.HwmonDevice != b.HwmonDevice {
		f = append(f, DiffField{Name: "hwmon_device", From: a.HwmonDevice, To: b.HwmonDevice})
	}
	if a.ChipName != b.ChipName {
		f = append(f, DiffField{Name: "chip_name", From: a.ChipName, To: b.ChipName})
	}
	return f
}

func diffFans(live, next []config.Fan) []DiffSection {
	key := func(f config.Fan) string { return f.Name }
	res := pairByKey(live, next, key)
	var out []DiffSection
	for _, f := range res.added {
		out = append(out, DiffSection{Section: "fans", Kind: "added", Name: f.Name})
	}
	for _, f := range res.removed {
		out = append(out, DiffSection{Section: "fans", Kind: "removed", Name: f.Name})
	}
	for _, p := range res.paired {
		fields := diffFanFields(p.from, p.to)
		if len(fields) == 0 {
			continue
		}
		out = append(out, DiffSection{Section: "fans", Kind: "modified", Name: p.to.Name, Fields: fields})
	}
	return out
}

func diffFanFields(a, b config.Fan) []DiffField {
	var f []DiffField
	if a.Type != b.Type {
		f = append(f, DiffField{Name: "type", From: a.Type, To: b.Type})
	}
	if a.PWMPath != b.PWMPath {
		f = append(f, DiffField{Name: "pwm_path", From: a.PWMPath, To: b.PWMPath})
	}
	if a.RPMPath != b.RPMPath {
		f = append(f, DiffField{Name: "rpm_path", From: a.RPMPath, To: b.RPMPath})
	}
	if a.HwmonDevice != b.HwmonDevice {
		f = append(f, DiffField{Name: "hwmon_device", From: a.HwmonDevice, To: b.HwmonDevice})
	}
	if a.ChipName != b.ChipName {
		f = append(f, DiffField{Name: "chip_name", From: a.ChipName, To: b.ChipName})
	}
	if a.ControlKind != b.ControlKind {
		f = append(f, DiffField{Name: "control_kind", From: a.ControlKind, To: b.ControlKind})
	}
	if a.MinPWM != b.MinPWM {
		f = append(f, DiffField{Name: "min_pwm", From: fmt.Sprint(a.MinPWM), To: fmt.Sprint(b.MinPWM)})
	}
	if a.MaxPWM != b.MaxPWM {
		f = append(f, DiffField{Name: "max_pwm", From: fmt.Sprint(a.MaxPWM), To: fmt.Sprint(b.MaxPWM)})
	}
	if a.IsPump != b.IsPump {
		f = append(f, DiffField{Name: "is_pump", From: boolStr(a.IsPump), To: boolStr(b.IsPump)})
	}
	if a.PumpMinimum != b.PumpMinimum {
		f = append(f, DiffField{Name: "pump_minimum", From: fmt.Sprint(a.PumpMinimum), To: fmt.Sprint(b.PumpMinimum)})
	}
	if a.AllowStop != b.AllowStop {
		f = append(f, DiffField{Name: "allow_stop", From: boolStr(a.AllowStop), To: boolStr(b.AllowStop)})
	}
	return f
}

func diffCurves(live, next []config.CurveConfig) []DiffSection {
	key := func(c config.CurveConfig) string { return c.Name }
	res := pairByKey(live, next, key)
	var out []DiffSection
	for _, c := range res.added {
		out = append(out, DiffSection{Section: "curves", Kind: "added", Name: c.Name})
	}
	for _, c := range res.removed {
		out = append(out, DiffSection{Section: "curves", Kind: "removed", Name: c.Name})
	}
	for _, p := range res.paired {
		fields := diffCurveFields(p.from, p.to)
		if len(fields) == 0 {
			continue
		}
		out = append(out, DiffSection{Section: "curves", Kind: "modified", Name: p.to.Name, Fields: fields})
	}
	return out
}

func diffCurveFields(a, b config.CurveConfig) []DiffField {
	var f []DiffField
	if a.Type != b.Type {
		f = append(f, DiffField{Name: "type", From: a.Type, To: b.Type})
	}
	if a.Sensor != b.Sensor {
		f = append(f, DiffField{Name: "sensor", From: a.Sensor, To: b.Sensor})
	}
	if a.MinTemp != b.MinTemp {
		f = append(f, DiffField{Name: "min_temp", From: fmt.Sprint(a.MinTemp), To: fmt.Sprint(b.MinTemp)})
	}
	if a.MaxTemp != b.MaxTemp {
		f = append(f, DiffField{Name: "max_temp", From: fmt.Sprint(a.MaxTemp), To: fmt.Sprint(b.MaxTemp)})
	}
	if a.MinPWM != b.MinPWM {
		f = append(f, DiffField{Name: "min_pwm", From: fmt.Sprint(a.MinPWM), To: fmt.Sprint(b.MinPWM)})
	}
	if a.MaxPWM != b.MaxPWM {
		f = append(f, DiffField{Name: "max_pwm", From: fmt.Sprint(a.MaxPWM), To: fmt.Sprint(b.MaxPWM)})
	}
	if a.Value != b.Value {
		f = append(f, DiffField{Name: "value", From: fmt.Sprint(a.Value), To: fmt.Sprint(b.Value)})
	}
	if a.Function != b.Function {
		f = append(f, DiffField{Name: "function", From: a.Function, To: b.Function})
	}
	if !equalStringSlices(a.Sources, b.Sources) {
		f = append(f, DiffField{Name: "sources",
			From: "[" + strings.Join(a.Sources, ", ") + "]",
			To:   "[" + strings.Join(b.Sources, ", ") + "]"})
	}
	return f
}

func diffControls(live, next []config.Control) []DiffSection {
	key := func(c config.Control) string { return c.Fan }
	res := pairByKey(live, next, key)
	var out []DiffSection
	for _, c := range res.added {
		out = append(out, DiffSection{Section: "controls", Kind: "added", Name: c.Fan})
	}
	for _, c := range res.removed {
		out = append(out, DiffSection{Section: "controls", Kind: "removed", Name: c.Fan})
	}
	for _, p := range res.paired {
		fields := diffControlFields(p.from, p.to)
		if len(fields) == 0 {
			continue
		}
		out = append(out, DiffSection{Section: "controls", Kind: "modified", Name: p.to.Fan, Fields: fields})
	}
	return out
}

func diffControlFields(a, b config.Control) []DiffField {
	var f []DiffField
	if a.Curve != b.Curve {
		f = append(f, DiffField{Name: "curve", From: a.Curve, To: b.Curve})
	}
	// ManualPWM is *uint8; treat nil and zero-pointer as distinct because
	// nil means curve-mode while *x means fixed-duty. Display nil as
	// "(curve mode)" so a reviewer understands the transition.
	if manualPWMString(a.ManualPWM) != manualPWMString(b.ManualPWM) {
		f = append(f, DiffField{Name: "manual_pwm", From: manualPWMString(a.ManualPWM), To: manualPWMString(b.ManualPWM)})
	}
	return f
}

func manualPWMString(p *uint8) string {
	if p == nil {
		return "(curve mode)"
	}
	return fmt.Sprint(*p)
}

func equalStringSlices(a, b []string) bool {
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

func boolStr(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
