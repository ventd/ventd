// Package conflicts is the v0.8.x catalogue of competing fan-control
// daemons + the multi-modal detector that surfaces them so ventd can take
// exclusive PWM control without racing another writer.
//
// Goal 2 of the wizard rework: detect and stop competing daemons. The
// existing recovery.DetectVendorDaemon helper only covered 5 OEM units
// via systemctl is-active; the long tail (fancontrol, thinkfan,
// coolercontrol, nbfc-linux, liquidctl, i8kutils, …) was invisible to
// the wizard.
//
// Design notes
//   - Registry is data, not code. Adding a new competitor means appending
//     one struct literal — no detection logic to write.
//   - Detection is multi-modal: each Entry can declare any combination of
//     systemd Units, ProcPatterns (regex against /proc/PID/comm + cmdline),
//     ConfigPaths (presence of files that prove the daemon is installed
//     even when inactive), or ModprobeDropIns (drop-in filenames that
//     load PWM-controlling kernel modules out-of-band).
//   - Conflict resolution is consent-gated by default. Headless
//     VENTD_AUTO_STOP_CONFLICTS=yes opts into stopping non-vendor
//     daemons; vendor daemons (asusd, system76-power, fw-fanctrl) always
//     require explicit operator action because stopping them disables
//     adjacent functionality the user cares about (kbd backlight, GPU
//     switching, charge thresholds).
package conflicts

import "regexp"

// Intrusiveness rates how disruptive stopping a daemon is. The wizard's
// UI sorts conflicts low → high so the operator sees the cheap stops
// first and the scary "this will disable your keyboard backlight"
// vendor daemons last.
type Intrusiveness int

const (
	IntrusivenessLow    Intrusiveness = 1 // generic third-party fan daemon; stop is safe
	IntrusivenessMedium Intrusiveness = 2 // userspace tool with side effects
	IntrusivenessHigh   Intrusiveness = 3 // vendor daemon with adjacent features
)

// Entry describes one competing daemon. All slices may be empty: a Unit-
// only entry covers daemons that always run through systemd, a
// ProcPatterns-only entry covers handcrafted scripts with no unit file,
// etc. The detector reports a conflict when ANY signal matches.
type Entry struct {
	// Name is the short, stable identifier used in logs, the wizard's
	// recovery card, and the registry-driven test fixtures. Snake-case,
	// no whitespace. Must be unique within the registry.
	Name string

	// Description is a one-line summary shown in the recovery card.
	// Plain English. Aim: a sysadmin recognises the tool from the
	// description even if the Name is unfamiliar.
	Description string

	// Units is the list of systemd unit names this daemon ships
	// (typically one, sometimes two for daemon+helper pairs). Empty
	// when the daemon ships no unit file.
	Units []string

	// ProcPatterns is a regex list matched against /proc/*/comm and
	// /proc/*/cmdline. Use ^name$ for an exact match; broader patterns
	// catch wrapper scripts. Empty disables proc scanning for this
	// entry.
	ProcPatterns []*regexp.Regexp

	// ConfigPaths is the list of absolute paths whose existence proves
	// the daemon is installed (regardless of running state). Used by
	// the wizard to warn "fancontrol is installed but not running —
	// did you mean to enable it instead of ventd?" Empty disables
	// config-presence detection.
	ConfigPaths []string

	// ModprobeDropIns is the list of filenames (basename only, no
	// directory) the daemon writes into /etc/modprobe.d/ or
	// /etc/modules-load.d/. Detection looks in both directories. Empty
	// disables modprobe detection.
	ModprobeDropIns []string

	// Intrusiveness ranks the cost of stopping this daemon.
	Intrusiveness Intrusiveness

	// Vendor is true for OEM/vendor daemons (asusd, system76-power,
	// fw-fanctrl, supergfxd, tccd/tailord, legiond). Vendor daemons
	// own additional non-fan features (kbd backlight, GPU switching,
	// charge thresholds, RGB) that the user almost certainly wants to
	// keep. The headless auto-stop flag does NOT stop vendor daemons;
	// the operator must consent explicitly via
	// VENTD_AUTO_STOP_VENDOR_CONFLICTS=yes.
	Vendor bool

	// ConflictReason is the wizard card body explaining WHY this
	// conflicts with ventd. One or two sentences, plain English.
	ConflictReason string
}

// mustRegex panics at package-init time if a registry regex doesn't
// parse. This is a programmer error in the registry data, not a runtime
// failure, so panic is the right behaviour — a bad regex would silently
// disable detection otherwise.
func mustRegex(pat string) *regexp.Regexp {
	r, err := regexp.Compile(pat)
	if err != nil {
		panic("conflicts: bad regex " + pat + ": " + err.Error())
	}
	return r
}

// Registry is the canonical list of competing fan-control daemons ventd
// knows how to detect. Order is not significant — the detector iterates
// the whole list each scan.
//
// Adding a new competitor: append one literal, write a fixture-based
// test exercising the detection helper most relevant to the new entry,
// and update CHANGELOG.md.
var Registry = []Entry{
	// ─── Generic third-party fan-control daemons (low intrusiveness) ────

	{
		Name:        "fancontrol",
		Description: "lm-sensors fancontrol daemon (the canonical pwmconfig-generated /etc/fancontrol setup)",
		Units:       []string{"fancontrol.service"},
		ProcPatterns: []*regexp.Regexp{
			mustRegex(`^fancontrol$`),
		},
		ConfigPaths:     []string{"/etc/fancontrol"},
		ModprobeDropIns: nil,
		Intrusiveness:   IntrusivenessLow,
		ConflictReason:  "fancontrol writes the same /sys/class/hwmon/*/pwm* paths ventd needs; concurrent writers produce flapping fan speeds.",
	},
	{
		Name:        "thinkfan",
		Description: "ThinkPad-specific fan daemon driving /proc/acpi/ibm/fan via thinkpad_acpi",
		Units:       []string{"thinkfan.service"},
		ProcPatterns: []*regexp.Regexp{
			mustRegex(`^thinkfan$`),
		},
		ConfigPaths:    []string{"/etc/thinkfan.conf"},
		Intrusiveness:  IntrusivenessLow,
		ConflictReason: "thinkfan claims /proc/acpi/ibm/fan; ventd's thinkpad HAL backend cannot coexist.",
	},
	{
		Name:        "coolercontrol",
		Description: "CoolerControl daemon (GUI fan-control with hwmon, NVIDIA, AMD support)",
		Units:       []string{"coolercontrold.service", "coolercontrol.service"},
		ProcPatterns: []*regexp.Regexp{
			mustRegex(`^coolercontrold?$`),
		},
		ConfigPaths:    []string{"/etc/coolercontrol/", "/etc/coolercontrol/config.toml"},
		Intrusiveness:  IntrusivenessLow,
		ConflictReason: "coolercontrold writes the same hwmon PWM paths and may also drive NVIDIA fans; both daemons cannot share exclusive control.",
	},
	{
		Name:        "nbfc",
		Description: "NoteBook FanControl (nbfc-linux) — userspace EC port-IO fan daemon for 297+ laptop models",
		Units:       []string{"nbfc_service.service", "nbfc.service"},
		ProcPatterns: []*regexp.Regexp{
			mustRegex(`^nbfc(_service)?$`),
		},
		ConfigPaths:    []string{"/etc/nbfc/", "/var/lib/nbfc/"},
		Intrusiveness:  IntrusivenessLow,
		ConflictReason: "nbfc-linux writes EC registers directly via /dev/port; ventd's EC backends (msi-ec, thinkpad, etc.) collide with it.",
	},
	{
		Name:        "liquidctl",
		Description: "liquidctl-managed AIO/RGB daemon (typically a systemd unit running 'liquidctl set ...' on boot)",
		Units:       []string{"liquidctl.service", "liquidcfg.service"},
		ProcPatterns: []*regexp.Regexp{
			mustRegex(`liquidctl`),
		},
		ConfigPaths:    []string{"/etc/liquidctl.conf"},
		Intrusiveness:  IntrusivenessLow,
		ConflictReason: "liquidctl drives NZXT/Corsair/EVGA AIO pumps + fans; concurrent control with ventd's liquidctl HAL backend (or hwmon for the same device) flaps.",
	},
	{
		Name:        "i8kutils",
		Description: "i8kutils (Dell-laptop SMI/EC fan daemon, legacy)",
		Units:       []string{"i8kmon.service"},
		ProcPatterns: []*regexp.Regexp{
			mustRegex(`^i8kmon$`),
		},
		ConfigPaths:    []string{"/etc/i8kmon.conf"},
		Intrusiveness:  IntrusivenessLow,
		ConflictReason: "i8kmon polls Dell EC and overrides fan speed; ventd's hwmon path (dell_smm_hwmon) cannot win the race.",
	},
	{
		Name:        "mbpfan",
		Description: "mbpfan — Apple MacBook Pro fan daemon driving applesmc",
		Units:       []string{"mbpfan.service"},
		ProcPatterns: []*regexp.Regexp{
			mustRegex(`^mbpfan$`),
		},
		ConfigPaths:    []string{"/etc/mbpfan.conf"},
		Intrusiveness:  IntrusivenessLow,
		ConflictReason: "mbpfan writes applesmc hwmon paths; ventd's hwmon backend would race it.",
	},
	{
		Name:        "fancontrol_pwmconfig_cron",
		Description: "cron-driven pwmconfig wrapper (operator-installed crontab entry that runs fancontrol periodically)",
		ConfigPaths: []string{
			"/etc/cron.d/fancontrol",
			"/etc/cron.hourly/fancontrol",
			"/etc/cron.daily/fancontrol",
		},
		Intrusiveness:  IntrusivenessLow,
		ConflictReason: "A cron-scheduled fancontrol run periodically clobbers ventd's PWM writes; even with the fancontrol unit disabled the cron entry can race.",
	},
	{
		Name:           "pwm_fan_script",
		Description:    "operator-installed fan-control shell scripts under /usr/local/{bin,sbin}",
		ConfigPaths:    []string{"/usr/local/bin/fan-control.sh", "/usr/local/sbin/fan-control.sh"},
		Intrusiveness:  IntrusivenessLow,
		ConflictReason: "Hand-rolled shell loops writing hwmon PWM paths — common on home-server / NAS builds. ventd cannot share exclusive control.",
	},

	// ─── Vendor daemons (high intrusiveness — explicit consent required) ─

	{
		Name:        "asusd",
		Description: "ASUS Linux daemon (asusctl) — owns ROG/TUF EC for fan profiles, kbd backlight, charge thresholds",
		Units:       []string{"asusd.service"},
		ProcPatterns: []*regexp.Regexp{
			mustRegex(`^asusd$`),
		},
		ConfigPaths:    []string{"/etc/asusd/"},
		Vendor:         true,
		Intrusiveness:  IntrusivenessHigh,
		ConflictReason: "asusd owns the ASUS EC and provides kbd backlight, fan profile, and charge-threshold control. Stopping it loses all three; consider ventd's monitor-only mode instead.",
	},
	{
		Name:        "supergfxd",
		Description: "asus-linux supergfxctl daemon — ASUS GPU switching (Hybrid/Integrated/Dedicated)",
		Units:       []string{"supergfxd.service"},
		ProcPatterns: []*regexp.Regexp{
			mustRegex(`^supergfxd$`),
		},
		ConfigPaths:    []string{"/etc/supergfxd.conf"},
		Vendor:         true,
		Intrusiveness:  IntrusivenessHigh,
		ConflictReason: "supergfxd does not touch fans directly but ships paired with asusd. The wizard surfaces it so an operator stopping asusd knows about its sibling.",
	},
	{
		Name:        "system76-power",
		Description: "System76 power management daemon — fan profiles + power profiles on System76 laptops",
		Units:       []string{"system76-power.service"},
		ProcPatterns: []*regexp.Regexp{
			mustRegex(`^system76-power$`),
		},
		ConfigPaths:    []string{"/etc/default/system76-power"},
		Vendor:         true,
		Intrusiveness:  IntrusivenessHigh,
		ConflictReason: "system76-power owns the EC for both fan curves and power profiles. Stopping it requires the operator to mask power-profiles-daemon manually (known footgun); the wizard recommends monitor-only mode instead.",
	},
	{
		Name:        "fw_fanctrl",
		Description: "Framework laptop fan controller (fw-fanctrl, Python daemon over ectool)",
		Units:       []string{"fw-fanctrl.service"},
		ProcPatterns: []*regexp.Regexp{
			mustRegex(`fw-fanctrl`),
		},
		ConfigPaths:    []string{"/etc/fw-fanctrl/", "/etc/fw-fanctrl/config.yaml"},
		Vendor:         true,
		Intrusiveness:  IntrusivenessHigh,
		ConflictReason: "fw-fanctrl drives the Framework EC via ectool with model-specific profiles. ventd cannot match those profiles today; prefer monitor-only mode on Framework hardware.",
	},
	{
		Name:        "tuxedo_tccd",
		Description: "Tuxedo Computers Control Center daemon (tuxedo-control-center)",
		Units:       []string{"tccd.service"},
		ProcPatterns: []*regexp.Regexp{
			mustRegex(`^tccd$`),
		},
		ConfigPaths:    []string{"/etc/tcc/"},
		Vendor:         true,
		Intrusiveness:  IntrusivenessHigh,
		ConflictReason: "tccd manages Tuxedo laptop EC for fans, kbd backlight, and power profiles. Stopping it disables all three.",
	},
	{
		Name:        "tuxedo_tailord",
		Description: "Tuxedo TUXEDO Aquaris daemon (tailord)",
		Units:       []string{"tailord.service"},
		ProcPatterns: []*regexp.Regexp{
			mustRegex(`^tailord$`),
		},
		Vendor:         true,
		Intrusiveness:  IntrusivenessHigh,
		ConflictReason: "tailord pairs with tccd on Tuxedo hardware. Surfaced for the same monitor-only recommendation.",
	},
	{
		Name:        "legion_legiond",
		Description: "LenovoLegionLinux daemon (legiond) — Lenovo Legion fan + RGB + power profile control",
		Units:       []string{"legiond.service"},
		ProcPatterns: []*regexp.Regexp{
			mustRegex(`^legiond$`),
		},
		ConfigPaths:    []string{"/etc/legion_linux/"},
		Vendor:         true,
		Intrusiveness:  IntrusivenessHigh,
		ConflictReason: "legiond owns the Legion EC for fans, RGB, and power profiles. Stopping it loses all three.",
	},
	{
		Name:        "lenovo_legion_controller",
		Description: "lenovo-legion-controller (alternative Legion fan daemon)",
		ProcPatterns: []*regexp.Regexp{
			mustRegex(`lenovo-legion-controller`),
		},
		ConfigPaths:    []string{"/etc/lenovo-legion-controller/"},
		Vendor:         true,
		Intrusiveness:  IntrusivenessHigh,
		ConflictReason: "Alternate Legion fan controller. Surfaced so the wizard can warn the operator if both legiond and this are installed.",
	},
	{
		Name:        "system76_io_dkms_unit",
		Description: "system76-io-dkms helper that loads the system76_io module on boot",
		ModprobeDropIns: []string{
			"system76-io.conf",
			"system76_io.conf",
		},
		ConfigPaths:    []string{"/etc/modules-load.d/system76-io.conf"},
		Vendor:         true,
		Intrusiveness:  IntrusivenessMedium,
		ConflictReason: "On System76 hardware the system76_io kernel module exposes additional EC paths; loading it changes hwmon topology beneath ventd.",
	},
	{
		Name:        "open_rgb",
		Description: "OpenRGB daemon (RGB control with EC-poking side effects on some boards)",
		Units:       []string{"openrgb.service"},
		ProcPatterns: []*regexp.Regexp{
			mustRegex(`^openrgb`),
		},
		Intrusiveness:  IntrusivenessMedium,
		ConflictReason: "OpenRGB does not own fan control but its low-level EC writes on some MSI/Gigabyte boards reset PWM state. Surface so the operator can correlate fan-flap reports.",
	},

	// ─── Modprobe-only competitors (no daemon, just a module load) ──────

	{
		Name:        "fancontrol_modprobe_dropin",
		Description: "/etc/modprobe.d entries that load fancontrol-driven Super-I/O modules with non-default options",
		ModprobeDropIns: []string{
			"it87.conf",
			"nct6775.conf",
			"nct6683.conf",
		},
		Intrusiveness:  IntrusivenessMedium,
		ConflictReason: "Operator-supplied modprobe options on the Super-I/O drivers can disable features ventd's wizard expects (e.g. `force=` flags). Surface as a warning, not a stop.",
	},
}

// Conflict is the runtime report produced by Detect for one matched
// registry entry. Multiple signals may match the same entry; the
// Conflict aggregates them so the wizard's recovery card shows the
// operator everything we found in one place.
type Conflict struct {
	Entry          Entry    `json:"entry"`
	UnitsActive    []string `json:"units_active,omitempty"`
	UnitsEnabled   []string `json:"units_enabled,omitempty"`
	ProcessesFound []string `json:"processes_found,omitempty"`
	ConfigsFound   []string `json:"configs_found,omitempty"`
	ModprobeFound  []string `json:"modprobe_found,omitempty"`
	FDHolders      []string `json:"fd_holders,omitempty"` // process names holding open hwmon PWM fds
}

// HasSignal returns true when any detection source found evidence of
// this conflict. A Conflict with no signals is filtered out before the
// detector returns.
func (c Conflict) HasSignal() bool {
	return len(c.UnitsActive) > 0 ||
		len(c.UnitsEnabled) > 0 ||
		len(c.ProcessesFound) > 0 ||
		len(c.ConfigsFound) > 0 ||
		len(c.ModprobeFound) > 0 ||
		len(c.FDHolders) > 0
}
