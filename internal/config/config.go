package config

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

// hwmonRootFS is the fs.FS used by Load to re-anchor hwmon paths via
// ResolveHwmonPaths. Defaults to the live /sys/class/hwmon class
// directory; tests override via SetHwmonRootFS so the resolver can be
// driven from a fstest.MapFS fixture without touching real sysfs.
var hwmonRootFS fs.FS = os.DirFS("/sys/class/hwmon")

// SetHwmonRootFS overrides the fs.FS that Load passes to
// ResolveHwmonPaths. Tests use it to drive the resolver from a fixture.
// Pass nil to restore the default. Returns the previous root so tests
// can restore it via t.Cleanup.
func SetHwmonRootFS(fsys fs.FS) fs.FS {
	prev := hwmonRootFS
	if fsys == nil {
		hwmonRootFS = os.DirFS("/sys/class/hwmon")
	} else {
		hwmonRootFS = fsys
	}
	return prev
}

const (
	DefaultPollInterval = 2 * time.Second
	MinPumpPWM          = 20
	CurrentVersion      = 1
)

// ExperimentalConfig holds the config-file opt-in flags for experimental features.
// Each field maps 1:1 to an experimental.Flags field. Absent keys default to false.
type ExperimentalConfig struct {
	AMDOverdrive    bool `yaml:"amd_overdrive,omitempty" json:"amd_overdrive,omitempty"`
	NVIDIACoolbits  bool `yaml:"nvidia_coolbits,omitempty" json:"nvidia_coolbits,omitempty"`
	ILO4Unlocked    bool `yaml:"ilo4_unlocked,omitempty" json:"ilo4_unlocked,omitempty"`
	IDRAC9LegacyRaw bool `yaml:"idrac9_legacy_raw,omitempty" json:"idrac9_legacy_raw,omitempty"`
}

// EnvelopeClassThresholds overrides the per-class thermal abort and headroom
// thresholds used by the Envelope C/D probe for a specific system class.
// Zero values leave the built-in defaults from envelope.LookupThresholds unchanged.
type EnvelopeClassThresholds struct {
	DTDtAbortCPerSec     float64 `yaml:"dtdt_abort_c_per_sec,omitempty" json:"dtdt_abort_c_per_sec,omitempty"`
	TAbsOffsetBelowTjmax float64 `yaml:"tabs_offset_below_tjmax,omitempty" json:"tabs_offset_below_tjmax,omitempty"`
	AmbientHeadroomMin   float64 `yaml:"ambient_headroom_min,omitempty" json:"ambient_headroom_min,omitempty"`
}

// EnvelopeClassesConfig groups optional per-class threshold overrides.
type EnvelopeClassesConfig struct {
	HEDTAir    *EnvelopeClassThresholds `yaml:"hedt_air,omitempty" json:"hedt_air,omitempty"`
	HEDTAio    *EnvelopeClassThresholds `yaml:"hedt_aio,omitempty" json:"hedt_aio,omitempty"`
	MidDesktop *EnvelopeClassThresholds `yaml:"mid_desktop,omitempty" json:"mid_desktop,omitempty"`
	Server     *EnvelopeClassThresholds `yaml:"server,omitempty" json:"server,omitempty"`
	Laptop     *EnvelopeClassThresholds `yaml:"laptop,omitempty" json:"laptop,omitempty"`
	MiniPC     *EnvelopeClassThresholds `yaml:"mini_pc,omitempty" json:"mini_pc,omitempty"`
	NASHDD     *EnvelopeClassThresholds `yaml:"nas_hdd,omitempty" json:"nas_hdd,omitempty"`
}

// EnvelopeConfig holds config-file settings for the Envelope C/D probe.
type EnvelopeConfig struct {
	// AllowServerProbe permits Envelope C on server-class hardware with a BMC present.
	// Equivalent to the --allow-server-probe CLI flag; config-file opt-in for headless setups.
	AllowServerProbe bool                  `yaml:"allow_server_probe,omitempty" json:"allow_server_probe,omitempty"`
	Classes          EnvelopeClassesConfig `yaml:"classes,omitempty" json:"classes,omitempty"`
}

// IdleConfig tunes the idle.StartupGate used before Envelope C begins.
type IdleConfig struct {
	// TickInterval is the polling cadence of the idle predicate (default: 10s).
	TickInterval Duration `yaml:"tick_interval,omitempty" json:"tick_interval,omitempty"`
	// Durability is how long the idle predicate must stay true before the gate
	// opens (default: 300s / 5 minutes).
	Durability Duration `yaml:"durability,omitempty" json:"durability,omitempty"`
	// AllowOverride skips the storage-maintenance (RAID rebuild) hard-refusal.
	// Battery and container refusals are never overridable.
	AllowOverride bool `yaml:"allow_override,omitempty" json:"allow_override,omitempty"`
}

type Config struct {
	Version       int                `yaml:"version" json:"version"`
	PollInterval  Duration           `yaml:"poll_interval" json:"poll_interval"`
	Web           Web                `yaml:"web" json:"web"`
	Hwmon         Hwmon              `yaml:"hwmon,omitempty" json:"hwmon,omitempty"`
	HWDB          HWDB               `yaml:"hwdb,omitempty" json:"hwdb,omitempty"`
	Sensors       []Sensor           `yaml:"sensors" json:"sensors"`
	Fans          []Fan              `yaml:"fans" json:"fans"`
	Curves        []CurveConfig      `yaml:"curves" json:"curves"`
	Controls      []Control          `yaml:"controls" json:"controls"`
	Profiles      map[string]Profile `yaml:"profiles,omitempty" json:"profiles,omitempty"`
	ActiveProfile string             `yaml:"active_profile,omitempty" json:"active_profile,omitempty"`
	Experimental  ExperimentalConfig `yaml:"experimental,omitempty" json:"experimental,omitempty"`
	Envelope      EnvelopeConfig     `yaml:"envelope,omitempty" json:"envelope,omitempty"`
	Idle          IdleConfig         `yaml:"idle,omitempty" json:"idle,omitempty"`
	// NeverActivelyProbeAfterInstall disables v0.5.5 opportunistic active
	// probing system-wide. Default false (probing enabled in auto mode).
	// Per spec-smart-mode §6.4 / §7.4 and spec-12 amendment §3.5
	// (RULE-OPP-PROBE-08, RULE-UI-SMART-07).
	NeverActivelyProbeAfterInstall bool `yaml:"never_actively_probe_after_install,omitempty" json:"never_actively_probe_after_install,omitempty"`
	// SignatureLearningDisabled disables v0.5.6 workload signature
	// learning system-wide. Default false (learning enabled in auto
	// mode). Per spec-smart-mode §6.6 / §7.4 and spec-12 amendment
	// §3.5 (RULE-SIG-LIB-08, RULE-UI-SMART-07).
	SignatureLearningDisabled bool `yaml:"signature_learning_disabled,omitempty" json:"signature_learning_disabled,omitempty"`
	// SmartMarginalBenefitDisabled disables v0.5.8 Layer-C
	// per-(channel, signature) marginal-benefit learning system-wide.
	// Default false (learning enabled in auto mode). Per
	// spec-v0_5_8-marginal-benefit.md §3.6.
	SmartMarginalBenefitDisabled bool `yaml:"smart_marginal_benefit_disabled,omitempty" json:"smart_marginal_benefit_disabled,omitempty"`
	// AcousticOptimisation chooses the quietest PWM that still cools
	// effectively (#789). When true (the daemon's default — see
	// applyDefaults below), the controller refuses ramp-ups whose
	// predicted ΔT contribution falls below the v0.5.8 marginal-benefit
	// saturation threshold (R11 §0 SaturationDeltaT = 2.0 °C). Tracks
	// the v0.5.8 marginal estimator's "Path A predicted saturation"
	// signal verbatim — toggling this off makes the controller follow
	// the curve straight without the saturation gate.
	//
	// Default-on. Operators who want maximum-cooling-regardless-of-
	// noise behaviour set acoustic_optimisation: false in config.yaml
	// or toggle the Settings → Smart mode switch.
	AcousticOptimisation *bool `yaml:"acoustic_optimisation,omitempty" json:"acoustic_optimisation,omitempty"`

	// Smart groups v0.5.9 confidence-controller knobs introduced by
	// PR-A.4. The legacy flat *Disabled fields above stay where they
	// are for backward compatibility with deployed config.yaml files.
	Smart SmartConfig `yaml:"smart,omitempty" json:"smart,omitempty"`
}

// SmartConfig holds the v0.5.9 smart-mode operator surface. The
// `Preset` enum string drives both the IMC-PI controller's
// aggressiveness (Silent: λ=2τ, Balanced: λ=τ, Performance: λ=τ/2)
// and the acoustic cost factor (3× / 1× / 0.2× of the base 0.01
// °C-equiv per PWM unit). Setpoints map controllable channels to
// their target temperatures in °C — the IMC-PI's reference signal.
//
// Per spec-v0_5_9-confidence-controller.md §3.1 / §3.2 / §4.
type SmartConfig struct {
	// Preset is one of "silent" | "balanced" | "performance".
	// Empty / unrecognised values fall back to "balanced" with a
	// single startup-time WARN log.
	Preset string `yaml:"preset,omitempty" json:"preset,omitempty"`

	// PresetWeightVector is the reserved 4-tuple
	// {w_thermal, w_acoustic, w_power, w_responsiveness}. v0.5.9
	// only consumes w_acoustic (mapped from `Preset`); v0.7+ R18
	// fills the other three and adds R19 battery-state overlays.
	// Operator surface stays stable across versions — present for
	// schema forward-compat.
	PresetWeightVector *[4]float64 `yaml:"preset_weight_vector,omitempty" json:"preset_weight_vector,omitempty"`

	// Setpoints maps channel ID (PWM sysfs path or fan name) to
	// target temperature in °C. The IMC-PI controller's reference
	// signal. Missing entries fall back to a class-default
	// computed by the wiring layer (PR-B). Values outside
	// [10, 100] °C are rejected at config load.
	Setpoints map[string]float64 `yaml:"setpoints,omitempty" json:"setpoints,omitempty"`
}

// Closed set of preset names. Empty string is allowed (treated as
// "balanced" for default-config files).
var smartPresets = map[string]struct{}{
	"":            {},
	"silent":      {},
	"balanced":    {},
	"performance": {},
}

// SmartPreset returns the canonical preset name with empty/unknown
// values normalised to "balanced". A second return reports whether
// the input was a recognised value — false for unknown strings, true
// for empty / known values. Wiring layer (PR-B) emits a startup WARN
// when the second return is false.
func (s SmartConfig) SmartPreset() (name string, ok bool) {
	switch s.Preset {
	case "":
		return "balanced", true
	case "silent", "balanced", "performance":
		return s.Preset, true
	default:
		return "balanced", false
	}
}

// AcousticOptimisationEnabled returns true when the operator wants
// quietest-that-still-cools behaviour. Defaults to true when the field
// is unset (the v0.5.8.1 default, per #789). A pointer-bool field is
// used so an operator can explicitly set false in YAML and have the
// daemon honour that intent — a plain bool would be indistinguishable
// from "unset (default true)" once round-tripped through YAML.
func (c *Config) AcousticOptimisationEnabled() bool {
	if c == nil || c.AcousticOptimisation == nil {
		return true
	}
	return *c.AcousticOptimisation
}

// Profile groups a named set of fan→curve bindings so an operator can
// switch the whole dashboard between Silent / Balanced / Performance
// (or custom sets) without editing each Control row. The bindings map
// keys by Fan.Name and values by CurveConfig.Name; missing keys leave
// that fan's existing Control untouched when the profile is applied.
//
// Profile shape is intentionally a strict subset of Control — the
// `manual_pwm` override lives on the Control itself, not on the
// profile, because "force a fixed duty" is a per-fan ad-hoc
// intervention, not a preset the operator wants to switch back to.
//
// The optional Schedule string lets the daemon auto-switch profiles on
// a time-of-day / day-of-week cadence — see ParseSchedule for grammar.
// Profiles with an empty Schedule are eligible for manual selection
// only; they are the implicit "default fallback" when no scheduled
// profile matches the current local time.
//
// Zero value (empty map) is safe: v0.2.x configs omit the profiles
// block entirely and the omitempty tags keep round-trip clean.
type Profile struct {
	Bindings map[string]string `yaml:"bindings" json:"bindings"`
	Schedule string            `yaml:"schedule,omitempty" json:"schedule,omitempty"`
}

// HWDB groups knobs for the hardware fingerprint database. All fields are
// optional; zero values preserve existing behaviour so configs without an
// hwdb: block load unchanged.
type HWDB struct {
	// AllowRemote opts in to CLI-triggered remote refresh of profiles.yaml
	// from ventd/hardware-profiles. Default false: no network calls at daemon
	// startup or at runtime — refresh is CLI-only.
	AllowRemote bool `yaml:"allow_remote,omitempty" json:"allow_remote,omitempty"`
}

// Hwmon groups runtime-tunable knobs for the hwmon watcher. All fields are
// optional; zero values preserve pre-v0.3 behaviour so existing on-disk
// configs load unchanged and round-trip without gaining a new hwmon: key.
type Hwmon struct {
	// DynamicRebind opts in to the action=added rebind path (#95/#98
	// Option A). When true, the daemon re-execs on a topology change
	// that adds a configured hwmon chip so ResolveHwmonPaths can bind
	// the now-present device. Default false: a gap-free rollout
	// preserves v0.2.x semantics and gives operators an escape hatch
	// if the rebind destabilises their host.
	DynamicRebind bool `yaml:"dynamic_rebind,omitempty" json:"dynamic_rebind,omitempty"`
}

type Web struct {
	Listen string `yaml:"listen" json:"listen"`
	// PasswordHash is the bcrypt-hashed admin password. Kept as a YAML field
	// for migration: on first startup after an upgrade the daemon reads this
	// value and moves it to auth.json, then re-saves the config without it.
	// It is intentionally excluded from JSON marshaling so that GET /api/config
	// never exposes the hash to web clients, and PUT /api/config can never
	// inadvertently overwrite it.
	PasswordHash string   `yaml:"password_hash,omitempty" json:"-"`
	TLSCert      string   `yaml:"tls_cert,omitempty" json:"tls_cert,omitempty"`
	TLSKey       string   `yaml:"tls_key,omitempty" json:"tls_key,omitempty"`
	SessionTTL   Duration `yaml:"session_ttl,omitempty" json:"session_ttl,omitempty"`
	// SecureCookies forces the Secure flag on the session cookie. When nil
	// (default), the flag is set automatically iff TLS is configured
	// (tls_cert/tls_key present). Set to true explicitly when fronted by a
	// TLS-terminating reverse proxy; set to false only when testing over
	// plain HTTP on localhost — leaving session tokens unprotected on LAN.
	SecureCookies *bool `yaml:"secure_cookies,omitempty" json:"secure_cookies,omitempty"`

	// LoginFailThreshold is the number of consecutive failed logins from
	// the same peer IP that triggers a cooldown lockout. Zero → default
	// (see DefaultLoginFailThreshold). Tune up on shared homelabs where a
	// fat-finger admin shouldn't be locked out after 5 tries.
	LoginFailThreshold int `yaml:"login_fail_threshold,omitempty" json:"login_fail_threshold,omitempty"`
	// LoginLockoutCooldown is how long a locked-out peer must wait before
	// trying again. Zero → default (15m). Short enough to forgive human
	// error, long enough to make online guessing unproductive.
	LoginLockoutCooldown Duration `yaml:"login_lockout_cooldown,omitempty" json:"login_lockout_cooldown,omitempty"`

	// TrustProxy lists CIDRs whose requests are allowed to set
	// X-Forwarded-For. When the peer RemoteAddr matches one of these CIDRs
	// the rate limiter and logs use the leftmost *untrusted* XFF entry as
	// the client IP; otherwise XFF is ignored. Empty disables XFF entirely
	// (the default — safe on a LAN-bound daemon with no reverse proxy).
	TrustProxy []string `yaml:"trust_proxy,omitempty" json:"trust_proxy,omitempty"`
}

// UseSecureCookies reports whether the session cookie should set Secure.
// Explicit override wins; otherwise derive from TLS configuration.
func (w Web) UseSecureCookies() bool {
	if w.SecureCookies != nil {
		return *w.SecureCookies
	}
	return w.TLSCert != "" && w.TLSKey != ""
}

// TLSEnabled reports whether TLS serving is configured on this server.
func (w Web) TLSEnabled() bool {
	return w.TLSCert != "" && w.TLSKey != ""
}

// ListenIsLoopback reports whether Listen binds to a loopback address.
// "0.0.0.0:..." and "[::]:..." count as non-loopback because they accept
// connections on every interface, not just lo. An unresolved host defaults
// to non-loopback so the guard errs on the side of requiring transport
// security.
func (w Web) ListenIsLoopback() bool {
	host, _, err := net.SplitHostPort(w.Listen)
	if err != nil {
		return false
	}
	if host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// RequireTransportSecurity returns nil when the current Web config is safe
// to serve on Listen. It refuses to start if the daemon would accept
// plaintext sessions across the network: a non-loopback Listen with no TLS
// and no trusted proxy fronting it. The error is operator-facing: keep it
// multi-line and actionable.
//
// When TLS is configured, both files must exist and be regular files.
// Surfacing the misconfiguration here is deliberately louder than letting
// the HTTPS listener crash at bind time — the operator sees a clear startup
// error naming the missing path instead of a generic open() failure buried
// in the journal. No silent fallback to loopback: explicit tls_cert/tls_key
// = explicit operator responsibility.
func (w Web) RequireTransportSecurity() error {
	if w.TLSEnabled() {
		return w.verifyTLSFiles()
	}
	if w.ListenIsLoopback() {
		return nil
	}
	if len(w.TrustProxy) > 0 {
		return nil
	}
	// Multi-line operator-facing error is intentional here — this is
	// printed once at startup to stderr, not wrapped into another error.
	return fmt.Errorf( //nolint:staticcheck // ST1005: intentional multi-line operator message
		"web: refusing to serve plaintext HTTP on %q.\n"+
			"Session cookies and passwords would travel unencrypted on the LAN.\n"+
			"Pick one of:\n"+
			"  1. Set web.tls_cert / web.tls_key to enable TLS (a self-signed\n"+
			"     pair is generated automatically on first boot).\n"+
			"  2. Set web.trust_proxy to the CIDR of a TLS-terminating reverse\n"+
			"     proxy (e.g. [127.0.0.1/32] for a local nginx/Caddy).\n"+
			"  3. Bind web.listen to 127.0.0.1:9999 for loopback-only access.",
		w.Listen,
	)
}

// verifyTLSFiles checks that both configured TLS paths resolve to readable
// regular files. It is called from RequireTransportSecurity only when
// TLSEnabled() — auto-gen at first boot writes real files before the guard
// runs, so that flow is unaffected.
func (w Web) verifyTLSFiles() error {
	for _, f := range []struct{ field, path string }{
		{"web.tls_cert", w.TLSCert},
		{"web.tls_key", w.TLSKey},
	} {
		fi, err := os.Stat(f.path)
		if err != nil {
			return fmt.Errorf( //nolint:staticcheck // ST1005: operator-facing multi-line
				"web: %s configured at %q but not readable: %w.\n"+
					"Fix: create the file, or remove %s (and the matching key/cert) from\n"+
					"config and ventd will auto-generate a self-signed pair on first boot.",
				f.field, f.path, err, f.field,
			)
		}
		if fi.IsDir() {
			return fmt.Errorf( //nolint:staticcheck // ST1005: operator-facing multi-line
				"web: %s at %q is a directory, expected a PEM file",
				f.field, f.path,
			)
		}
		if !fi.Mode().IsRegular() {
			return fmt.Errorf( //nolint:staticcheck // ST1005: operator-facing multi-line
				"web: %s at %q is not a regular file",
				f.field, f.path,
			)
		}
	}
	return nil
}

type Sensor struct {
	Name        string `yaml:"name" json:"name"`
	Type        string `yaml:"type" json:"type"`
	Path        string `yaml:"path" json:"path"`
	Metric      string `yaml:"metric,omitempty" json:"metric,omitempty"`             // nvidia: temp(default), util, mem_util, power, clock_gpu, clock_mem, fan_pct
	HwmonDevice string `yaml:"hwmon_device,omitempty" json:"hwmon_device,omitempty"` // stable /sys/devices/... path for hwmon path resolution
	ChipName    string `yaml:"chip_name,omitempty" json:"chip_name,omitempty"`       // hwmonN/name attribute; used by ResolveHwmonPaths to re-anchor Path across renumbering
	Heuristic   bool   `yaml:"heuristic,omitempty" json:"heuristic,omitempty"`       // true when auto-assigned by heuristic binding; verify in Curves page
}

type Fan struct {
	Name        string `yaml:"name" json:"name"`
	Type        string `yaml:"type" json:"type"`
	PWMPath     string `yaml:"pwm_path" json:"pwm_path"`
	RPMPath     string `yaml:"rpm_path,omitempty" json:"rpm_path,omitempty"`         // override auto-derived fan*_input path
	HwmonDevice string `yaml:"hwmon_device,omitempty" json:"hwmon_device,omitempty"` // stable /sys/devices/... path for hwmon path resolution
	ChipName    string `yaml:"chip_name,omitempty" json:"chip_name,omitempty"`       // hwmonN/name attribute; used by ResolveHwmonPaths to re-anchor PWMPath/RPMPath across renumbering
	// ControlKind distinguishes how the PWMPath is written. Empty or "pwm"
	// means a standard pwm* duty-cycle file (0–255). "rpm_target" means a
	// fan*_target RPM setpoint file (pre-RDNA AMD amdgpu cards).
	ControlKind string `yaml:"control_kind,omitempty" json:"control_kind,omitempty"`
	MinPWM      uint8  `yaml:"min_pwm" json:"min_pwm"`
	MaxPWM      uint8  `yaml:"max_pwm" json:"max_pwm"`
	IsPump      bool   `yaml:"is_pump,omitempty" json:"is_pump,omitempty"`
	PumpMinimum uint8  `yaml:"pump_minimum,omitempty" json:"pump_minimum,omitempty"`
	// AllowStop is the explicit opt-in required to permit a PWM=0 write.
	// Rule: NEVER write PWM=0 unless MinPWM=0 AND AllowStop=true. Absent
	// (zero value) means the controller will refuse PWM=0 even if MinPWM=0.
	AllowStop bool `yaml:"allow_stop,omitempty" json:"allow_stop,omitempty"`
}

type CurveConfig struct {
	Name string `yaml:"name" json:"name"`
	Type string `yaml:"type" json:"type"`

	Sensor  string  `yaml:"sensor,omitempty" json:"sensor,omitempty"`
	MinTemp float64 `yaml:"min_temp,omitempty" json:"min_temp,omitempty"`
	MaxTemp float64 `yaml:"max_temp,omitempty" json:"max_temp,omitempty"`
	MinPWM  uint8   `yaml:"min_pwm,omitempty" json:"min_pwm,omitempty"`
	MaxPWM  uint8   `yaml:"max_pwm,omitempty" json:"max_pwm,omitempty"`
	// MinPWMPct / MaxPWMPct are the canonical persistence shape in
	// percent (0–100). Pointer-typed to distinguish "not set in YAML"
	// from "explicit zero". MigrateCurvePWMFields populates these from
	// legacy MinPWM / MaxPWM on Load and back-fills the raw fields for
	// the runtime. On Save the raw fields are suppressed via a local
	// copy so YAML carries only the `_pct` form. Tests that construct
	// CurveConfig directly with raw fields continue to work because
	// buildCurve reads MinPWM / MaxPWM.
	MinPWMPct *uint8 `yaml:"min_pwm_pct,omitempty" json:"min_pwm_pct,omitempty"`
	MaxPWMPct *uint8 `yaml:"max_pwm_pct,omitempty" json:"max_pwm_pct,omitempty"`

	// fixed fields
	Value    uint8  `yaml:"value,omitempty" json:"value,omitempty"`
	ValuePct *uint8 `yaml:"value_pct,omitempty" json:"value_pct,omitempty"`

	// mix fields
	Function string   `yaml:"function,omitempty" json:"function,omitempty"`
	Sources  []string `yaml:"sources,omitempty" json:"sources,omitempty"`

	// points fields — used by the "points" curve type. Each anchor
	// pins a (temperature, pwm) coordinate; the runtime interpolates
	// linearly between adjacent anchors and clamps outside the
	// first/last. validate() sorts by ascending Temp at load time, so
	// the runtime can assume a sorted slice.
	Points []CurvePoint `yaml:"points,omitempty" json:"points,omitempty"`

	// Hysteresis is a per-curve deadband (in sensor units, typically °C)
	// applied to ramp-DOWN transitions only. The controller suppresses a
	// new lower PWM write until the current temperature has dropped this
	// much below the temp at the last PWM write. Ramp-UP is never
	// delayed — high temperature is a safety-urgent signal. Zero (the
	// default) disables hysteresis. Applies only to curves with a single
	// sensor input (linear, points); ignored for mix/fixed.
	Hysteresis float64 `yaml:"hysteresis,omitempty" json:"hysteresis,omitempty"`

	// Smoothing is the EMA time-constant applied to raw sensor reads
	// before curve evaluation. The per-tick weight is
	// α = poll_interval / (smoothing + poll_interval). Zero (the
	// default) passes raw readings through unchanged. Intended to damp
	// noisy sensors that cause PWM jitter at steady state.
	Smoothing Duration `yaml:"smoothing,omitempty" json:"smoothing,omitempty"`

	// PI curve fields. Pointer types distinguish "not set" from "zero".
	// validate() requires all five for kind="pi"; nil = missing field.
	Setpoint      *float64 `yaml:"setpoint,omitempty" json:"setpoint,omitempty"`
	Kp            *float64 `yaml:"kp,omitempty" json:"kp,omitempty"`
	Ki            *float64 `yaml:"ki,omitempty" json:"ki,omitempty"`
	FeedForward   *uint8   `yaml:"feed_forward,omitempty" json:"feed_forward,omitempty"`
	IntegralClamp *float64 `yaml:"integral_clamp,omitempty" json:"integral_clamp,omitempty"`
}

// CurvePoint is one anchor in a multi-point curve. Temp is in the
// curve's sensor unit (°C for hwmon temps); PWMPct is the canonical
// duty cycle in percent (0-100). PWM is the raw 0-255 mirror the
// runtime reads; MigrateCurvePWMFields keeps the two in sync.
//
// `pwm` carries `omitempty` so Save can suppress the legacy field
// from YAML output after migration (zero-PWM anchors round-trip via
// `pwm_pct: 0` — the *uint8 pointer is non-nil even when the value
// is zero, so omitempty skips only the truly-absent case).
type CurvePoint struct {
	Temp   float64 `yaml:"temp" json:"temp"`
	PWM    uint8   `yaml:"pwm,omitempty" json:"pwm,omitempty"`
	PWMPct *uint8  `yaml:"pwm_pct,omitempty" json:"pwm_pct,omitempty"`
}

type Control struct {
	Fan       string `yaml:"fan" json:"fan"`
	Curve     string `yaml:"curve" json:"curve"`
	ManualPWM *uint8 `yaml:"manual_pwm,omitempty" json:"manual_pwm,omitempty"` // nil = curve mode; non-nil = fixed duty
}

type Duration struct{ time.Duration }

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	dur, err := time.ParseDuration(value.Value)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", value.Value, err)
	}
	d.Duration = dur
	return nil
}

func (d Duration) MarshalYAML() (interface{}, error) {
	return d.String(), nil
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("config: duration unmarshal: %w", err)
	}
	dur, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	d.Duration = dur
	return nil
}

// DefaultSessionTTL is the lifetime of an authenticated web session.
const DefaultSessionTTL = 24 * time.Hour

// Login brute-force guard defaults. Applied in validate() when the
// operator leaves the fields zero, and exported so the web package can
// reuse them in tests.
const (
	DefaultLoginFailThreshold   = 5
	DefaultLoginLockoutCooldown = 15 * time.Minute
)

// Empty returns a minimal valid config with no fans, sensors, or controls.
// Used when starting the daemon before first-boot setup is complete.
//
// Collection fields are initialised to non-nil empty slices so /api/config
// renders them as `[]` rather than `null`. The UI iterates these lists
// without a null-guard; a JSON null would throw TypeError mid-render.
func Empty() *Config {
	return &Config{
		Version:      CurrentVersion,
		PollInterval: Duration{Duration: DefaultPollInterval},
		Web: Web{
			Listen:     "127.0.0.1:9999",
			SessionTTL: Duration{Duration: DefaultSessionTTL},
		},
		Sensors:  []Sensor{},
		Fans:     []Fan{},
		Curves:   []CurveConfig{},
		Controls: []Control{},
	}
}

// Load reads the YAML config at path, validates it, and re-anchors any
// hwmon Sensor / Fan paths whose ChipName is set so they survive a
// hwmonN renumbering across reboots. Re-anchor failures (chip missing,
// chip ambiguous, malformed path) are fatal — refusing to start is
// safer than writing PWM to a wrong-chip sysfs file. Entries with empty
// ChipName are left untouched.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	cfg, err := Parse(data)
	if err != nil {
		return nil, err
	}
	// One-shot compatibility repairs for configs written by earlier
	// ventd versions. Currently only repopulates web.tls_cert/tls_key
	// from a sibling first-boot keypair, so post-F2 installs stop
	// crashlooping on configs that pre-date the relevant Save() fix.
	// Mutations are persisted here so the next boot is idempotent.
	if mutated, mErr := Migrate(cfg, path, nil); mErr != nil {
		return nil, fmt.Errorf("migrate config %s: %w", path, mErr)
	} else if mutated {
		if _, sErr := Save(cfg, path); sErr != nil {
			return nil, fmt.Errorf("persist migrated config %s: %w", path, sErr)
		}
	}
	// Self-heal upgrade case: if the on-disk config pre-dates the
	// ChipName field and the hwmon paths are still valid (no
	// renumber happened), populate ChipName from the live name file
	// so future renumbers can be re-anchored. If a renumber DID
	// happen, the read fails silently and the resolver call below
	// will surface the misconfiguration loudly.
	EnrichChipName(cfg)
	if err := ResolveHwmonPaths(cfg, hwmonRootFS); err != nil {
		return nil, fmt.Errorf("resolve hwmon paths in %s: %w", path, err)
	}
	return cfg, nil
}

func Parse(data []byte) (*Config, error) {
	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	// Migrate legacy raw-PWM curve fields to / from their `_pct` siblings
	// BEFORE validate so the MinPWM<=MaxPWM and points-ordering checks
	// see the populated raw values regardless of which form the YAML
	// used. Warnings ride slog.Default() so CLI and tests still see them
	// without having to plumb a logger through every Parse call site.
	MigrateCurvePWMFields(cfg)
	if err := validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// MigrateCurvePWMFields reconciles the raw (0-255) and percent (0-100)
// duty-cycle fields on every curve and curve-point. The `_pct` form is
// the authoritative persistence shape; raw fields stay populated for
// runtime consumers (buildCurve, validate) that have always read them.
//
// Precedence: if both sides are set and disagree, `_pct` wins and a
// warning fires; otherwise the missing side is computed from the one
// that was set. A fresh config (both zero / nil on every field)
// round-trips through here without emitting any warning — the `_pct`
// sides end up as non-nil pointers to zero, which is distinguishable
// from nil via the YAML `omitempty` rule but prints the same to an
// operator.
//
// The conversion rounds: rawToPct(rawToPct⁻¹(x)) can differ from x
// by 1. That's fine for fan behaviour (±1 PWM is indistinguishable
// from motor noise) but tests that migrate a legacy value must
// compare against the round-tripped number, not the original.
func MigrateCurvePWMFields(cfg *Config) {
	if cfg == nil {
		return
	}
	for i := range cfg.Curves {
		c := &cfg.Curves[i]
		reconcile("curve "+c.Name+" min_pwm", &c.MinPWM, &c.MinPWMPct)
		reconcile("curve "+c.Name+" max_pwm", &c.MaxPWM, &c.MaxPWMPct)
		// Value is fixed-curve specific; still reconcile so other
		// curve types (which leave both at zero) emit a benign
		// `value_pct: 0` that round-trips.
		reconcile("curve "+c.Name+" value", &c.Value, &c.ValuePct)
		for j := range c.Points {
			p := &c.Points[j]
			reconcile(fmt.Sprintf("curve %s points[%d]", c.Name, j), &p.PWM, &p.PWMPct)
		}
	}
}

// reconcile is the per-field body of MigrateCurvePWMFields. label is
// an operator-facing identifier used in the "both set and disagree"
// warning.
//
// When both fields are populated, reconcile tolerates a ±1 round-trip
// drift (pctToRaw(rawToPct(x)) can differ from x by 1 due to the
// 255/100 scaling). A drift inside that tolerance is treated as "in
// sync": the raw side wins so successive calls to MigrateCurvePWMFields
// are idempotent. Only larger disagreements fire a warning.
func reconcile(label string, raw *uint8, pct **uint8) {
	if raw == nil || pct == nil {
		return
	}
	if *pct != nil {
		expected := pctToRaw(**pct)
		drift := int(*raw) - int(expected)
		if drift < 0 {
			drift = -drift
		}
		if *raw != 0 && drift > 1 {
			slog.Default().Warn("config: legacy and _pct fields disagree, preferring _pct",
				"field", label,
				"raw", *raw,
				"pct", **pct,
				"raw_from_pct", expected,
			)
			*raw = expected
			return
		}
		// Inside the ±1 rounding tolerance (or raw is the fresh zero we
		// haven't migrated yet): keep raw as it is if non-zero, else
		// populate it from pct.
		if *raw == 0 {
			*raw = expected
		}
		return
	}
	// _pct is nil; derive it from raw. rawToPct on zero is zero — the
	// fresh-config case produces ptr(0), which marshals as
	// `field_pct: 0` and reloads as the same.
	v := rawToPct(*raw)
	*pct = &v
}

// pctToRaw converts a 0-100 percent duty cycle to a 0-255 PWM byte.
// Clamps above 100; preserves the canonical zero.
func pctToRaw(pct uint8) uint8 {
	if pct > 100 {
		pct = 100
	}
	return uint8(math.Round(float64(pct) * 255 / 100))
}

// rawToPct converts a 0-255 PWM byte to a 0-100 percent duty cycle.
func rawToPct(raw uint8) uint8 {
	return uint8(math.Round(float64(raw) * 100 / 255))
}

// Save validates cfg, marshals it to YAML, and writes it atomically to path.
// Returns the validated config (with defaults applied) for swapping into the
// live pointer.
//
// EnrichChipName is called before marshal so every config produced via
// Save (web UI submissions, calibration completions, password updates)
// carries the chip identifier ResolveHwmonPaths needs on the next
// boot. Operators authoring config through the UI never have to touch
// the chip_name YAML field.
func Save(cfg *Config, path string) (*Config, error) {
	EnrichChipName(cfg)
	// Ensure every curve has its `_pct` fields populated before we
	// marshal. Callers that mutate cfg directly (the web UI's /api/config
	// write path, tests constructing CurveConfig{MinPWM: 30, ...}) won't
	// have set `_pct` themselves — without this call the Save would
	// emit a YAML that's missing the percent keys entirely.
	MigrateCurvePWMFields(cfg)
	// Build a shadow Config that carries only the `_pct` curve fields
	// in its Curves slice. The runtime cfg keeps legacy MinPWM /
	// MaxPWM / Value / PWM for every reader that has always used them
	// (buildCurve, validate); the YAML round-trip writes the percent
	// form and drops the legacy keys on every Save, so a Load→Save
	// cycle strips legacy lines from any pre-3f config in one pass.
	out := *cfg
	if len(cfg.Curves) > 0 {
		out.Curves = make([]CurveConfig, len(cfg.Curves))
		for i, c := range cfg.Curves {
			out.Curves[i] = c
			out.Curves[i].MinPWM = 0
			out.Curves[i].MaxPWM = 0
			out.Curves[i].Value = 0
			if len(c.Points) > 0 {
				pts := make([]CurvePoint, len(c.Points))
				copy(pts, c.Points)
				for j := range pts {
					pts[j].PWM = 0
				}
				out.Curves[i].Points = pts
			}
		}
	}
	data, err := yaml.Marshal(&out)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
	validated, err := Parse(data)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}
	if err := writeFileSync(path, data, 0600); err != nil {
		return nil, err
	}
	return validated, nil
}

// SavePasswordHash writes a minimal config file containing only the web
// section with the given bcrypt password hash. Used during first boot, before
// the setup wizard has produced a full config. On next daemon start, the
// wizard's full config replaces this file.
func SavePasswordHash(hash, path string) error {
	minimal := Empty()
	minimal.Web.PasswordHash = hash
	data, err := yaml.Marshal(minimal)
	if err != nil {
		return fmt.Errorf("marshal minimal config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	return writeFileSync(path, data, 0600)
}

// writeFileSync writes data to path atomically (via a .tmp rename) and calls
// f.Sync() before the rename so the content survives an unclean reboot.
func writeFileSync(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("write config %s: %w", tmp, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("write config %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("sync config %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close config %s: %w", tmp, err)
	}
	// When invoked as root (manual `sudo ventd ...` run, rescue/debug
	// session, etc.) match the tmp file's owner/group to the parent
	// config dir before the atomic rename. Without this, every save
	// by a root-euid process leaves root:root files in /etc/ventd,
	// and the systemd User=ventd service can no longer read its own
	// config on the next start. No-op when euid != 0.
	if os.Geteuid() == 0 {
		if info, err := os.Stat(filepath.Dir(path)); err == nil {
			if st, ok := info.Sys().(*syscall.Stat_t); ok {
				_ = os.Chown(tmp, int(st.Uid), int(st.Gid))
			}
		}
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename config: %w", err)
	}
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync() // best-effort; some filesystems don't support this
		_ = dir.Close()
	}
	return nil
}

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
	if !strings.HasPrefix(p, rootClass) && !strings.HasPrefix(p, rootDevice) {
		return fmt.Errorf("pwm_path %q must start with %s or %s", p, rootClass, rootDevice)
	}
	cleaned := filepath.Clean(p)
	if !strings.HasPrefix(cleaned, rootClass) && !strings.HasPrefix(cleaned, rootDevice) {
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
		case "":
			return fmt.Errorf("config: sensor %q: type is required", s.Name)
		default:
			return fmt.Errorf("config: sensor %q: unknown type %q (want: hwmon, nvidia)", s.Name, s.Type)
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
			return fmt.Errorf("config: fan %q: unknown type %q (want: hwmon, nvidia)", f.Name, f.Type)
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

	return nil
}
