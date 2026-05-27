package config

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

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

// Config is the in-memory shape of /etc/ventd/config.yaml.
//
// Concurrency contract (issue #978): instances handed out by
// atomic.Pointer[Config].Load() are READ-ONLY. Any caller that needs
// to mutate fields MUST first call Clone() to obtain an unshared
// deep copy, mutate the clone, and Store the new pointer. The
// pre-Clone shallow-copy-then-selective-deep-copy pattern (used
// historically by the web layer's applyProfile + applyConfigPatch)
// silently aliased Fans/Curves/Sensors/etc. between the live and
// the in-flight pointers — a concurrent reader iterating the live
// slice while a writer mutated the same backing array under the
// in-flight pointer was the latent race.
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
	Scheduler     Scheduler          `yaml:"scheduler,omitempty" json:"scheduler,omitempty"`
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

	// Diag groups v0.5.12 #64's diagnostic-bundle outbound config.
	// Specifically: optional Tailscale-style upstream ingest the
	// daemon can post bundles to with explicit per-bundle operator
	// consent. Empty (default) → no ingest, behaves as v0.5.11.
	Diag DiagConfig `yaml:"diag,omitempty" json:"diag,omitempty"`
}

// DiagConfig groups diagnostic-bundle outbound transport options.
// v0.5.12 #64 / spec issue #809.
type DiagConfig struct {
	// UpstreamIngest configures an optional maintainer-controlled
	// HTTP endpoint the daemon posts bundles to when the operator
	// clicks "Send to maintainers" + accepts the per-bundle consent
	// dialog. Empty Enabled / URL means the feature is off and the
	// /api/v1/diag/send endpoint refuses (no surprise outbound
	// traffic).
	UpstreamIngest UpstreamIngestConfig `yaml:"upstream_ingest,omitempty" json:"upstream_ingest,omitempty"`
}

// UpstreamIngestConfig holds the URL + bearer token for the optional
// outbound ingest. Token is per-installation, generated on first
// daemon start when the operator enables the feature; the maintainer
// endpoint uses it to dedupe + rate-limit per installation. Never
// logged or surfaced in diag bundles (the redactor's denylist covers
// the config path, but the value itself never enters a bundle either).
type UpstreamIngestConfig struct {
	// Enabled is the operator opt-in toggle. Default false.
	Enabled bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	// URL is the canonical maintainer ingest endpoint. Empty disables
	// regardless of Enabled. Schemes other than https:// are rejected
	// at validate time so the bundle never travels in cleartext.
	URL string `yaml:"url,omitempty" json:"url,omitempty"`
	// Token is the per-installation bearer secret. Auto-generated on
	// first POST to /api/v1/diag/send when empty; persisted via the
	// existing config save path.
	Token string `yaml:"token,omitempty" json:"token,omitempty"`
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

	// DBATarget is the operator-typed quietness budget in dBA. When
	// nil (the canonical case) the controller resolves the budget
	// from the active preset via PresetDBATargets — Silent: 25 dBA
	// (Whisper), Balanced: 32 dBA (Office), Performance: 45 dBA. An
	// explicit value overrides the preset default, so an operator
	// can pick "Balanced predictive aggressiveness" + "but cap noise
	// at 28 dBA" independently. R32 user-perception thresholds.
	//
	// Values outside [10, 80] dBA are rejected at config load —
	// 10 dBA is below typical room-ambient floor (impossible to
	// honour); 80 dBA is louder than any consumer fan setup
	// can plausibly produce, so a value above 80 indicates a typo
	// or a wrong unit.
	DBATarget *float64 `yaml:"dba_target,omitempty" json:"dba_target,omitempty"`

	// Disabled, when true, forces the v0.5.9 confidence controller's
	// global gate (w_pred_system) to false: every channel falls through
	// to pure reactive output (spec-v0_5_9 §3.6 disable inheritance).
	// The per-layer learning toggles (SignatureLearningDisabled,
	// SmartMarginalBenefitDisabled) are unaffected. Pointer-bool so an
	// explicit false is distinguishable from "unset"; nil/unset means
	// smart mode enabled (the default).
	Disabled *bool `yaml:"disabled,omitempty" json:"disabled,omitempty"`
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

// SmartDisabled reports whether the operator turned smart mode off via
// Config.Smart.Disabled (spec-v0_5_9 §3.6 disable inheritance). Defaults
// to false (enabled) when unset. When true the w_pred_system global gate
// is forced closed and the predictive arm never blends — every channel
// runs its reactive curve only.
func (c *Config) SmartDisabled() bool {
	if c == nil || c.Smart.Disabled == nil {
		return false
	}
	return *c.Smart.Disabled
}

// Clone returns a deep copy of c. Callers that need to mutate a
// Config loaded from atomic.Pointer[Config] MUST call Clone first so
// the live pointer's slices and maps stay unaliased — issue #978.
//
// Every slice, map, and pointer field is fresh-allocated. Inner
// slices/maps (Curve.Sources, Curve.Points, Profile.Bindings) are
// deep-copied so a clone-then-mutate of any nested container is
// safe. Value-typed fields (Web, Hwmon, HWDB, Experimental, Envelope,
// Idle, Smart, Diag, and the *bool toggles) are copied by the shallow
// struct assignment plus an explicit *bool dereference for
// AcousticOptimisation.
//
// nil-safe: Clone(nil) returns nil so callers can chain without a
// guard.
func (c *Config) Clone() *Config {
	if c == nil {
		return nil
	}
	out := *c // shallow value copy — covers scalars, embedded structs

	// Slices: re-allocate then copy. Inner slices in CurveConfig are
	// re-allocated below.
	if c.Sensors != nil {
		out.Sensors = make([]Sensor, len(c.Sensors))
		copy(out.Sensors, c.Sensors)
	}
	if c.Fans != nil {
		out.Fans = make([]Fan, len(c.Fans))
		copy(out.Fans, c.Fans)
	}
	if c.Curves != nil {
		out.Curves = make([]CurveConfig, len(c.Curves))
		for i, cv := range c.Curves {
			cv2 := cv
			if cv.Sources != nil {
				cv2.Sources = make([]string, len(cv.Sources))
				copy(cv2.Sources, cv.Sources)
			}
			if cv.Points != nil {
				cv2.Points = make([]CurvePoint, len(cv.Points))
				copy(cv2.Points, cv.Points)
			}
			// PI-curve *float64 / *uint8 fields are leaf pointers
			// from validate(); deep-copy each so a clone can be
			// mutated without writing through to the live config.
			cv2.MinPWMPct = clonePtrUint8(cv.MinPWMPct)
			cv2.MaxPWMPct = clonePtrUint8(cv.MaxPWMPct)
			cv2.ValuePct = clonePtrUint8(cv.ValuePct)
			cv2.Setpoint = clonePtrFloat64(cv.Setpoint)
			cv2.Kp = clonePtrFloat64(cv.Kp)
			cv2.Ki = clonePtrFloat64(cv.Ki)
			cv2.IntegralClamp = clonePtrFloat64(cv.IntegralClamp)
			cv2.FeedForward = clonePtrUint8(cv.FeedForward)
			out.Curves[i] = cv2
		}
	}
	if c.Controls != nil {
		out.Controls = make([]Control, len(c.Controls))
		for i, ctl := range c.Controls {
			ctl2 := ctl
			ctl2.ManualPWM = clonePtrUint8(ctl.ManualPWM)
			out.Controls[i] = ctl2
		}
	}
	if c.Profiles != nil {
		out.Profiles = make(map[string]Profile, len(c.Profiles))
		for k, p := range c.Profiles {
			p2 := p
			if p.Bindings != nil {
				p2.Bindings = make(map[string]string, len(p.Bindings))
				for bk, bv := range p.Bindings {
					p2.Bindings[bk] = bv
				}
			}
			out.Profiles[k] = p2
		}
	}
	out.AcousticOptimisation = clonePtrBool(c.AcousticOptimisation)
	out.Smart.Disabled = clonePtrBool(c.Smart.Disabled)
	return &out
}

func clonePtrUint8(p *uint8) *uint8 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func clonePtrFloat64(p *float64) *float64 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func clonePtrBool(p *bool) *bool {
	if p == nil {
		return nil
	}
	v := *p
	return &v
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

// Scheduler groups runtime knobs for the profile scheduler. Currently
// the only knob is the timezone used to interpret schedule strings —
// historically these were evaluated in the daemon's local time
// (time.Now() with no .UTC()), which silently shifted when a laptop /
// NAS moved timezones or DST transitioned. Issue #624.
//
// Zero value (empty Timezone) preserves the pre-#624 behaviour
// (Local) for back-compat with existing configs; the documented
// recommendation for new installs is "utc" or an explicit IANA name.
type Scheduler struct {
	// Timezone controls how the scheduler interprets schedule strings
	// like "22:00-07:00 mon,tue". Accepted values:
	//
	//	""        — back-compat: Local time (pre-#624 behaviour).
	//	"local"   — explicit Local (same as empty; new configs should
	//	            prefer this over the empty form so the intent reads
	//	            clearly on the config file).
	//	"utc"     — UTC; recommended for new installs because it doesn't
	//	            drift across geographic moves or DST transitions.
	//	"<IANA>"  — any IANA zone name (e.g. "Australia/Sydney"). The
	//	            scheduler converts the live wall clock into the
	//	            named zone before evaluating Matches().
	//
	// Empty / unrecognised names log a one-time WARN at daemon start
	// and fall back to Local. Per RULE-SCHEDULE-TZ-01.
	Timezone string `yaml:"timezone,omitempty" json:"timezone,omitempty"`
}

// SchedulerLocation resolves the scheduler's configured Timezone into a
// *time.Location the scheduler can pass through time.In(). Empty / "local"
// returns time.Local. "utc" returns time.UTC. Any other value is treated
// as an IANA name and looked up via time.LoadLocation; a lookup failure
// returns time.Local with `ok=false` so the caller can log the
// fallback.
//
// Test seam: loadLocationFn is the package-level seam tests use to stub
// time.LoadLocation; nil → use the real LoadLocation.
func (sc Scheduler) Location() (loc *time.Location, ok bool) {
	switch strings.ToLower(strings.TrimSpace(sc.Timezone)) {
	case "", "local":
		return time.Local, true
	case "utc":
		return time.UTC, true
	default:
		lookup := loadLocationFn
		if lookup == nil {
			lookup = time.LoadLocation
		}
		l, err := lookup(sc.Timezone)
		if err != nil || l == nil {
			return time.Local, false
		}
		return l, true
	}
}

// loadLocationFn is the test seam for time.LoadLocation. Production
// leaves it nil.
var loadLocationFn func(name string) (*time.Location, error)

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
// optional; missing fields take the zero-config-smart default so existing
// on-disk configs load unchanged and round-trip without gaining a new
// hwmon: key (the missing-field state means "honour the default").
type Hwmon struct {
	// DynamicRebind controls the action=added rebind path (#95/#98
	// Option A / #1265). When the daemon observes a uevent topology
	// change that adds a configured hwmon chip, it triggers an in-
	// process restart so ResolveHwmonPaths can bind the now-present
	// device at its new hwmonN path. Without this the daemon stays
	// bound to the stale path after rmmod+modprobe / DKMS upgrade /
	// USB GPU hotplug and silently writes to a vanished sysfs entry.
	//
	// Nil → default true (zero-config-smart). Explicit false opts out.
	// Explicit true still works for configs persisted by an earlier
	// daemon.
	DynamicRebind *bool `yaml:"dynamic_rebind,omitempty" json:"dynamic_rebind,omitempty"`
}

// DynamicRebindEnabled returns the effective value of the
// Hwmon.DynamicRebind toggle. The pointer-with-default shape keeps the
// "missing field means default-on" semantics: the field is on by
// default (#1265) and can only be flipped off by an explicit false in
// the config.
func (h Hwmon) DynamicRebindEnabled() bool {
	if h.DynamicRebind == nil {
		return true
	}
	return *h.DynamicRebind
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
	// Position is the operator-supplied normalised (x,y) coordinate
	// inside the case heatmap, both in [0,1]. Optional; nil omits the
	// sensor from the heatmap view. Independent of any thermal logic.
	Position *Position `yaml:"position,omitempty" json:"position,omitempty"`
}

type Fan struct {
	Name string `yaml:"name" json:"name"`
	// DisplayLabel is the operator-overridable user-facing label for
	// this fan (#631). Empty (default) → the wizard's auto-derived
	// Name is also the display name; non-empty supersedes Name for
	// display purposes only (Controls bindings, watchdog identity,
	// and every internal reference continue to key off Name). The
	// hwmon-reported fan label (CPU_FAN, CHA_FAN1, etc.) bakes into
	// Name at wizard time via hwmonFanName; an operator who finds it
	// wrong or unhelpful sets DisplayLabel without disturbing the
	// fan's stable identity. Surfaced through fanStatus.Label so the
	// dashboard / curve-editor / settings UI render the override
	// transparently. Defer to Display() rather than reaching into
	// the field directly.
	DisplayLabel string `yaml:"display_label,omitempty" json:"display_label,omitempty"`
	Type         string `yaml:"type" json:"type"`
	PWMPath      string `yaml:"pwm_path" json:"pwm_path"`
	RPMPath      string `yaml:"rpm_path,omitempty" json:"rpm_path,omitempty"`         // override auto-derived fan*_input path
	HwmonDevice  string `yaml:"hwmon_device,omitempty" json:"hwmon_device,omitempty"` // stable /sys/devices/... path for hwmon path resolution
	ChipName     string `yaml:"chip_name,omitempty" json:"chip_name,omitempty"`       // hwmonN/name attribute; used by ResolveHwmonPaths to re-anchor PWMPath/RPMPath across renumbering
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
	// Position is the operator-supplied normalised (x,y) coordinate
	// inside the case heatmap, both in [0,1]. Optional; nil omits the
	// fan from the heatmap view. Independent of any control logic.
	Position *Position `yaml:"position,omitempty" json:"position,omitempty"`
}

// Display returns the operator-facing label for this fan: DisplayLabel
// when set, otherwise Name. Internal references (Control.Fan,
// watchdog identity, polarity persistence) always use Name; only the
// UI layer reaches through Display(). Issue #631.
func (f Fan) Display() string {
	if dl := strings.TrimSpace(f.DisplayLabel); dl != "" {
		return dl
	}
	return f.Name
}

// Position is the normalised (x, y) coordinate of a sensor or fan
// inside the heatmap view. Both axes run [0,1] from top-left
// origin; rendering scales to whatever case-shape geometry the
// view chooses. Operator-supplied via YAML; absent defaults to nil
// and excludes the entity from the heatmap.
type Position struct {
	X float64 `yaml:"x" json:"x"`
	Y float64 `yaml:"y" json:"y"`
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
