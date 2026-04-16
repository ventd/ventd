package setup

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/ventd/ventd/internal/config"
)

// TestBuildConfig_MinimalHwmonCPUOnly pins the default-shape output when
// setup found exactly one CPU sensor and one hwmon fan: a cpu_temp sensor,
// a linear cpu_curve bounded by the calibrated StopPWM, and a Control
// binding the fan to that curve.
func TestBuildConfig_MinimalHwmonCPUOnly(t *testing.T) {
	fans := []fanDiscovery{{
		name: "CPU Fan", fanType: "hwmon", chipName: "nct6687",
		pwmPath:  "/sys/class/hwmon/hwmon3/pwm1",
		rpmPath:  "/sys/class/hwmon/hwmon3/fan1_input",
		startPWM: 80, stopPWM: 40, maxRPM: 1800,
	}}
	profile := &HWProfile{}
	cfg := buildConfig(fans, "coretemp Package",
		"/sys/class/hwmon/hwmon0/temp1_input", 55.0,
		false, "", 0, profile)

	if cfg.Version != config.CurrentVersion {
		t.Errorf("Version = %d, want %d", cfg.Version, config.CurrentVersion)
	}
	if cfg.PollInterval.Seconds() != 2 {
		t.Errorf("PollInterval = %v, want 2s", cfg.PollInterval)
	}
	if cfg.Web.Listen != "0.0.0.0:9999" {
		t.Errorf("Web.Listen = %q, want 0.0.0.0:9999", cfg.Web.Listen)
	}

	if len(cfg.Sensors) != 1 {
		t.Fatalf("Sensors len = %d, want 1", len(cfg.Sensors))
	}
	if cfg.Sensors[0].Name != "cpu_temp" || cfg.Sensors[0].Type != "hwmon" {
		t.Errorf("cpu sensor = %+v", cfg.Sensors[0])
	}
	// ChipName is populated from <dirname>/name file, which won't exist in
	// this test fixture (we're passing a literal path string). That's fine —
	// the chipNameOf path is covered by its own dedicated test.

	if len(cfg.Curves) != 1 || cfg.Curves[0].Name != "cpu_curve" {
		t.Fatalf("Curves = %+v, want single cpu_curve", cfg.Curves)
	}
	if cfg.Curves[0].Type != "linear" || cfg.Curves[0].Sensor != "cpu_temp" {
		t.Errorf("cpu_curve type/sensor = %q/%q", cfg.Curves[0].Type, cfg.Curves[0].Sensor)
	}
	// MinPWM floor should be StopPWM (40), not StartPWM (80).
	if cfg.Curves[0].MinPWM != 40 {
		t.Errorf("cpu_curve MinPWM = %d, want 40 (StopPWM)", cfg.Curves[0].MinPWM)
	}
	// MaxTemp defaults to 85 when profile has no thermal info.
	if cfg.Curves[0].MaxTemp != 85 {
		t.Errorf("cpu_curve MaxTemp = %v, want 85 (default)", cfg.Curves[0].MaxTemp)
	}

	if len(cfg.Fans) != 1 || cfg.Fans[0].Name != "CPU Fan" {
		t.Fatalf("Fans = %+v", cfg.Fans)
	}
	if cfg.Fans[0].ChipName != "nct6687" {
		t.Errorf("Fan ChipName = %q, want nct6687 (from fanDiscovery.chipName)", cfg.Fans[0].ChipName)
	}
	if cfg.Fans[0].MinPWM != 40 {
		t.Errorf("Fan MinPWM = %d, want 40 (StopPWM)", cfg.Fans[0].MinPWM)
	}
	if cfg.Fans[0].MaxPWM != 255 {
		t.Errorf("Fan MaxPWM = %d, want 255", cfg.Fans[0].MaxPWM)
	}

	if len(cfg.Controls) != 1 || cfg.Controls[0].Fan != "CPU Fan" || cfg.Controls[0].Curve != "cpu_curve" {
		t.Errorf("Controls = %+v", cfg.Controls)
	}

	// A curve-design note must be appended for CPU — the operator-facing
	// explanation is part of the contract of the hardware profile.
	if !hasNoteContaining(profile.CurveNotes, "cpu_curve") {
		t.Errorf("profile.CurveNotes missing cpu_curve entry: %v", profile.CurveNotes)
	}
}

// TestBuildConfig_NoCPUSensorUsesFixedCurve pins the fallback shape: when
// setup couldn't find a CPU temperature sensor but did find hwmon fans,
// emit a fixed-speed curve at ~60% PWM rather than refusing to proceed.
func TestBuildConfig_NoCPUSensorUsesFixedCurve(t *testing.T) {
	fans := []fanDiscovery{{
		name: "sys fan1", fanType: "hwmon", chipName: "nct6687",
		pwmPath: "/sys/class/hwmon/hwmon3/pwm1", startPWM: 80, stopPWM: 40,
	}}
	cfg := buildConfig(fans, "", "", 0, false, "", 0, &HWProfile{})

	// Exactly one sensor-less "fixed" cpu_curve at value=153 (~60%).
	if len(cfg.Sensors) != 0 {
		t.Errorf("Sensors = %v, want 0 (no CPU sensor discovered)", cfg.Sensors)
	}
	if len(cfg.Curves) != 1 {
		t.Fatalf("Curves = %v, want exactly 1", cfg.Curves)
	}
	c := cfg.Curves[0]
	if c.Name != "cpu_curve" || c.Type != "fixed" || c.Value != 153 {
		t.Errorf("fallback curve = %+v, want cpu_curve/fixed/153", c)
	}
}

// TestBuildConfig_CPUThermalLimitDrivesMaxTemp pins the safety-derived
// MaxTemp: with a CPU thermal limit of 100°C in the profile, the curve
// max becomes 85°C (100 − 15 margin). The operator note must cite both
// numbers.
func TestBuildConfig_CPUThermalLimitDrivesMaxTemp(t *testing.T) {
	fans := []fanDiscovery{{
		name: "CPU Fan", fanType: "hwmon", chipName: "nct6687",
		pwmPath: "/sys/class/hwmon/hwmon3/pwm1", stopPWM: 30,
	}}
	profile := &HWProfile{CPUThermalC: 100}
	cfg := buildConfig(fans, "coretemp", "/sys/class/hwmon/hwmon0/temp1_input",
		60.0, false, "", 0, profile)

	if len(cfg.Curves) != 1 {
		t.Fatalf("Curves = %+v, want 1", cfg.Curves)
	}
	if cfg.Curves[0].MaxTemp != 85 {
		t.Errorf("MaxTemp = %v, want 85 (100 − 15 margin)", cfg.Curves[0].MaxTemp)
	}
	if !hasNoteContaining(profile.CurveNotes, "TjMax") {
		t.Errorf("profile.CurveNotes missing TjMax attribution: %v", profile.CurveNotes)
	}
}

// TestBuildConfig_CPUThermalLimitClampedToCeiling pins the upper clamp:
// a TjMax of 110°C would suggest a curve max of 95°C, which the clamp
// caps at 95. A higher suggestion must not slip through.
func TestBuildConfig_CPUThermalLimitClampedToCeiling(t *testing.T) {
	fans := []fanDiscovery{{
		name: "CPU Fan", fanType: "hwmon", chipName: "nct6687",
		pwmPath: "/sys/class/hwmon/hwmon3/pwm1", stopPWM: 30,
	}}
	profile := &HWProfile{CPUThermalC: 130} // → 130-15=115, clamp to 95
	cfg := buildConfig(fans, "coretemp", "/sys/class/hwmon/hwmon0/temp1_input",
		60.0, false, "", 0, profile)

	if cfg.Curves[0].MaxTemp != 95 {
		t.Errorf("MaxTemp = %v, want 95 (clamped upper bound)", cfg.Curves[0].MaxTemp)
	}
}

// TestBuildConfig_GPUNVMLGetsSlowdownMargin pins the GPU NVML path: when
// the GPU temp sensor is NVML-based (empty gpuTempPath), the margin is
// 5°C (slowdown is already the throttle point), not 15°C.
func TestBuildConfig_GPUNVMLGetsSlowdownMargin(t *testing.T) {
	gpuFans := []fanDiscovery{{
		name: "gpu0", fanType: "nvidia",
		pwmPath: "0", stopPWM: 30,
	}}
	profile := &HWProfile{GPUThermalC: 90} // slowdown 90°C → curve max 85°C
	cfg := buildConfig(gpuFans, "", "", 0, true, "", 65.0, profile)

	var gpuCurve *config.CurveConfig
	for i, c := range cfg.Curves {
		if c.Name == "gpu_curve" {
			gpuCurve = &cfg.Curves[i]
		}
	}
	if gpuCurve == nil {
		t.Fatalf("no gpu_curve emitted: %+v", cfg.Curves)
	}
	if gpuCurve.MaxTemp != 85 {
		t.Errorf("gpu_curve MaxTemp = %v, want 85 (90 − 5 NVML margin)", gpuCurve.MaxTemp)
	}
	if !hasNoteContaining(profile.CurveNotes, "slowdown") {
		t.Errorf("profile.CurveNotes missing slowdown attribution: %v", profile.CurveNotes)
	}

	// NVML sensor uses type=nvidia with path="0" and metric="temp".
	var nvSensor *config.Sensor
	for i, s := range cfg.Sensors {
		if s.Type == "nvidia" {
			nvSensor = &cfg.Sensors[i]
		}
	}
	if nvSensor == nil {
		t.Fatalf("no nvidia sensor emitted")
	}
	if nvSensor.Name != "gpu_temp" || nvSensor.Path != "0" || nvSensor.Metric != "temp" {
		t.Errorf("nvidia sensor = %+v", nvSensor)
	}
}

// TestBuildConfig_GPUAMDGetsCritMargin pins the AMD GPU path: when the
// gpuTempPath is a real hwmon path, margin is 15°C (crit) and the sensor
// is emitted as type="hwmon".
func TestBuildConfig_GPUAMDGetsCritMargin(t *testing.T) {
	gpuFans := []fanDiscovery{{
		name: "gpu0", fanType: "hwmon", chipName: "amdgpu",
		pwmPath: "/sys/class/hwmon/hwmon4/pwm1",
		rpmPath: "/sys/class/hwmon/hwmon4/fan1_input",
		stopPWM: 30,
	}}
	profile := &HWProfile{GPUThermalC: 105} // 105-15=90
	cfg := buildConfig(gpuFans, "", "", 0, true,
		"/sys/class/hwmon/hwmon4/temp2_input", 65.0, profile)

	var gpuCurve *config.CurveConfig
	for i, c := range cfg.Curves {
		if c.Name == "gpu_curve" {
			gpuCurve = &cfg.Curves[i]
		}
	}
	if gpuCurve == nil {
		t.Fatalf("no gpu_curve: %+v", cfg.Curves)
	}
	if gpuCurve.MaxTemp != 90 {
		t.Errorf("AMD GPU MaxTemp = %v, want 90 (105 − 15 crit margin)", gpuCurve.MaxTemp)
	}
	if !hasNoteContaining(profile.CurveNotes, "crit") {
		t.Errorf("profile.CurveNotes missing crit attribution: %v", profile.CurveNotes)
	}

	// AMD GPU sensor is emitted as type=hwmon.
	var gpuSensor *config.Sensor
	for i, s := range cfg.Sensors {
		if s.Name == "gpu_temp" {
			gpuSensor = &cfg.Sensors[i]
		}
	}
	if gpuSensor == nil || gpuSensor.Type != "hwmon" {
		t.Errorf("AMD GPU sensor = %+v, want type=hwmon", gpuSensor)
	}
}

// TestBuildConfig_CaseCurveEmittedWhenBothSensorsAndCaseFan pins the
// combined-curve shape: case fans route to case_curve only when both CPU
// and GPU sensors are present AND at least one hwmon fan was found.
func TestBuildConfig_CaseCurveEmittedWhenBothSensorsAndCaseFan(t *testing.T) {
	fans := []fanDiscovery{
		{
			name: "cpu fan", fanType: "hwmon", chipName: "nct6687",
			pwmPath: "/sys/class/hwmon/hwmon3/pwm1", stopPWM: 40,
		},
		{
			name: "sys fan1", fanType: "hwmon", chipName: "nct6687",
			pwmPath: "/sys/class/hwmon/hwmon3/pwm2", stopPWM: 30,
		},
		// A nvidia GPU fan is required for gpu_curve (and therefore
		// case_curve) to be emitted.
		{
			name: "gpu0", fanType: "nvidia",
			pwmPath: "0", stopPWM: 60,
		},
	}
	cfg := buildConfig(fans, "coretemp",
		"/sys/class/hwmon/hwmon0/temp1_input", 55.0,
		true, "", 60.0, &HWProfile{})

	var caseCurve *config.CurveConfig
	for i, c := range cfg.Curves {
		if c.Name == "case_curve" {
			caseCurve = &cfg.Curves[i]
		}
	}
	if caseCurve == nil {
		t.Fatalf("case_curve missing; curves=%+v", cfg.Curves)
	}
	if caseCurve.Type != "mix" || caseCurve.Function != "max" {
		t.Errorf("case_curve = %+v, want mix/max", caseCurve)
	}
	if len(caseCurve.Sources) != 2 {
		t.Errorf("case_curve sources = %v, want [cpu_curve, gpu_curve]", caseCurve.Sources)
	}

	// The "sys fan1" control should bind to case_curve; "cpu fan" stays on cpu_curve.
	sysCurve, cpuCurve := findCurveForFan(cfg, "sys fan1"), findCurveForFan(cfg, "cpu fan")
	if sysCurve != "case_curve" {
		t.Errorf("sys fan1 bound to %q, want case_curve", sysCurve)
	}
	if cpuCurve != "cpu_curve" {
		t.Errorf("cpu fan bound to %q, want cpu_curve", cpuCurve)
	}
}

// TestBuildConfig_CaseCurveNotEmittedWhenGPUAbsent pins the other side of
// the case_curve contract: with no GPU sensor, case fans fall back to
// cpu_curve.
func TestBuildConfig_CaseCurveNotEmittedWhenGPUAbsent(t *testing.T) {
	fans := []fanDiscovery{{
		name: "sys fan", fanType: "hwmon", chipName: "nct6687",
		pwmPath: "/sys/class/hwmon/hwmon3/pwm2", stopPWM: 30,
	}}
	cfg := buildConfig(fans, "coretemp",
		"/sys/class/hwmon/hwmon0/temp1_input", 55.0,
		false, "", 0, &HWProfile{})

	for _, c := range cfg.Curves {
		if c.Name == "case_curve" {
			t.Errorf("case_curve emitted despite no GPU sensor: %+v", c)
		}
	}
	if got := findCurveForFan(cfg, "sys fan"); got != "cpu_curve" {
		t.Errorf("sys fan bound to %q, want cpu_curve fallback", got)
	}
}

// TestBuildConfig_CaseCurveNotEmittedWhenGPUSensorButNoGPUFans reproduces
// the NVML-permission-fail rig scenario (phoenix-MS-7D25, 2026-04-15):
// the GPU temperature sensor is detected via NVML, but calibration could
// not write to any GPU fan (NVML set-fan-speed returned Insufficient
// Permissions), so no nvidia fan made it into the fan list. Previously
// buildConfig emitted case_curve referencing gpu_curve, but gpu_curve was
// never emitted (it's gated on len(gpuFans) > 0), so config.Parse rejected
// the Apply with `source "gpu_curve" is not defined`. case_curve must
// now be gated on len(gpuFans) > 0 to match gpu_curve's emission.
func TestBuildConfig_CaseCurveNotEmittedWhenGPUSensorButNoGPUFans(t *testing.T) {
	fans := []fanDiscovery{
		{
			name: "cpu fan", fanType: "hwmon", chipName: "nct6687",
			pwmPath: "/sys/class/hwmon/hwmon3/pwm1", stopPWM: 40,
		},
		{
			name: "sys fan1", fanType: "hwmon", chipName: "nct6687",
			pwmPath: "/sys/class/hwmon/hwmon3/pwm2", stopPWM: 30,
		},
		// No nvidia/amdgpu fan — NVML set-fan-speed failed during calibration.
	}
	// hasGPUTemp=true: the GPU sensor was detected via NVML.
	cfg := buildConfig(fans, "coretemp",
		"/sys/class/hwmon/hwmon0/temp1_input", 55.0,
		true, "", 62.0, &HWProfile{})

	for _, c := range cfg.Curves {
		if c.Name == "case_curve" {
			t.Errorf("case_curve emitted without any GPU fan: %+v", c)
		}
		if c.Name == "gpu_curve" {
			t.Errorf("gpu_curve emitted without any GPU fan: %+v", c)
		}
	}
	if got := findCurveForFan(cfg, "sys fan1"); got != "cpu_curve" {
		t.Errorf("sys fan1 bound to %q, want cpu_curve fallback", got)
	}

	// Critical contract: the generated config must pass config.Parse.
	// Previously it failed with: curve "case_curve": source "gpu_curve" is not defined.
	buf, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := config.Parse(buf); err != nil {
		t.Fatalf("generated config fails its own validation: %v", err)
	}
}

// TestBuildConfig_PumpEmitsPumpCurveAndFloor pins the pump-fan contract:
// - IsPump is propagated on config.Fan
// - PumpMinimum is at least MinPumpPWM (20)
// - PumpMinimum wins over MinPWM; the Fan's MinPWM is raised to match
// - pump_curve is emitted at 204 (~80%)
// - the pump Control binds to pump_curve, not cpu/gpu_curve
func TestBuildConfig_PumpEmitsPumpCurveAndFloor(t *testing.T) {
	fans := []fanDiscovery{{
		name: "Pump", fanType: "hwmon", chipName: "nct6687",
		pwmPath:  "/sys/class/hwmon/hwmon3/pwm4",
		startPWM: 15, stopPWM: 10, // lower than MinPumpPWM=20
		isPump: true,
	}}
	cfg := buildConfig(fans, "coretemp",
		"/sys/class/hwmon/hwmon0/temp1_input", 55.0,
		false, "", 0, &HWProfile{})

	var pumpCurve *config.CurveConfig
	for i, c := range cfg.Curves {
		if c.Name == "pump_curve" {
			pumpCurve = &cfg.Curves[i]
		}
	}
	if pumpCurve == nil {
		t.Fatalf("pump_curve missing: %+v", cfg.Curves)
	}
	if pumpCurve.Type != "fixed" || pumpCurve.Value != 204 {
		t.Errorf("pump_curve = %+v, want fixed/204", pumpCurve)
	}

	if len(cfg.Fans) != 1 {
		t.Fatalf("Fans = %+v", cfg.Fans)
	}
	fan := cfg.Fans[0]
	if !fan.IsPump {
		t.Errorf("IsPump = false, want true")
	}
	if fan.PumpMinimum < uint8(config.MinPumpPWM) {
		t.Errorf("PumpMinimum = %d, want >= %d", fan.PumpMinimum, config.MinPumpPWM)
	}
	// Fan MinPWM must be bumped to the pump floor, not the raw stopPWM=10.
	if fan.MinPWM < uint8(config.MinPumpPWM) {
		t.Errorf("Fan MinPWM = %d, want >= %d (pump floor)", fan.MinPWM, config.MinPumpPWM)
	}

	if got := findCurveForFan(cfg, "Pump"); got != "pump_curve" {
		t.Errorf("Pump Control bound to %q, want pump_curve", got)
	}
}

// TestBuildConfig_PumpFloorNeverBelowMinPumpPWM sweeps boundary conditions
// around MinPumpPWM to pin the invariant that a pump's fan.MinPWM is never
// below config.MinPumpPWM, regardless of what calibration measured.
func TestBuildConfig_PumpFloorNeverBelowMinPumpPWM(t *testing.T) {
	cases := []struct {
		name           string
		stopPWM        uint8
		wantMinAtLeast uint8
	}{
		{"stop_below_floor", 5, uint8(config.MinPumpPWM)},
		{"stop_at_floor", uint8(config.MinPumpPWM), uint8(config.MinPumpPWM)},
		{"stop_above_floor", 40, 40}, // higher calibrated value wins
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fans := []fanDiscovery{{
				name: "Pump", fanType: "hwmon", chipName: "nct6687",
				pwmPath: "/sys/class/hwmon/hwmon3/pwm4",
				stopPWM: tc.stopPWM, isPump: true,
			}}
			cfg := buildConfig(fans, "coretemp",
				"/sys/class/hwmon/hwmon0/temp1_input", 55.0,
				false, "", 0, &HWProfile{})
			if got := cfg.Fans[0].MinPWM; got < tc.wantMinAtLeast {
				t.Errorf("Fan.MinPWM = %d, want >= %d", got, tc.wantMinAtLeast)
			}
			if got := cfg.Fans[0].PumpMinimum; got < uint8(config.MinPumpPWM) {
				t.Errorf("Fan.PumpMinimum = %d, want >= %d", got, config.MinPumpPWM)
			}
		})
	}
}

// TestBuildConfig_UncalibratedFanGetsSafeFloor pins the "all zero" fallback
// contract: a fan with stopPWM=0 and startPWM=0 (should not happen in real
// operation, but guardrails matter) gets MinPWM=20.
func TestBuildConfig_UncalibratedFanGetsSafeFloor(t *testing.T) {
	fans := []fanDiscovery{{
		name: "sys fan", fanType: "hwmon", chipName: "nct6687",
		pwmPath: "/sys/class/hwmon/hwmon3/pwm2",
		// no startPWM / stopPWM
	}}
	cfg := buildConfig(fans, "coretemp",
		"/sys/class/hwmon/hwmon0/temp1_input", 55.0,
		false, "", 0, &HWProfile{})
	if cfg.Fans[0].MinPWM != 20 {
		t.Errorf("Fan.MinPWM = %d, want 20 (safe default for uncalibrated fan)", cfg.Fans[0].MinPWM)
	}
}

// TestBuildConfig_RPMTargetFanPreservesControlKind pins the invariant that
// a fan discovered as control_kind=rpm_target emits a config.Fan with the
// same ControlKind. The controller dispatches on this at tick time.
func TestBuildConfig_RPMTargetFanPreservesControlKind(t *testing.T) {
	fans := []fanDiscovery{{
		name: "gpu fan", fanType: "hwmon", chipName: "amdgpu",
		pwmPath:     "/sys/class/hwmon/hwmon4/fan1_target",
		controlKind: "rpm_target",
	}}
	cfg := buildConfig(fans, "", "", 0, true,
		"/sys/class/hwmon/hwmon4/temp2_input", 60.0, &HWProfile{})
	if len(cfg.Fans) != 1 {
		t.Fatalf("Fans = %+v", cfg.Fans)
	}
	if cfg.Fans[0].ControlKind != "rpm_target" {
		t.Errorf("ControlKind = %q, want rpm_target", cfg.Fans[0].ControlKind)
	}
}

// TestBuildConfig_MixedHwmonAndNvidiaFans covers the common dual-GPU-fan
// host case: one CPU/case fan set on hwmon and one GPU fan via NVML. Both
// curves and a control per fan must be emitted; only hwmon fans get
// ChipName + HwmonDevice enrichment.
func TestBuildConfig_MixedHwmonAndNvidiaFans(t *testing.T) {
	fans := []fanDiscovery{
		{
			name: "cpu fan", fanType: "hwmon", chipName: "nct6687",
			pwmPath: "/sys/class/hwmon/hwmon3/pwm1", stopPWM: 40,
		},
		{
			name: "gpu0", fanType: "nvidia",
			pwmPath: "0", stopPWM: 60,
		},
	}
	cfg := buildConfig(fans, "coretemp",
		"/sys/class/hwmon/hwmon0/temp1_input", 55.0,
		true, "", 62.0, &HWProfile{})

	if len(cfg.Fans) != 2 {
		t.Fatalf("Fans len = %d, want 2; got %+v", len(cfg.Fans), cfg.Fans)
	}
	if len(cfg.Controls) != 2 {
		t.Fatalf("Controls len = %d, want 2; got %+v", len(cfg.Controls), cfg.Controls)
	}
	if findCurveForFan(cfg, "gpu0") != "gpu_curve" {
		t.Errorf("nvidia fan not bound to gpu_curve: %+v", cfg.Controls)
	}
	if findCurveForFan(cfg, "cpu fan") != "cpu_curve" {
		t.Errorf("cpu fan not bound to cpu_curve: %+v", cfg.Controls)
	}
	// Nvidia fan must NOT have a ChipName — it isn't an hwmon device.
	for _, f := range cfg.Fans {
		if f.Type == "nvidia" && f.ChipName != "" {
			t.Errorf("nvidia fan has ChipName=%q, want empty", f.ChipName)
		}
	}
}

// TestBuildConfig_MinPWMFloorAcrossFans pins the interaction between
// buildConfig's per-fan floor (used by the fan itself) and the shared curve
// floor (the min across all hwmon fans). The curve floor is the minimum
// across all non-zero stop/start PWM values.
func TestBuildConfig_MinPWMFloorAcrossFans(t *testing.T) {
	fans := []fanDiscovery{
		{name: "f1", fanType: "hwmon", chipName: "nct6687",
			pwmPath: "/sys/class/hwmon/hwmon3/pwm1", stopPWM: 60},
		{name: "f2", fanType: "hwmon", chipName: "nct6687",
			pwmPath: "/sys/class/hwmon/hwmon3/pwm2", stopPWM: 30},
		{name: "f3", fanType: "hwmon", chipName: "nct6687",
			pwmPath: "/sys/class/hwmon/hwmon3/pwm3", stopPWM: 45},
	}
	cfg := buildConfig(fans, "coretemp",
		"/sys/class/hwmon/hwmon0/temp1_input", 55.0,
		false, "", 0, &HWProfile{})
	var cpuCurve *config.CurveConfig
	for i, c := range cfg.Curves {
		if c.Name == "cpu_curve" {
			cpuCurve = &cfg.Curves[i]
		}
	}
	if cpuCurve == nil {
		t.Fatalf("cpu_curve missing")
	}
	// Curve MinPWM picks the lowest stopPWM across all hwmon fans.
	if cpuCurve.MinPWM != 30 {
		t.Errorf("cpu_curve MinPWM = %d, want 30 (min across fans)", cpuCurve.MinPWM)
	}
	// Individual fans retain their own stop floors.
	for _, f := range cfg.Fans {
		want := map[string]uint8{"f1": 60, "f2": 30, "f3": 45}[f.Name]
		if f.MinPWM != want {
			t.Errorf("fan %q MinPWM = %d, want %d", f.Name, f.MinPWM, want)
		}
	}
}

// TestBuildConfig_SetsCPUCurveMin covers the pinned "silent below 40°C"
// design constant. cpu_curve.MinTemp must be 40 regardless of current
// temperature.
func TestBuildConfig_SetsCPUCurveMin(t *testing.T) {
	fans := []fanDiscovery{{
		name: "cpu fan", fanType: "hwmon", chipName: "nct6687",
		pwmPath: "/sys/class/hwmon/hwmon3/pwm1", stopPWM: 40,
	}}
	cfg := buildConfig(fans, "coretemp",
		"/sys/class/hwmon/hwmon0/temp1_input", 90.0, // very hot right now
		false, "", 0, &HWProfile{})
	if cfg.Curves[0].MinTemp != 40 {
		t.Errorf("cpu_curve MinTemp = %v, want 40 (fixed silent-floor)", cfg.Curves[0].MinTemp)
	}
}

// TestBuildConfig_GPUMinTempFloor covers the pinned "silent below 50°C"
// GPU design constant.
func TestBuildConfig_GPUMinTempFloor(t *testing.T) {
	gpuFans := []fanDiscovery{{
		name: "gpu0", fanType: "nvidia", pwmPath: "0", stopPWM: 40,
	}}
	cfg := buildConfig(gpuFans, "", "", 0, true, "", 80.0, &HWProfile{})

	var gpuCurve *config.CurveConfig
	for i, c := range cfg.Curves {
		if c.Name == "gpu_curve" {
			gpuCurve = &cfg.Curves[i]
		}
	}
	if gpuCurve == nil {
		t.Fatalf("gpu_curve missing")
	}
	if gpuCurve.MinTemp != 50 {
		t.Errorf("gpu_curve MinTemp = %v, want 50 (fixed silent-floor)", gpuCurve.MinTemp)
	}
}

// TestBuildConfig_GPUFanWithoutGPUSensorGetsFixed pins the sensor-less
// GPU-fan fallback: a system with NVML fans but a missing GPU temp read
// (hasGPUTemp=false) must still emit a fixed gpu_curve at 153.
func TestBuildConfig_GPUFanWithoutGPUSensorGetsFixed(t *testing.T) {
	gpuFans := []fanDiscovery{{
		name: "gpu0", fanType: "nvidia", pwmPath: "0", stopPWM: 40,
	}}
	cfg := buildConfig(gpuFans, "", "", 0, false, "", 0, &HWProfile{})

	var gpuCurve *config.CurveConfig
	for i, c := range cfg.Curves {
		if c.Name == "gpu_curve" {
			gpuCurve = &cfg.Curves[i]
		}
	}
	if gpuCurve == nil {
		t.Fatalf("gpu_curve missing for GPU fan without sensor")
	}
	if gpuCurve.Type != "fixed" || gpuCurve.Value != 153 {
		t.Errorf("gpu_curve = %+v, want fixed/153 fallback", gpuCurve)
	}
}

// helpers -----------------------------------------------------------

func hasNoteContaining(notes []string, s string) bool {
	for _, n := range notes {
		if strings.Contains(n, s) {
			return true
		}
	}
	return false
}

func findCurveForFan(cfg *config.Config, fanName string) string {
	for _, c := range cfg.Controls {
		if c.Fan == fanName {
			return c.Curve
		}
	}
	return ""
}
