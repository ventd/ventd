// Package web — smart-mode HTTP handlers (split from server.go in v0.5.33).
//
// Hosts the four /api/v1/{confidence,smart}/* handlers + their JSON wire
// types. The handlers are read-only surfaces that snapshot the live
// aggregator + LayerA/LayerB/LayerC runtime state for the dashboard's
// 5-state pill UI and the per-channel deep-dive page.
//
// Split is mechanical — zero behaviour change. The route-table entries
// in server.go's registerAPIRoutes() still point at these methods on
// the same Server struct.
package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	acrunner "github.com/ventd/ventd/internal/acoustic/runner"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/controller"
	"github.com/ventd/ventd/internal/coupling"
	"github.com/ventd/ventd/internal/marginal"
)

// micCalibrated reports whether a per-host R30 microphone calibration
// record is present and parseable at s.kCalPath. When true, the
// smart-mode status handler's acoustic.current_dba is true dBA at the
// mic position; when false, it is the within-host "au" scale and the
// UI surfaces a calibrate-mic hint. (#1281)
func (s *Server) micCalibrated() bool {
	path := s.kCalPath
	if path == "" {
		path = acrunner.DefaultKCalPath
	}
	_, present, err := acrunner.LoadResult(path)
	return err == nil && present
}

// displayChannelID composes the operator-visible channel_id for the
// smart-mode JSON surfaces. The internal key (raw PWMPath) stays the
// canonical aggregator/coupling/marginal/decision-cache key — this
// helper only affects what the dashboard and CLI tooling see.
//
// Issue #998: NVML controllable channels store their PWMPath as the
// bare GPU index (e.g. "0") since the controller's NVIDIA backend
// addresses GPUs by integer index. Surfaced verbatim that's a
// confusing "channel_id: 0" in the UI; the parallel sensor-side fix
// (#927) composed sensor IDs as "gpu0:temp", "gpu0:fan_pct", etc., so
// the controllable form to match is "gpu<idx>:fan0".
//
// Recognises only the bare-integer shape (the NVML signature). Any
// channel_id containing a "/" (hwmon sysfs path) or already-composed
// form passes through unchanged. Looks up Fan.Type via the live config
// so a hwmon fan whose PWMPath accidentally collides with a single
// digit doesn't get re-labelled.
func displayChannelID(rawID string, live *config.Config) string {
	if live == nil {
		return rawID
	}
	idx, err := strconv.ParseUint(rawID, 10, 32)
	if err != nil {
		// Not a bare integer; safe to surface verbatim.
		return rawID
	}
	for _, f := range live.Fans {
		if f.PWMPath == rawID && f.Type == "nvidia" {
			return fmt.Sprintf("gpu%d:fan0", idx)
		}
	}
	return rawID
}

// fanNameFor returns the operator-facing fan name from config when
// rawID matches a configured fan's PWMPath. Empty when no match
// (channel not in config, or config not loaded). Lets the web UI
// surface "CPU Fan" instead of "/sys/class/hwmon/hwmon10/pwm6" on
// smart-mode strips and the confidence breakdown.
func fanNameFor(rawID string, live *config.Config) string {
	if live == nil || rawID == "" {
		return ""
	}
	for _, f := range live.Fans {
		if f.PWMPath == rawID && f.Name != "" {
			return f.Name
		}
	}
	return ""
}

// confidence snapshot. UI renders this directly.
type confidenceChannel struct {
	ChannelID string `json:"channel_id"`
	// Name mirrors smartChannelEntry.Name — operator-friendly fan
	// name from config.yaml when configured, empty otherwise.
	Name             string  `json:"name,omitempty"`
	Wpred            float64 `json:"w_pred"`
	UIState          string  `json:"ui_state"`
	ConfA            float64 `json:"conf_a"`
	ConfB            float64 `json:"conf_b"`
	ConfC            float64 `json:"conf_c"`
	Tier             uint8   `json:"tier"`
	Coverage         float64 `json:"coverage"`
	SeenFirstContact bool    `json:"seen_first_contact"`
	AgeSeconds       float64 `json:"age_seconds"`
}

type confidenceStatus struct {
	Enabled  bool                `json:"enabled"`
	Global   string              `json:"global_state"` // worst-of-channels collapse
	Preset   string              `json:"preset"`
	Channels []confidenceChannel `json:"channels"`
}

// handleConfidenceStatus GET /api/v1/confidence/status (v0.5.9).
// Returns the live aggregator + LayerA snapshots per channel for
// the dashboard's 5-state pill UI. Read-only; never blocks the
// controller hot loop (atomic.Pointer reads + a brief mutex on
// SnapshotAll).
func (s *Server) handleConfidenceStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	live := s.cfg.Load()
	preset := "balanced"
	if live != nil {
		if name, _ := live.Smart.SmartPreset(); name != "" {
			preset = name
		}
	}
	if s.aggregator == nil || s.layerA == nil {
		s.writeJSON(r, w, confidenceStatus{Enabled: false, Preset: preset})
		return
	}

	aggSnaps := s.aggregator.SnapshotAll()
	out := confidenceStatus{
		Enabled:  true,
		Preset:   preset,
		Channels: make([]confidenceChannel, 0, len(aggSnaps)),
	}
	worst := "converged" // best state; downgrades below
	priority := map[string]int{
		"refused": 0, "drifting": 1, "cold-start": 2,
		"warming": 3, "converged": 4,
	}
	for _, a := range aggSnaps {
		la := s.layerA.Read(a.ChannelID)
		entry := confidenceChannel{
			ChannelID: displayChannelID(a.ChannelID, live),
			Name:      fanNameFor(a.ChannelID, live),
			Wpred:     a.Wpred,
			UIState:   a.UIState,
			ConfA:     a.ConfA,
			ConfB:     a.ConfB,
			ConfC:     a.ConfC,
		}
		if la != nil {
			entry.Tier = la.Tier
			entry.Coverage = la.Coverage
			entry.SeenFirstContact = la.SeenFirstContact
			entry.AgeSeconds = la.Age.Seconds()
		}
		out.Channels = append(out.Channels, entry)
		if priority[a.UIState] < priority[worst] {
			worst = a.UIState
		}
	}
	if len(out.Channels) > 0 {
		out.Global = worst
	} else {
		out.Global = "idle"
	}
	s.writeJSON(r, w, out)
}

// handleConfidencePreset GET/PUT /api/v1/confidence/preset (v0.5.9).
// GET returns the active preset; PUT mutates the live Config in
// memory + persists via Save. Recognised values: silent / balanced /
// performance. Unknown values produce 400.
func (s *Server) handleConfidencePreset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodGet {
		live := s.cfg.Load()
		preset := "balanced"
		if live != nil {
			if name, _ := live.Smart.SmartPreset(); name != "" {
				preset = name
			}
		}
		s.writeJSON(r, w, map[string]string{"preset": preset})
		return
	}

	// PUT: read+validate body.
	var body struct {
		Preset string `json:"preset"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	switch body.Preset {
	case "silent", "balanced", "performance":
	default:
		http.Error(w, "preset must be silent|balanced|performance", http.StatusBadRequest)
		return
	}

	live := s.cfg.Load()
	if live == nil {
		http.Error(w, "no config loaded", http.StatusServiceUnavailable)
		return
	}
	// Deep-copy via JSON so we don't mutate the live pointer's
	// state under a concurrent reader.
	var next config.Config
	raw, err := json.Marshal(live)
	if err != nil {
		http.Error(w, "marshal", http.StatusInternalServerError)
		return
	}
	if err := json.Unmarshal(raw, &next); err != nil {
		http.Error(w, "unmarshal", http.StatusInternalServerError)
		return
	}
	next.Smart.Preset = body.Preset
	saved, err := config.Save(&next, s.configPath)
	if err != nil {
		s.logger.Warn("web: confidence preset save failed", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.cfg.Store(saved)
	s.logger.Info("web: confidence preset updated", "preset", body.Preset)
	s.writeJSON(r, w, map[string]string{"preset": body.Preset})
}

// ─── v0.5.12 #104: deeper smart-mode telemetry endpoints ──────────────

// smartStatusResponse is the aggregate dashboard payload for
// /api/v1/smart/status. One JSON object summarising the worst-case
// global state, the active preset, and channel counts. UI shows it as
// a single status pill or banner.
type smartStatusResponse struct {
	Enabled     bool   `json:"enabled"`
	Preset      string `json:"preset"`
	GlobalState string `json:"global_state"` // worst per-channel UI state across the fleet
	Channels    int    `json:"channels"`
	WarmingUp   int    `json:"warming_up"` // count of channels still warming Layer B/C
	Converged   int    `json:"converged"`  // count fully converged
	// ConfidenceMin/Max are nullable: emit JSON null when no channel
	// has positive Wpred yet (pre-warmup, monitor-only, or all
	// channels refused). The UI's smart-mode globals card handles
	// null as "—". Pre-fix, the handler emitted 0.0 here during the
	// 5-min cold-start window (RULE-AGG-COLDSTART-01), turning the
	// page into a literal "Conf min: 0.00 / Conf max: 0.00" — B1
	// from the v0.5.26 bug-floor probe.
	ConfidenceMin *float64 `json:"confidence_min"` // min w_pred across channels (0..1); null pre-warmup
	ConfidenceMax *float64 `json:"confidence_max"` // max w_pred across channels; null pre-warmup

	// Acoustic is the live acoustic-budget snapshot used by the
	// dBA-gate (#1273). target_dba is the operator-resolved dBA cap
	// (PresetDBATargets[preset] or smart.dba_target override).
	// current_dba is the R33-proxy-composed host loudness across all
	// hwmon fans, sampled at the most recent controller tick.
	// enabled reflects Config.AcousticOptimisationEnabled() — when
	// false the gate never refuses regardless of preset.
	Acoustic smartAcousticBudget `json:"acoustic"`

	// Cooling is the chassis cooling-capacity-W estimate (#1285)
	// surfaced beside the CPU TDP so the UI can render "chassis
	// cooling capacity: ~120 W" beside "CPU TDP: 125 W". Doctor
	// fires a warning when adequate=false AND has_signal=true.
	Cooling CoolingStatus `json:"cooling"`
}

type smartAcousticBudget struct {
	Enabled    bool    `json:"enabled"`
	TargetDBA  float64 `json:"target_dba,omitempty"`
	CurrentDBA float64 `json:"current_dba,omitempty"`
	// MicCalibrated is true when a per-host R30 microphone
	// calibration record is on disk (i.e. /var/lib/ventd/acoustic/
	// k_cal.json exists). When true, CurrentDBA is true dBA at the
	// mic position; when false, CurrentDBA is the within-host "au"
	// scale and the UI surfaces a "calibrate mic" hint. (#1281)
	MicCalibrated bool `json:"mic_calibrated"`
}

// CoolingStatus is the public seam the daemon uses to publish the
// chassis cooling-capacity-W estimate (#1285) into the smart-mode
// status surface. CapacityW is watts at 70 °C ΔT; CPUTDPW is the
// host CPU package power limit (RAPL). HasSignal is false on hosts
// where the data isn't available yet (pre-calibrate / AMD without
// RAPL / virtualised); UI hides the panel in that case.
type CoolingStatus struct {
	CapacityW float64 `json:"capacity_w,omitempty"`
	CPUTDPW   int     `json:"cpu_tdp_w,omitempty"`
	Adequate  bool    `json:"adequate"`
	HasSignal bool    `json:"has_signal"`
}

// smartChannelEntry is the deep per-channel snapshot for
// /api/v1/smart/channels. Fields are nullable when the corresponding
// runtime is absent (e.g. coupling but not marginal).
type smartChannelEntry struct {
	ChannelID string `json:"channel_id"`
	// Name is the operator-friendly fan name from config.yaml when
	// the channel matches a configured fan; empty otherwise. The web
	// UI prefers Name over the raw ChannelID for display so smart-mode
	// strips show "CPU Fan" instead of "/sys/class/hwmon/hwmon10/pwm6".
	// (#1228 / #1254 child fix.)
	Name           string                `json:"name,omitempty"`
	UIState        string                `json:"ui_state"` // converged|warming|cold-start|drifting|refused
	Wpred          float64               `json:"w_pred"`   // 0..1 final blend weight
	Coupling       *smartCouplingShard   `json:"coupling,omitempty"`
	Marginal       []*smartMarginalShard `json:"marginal,omitempty"`
	SignatureLabel string                `json:"signature_label,omitempty"`
	// Decision is the controller's most-recent BlendedResult for this
	// channel — what the next tick will write. Lets the dashboard
	// render the predicted next-tick ΔT (= MarginalSlope ×
	// (OutputPWM − ReactivePWM)) instead of just the per-PWM rate.
	// nil when no decision has been recorded (monitor-only / fresh
	// daemon / channel just admitted). (#790)
	Decision *smartChannelDecision `json:"decision,omitempty"`
}

// smartChannelDecision mirrors the operator-meaningful fields of
// controller.BlendedResult. The wire shape stays small — the doctor
// surface still consumes the full Result via its own path.
type smartChannelDecision struct {
	OutputPWM        uint8   `json:"output_pwm"`
	ReactivePWM      uint8   `json:"reactive_pwm"`
	PredictivePWM    uint8   `json:"predictive_pwm"`
	UIState          string  `json:"ui_state"` // reactive | blended | refused-pi | refused-pathA | refused-cost | refused-dba
	PredictedDBA     float64 `json:"predicted_dba,omitempty"`
	PathARefused     bool    `json:"path_a_refused,omitempty"`
	CostRefused      bool    `json:"cost_refused,omitempty"`
	DBABudgetRefused bool    `json:"dba_budget_refused,omitempty"`
	PIRefused        bool    `json:"pi_refused,omitempty"`
	IntegratorFrozen bool    `json:"integrator_frozen,omitempty"`
	DiagnosticReason string  `json:"diagnostic_reason,omitempty"`
}

type smartCouplingShard struct {
	Theta        []float64 `json:"theta"`
	NSamples     uint64    `json:"n_samples"`
	TrP          float64   `json:"tr_p"`
	Kappa        float64   `json:"kappa"`
	Lambda       float64   `json:"lambda"`
	WarmingUp    bool      `json:"warming_up"`
	GroupedFans  []int     `json:"grouped_fans,omitempty"`
	Reason       string    `json:"reason,omitempty"`
	LastTickUnix int64     `json:"last_tick_unix"`
}

type smartMarginalShard struct {
	SignatureLabel string    `json:"signature_label"`
	Kind           string    `json:"kind"`
	Theta          []float64 `json:"theta"`
	NSamples       uint64    `json:"n_samples"`
	TrP            float64   `json:"tr_p"`
	EWMAResidual   float64   `json:"ewma_residual"`
	// MarginalSlope is the Layer-C model's prediction of how much the
	// channel's temperature changes per +1 PWM unit at the last
	// observed load — i.e. β_0 + β_1·load (RULE-CMB-SAT-01). Cooling
	// fans yield negative values (more PWM → lower temp). Consumed by
	// the dashboard hero card to render an honest forecast arrow:
	// magnitude → arrow size, sign → arrow direction. Zero / NaN means
	// the shard has no usable estimate yet (warming up or
	// unidentifiable); the UI shows "—".
	MarginalSlope float64 `json:"marginal_slope"`
	WarmingUp     bool    `json:"warming_up"`
}

// handleSmartStatus GET /api/v1/smart/status — aggregate dashboard
// payload covering the whole smart-mode fleet in one read. Read-only,
// hot-loop safe (atomic.Pointer reads + brief mutex on aggregator's
// SnapshotAll). Returns enabled=false when the smart-mode runtimes
// are absent (monitor-only mode).
func (s *Server) handleSmartStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	live := s.cfg.Load()
	preset := "balanced"
	if live != nil {
		if name, _ := live.Smart.SmartPreset(); name != "" {
			preset = name
		}
	}
	if s.aggregator == nil {
		resp := smartStatusResponse{
			Enabled:  false,
			Preset:   preset,
			Acoustic: smartAcousticBudget{Enabled: false, MicCalibrated: s.micCalibrated()},
		}
		if s.coolingFn != nil {
			resp.Cooling = s.coolingFn()
		} else {
			resp.Cooling = CoolingStatus{Adequate: true, HasSignal: false}
		}
		s.writeJSON(r, w, resp)
		return
	}

	aggSnaps := s.aggregator.SnapshotAll()
	out := smartStatusResponse{
		Enabled:  true,
		Preset:   preset,
		Channels: len(aggSnaps),
	}
	priority := map[string]int{
		"refused": 0, "drifting": 1, "cold-start": 2,
		"warming": 3, "converged": 4,
	}
	worst := "converged"
	cmin := 1.0
	cmax := 0.0
	for _, a := range aggSnaps {
		if a.UIState == "warming" || a.UIState == "cold-start" {
			out.WarmingUp++
		}
		if a.UIState == "converged" {
			out.Converged++
		}
		if a.Wpred < cmin {
			cmin = a.Wpred
		}
		if a.Wpred > cmax {
			cmax = a.Wpred
		}
		if priority[a.UIState] < priority[worst] {
			worst = a.UIState
		}
	}
	if len(aggSnaps) == 0 {
		// No channels at all: ConfidenceMin/Max stay nil (UI shows "—").
		// Report "idle" rather than "converged" — there is nothing to
		// converge on, and the dashboard's status pill should not show
		// a green "converged" badge while no channels are tracked.
		out.GlobalState = "idle"
	} else {
		out.GlobalState = worst
		// Only emit numeric confidence_min/max when at least one
		// channel has positive Wpred — i.e. someone has emerged from
		// the cold-start / warming window. Otherwise leave them nil
		// so the UI shows "—" rather than a literal "0.00".
		if cmax > 0 {
			out.ConfidenceMin = &cmin
			out.ConfidenceMax = &cmax
		}
	}

	// Acoustic budget surface (#1273): mirror back the operator-
	// resolved dBA cap and the most-recent-tick CurrentDBA so the web
	// UI can render a quietness meter alongside the temperature
	// stats. The Decisions cache holds per-channel
	// BlendedResult.PredictedDBA from the last tick; the max across
	// channels (with the candidate ramp folded in) is the closest
	// proxy for current host loudness we expose. Pre-warmup or
	// monitor-only hosts get enabled=false / zero values.
	out.Acoustic = smartAcousticBudget{Enabled: false, MicCalibrated: s.micCalibrated()}
	if live != nil && live.AcousticOptimisationEnabled() {
		preset, _ := controller.PresetFromString(live.Smart.Preset)
		target := controller.DBATargetFor(preset, live.Smart.DBATarget)
		out.Acoustic.Enabled = target > 0
		if out.Acoustic.Enabled {
			out.Acoustic.TargetDBA = target
			// Pull the most-recent predicted dBA across channels as a
			// proxy for current host loudness. We take the max because
			// PredictedDBA is the per-channel candidate-ramp loudness
			// estimate — the host total tracks the loudest fan plus
			// the energetic-sum tail, well-approximated by max for the
			// dashboard surface.
			if s.decisions != nil {
				for _, dec := range s.decisions.LoadAll() {
					if dec.Result.PredictedDBA > out.Acoustic.CurrentDBA {
						out.Acoustic.CurrentDBA = dec.Result.PredictedDBA
					}
				}
			}
		}
	}

	if s.coolingFn != nil {
		out.Cooling = s.coolingFn()
	} else {
		out.Cooling = CoolingStatus{Adequate: true, HasSignal: false}
	}

	s.writeJSON(r, w, out)
}

// handleSmartChannels GET /api/v1/smart/channels — the deep
// per-channel telemetry view. Combines aggregator UIState, coupling
// (Layer B) RLS shard, and marginal (Layer C) shards keyed by
// signature label. Used by the dashboard's channel-detail panel and
// the doctor surface for confidence-related issue triage.
func (s *Server) handleSmartChannels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if s.aggregator == nil {
		s.writeJSON(r, w, []smartChannelEntry{})
		return
	}

	aggSnaps := s.aggregator.SnapshotAll()
	entries := make([]smartChannelEntry, 0, len(aggSnaps))

	// Pre-index coupling + marginal snapshots by channel for O(1)
	// lookup during the per-aggregator-channel loop.
	var couplingByChannel map[string]*coupling.Snapshot
	if s.couplingRT != nil {
		cs := s.couplingRT.SnapshotAll()
		couplingByChannel = make(map[string]*coupling.Snapshot, len(cs))
		for _, c := range cs {
			if c == nil {
				continue
			}
			couplingByChannel[c.ChannelID] = c
		}
	}

	var marginalByChannel map[string][]*marginal.Snapshot
	if s.marginalRT != nil {
		ms := s.marginalRT.SnapshotAll()
		marginalByChannel = make(map[string][]*marginal.Snapshot, len(ms))
		for _, m := range ms {
			if m == nil {
				continue
			}
			marginalByChannel[m.ChannelID] = append(marginalByChannel[m.ChannelID], m)
		}
	}

	// Snapshot the controller's per-channel decision cache once per
	// request — gives the dashboard the next-tick PWM target +
	// refusal flags alongside Layer-C's MarginalSlope. (#790)
	decisionsByChannel := s.decisions.LoadAll()

	live := s.cfg.Load()
	for _, a := range aggSnaps {
		entry := smartChannelEntry{
			ChannelID: displayChannelID(a.ChannelID, live),
			Name:      fanNameFor(a.ChannelID, live),
			UIState:   a.UIState,
			Wpred:     a.Wpred,
		}
		if dec, ok := decisionsByChannel[a.ChannelID]; ok {
			entry.Decision = &smartChannelDecision{
				OutputPWM:        dec.Result.OutputPWM,
				ReactivePWM:      dec.ReactivePWM,
				PredictivePWM:    dec.Result.PredictivePWM,
				UIState:          dec.Result.UIState,
				PredictedDBA:     dec.Result.PredictedDBA,
				PathARefused:     dec.Result.PathARefused,
				CostRefused:      dec.Result.CostRefused,
				DBABudgetRefused: dec.Result.DBABudgetRefused,
				PIRefused:        dec.Result.PIRefused,
				IntegratorFrozen: dec.Result.IntegratorFrozen,
				DiagnosticReason: dec.Result.DiagnosticReason,
			}
		}
		if c, ok := couplingByChannel[a.ChannelID]; ok && c != nil {
			entry.Coupling = &smartCouplingShard{
				Theta:        append([]float64(nil), c.Theta...),
				NSamples:     c.NSamples,
				TrP:          c.TrP,
				Kappa:        c.Kappa,
				Lambda:       c.Lambda,
				WarmingUp:    c.WarmingUp,
				GroupedFans:  append([]int(nil), c.GroupedFans...),
				Reason:       c.Reason,
				LastTickUnix: c.LastTickUnix,
			}
		}
		if msList, ok := marginalByChannel[a.ChannelID]; ok {
			for _, m := range msList {
				if m == nil {
					continue
				}
				entry.Marginal = append(entry.Marginal, &smartMarginalShard{
					SignatureLabel: m.SignatureLabel,
					Kind:           string(m.Kind),
					Theta:          append([]float64(nil), m.Theta...),
					NSamples:       m.NSamples,
					TrP:            m.TrP,
					EWMAResidual:   m.EWMAResidual,
					MarginalSlope:  m.MarginalSlope,
					WarmingUp:      m.WarmingUp,
				})
				if entry.SignatureLabel == "" {
					// Use the first marginal shard's signature as the
					// channel's "active" label for surfaces that show
					// only one.
					entry.SignatureLabel = m.SignatureLabel
				}
			}
		}
		entries = append(entries, entry)
	}
	s.writeJSON(r, w, entries)
}
