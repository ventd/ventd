package config

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

type Config struct {
	Version      int           `yaml:"version" json:"version"`
	PollInterval Duration      `yaml:"poll_interval" json:"poll_interval"`
	Web          Web           `yaml:"web" json:"web"`
	Sensors      []Sensor      `yaml:"sensors" json:"sensors"`
	Fans         []Fan         `yaml:"fans" json:"fans"`
	Curves       []CurveConfig `yaml:"curves" json:"curves"`
	Controls     []Control     `yaml:"controls" json:"controls"`
}

type Web struct {
	Listen       string   `yaml:"listen" json:"listen"`
	PasswordHash string   `yaml:"password_hash,omitempty" json:"password_hash,omitempty"`
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
	Metric      string `yaml:"metric,omitempty" json:"metric,omitempty"` // nvidia: temp(default), util, mem_util, power, clock_gpu, clock_mem, fan_pct
	HwmonDevice string `yaml:"hwmon_device,omitempty" json:"hwmon_device,omitempty"` // stable /sys/devices/... path for hwmon path resolution
	ChipName    string `yaml:"chip_name,omitempty" json:"chip_name,omitempty"`       // hwmonN/name attribute; used by ResolveHwmonPaths to re-anchor Path across renumbering
}

type Fan struct {
	Name        string `yaml:"name" json:"name"`
	Type        string `yaml:"type" json:"type"`
	PWMPath     string `yaml:"pwm_path" json:"pwm_path"`
	RPMPath     string `yaml:"rpm_path,omitempty" json:"rpm_path,omitempty"` // override auto-derived fan*_input path
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
}

type CurveConfig struct {
	Name string `yaml:"name" json:"name"`
	Type string `yaml:"type" json:"type"`

	Sensor  string  `yaml:"sensor,omitempty" json:"sensor,omitempty"`
	MinTemp float64 `yaml:"min_temp,omitempty" json:"min_temp,omitempty"`
	MaxTemp float64 `yaml:"max_temp,omitempty" json:"max_temp,omitempty"`
	MinPWM  uint8   `yaml:"min_pwm,omitempty" json:"min_pwm,omitempty"`
	MaxPWM  uint8   `yaml:"max_pwm,omitempty" json:"max_pwm,omitempty"`

	// fixed fields
	Value uint8 `yaml:"value,omitempty" json:"value,omitempty"`

	// mix fields
	Function string   `yaml:"function,omitempty" json:"function,omitempty"`
	Sources  []string `yaml:"sources,omitempty" json:"sources,omitempty"`
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
func Empty() *Config {
	return &Config{
		Version:      CurrentVersion,
		PollInterval: Duration{Duration: DefaultPollInterval},
		Web: Web{
			Listen:     "0.0.0.0:9999",
			SessionTTL: Duration{Duration: DefaultSessionTTL},
		},
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
	if err := validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
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
	data, err := yaml.Marshal(cfg)
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
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename config: %w", err)
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
		cfg.Web.Listen = "0.0.0.0:9999"
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
			floor := uint8(MinPumpPWM)
			if f.PumpMinimum > floor {
				floor = f.PumpMinimum
			}
			if f.MinPWM < floor {
				return fmt.Errorf("config: fan %q: is_pump=true but min_pwm (%d) is below pump floor (%d)", f.Name, f.MinPWM, floor)
			}
		}
		fans[f.Name] = f
	}

	curves := make(map[string]struct{}, len(cfg.Curves))
	for i, c := range cfg.Curves {
		if c.Name == "" {
			return fmt.Errorf("config: curves[%d]: name is required", i)
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
		case "fixed":
			// Value defaults to 0; clamped by fan min_pwm at runtime
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

	return nil
}
