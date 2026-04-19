package web

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/ventd/ventd/internal/config"
)

// ConfigPatch carries a partial config update for PATCH /api/config.
// Only non-nil fields are merged into the live config; unset fields are
// left unchanged. This prevents partial form submissions from zeroing
// out fields the user did not touch — the root cause of #483.
type ConfigPatch struct {
	Curves []CurvePatch `json:"curves,omitempty"`
}

// CurvePatch updates one named curve. Name is required; all other fields
// are optional pointers — nil means "leave unchanged".
type CurvePatch struct {
	Name       string   `json:"name"`
	MinTemp    *float64 `json:"min_temp,omitempty"`
	MaxTemp    *float64 `json:"max_temp,omitempty"`
	MinPWMPct  *float64 `json:"min_pwm_pct,omitempty"`
	MaxPWMPct  *float64 `json:"max_pwm_pct,omitempty"`
	Sensor     *string  `json:"sensor,omitempty"`
	Hysteresis *float64 `json:"hysteresis,omitempty"`
	Smoothing  *string  `json:"smoothing,omitempty"`
}

// applyConfigPatch merges non-nil fields from patch into a deep copy of
// current. Curve patches are matched by name; an unknown name is an error.
func applyConfigPatch(current *config.Config, patch *ConfigPatch) (*config.Config, error) {
	merged := *current
	merged.Curves = make([]config.CurveConfig, len(current.Curves))
	copy(merged.Curves, current.Curves)

	for _, cp := range patch.Curves {
		if cp.Name == "" {
			return nil, fmt.Errorf("curve patch missing name")
		}
		idx := -1
		for i, c := range merged.Curves {
			if c.Name == cp.Name {
				idx = i
				break
			}
		}
		if idx == -1 {
			return nil, fmt.Errorf("curve %q not found in current config", cp.Name)
		}
		c := &merged.Curves[idx]
		if cp.MinTemp != nil {
			c.MinTemp = *cp.MinTemp
		}
		if cp.MaxTemp != nil {
			c.MaxTemp = *cp.MaxTemp
		}
		if cp.Sensor != nil {
			c.Sensor = *cp.Sensor
		}
		if cp.Hysteresis != nil {
			c.Hysteresis = *cp.Hysteresis
		}
		if cp.MinPWMPct != nil {
			v := patchClampPct(*cp.MinPWMPct)
			c.MinPWMPct = &v
			c.MinPWM = 0 // cleared; MigrateCurvePWMFields derives raw from pct
		}
		if cp.MaxPWMPct != nil {
			v := patchClampPct(*cp.MaxPWMPct)
			c.MaxPWMPct = &v
			c.MaxPWM = 0
		}
		if cp.Smoothing != nil {
			s := *cp.Smoothing
			if s == "" || s == "0s" || s == "0" {
				c.Smoothing = config.Duration{}
			} else {
				d, err := time.ParseDuration(s)
				if err != nil {
					return nil, fmt.Errorf("curve %q: invalid smoothing %q: %w", cp.Name, s, err)
				}
				c.Smoothing = config.Duration{Duration: d}
			}
		}
	}
	return &merged, nil
}

// patchClampPct converts a float64 percent to a uint8 clamped to [0, 100].
func patchClampPct(v float64) uint8 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return uint8(math.Round(v))
}

// handleConfigPatch handles PATCH /api/config — merges a partial update
// into the live config rather than replacing it wholesale. On success it
// returns the full merged config so the UI can rehydrate the form.
func (s *Server) handleConfigPatch(w http.ResponseWriter, r *http.Request) {
	limitBody(w, r, defaultMaxBody)
	var patch ConfigPatch
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		if isMaxBytesErr(err) {
			http.Error(w, "config too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	current := s.cfg.Load()
	merged, err := applyConfigPatch(current, &patch)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	validated, err := config.Save(merged, s.configPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.cfg.Store(validated)
	s.logger.Info("config patched via web UI")
	s.triggerReload()

	s.writeJSON(r, w, validated)
}
