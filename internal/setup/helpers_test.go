package setup

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ventd/ventd/internal/config"
)

// TestNeeded covers the first-boot detection predicate. It returns true
// whenever the live config has no Controls defined — the same signal the
// web handler uses to decide whether to redirect to the wizard.
func TestNeeded(t *testing.T) {
	cases := []struct {
		name string
		cfg  *config.Config
		want bool
	}{
		{"empty_controls_is_needed", &config.Config{}, true},
		{"nil_controls_is_needed", &config.Config{Controls: nil}, true},
		{"zero_length_is_needed", &config.Config{Controls: []config.Control{}}, true},
		{"one_control_is_not_needed", &config.Config{
			Controls: []config.Control{{Fan: "cpu_fan", Curve: "cpu_curve"}},
		}, false},
		{"many_controls_is_not_needed", &config.Config{
			Controls: []config.Control{
				{Fan: "f1", Curve: "c1"},
				{Fan: "f2", Curve: "c2"},
			},
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Needed(tc.cfg); got != tc.want {
				t.Errorf("Needed() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestClampTemp pins the safety clamp used by buildConfig when deriving curve
// max temperatures from hardware thermal limits. Any value inside [lo, hi] is
// returned rounded to the nearest integer; values outside are clipped. Only
// the clamping boundaries matter to downstream callers — no silent passthrough
// of negative/high thermal readings.
func TestClampTemp(t *testing.T) {
	cases := []struct {
		name       string
		v, lo, hi  float64
		want       float64
	}{
		{"below_lo", 50, 75, 95, 75},
		{"above_hi", 120, 75, 95, 95},
		{"at_lo", 75, 75, 95, 75},
		{"at_hi", 95, 75, 95, 95},
		{"inside_rounds_down", 80.3, 75, 95, 80},
		{"inside_rounds_up", 80.7, 75, 95, 81},
		{"exactly_half_rounds_away_from_zero", 80.5, 75, 95, 81},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := clampTemp(tc.v, tc.lo, tc.hi); got != tc.want {
				t.Errorf("clampTemp(%v,%v,%v) = %v, want %v",
					tc.v, tc.lo, tc.hi, got, tc.want)
			}
		})
	}
}

// TestParseMilliC covers the sysfs millidegree parser used for _crit files.
// An empty or non-positive value yields 0 — the documented "unknown" signal
// that tells buildConfig to fall back to hard-coded 85°C defaults.
func TestParseMilliC(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{"", 0},
		{"0", 0},
		{"-5000", 0},
		{"95000", 95.0},
		{"85000", 85.0},
		{"85500", 85.5},
		{"notanumber", 0},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := parseMilliC(tc.in); got != tc.want {
				t.Errorf("parseMilliC(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestIsCaseFan covers the case-fan classifier used by buildConfig to route
// system/chassis fans to case_curve. Match must be substring and case-insensitive
// so "SYS_FAN1", "Cha_Fan", and "CASE_FAN_2" all qualify.
func TestIsCaseFan(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"SYS_FAN1", true},
		{"sys_fan", true},
		{"CHA_FAN2", true},
		{"Cha_Fan", true},
		{"CASE_FAN_3", true},
		{"case-fan", true},
		{"CPU_FAN", false},
		{"pump_fan", false},
		{"gpu0", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isCaseFan(tc.name); got != tc.want {
				t.Errorf("isCaseFan(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// TestTitleCaseWords pins the zero-dep title-caser used for driver-provided
// fan labels. Each whitespace-separated word is uppercased in its first rune
// and lowercased in the rest; empty strings and all-empty inputs are tolerated.
func TestTitleCaseWords(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"CPU FAN", "Cpu Fan"},
		{"sys fan1", "Sys Fan1"},
		{"SYS_FAN", "Sys_fan"},     // underscore is not whitespace
		{"  hello   world  ", "Hello World"},
		{"", ""},
		{"a", "A"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := titleCaseWords(tc.in); got != tc.want {
				t.Errorf("titleCaseWords(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestSortPathsNumerically pins the numeric sort used throughout setup to
// keep hwmon paths in the order an operator expects. Lexicographic sort
// would rank "hwmon10" before "hwmon2", and pwm channel order would break
// across multi-chip systems. Tests assert the numeric semantics explicitly.
func TestSortPathsNumerically(t *testing.T) {
	in := []string{
		"/sys/class/hwmon/hwmon10/pwm1",
		"/sys/class/hwmon/hwmon2/pwm1",
		"/sys/class/hwmon/hwmon2/pwm10",
		"/sys/class/hwmon/hwmon2/pwm2",
		"/sys/class/hwmon/hwmon1/pwm1",
	}
	want := []string{
		"/sys/class/hwmon/hwmon1/pwm1",
		"/sys/class/hwmon/hwmon2/pwm1",
		"/sys/class/hwmon/hwmon2/pwm2",
		"/sys/class/hwmon/hwmon2/pwm10",
		"/sys/class/hwmon/hwmon10/pwm1",
	}
	sortPathsNumerically(in)
	if !reflect.DeepEqual(in, want) {
		t.Errorf("after sortPathsNumerically:\ngot  %v\nwant %v", in, want)
	}
}

// TestSortPathsNumerically_Empty covers the trivial cases so the helper is
// safe to call unconditionally during discovery.
func TestSortPathsNumerically_Empty(t *testing.T) {
	var empty []string
	sortPathsNumerically(empty) // must not panic

	one := []string{"/sys/class/hwmon/hwmon1/pwm1"}
	sortPathsNumerically(one)
	if one[0] != "/sys/class/hwmon/hwmon1/pwm1" {
		t.Errorf("single element mutated: %v", one)
	}
}

// TestMinStartPWM covers the curve-floor selector used by buildConfig.
// Strict contract:
//   - Prefer stop_pwm over start_pwm (fan already spinning needs less).
//   - Fall back to 20 when every fan has zero stop_pwm and start_pwm (safe
//     default for uncalibrated fans).
//   - Never return 0.
func TestMinStartPWM(t *testing.T) {
	cases := []struct {
		name string
		fans []fanDiscovery
		want uint8
	}{
		{
			name: "prefers_stop_pwm_over_start_pwm",
			fans: []fanDiscovery{{startPWM: 80, stopPWM: 40}},
			want: 40,
		},
		{
			name: "falls_back_to_start_pwm_when_stop_zero",
			fans: []fanDiscovery{{startPWM: 50, stopPWM: 0}},
			want: 50,
		},
		{
			name: "picks_minimum_across_fans",
			fans: []fanDiscovery{
				{startPWM: 80, stopPWM: 40},
				{startPWM: 60, stopPWM: 30},
				{startPWM: 100, stopPWM: 50},
			},
			want: 30,
		},
		{
			name: "fallback_safe_default_when_all_zero",
			fans: []fanDiscovery{{startPWM: 0, stopPWM: 0}},
			want: 20,
		},
		{
			name: "mixed_zero_and_non_zero",
			fans: []fanDiscovery{
				{startPWM: 0, stopPWM: 0},
				{startPWM: 80, stopPWM: 45},
			},
			want: 45,
		},
		{
			name: "empty_returns_safe_default",
			fans: nil,
			want: 20,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := minStartPWM(tc.fans); got != tc.want {
				t.Errorf("minStartPWM(%v) = %v, want %v", tc.fans, got, tc.want)
			}
		})
	}
}

// TestReadTrimmed covers the file-read helper used on every hwmon *_label,
// *name*, and sysfs *_crit read. Missing files yield "" (a documented
// "unknown" signal) rather than an error.
func TestReadTrimmed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data")

	// Missing file → empty string, no panic.
	if got := readTrimmed(path); got != "" {
		t.Errorf("missing file: got %q, want empty", got)
	}

	// File with trailing whitespace → trimmed.
	if err := os.WriteFile(path, []byte("  hello\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readTrimmed(path); got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}

	// Empty file → empty string.
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readTrimmed(path); got != "" {
		t.Errorf("empty file: got %q, want empty", got)
	}
}
