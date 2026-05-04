// Package web — doctor surface (spec-10, v0.6 prereq #2).
//
// Endpoint: GET /api/v1/doctor
//
//	Runs the doctor.Runner with a stateless detector set and returns
//	the resulting Report as JSON. The web UI polls this every 5
//	seconds; the handler caches the previous Report for the same
//	window so a multi-tab dashboard doesn't fan out into N detector
//	re-runs per tick.
//
// MVP detector set (no daemon-startup baseline required):
//   - container_postboot   — RULE-DOCTOR-DETECTOR-CONTAINERPOSTBOOT
//   - dkms_status          — RULE-DOCTOR-DETECTOR-DKMSSTATUS
//   - battery_transition   — RULE-DOCTOR-DETECTOR-BATTERY
//   - gpu_readiness        — RULE-DOCTOR-DETECTOR-GPUREADINESS
//   - permissions          — RULE-DOCTOR-DETECTOR-PERMISSIONS
//   - experimental_flags   — RULE-DOCTOR-DETECTOR-EXPERIMENTALFLAGS
//
// Baseline-requiring detectors (kernel_update, hwmon_swap,
// apparmor_profile_drift, dmi_fingerprint, calibration_freshness)
// land in a follow-up that captures their baselines at daemon start
// and threads them through the runner constructor.
package web

import (
	"net/http"
	"sync"
	"time"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/doctor/detectors"
)

// doctorReportCacheTTL caps how often the detectors actually run.
// Long enough to absorb a multi-tab dashboard (10 panels each polling
// at 5 s = 2 detector runs/sec without the cache); short enough that
// an operator clicking around the doctor page sees a fresh report
// within a few seconds. Detectors are bounded at 200 ms each
// (PerDetectorTimeout) so a full run takes well under 2 s.
const doctorReportCacheTTL = 5 * time.Second

// doctorRunnerCache holds the per-Server doctor runner + its most
// recent Report. The runner is constructed lazily on first GET so
// detectors that touch /sys / /proc don't run during daemon startup.
type doctorRunnerCache struct {
	mu       sync.Mutex
	runner   *doctor.Runner
	report   doctor.Report
	reportAt time.Time
}

// doctorRunner returns the per-Server runner, constructing it on first
// use. The detector set is stateless (no per-host baselines) so the
// constructor is safe to call after daemon start.
func (s *Server) doctorRunner() *doctor.Runner {
	s.doctorCache.mu.Lock()
	defer s.doctorCache.mu.Unlock()
	if s.doctorCache.runner != nil {
		return s.doctorCache.runner
	}
	det := []doctor.Detector{
		detectors.NewContainerPostbootDetector(nil),
		detectors.NewDKMSStatusDetector(nil),
		detectors.NewBatteryTransitionDetector(nil),
		detectors.NewGPUReadinessDetector(nil),
		detectors.NewPermissionsDetector(nil),
		detectors.NewExperimentalFlagsDetector(s.diag),
	}
	s.doctorCache.runner = doctor.NewRunner(det, nil, nil, nil)
	return s.doctorCache.runner
}

// handleDoctorReport GET /api/v1/doctor — runs the doctor runner (or
// returns the cached Report if it's < doctorReportCacheTTL old).
// Cache is per-Server; multi-tab dashboards share one report.
func (s *Server) handleDoctorReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")

	now := time.Now()
	s.doctorCache.mu.Lock()
	stale := now.Sub(s.doctorCache.reportAt) >= doctorReportCacheTTL
	s.doctorCache.mu.Unlock()

	if stale {
		runner := s.doctorRunner()
		report, err := runner.RunOnce(r.Context(), doctor.RunOptions{})
		if err != nil {
			// Runner-level failure (typically ctx cancelled before
			// any detector ran). Surface so the operator knows the
			// page is broken rather than silently empty.
			http.Error(w, "doctor: "+err.Error(), http.StatusInternalServerError)
			return
		}
		s.doctorCache.mu.Lock()
		s.doctorCache.report = report
		s.doctorCache.reportAt = now
		s.doctorCache.mu.Unlock()
	}

	s.doctorCache.mu.Lock()
	report := s.doctorCache.report
	s.doctorCache.mu.Unlock()
	s.writeJSON(r, w, report)
}
