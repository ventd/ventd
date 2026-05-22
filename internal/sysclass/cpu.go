package sysclass

import (
	"os"
	"regexp"
	"strings"
)

// cpuProfile maps a detected CPU to a class and Tjmax.
type cpuProfile struct {
	class SystemClass
	tjmax float64
	key   string // evidence string
}

// Patterns are ordered: first match wins. Use word-boundary anchors to avoid
// false matches (e.g. \bEPYC\b avoids matching "Intel EPYC-clone").
var cpuPatterns = []struct {
	re      *regexp.Regexp
	profile cpuProfile
}{
	// HEDT Intel: 13900K/14900K
	{
		re:      regexp.MustCompile(`\b1[34]900K\b`),
		profile: cpuProfile{class: ClassHEDTAir, tjmax: 100.0, key: "cpu_model_regex:intel_hedt_13_14_gen"},
	},
	// HEDT AMD: 7950X / 9950X / 9950X3D
	{
		re:      regexp.MustCompile(`\b(7950X|9950X3D|9950X)\b`),
		profile: cpuProfile{class: ClassHEDTAir, tjmax: 95.0, key: "cpu_model_regex:amd_hedt_7950x_9950x"},
	},
	// Server: Xeon Platinum/Gold
	{
		re:      regexp.MustCompile(`\bXeon\b.*(Platinum|Gold)\b`),
		profile: cpuProfile{class: ClassServer, tjmax: 100.0, key: "cpu_model_regex:intel_xeon_plat_gold"},
	},
	// Server: AMD EPYC (Milan/Genoa/Bergamo)
	{
		re:      regexp.MustCompile(`\bEPYC\b`),
		profile: cpuProfile{class: ClassServer, tjmax: 95.0, key: "cpu_model_regex:amd_epyc"},
	},
	// Server: Threadripper PRO
	{
		re:      regexp.MustCompile(`\bThreadripper\s+PRO\b`),
		profile: cpuProfile{class: ClassServer, tjmax: 95.0, key: "cpu_model_regex:amd_threadripper_pro"},
	},
	// Laptop: Intel Tiger/Alder/Raptor Lake -P / -H suffixes
	{
		re:      regexp.MustCompile(`\bi[357]-1[23456]\d{3}[PH]\b`),
		profile: cpuProfile{class: ClassLaptop, tjmax: 100.0, key: "cpu_model_regex:intel_laptop_tgl_adl_rpl"},
	},
	// Laptop: AMD Phoenix / Strix (7x40 / 8x40 / HX series)
	{
		re:      regexp.MustCompile(`\b(7[2-9]\d0[HU]|8[2-9]\d0[HU]|AI\s+9\s+HX)\b`),
		profile: cpuProfile{class: ClassLaptop, tjmax: 95.0, key: "cpu_model_regex:amd_laptop_phoenix_strix"},
	},
	// Mini-PC: Intel N-series (N100/N150/N200/N305)
	{
		re:      regexp.MustCompile(`\bN[1-3]\d{2,3}\b`),
		profile: cpuProfile{class: ClassMiniPC, tjmax: 105.0, key: "cpu_model_regex:intel_n_series"},
	},
	// Mini-PC: Intel Celeron/Pentium J-series
	{
		re:      regexp.MustCompile(`\b(Celeron|Pentium)\b.*\bJ\d{4}\b`),
		profile: cpuProfile{class: ClassMiniPC, tjmax: 105.0, key: "cpu_model_regex:intel_celeron_j_series"},
	},
	// Mid-desktop AMD: 5800X / 5700X
	{
		re:      regexp.MustCompile(`\b5[78]00X\b`),
		profile: cpuProfile{class: ClassMidDesktop, tjmax: 90.0, key: "cpu_model_regex:amd_mid_5xxx"},
	},
	// Mid-desktop Intel: 12700K / 13700K
	{
		re:      regexp.MustCompile(`\b1[23]700K\b`),
		profile: cpuProfile{class: ClassMidDesktop, tjmax: 100.0, key: "cpu_model_regex:intel_mid_12_13_gen"},
	},
}

// TjmaxFromCPUInfo returns the CPU-model-derived Tjmax in °C using the
// in-package regex profile table. Returns 0 when the CPU model doesn't
// match any profile or /proc/cpuinfo is unreadable.
//
// Exported as a thin wrapper so callers outside internal/sysclass (the
// wizard's curve-gen fallback, the platform-profile controller's
// thermal-headroom heuristic) can pull the Tjmax fact without taking a
// dependency on probe.ProbeResult or running full sysclass.Detect.
func TjmaxFromCPUInfo() float64 {
	_, tjmax, _ := classifyCPU(defaultDeps())
	return tjmax
}

// classifyCPU reads /proc/cpuinfo (via deps) and returns the best matching
// class, Tjmax, and evidence string. Returns (ClassUnknown, 0, nil) when
// /proc/cpuinfo is unreadable or has no model name. Returns
// (ClassUnknown, 0, ["cpu_model_unrecognized", "model_name:..."]) when
// the model name is present but matches no profile — Tjmax stays 0 here
// because PersistDetection json.Marshals Detection.Tjmax and Go's
// encoding/json rejects NaN/Inf; the JSON-safety contract is the reason
// the unknown-Tjmax sentinel is the zero value rather than math.NaN.
func classifyCPU(d deps) (SystemClass, float64, []string) {
	path := procPath(d, "cpuinfo")
	data, err := os.ReadFile(path)
	if err != nil {
		return ClassUnknown, 0, nil
	}

	modelName := extractModelName(string(data))
	if modelName == "" {
		return ClassUnknown, 0, nil
	}

	for _, p := range cpuPatterns {
		if p.re.MatchString(modelName) {
			return p.profile.class, p.profile.tjmax, []string{p.profile.key, "model_name:" + sanitizeEvidence(modelName)}
		}
	}

	// Unknown CPU: return ClassUnknown with model name in evidence.
	return ClassUnknown, 0, []string{"cpu_model_unrecognized", "model_name:" + sanitizeEvidence(modelName)}
}

// extractModelName returns the first "model name" value from /proc/cpuinfo content.
func extractModelName(cpuinfo string) string {
	for _, line := range strings.Split(cpuinfo, "\n") {
		if strings.HasPrefix(line, "model name") {
			if idx := strings.Index(line, ":"); idx >= 0 {
				return strings.TrimSpace(line[idx+1:])
			}
		}
	}
	return ""
}

// sanitizeEvidence replaces characters that could break log line parsing.
func sanitizeEvidence(s string) string {
	r := strings.NewReplacer("\n", " ", "\t", " ")
	return strings.TrimSpace(r.Replace(s))
}
