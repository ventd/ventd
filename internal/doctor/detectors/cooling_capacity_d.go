package detectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/ventd/ventd/internal/cooling"
	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/recovery"
)

// CoolingCapacityArtifactLoader supplies the raw calibrate artifact
// bytes + CPU TDP (watts) to the cooling-capacity detector. The
// production loader reads /var/lib/ventd/setup/state.json and
// /sys/class/powercap/intel-rapl/intel-rapl:0/constraint_0_power_limit_uw;
// tests inject stubs. (#1285)
type CoolingCapacityArtifactLoader interface {
	// ReadCalibrateArtifact returns the calibrate phase artifact
	// bytes (CalibrateArtifact JSON). nil bytes = pre-calibrate
	// host (detector emits zero facts).
	ReadCalibrateArtifact() ([]byte, error)
	// ReadCPUTDPW returns the CPU package TDP in watts. 0 = unknown
	// (AMD without amd_energy, virtualised host); detector stays
	// silent.
	ReadCPUTDPW() int
}

// FileCoolingCapacityLoader is the production loader. The Path
// fields default to the orchestrator's state file +
// /sys/class/powercap when empty.
type FileCoolingCapacityLoader struct {
	StatePath string
	RAPLPaths []string
}

// ReadCalibrateArtifact reads the calibrate-phase JSON artifact from
// the orchestrator's checkpoint file. Mirrors
// FileCalibrationArtifactLoader's decoder. (#1285)
func (f FileCoolingCapacityLoader) ReadCalibrateArtifact() ([]byte, error) {
	path := f.StatePath
	if path == "" {
		path = "/var/lib/ventd/setup/state.json"
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read checkpoints: %w", err)
	}
	var envelope struct {
		Outcomes map[string]struct {
			Status   string          `json:"status"`
			Artifact json.RawMessage `json:"artifact"`
		} `json:"outcomes"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode envelope: %w", err)
	}
	cal, ok := envelope.Outcomes["calibrate"]
	if !ok || len(cal.Artifact) == 0 {
		return nil, nil
	}
	return cal.Artifact, nil
}

// ReadCPUTDPW returns the CPU TDP in watts from Intel RAPL. (#1285)
func (f FileCoolingCapacityLoader) ReadCPUTDPW() int {
	paths := f.RAPLPaths
	if len(paths) == 0 {
		paths = []string{
			"/sys/class/powercap/intel-rapl/intel-rapl:0/constraint_0_power_limit_uw",
			"/sys/class/powercap/intel-rapl:0/constraint_0_power_limit_uw",
		}
	}
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var uw int64
		if _, err := fmt.Sscanf(string(raw), "%d", &uw); err == nil && uw > 0 {
			return int(uw / 1_000_000)
		}
	}
	return 0
}

// calibrateForCoolingShape decodes the calibrate artifact down to
// the {pwm_path, max_rpm} pairs the capacity model needs. (#1285)
type calibrateForCoolingShape struct {
	Results []calibrateForCoolingResult `json:"results"`
}

type calibrateForCoolingResult struct {
	PWMPath string `json:"pwm_path"`
	MaxRPM  int    `json:"max_rpm"`
}

// CoolingCapacityDetector fires a Warning when the estimated
// chassis cooling capacity is below the CPU TDP (×1.25 margin).
// "your chassis tops out at 80 W of dissipation; your CPU draws
// 125 W under load — expect thermal throttling." (#1285)
type CoolingCapacityDetector struct {
	Loader CoolingCapacityArtifactLoader
}

// NewCoolingCapacityDetector constructs a detector. nil loader is a
// no-op (zero facts).
func NewCoolingCapacityDetector(loader CoolingCapacityArtifactLoader) *CoolingCapacityDetector {
	return &CoolingCapacityDetector{Loader: loader}
}

// Name returns the stable detector ID.
func (d *CoolingCapacityDetector) Name() string { return "cooling_capacity" }

// Probe reads the calibrate artifact + RAPL TDP and emits one
// Warning Fact when the estimated capacity is below CPU TDP × 1.25.
// Stays silent on pre-calibrate / AMD-without-RAPL / virtualised
// hosts. (#1285)
func (d *CoolingCapacityDetector) Probe(ctx context.Context, _ doctor.Deps) ([]doctor.Fact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.Loader == nil {
		return nil, nil
	}
	raw, err := d.Loader.ReadCalibrateArtifact()
	if err != nil {
		return nil, fmt.Errorf("read calibrate artifact: %w", err)
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var art calibrateForCoolingShape
	if err := json.Unmarshal(raw, &art); err != nil {
		return nil, fmt.Errorf("decode calibrate artifact: %w", err)
	}
	tdp := d.Loader.ReadCPUTDPW()

	// Treat every fan as a default 120 mm case axial since the
	// hwdb fan-profile catalog isn't yet wired into the doctor
	// path (lands as a follow-up under #1283). The capacity gate
	// is still meaningful: a chassis with three fans at 1200 RPM
	// and a 250 W CPU TDP will fire regardless of per-fan class.
	fans := make([]cooling.FanInput, 0, len(art.Results))
	for _, r := range art.Results {
		if r.MaxRPM <= 0 {
			continue
		}
		fans = append(fans, cooling.FanInput{
			Class:      "case_120_140",
			DiameterMM: 120,
			MaxRPM:     r.MaxRPM,
		})
	}
	capW := cooling.ChassisCapacityW(fans)
	adequate, hasSignal := cooling.CapacityAdequate(capW, float64(tdp))
	if !hasSignal || adequate {
		return nil, nil
	}
	return []doctor.Fact{{
		Detector:   d.Name(),
		Severity:   doctor.SeverityWarning,
		Class:      recovery.ClassUnknown,
		Title:      "Chassis cooling capacity may be inadequate",
		Detail: fmt.Sprintf(
			"Estimated chassis cooling capacity is %.0f W at 70 °C ΔT; CPU TDP is %d W. "+
				"Sustained loads may cause thermal throttling. Consider adding case fans "+
				"or upgrading existing fans to higher airflow ratings.",
			capW, tdp,
		),
		EntityHash: doctor.HashEntity("cooling_capacity_tight"),
	}}, nil
}
