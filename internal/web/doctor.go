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
	"github.com/ventd/ventd/internal/recovery"
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
		// Surfaces per-fan NonMonotonicCurve flags written by the
		// wizard's CalibratePhase — vendor-EC clamping (Dell SMM,
		// ASUS Q-Fan, HP Omen) typically. Reads the orchestrator
		// state.json directly; absent file = no signal (wizard not
		// yet run). #1274.
		detectors.NewCalibrationCurveQualityDetector(detectors.FileCalibrationArtifactLoader{}),
		// #1285: chassis cooling-capacity-W estimator. Reads the
		// same calibrate artifact + RAPL TDP and warns when the
		// estimated capacity falls below CPU TDP × 1.25.
		detectors.NewCoolingCapacityDetector(detectors.FileCoolingCapacityLoader{}),
		// R11: surface the w_pred_system gate — whether smart-mode
		// predictive control is engaged and, when it isn't, why. The
		// closure reads the same atomic snapshot the blend hook uses;
		// a nil gate (monitor-only) reports has=false → silent.
		detectors.NewWPredGateDetector(func() (bool, string, string, bool) {
			if s.gate == nil {
				return false, "", "", false
			}
			snap := s.gate.Read()
			if snap == nil {
				return false, "", "", false
			}
			return snap.Open, string(snap.Reason), snap.Detail, true
		}),
	}
	s.doctorCache.runner = doctor.NewRunner(det, nil, nil, nil)
	return s.doctorCache.runner
}

// handleDoctorReport GET /api/v1/doctor — runs the doctor runner (or
// returns the cached Report if it's < doctorReportCacheTTL old).
// Cache is per-Server; multi-tab dashboards share one report.
func (s *Server) handleDoctorReport(w http.ResponseWriter, r *http.Request) {
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
			s.writeJSONError(w, http.StatusInternalServerError, "doctor: "+err.Error())
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
	s.writeJSON(r, w, doctorReportWithRemediation(report))
}

// doctorFactView is a doctor.Fact plus the operator-actionable remediation
// entries for its failure class. The embedded Fact promotes every existing
// field + json tag, so the wire shape is a superset of the bare Fact — adding
// only `remediation` — and CLI/diff consumers that unmarshal into doctor.Fact
// ignore the extra field.
type doctorFactView struct {
	doctor.Fact
	Remediation []recovery.Remediation `json:"remediation,omitempty"`
}

// doctorReportView mirrors doctor.Report but carries enriched facts. Built
// per-request from the cached report (never mutates the cache).
type doctorReportView struct {
	Schema         string                 `json:"schema_version"`
	Generated      time.Time              `json:"generated"`
	Facts          []doctorFactView       `json:"facts"`
	DetectorErrors []doctor.DetectorError `json:"detector_errors,omitempty"`
	Severity       doctor.Severity        `json:"severity"`
}

// doctorReportWithRemediation attaches each fact's per-class remediation
// (recovery.RemediationFor — the same catalogue the calibration recovery cards
// use) so the Doctor page can render "Apply fix" / "Learn more" affordances,
// not just describe the problem. Findings whose class has no catalogue entry
// (ClassUnknown) get only the generic diagnostic-bundle action.
func doctorReportWithRemediation(report doctor.Report) doctorReportView {
	view := doctorReportView{
		Schema:         report.Schema,
		Generated:      report.Generated,
		DetectorErrors: report.DetectorErrors,
		Severity:       report.Severity,
		Facts:          make([]doctorFactView, len(report.Facts)),
	}
	for i, f := range report.Facts {
		view.Facts[i] = doctorFactView{Fact: f, Remediation: recovery.RemediationFor(f.Class)}
	}
	return view
}
