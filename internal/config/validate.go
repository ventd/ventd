package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ventd/ventd/internal/hal/msiec"
	"github.com/ventd/ventd/internal/hwmon"
)

// validateHwmonPWMPath restricts hwmon pwm_path values to real sysfs
// locations so an authenticated user cannot direct calibration writes at
// an arbitrary file. The path must:
//   - live under /sys/class/hwmon/ or /sys/devices/
//   - clean to a location that still has that prefix (blocks .. traversal)
//   - have a basename starting with "pwm" (optionally with digits)
//   - if present on disk, be a regular file
func validateHwmonPWMPath(p string) error {
	return validateHwmonSysfsPath(p, "pwm", "")
}

func validateHwmonSysfsPath(p, basePrefix, baseSuffix string) error {
	const (
		rootClass  = "/sys/class/hwmon/"
		rootDevice = "/sys/devices/"
	)
	// When VENTD_HWMON_ROOT redirects hwmon to a synthetic tree (dev/test,
	// tools/hwmonsim), enumerated channels carry paths rooted under that
	// override, not /sys — so a config that drives the simulated fans must be
	// allowed to reference them. Widen the allow-list to the override root ONLY
	// when it is active; production (env unset) keeps the strict /sys-only rule,
	// preserving the "can't point calibration writes at an arbitrary file"
	// guarantee. Setting the override already requires service-level privilege
	// and is logged loudly, so it grants no capability an attacker lacks.
	allowedRoots := []string{rootClass, rootDevice}
	if hwmon.RootIsOverridden() {
		if root := strings.TrimRight(hwmon.EffectiveRoot(), "/"); root != "" {
			allowedRoots = append(allowedRoots, root+"/")
		}
	}
	hasAllowedRoot := func(s string) bool {
		for _, r := range allowedRoots {
			if strings.HasPrefix(s, r) {
				return true
			}
		}
		return false
	}
	if !hasAllowedRoot(p) {
		return fmt.Errorf("pwm_path %q must start with %s or %s", p, rootClass, rootDevice)
	}
	cleaned := filepath.Clean(p)
	if !hasAllowedRoot(cleaned) {
		return fmt.Errorf("pwm_path %q escapes sysfs after cleaning (got %q)", p, cleaned)
	}
	base := filepath.Base(cleaned)
	if !strings.HasPrefix(base, basePrefix) {
		return fmt.Errorf("pwm_path %q basename %q must start with %q", p, base, basePrefix)
	}
	if baseSuffix != "" && !strings.HasSuffix(base, baseSuffix) {
		return fmt.Errorf("pwm_path %q basename %q must end with %q", p, base, baseSuffix)
	}
	// Stat is best-effort: if the file exists it must be regular.
	// Non-existent paths are allowed so configs survive transient hwmon
	// renumbering and ResolveHwmonPaths runs before controllers start.
	if fi, err := os.Lstat(cleaned); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			fi, err = os.Stat(cleaned)
			if err != nil {
				return fmt.Errorf("stat %q: %w", cleaned, err)
			}
		}
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("pwm_path %q is not a regular file", cleaned)
		}
	}
	return nil
}

func detectCycle(name string, curves map[string]CurveConfig, visiting, visited map[string]bool) error {
	if visited[name] {
		return nil
	}
	if visiting[name] {
		return fmt.Errorf("config: curve %q has a circular source reference", name)
	}
	visiting[name] = true
	for _, src := range curves[name].Sources {
		if err := detectCycle(src, curves, visiting, visited); err != nil {
			return err
		}
	}
	delete(visiting, name)
	visited[name] = true
	return nil
}

func validate(cfg *Config) error {
	if cfg.Version == 0 {
		cfg.Version = CurrentVersion
	}
	if cfg.Version != CurrentVersion {
		return fmt.Errorf("config: unsupported version %d (supported: %d)", cfg.Version, CurrentVersion)
	}
	if cfg.PollInterval.Duration <= 0 {
		cfg.PollInterval.Duration = DefaultPollInterval
	}
	if cfg.Web.Listen == "" {
		cfg.Web.Listen = "127.0.0.1:9999"
	}
	if cfg.Web.LoginFailThreshold <= 0 {
		cfg.Web.LoginFailThreshold = DefaultLoginFailThreshold
	}
	if cfg.Web.LoginLockoutCooldown.Duration <= 0 {
		cfg.Web.LoginLockoutCooldown.Duration = DefaultLoginLockoutCooldown
	}
	if cfg.Web.SessionTTL.Duration <= 0 {
		cfg.Web.SessionTTL.Duration = DefaultSessionTTL
	}
	if (cfg.Web.TLSCert == "") != (cfg.Web.TLSKey == "") {
		return fmt.Errorf("config: web.tls_cert and web.tls_key must both be set or both be empty")
	}
	for i, c := range cfg.Web.TrustProxy {
		if _, _, err := net.ParseCIDR(c); err != nil {
			return fmt.Errorf("config: web.trust_proxy[%d]: invalid CIDR %q: %w", i, c, err)
		}
	}

	sensors := make(map[string]struct{}, len(cfg.Sensors))
	for i, s := range cfg.Sensors {
		if s.Name == "" {
			return fmt.Errorf("config: sensors[%d]: name is required", i)
		}
		switch s.Type {
		case "hwmon":
			if s.Path == "" {
				return fmt.Errorf("config: sensor %q: path is required", s.Name)
			}
		case "nvidia":
			if s.Path == "" {
				return fmt.Errorf("config: sensor %q: path (GPU index) is required", s.Name)
			}
			idx, err := strconv.ParseUint(s.Path, 10, 32)
			if err != nil || idx > 255 {
				return fmt.Errorf("config: sensor %q: path must be a GPU index (0, 1, …), got %q", s.Name, s.Path)
			}
			switch s.Metric {
			case "", "temp", "util", "mem_util", "power", "clock_gpu", "clock_mem", "fan_pct":
			default:
				return fmt.Errorf("config: sensor %q: unknown metric %q", s.Name, s.Metric)
			}
		case "msiec":
			if s.Path == "" {
				return fmt.Errorf("config: sensor %q: path is required (relative to %s, e.g. cpu/realtime_temperature)", s.Name, msiec.DefaultSysfsRoot)
			}
			if err := msiec.ValidateSensorPath(s.Path); err != nil {
				return fmt.Errorf("config: sensor %q: %w", s.Name, err)
			}
		case "":
			return fmt.Errorf("config: sensor %q: type is required", s.Name)
		default:
			return fmt.Errorf("config: sensor %q: unknown type %q (want: hwmon, nvidia, msiec)", s.Name, s.Type)
		}
		sensors[s.Name] = struct{}{}
	}

	fans := make(map[string]Fan, len(cfg.Fans))
	for i, f := range cfg.Fans {
		if f.Name == "" {
			return fmt.Errorf("config: fans[%d]: name is required", i)
		}
		switch f.Type {
		case "hwmon":
			if f.PWMPath == "" {
				return fmt.Errorf("config: fan %q: pwm_path is required", f.Name)
			}
			if err := validateHwmonPWMPath(f.PWMPath); err != nil {
				return fmt.Errorf("config: fan %q: %w", f.Name, err)
			}
			if f.RPMPath != "" {
				if err := validateHwmonSysfsPath(f.RPMPath, "fan", "_input"); err != nil {
					return fmt.Errorf("config: fan %q: rpm_path: %w", f.Name, err)
				}
			}
		case "nvidia":
			if f.PWMPath == "" {
				return fmt.Errorf("config: fan %q: pwm_path (GPU index) is required", f.Name)
			}
			idx, err := strconv.ParseUint(f.PWMPath, 10, 32)
			if err != nil || idx > 255 {
				return fmt.Errorf("config: fan %q: pwm_path must be a GPU index (0, 1, …), got %q", f.Name, f.PWMPath)
			}
		case "":
			return fmt.Errorf("config: fan %q: type is required", f.Name)
		default:
			// Non-hwmon HAL backends (msiec, thinkpad, ipmi, nbfc,
			// crosec, asahi, pwmsys, legion, corsair — 9 of ventd's
			// 11 HAL backends). Accept any non-empty type with a non-
			// empty pwm_path; the runtime backend lookup in
			// internal/controller.New + the web fan-read path in
			// internal/web/server.go both consult the HAL registry by
			// this type name and fail clearly if it's not registered.
			// Without this branch, the wizard could never produce a
			// loadable config for any non-hwmon hardware — the bug
			// HudsonPH hit on MSI Thin GF63 12UDX (#1116 / #1154) and
			// the same trap would fire for every ThinkPad, every
			// Chromebook, every server with IPMI fans, every Apple
			// Silicon Mac, every Lenovo Legion, etc.
			if f.PWMPath == "" {
				return fmt.Errorf("config: fan %q: pwm_path is required for type %q", f.Name, f.Type)
			}
		}
		if f.MaxPWM < f.MinPWM {
			return fmt.Errorf("config: fan %q: max_pwm (%d) must be >= min_pwm (%d)", f.Name, f.MaxPWM, f.MinPWM)
		}
		if f.IsPump {
			// hwmon-safety rule 6: pumps must never stop. Require an
			// explicit pump_minimum rather than relying on the implicit
			// MinPumpPWM fallback — a missing or zero pump_minimum is
			// almost always a config typo, and silently substituting a
			// default risks running a pump below what the operator
			// believes is safe for their hardware.
			if f.PumpMinimum == 0 {
				return fmt.Errorf("config: fan %q: is_pump=true but pump_minimum is 0 — set pump_minimum to at least %d (the MinPumpPWM default) so the pump has an explicit floor", f.Name, MinPumpPWM)
			}
			floor := uint8(MinPumpPWM)
			if f.PumpMinimum > floor {
				floor = f.PumpMinimum
			}
			// Pumps also cover the min_pwm==0 case below: the floor is
			// at least MinPumpPWM (>0), so any pump with min_pwm==0 is
			// rejected here before reaching the allow_stop gate. That
			// is by design — pumps must never stop, even with
			// allow_stop: true.
			if f.MinPWM < floor {
				return fmt.Errorf("config: fan %q: is_pump=true but min_pwm (%d) is below pump floor (%d)", f.Name, f.MinPWM, floor)
			}
		}
		// hwmon-safety rule 1: never write PWM=0 unless min_pwm=0 AND
		// allow_stop=true. Reject the unsafe combination at config load
		// so a hand-edited typo can't silently spin a fan down to zero
		// at runtime. The controller enforces the same gate (see
		// internal/controller/controller.go), but catching it here fails
		// fast before the daemon starts controlling hardware.
		if f.MinPWM == 0 && !f.AllowStop {
			return fmt.Errorf("config: fan %q: min_pwm is 0 but allow_stop is false — add allow_stop: true if you really want the fan to stop, or raise min_pwm above 0", f.Name)
		}
		fans[f.Name] = f
	}

	for name := range fans {
		if _, clash := sensors[name]; clash {
			return fmt.Errorf("config: %q is used as both a sensor name and a fan name; names must be unique across sensors and fans so history keyspace stays unambiguous", name)
		}
	}

	curves := make(map[string]struct{}, len(cfg.Curves))
	for i, c := range cfg.Curves {
		if c.Name == "" {
			return fmt.Errorf("config: curves[%d]: name is required", i)
		}
		if c.Hysteresis < 0 {
			return fmt.Errorf("config: curve %q: hysteresis (%.1f) must be >= 0", c.Name, c.Hysteresis)
		}
		if c.Smoothing.Duration < 0 {
			return fmt.Errorf("config: curve %q: smoothing (%s) must be >= 0", c.Name, c.Smoothing.Duration)
		}
		switch c.Type {
		case "linear":
			if _, ok := sensors[c.Sensor]; !ok {
				return fmt.Errorf("config: curve %q: sensor %q is not defined", c.Name, c.Sensor)
			}
			if c.MinTemp >= c.MaxTemp {
				return fmt.Errorf("config: curve %q: min_temp (%.1f) must be < max_temp (%.1f)", c.Name, c.MinTemp, c.MaxTemp)
			}
			if c.MaxPWM < c.MinPWM {
				return fmt.Errorf("config: curve %q: max_pwm (%d) must be >= min_pwm (%d)", c.Name, c.MaxPWM, c.MinPWM)
			}
		case "points":
			if _, ok := sensors[c.Sensor]; !ok {
				return fmt.Errorf("config: curve %q: sensor %q is not defined", c.Name, c.Sensor)
			}
			if len(c.Points) < 2 {
				return fmt.Errorf("config: curve %q: points curve requires at least 2 anchors, got %d", c.Name, len(c.Points))
			}
			// Sort in place so the runtime can assume ascending Temp on
			// every tick; the sorted slice is what validate() hands back
			// via the slice header and what Save() marshals out to YAML.
			sort.SliceStable(cfg.Curves[i].Points, func(a, b int) bool {
				return cfg.Curves[i].Points[a].Temp < cfg.Curves[i].Points[b].Temp
			})
			// After sort, reject duplicates — equal temps collapse the
			// interpolation denominator to zero and are almost certainly
			// a typo rather than an intentional vertical step.
			for k := 1; k < len(cfg.Curves[i].Points); k++ {
				if cfg.Curves[i].Points[k].Temp <= cfg.Curves[i].Points[k-1].Temp {
					return fmt.Errorf("config: curve %q: points must have strictly increasing temps, got %.2f <= %.2f at index %d",
						c.Name, cfg.Curves[i].Points[k].Temp, cfg.Curves[i].Points[k-1].Temp, k)
				}
			}
		case "fixed":
			// Value defaults to 0; clamped by fan min_pwm at runtime
		case "pi":
			if c.Sensor == "" {
				return fmt.Errorf("config: curve %q: sensor is required for pi curve", c.Name)
			}
			if _, ok := sensors[c.Sensor]; !ok {
				return fmt.Errorf("config: curve %q: sensor %q is not defined", c.Name, c.Sensor)
			}
			if c.Setpoint == nil {
				return fmt.Errorf("config: curve %q: setpoint is required for pi curve", c.Name)
			}
			if *c.Setpoint < 0 || *c.Setpoint > 120 {
				return fmt.Errorf("config: curve %q: setpoint %.1f out of range [0, 120] °C", c.Name, *c.Setpoint)
			}
			if c.Kp == nil {
				return fmt.Errorf("config: curve %q: kp is required for pi curve", c.Name)
			}
			if *c.Kp <= 0 || *c.Kp > 100 {
				return fmt.Errorf("config: curve %q: kp %.4g out of range (0, 100]", c.Name, *c.Kp)
			}
			if c.Ki == nil {
				return fmt.Errorf("config: curve %q: ki is required for pi curve", c.Name)
			}
			if *c.Ki < 0 || *c.Ki > 100 {
				return fmt.Errorf("config: curve %q: ki %.4g out of range [0, 100]", c.Name, *c.Ki)
			}
			if c.FeedForward == nil {
				return fmt.Errorf("config: curve %q: feed_forward is required for pi curve", c.Name)
			}
			if c.IntegralClamp == nil {
				return fmt.Errorf("config: curve %q: integral_clamp is required for pi curve", c.Name)
			}
			if *c.IntegralClamp <= 0 || *c.IntegralClamp > 255 {
				return fmt.Errorf("config: curve %q: integral_clamp %.4g out of range (0, 255]", c.Name, *c.IntegralClamp)
			}
		case "mix":
			if c.Function == "" {
				return fmt.Errorf("config: curve %q: function is required (want: max, min, average)", c.Name)
			}
			switch c.Function {
			case "max", "min", "average":
			default:
				return fmt.Errorf("config: curve %q: unknown function %q (want: max, min, average)", c.Name, c.Function)
			}
			if len(c.Sources) < 2 {
				return fmt.Errorf("config: curve %q: mix requires at least 2 sources, got %d", c.Name, len(c.Sources))
			}
		default:
			return fmt.Errorf("config: curve %q: unknown type %q", c.Name, c.Type)
		}
		curves[c.Name] = struct{}{}
	}

	curveMap := make(map[string]CurveConfig, len(cfg.Curves))
	for _, c := range cfg.Curves {
		curveMap[c.Name] = c
	}
	for _, c := range cfg.Curves {
		if c.Type != "mix" {
			continue
		}
		for _, src := range c.Sources {
			if _, ok := curves[src]; !ok {
				return fmt.Errorf("config: curve %q: source %q is not defined", c.Name, src)
			}
		}
		if err := detectCycle(c.Name, curveMap, make(map[string]bool), make(map[string]bool)); err != nil {
			return err
		}
	}

	for i, ctrl := range cfg.Controls {
		if _, ok := fans[ctrl.Fan]; !ok {
			return fmt.Errorf("config: controls[%d]: fan %q is not defined", i, ctrl.Fan)
		}
		if _, ok := curves[ctrl.Curve]; !ok {
			return fmt.Errorf("config: controls[%d]: curve %q is not defined", i, ctrl.Curve)
		}
	}

	// Schedule grammar is validated strictly: a typo in a schedule string
	// would otherwise leave the profile silently manual-only, and the
	// operator would be debugging "why didn't my silent mode fire at 10pm"
	// for hours. Reject at load so the mistake surfaces immediately. The
	// parser is pure and cheap — no I/O, no allocations beyond the
	// returned Schedule.
	for name, p := range cfg.Profiles {
		if p.Schedule == "" {
			continue
		}
		if _, err := ParseSchedule(p.Schedule); err != nil {
			return fmt.Errorf("config: profile %q: %w", name, err)
		}
	}

	// v0.5.9 smart-mode preset + setpoints validation. Unknown
	// presets are NOT a load-time error (the daemon's wiring layer
	// emits a single startup WARN and falls back to "balanced");
	// out-of-range setpoints ARE rejected because a 200°C target
	// would silently lock the controller into perma-saturation.
	if _, ok := smartPresets[cfg.Smart.Preset]; !ok {
		// One-line note in the error chain: the *load* succeeds but
		// downstream parsers (controller.PresetFromString) treat it
		// as fall-through. We log a hint so a typo surfaces here too.
		// Not fatal — same forgiveness as Web.LoginFailThreshold==0
		// auto-defaulting above.
		// (Pre-emptive: if you hit this and want strict mode, flip
		// to a `return fmt.Errorf(...)` here.)
		// Continue loading; the wiring layer warns at runtime.
		_ = cfg.Smart.Preset
	}
	for ch, sp := range cfg.Smart.Setpoints {
		if sp < 10 || sp > 100 {
			return fmt.Errorf("config: smart.setpoints[%q]: %v°C out of physical range [10, 100]", ch, sp)
		}
	}
	if v := cfg.Smart.PresetWeightVector; v != nil {
		for i, w := range v {
			if w < 0 || w > 1 {
				return fmt.Errorf("config: smart.preset_weight_vector[%d]: %v out of [0, 1]", i, w)
			}
		}
	}
	if t := cfg.Smart.DBATarget; t != nil {
		if *t < 10 || *t > 80 {
			return fmt.Errorf("config: smart.dba_target: %v dBA out of plausible range [10, 80]", *t)
		}
	}

	return nil
}
