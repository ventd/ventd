package main

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/cooling"
	"github.com/ventd/ventd/internal/web"
)

// defaultSetupStatePath is the production location of the
// orchestrator's checkpoint file (in sync with
// orchestrator.DefaultStateDir). Tests override via the dependency
// arguments to newCoolingResolver. (#1285)
const defaultSetupStatePath = "/var/lib/ventd/setup/state.json"

// defaultRAPLConstraintPaths is the candidate set of sysfs paths the
// resolver tries for the Intel RAPL package power limit. Mirrors the
// orchestrator's readRAPLTDPW selection. (#1285)
var defaultRAPLConstraintPaths = []string{
	"/sys/class/powercap/intel-rapl/intel-rapl:0/constraint_0_power_limit_uw",
	"/sys/class/powercap/intel-rapl:0/constraint_0_power_limit_uw",
}

// newCoolingResolver returns a closure that pulls the live cooling-
// capacity-W estimate for /api/v1/smart/status. The closure is
// stateless and safe to call per-request — the underlying sysfs +
// state file reads are page-cache-resident after the first call.
//
// cfgPtr supplies the live fan list (operator names, types, paths).
// statePath / raplPaths are filesystem injection seams for tests;
// production passes the defaults. (#1285)
func newCoolingResolver(
	cfgPtr *atomic.Pointer[config.Config],
	statePath string,
	raplPaths []string,
) func() web.CoolingStatus {
	if statePath == "" {
		statePath = defaultSetupStatePath
	}
	if len(raplPaths) == 0 {
		raplPaths = defaultRAPLConstraintPaths
	}
	return func() web.CoolingStatus {
		live := cfgPtr.Load()
		tdpW := readRAPLTDPWFromPaths(raplPaths)
		maxRPMByPath := readCalibrateMaxRPM(statePath)

		var fans []cooling.FanInput
		if live != nil {
			for _, f := range live.Fans {
				maxRPM := maxRPMByPath[f.PWMPath]
				if maxRPM <= 0 {
					continue
				}
				fans = append(fans, cooling.FanInput{
					Class:      string(defaultFanClassFor(f)),
					DiameterMM: 120,
					MaxRPM:     maxRPM,
				})
			}
		}
		capW := cooling.ChassisCapacityW(fans)
		adequate, hasSignal := cooling.CapacityAdequate(capW, float64(tdpW))
		return web.CoolingStatus{
			CapacityW: capW,
			CPUTDPW:   tdpW,
			Adequate:  adequate,
			HasSignal: hasSignal,
		}
	}
}

// readRAPLTDPWFromPaths returns the first plausible RAPL package
// power limit (W) found across the candidate sysfs paths. 0 on
// failure / no signal. (#1285)
func readRAPLTDPWFromPaths(paths []string) int {
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		uw, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
		if err == nil && uw > 0 {
			return int(uw / 1_000_000)
		}
	}
	return 0
}

// readCalibrateMaxRPM decodes the orchestrator's state.json and
// returns a map[pwm_path]MaxRPM extracted from the CalibratePhase's
// artifact. Missing file / malformed envelope / no calibrate phase
// → empty map (the resolver then surfaces has_signal=false). (#1285)
func readCalibrateMaxRPM(statePath string) map[string]int {
	raw, err := os.ReadFile(statePath)
	if err != nil {
		return nil
	}
	var envelope struct {
		Outcomes map[string]struct {
			Status   string          `json:"status"`
			Artifact json.RawMessage `json:"artifact"`
		} `json:"outcomes"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil
	}
	cal, ok := envelope.Outcomes["calibrate"]
	if !ok || len(cal.Artifact) == 0 {
		return nil
	}
	var art struct {
		Results []struct {
			PWMPath string `json:"pwm_path"`
			MaxRPM  int    `json:"max_rpm"`
		} `json:"results"`
	}
	if err := json.Unmarshal(cal.Artifact, &art); err != nil {
		return nil
	}
	out := make(map[string]int, len(art.Results))
	for _, r := range art.Results {
		if r.MaxRPM > 0 {
			out[r.PWMPath] = r.MaxRPM
		}
	}
	return out
}
