package hwmon

// Fuzz coverage for the autoload.go parser surface.
//
// parseSensorsDetectModules and parseSensorsDetectChips consume the
// stdout of `sensors-detect`, which comes from a Perl script we do
// not control. Past regressions have shipped where a Perl-side change
// (trailing spaces, altered hash banners, moved comment lines) caused
// silent zero-module output. The daemon then boots "successfully"
// with no fan control, which is the worst kind of failure because
// the user doesn't know anything is wrong.
//
// This fuzzer's job is narrow: arbitrary bytes must not panic either
// parser, and parseSensorsDetectModules must always return a
// []candidate whose .module strings are non-empty.
//
// Running the fuzzer:
//
//   go test -run ^$ -fuzz FuzzParseSensorsDetect -fuzztime 20s ./internal/hwmon/...
//
// Reference for future sessions:
//
//   If the `sensors-detect` output format changes again — usually
//   after an lm-sensors upstream release — the first signal is
//   usually an autoload_test.go failure. If those stay green but
//   users still report "no fans controlled" after install, point
//   this fuzzer at a captured real-world output by dropping it into
//   testdata/fuzz/FuzzParseSensorsDetect/ and rerunning.

import (
	"strings"
	"testing"
)

// Seed corpus: a handful of real-world shapes plus pathological ones.
// Keep these string-literal so the seeds are inspectable from this
// file without jumping to a separate testdata tree.
var sensorsDetectSeeds = []string{
	// Normal, two-module + options case. Mirrors the shape
	// autoload_test.go checks by hand.
	`# Found chip
#----cut here----
# Chip drivers
coretemp
nct6683
options nct6683 force=1
#----cut here----
`,

	// Empty section.
	`#----cut here----
#----cut here----
`,

	// Stray whitespace + trailing comment after the modules.
	`#----cut here----
   coretemp
    nct6775
# trailing comment
#----cut here----
`,

	// Malformed "options" line with only a module name, no params.
	`#----cut here----
coretemp
options coretemp
#----cut here----
`,

	// No cut markers at all.
	`coretemp
it87
`,

	// Only one cut marker (truncated output).
	`#----cut here----
coretemp
it87
`,

	// Driver/Chip detection shape.
	"Driver `coretemp'\n  Chip `Intel digital thermal sensor' (confidence: 9)\n",

	// Driver with a to-be-written placeholder and no Chip line.
	"Driver `to-be-written'\n  (no chip)\n",

	// Multiple drivers interleaved.
	"Driver `nct6683'\n  Chip `Nuvoton NCT6687D' (confidence: 9)\nDriver `coretemp'\n  Chip `Intel digital thermal sensor' (confidence: 9)\n",
}

func FuzzParseSensorsDetect(f *testing.F) {
	for _, seed := range sensorsDetectSeeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data string) {
		// Guard against adversarial size; the real stdout is capped
		// by exec but the fuzzer has no such limit. Skip inputs that
		// would starve the -fuzztime budget.
		if len(data) > 1<<16 {
			t.Skip("input too large, skipping")
		}

		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("parser panic on %d-byte input: %v\n---input---\n%q", len(data), r, data)
			}
		}()

		mods := parseSensorsDetectModules(data)
		for i, m := range mods {
			if m.module == "" {
				t.Fatalf("parseSensorsDetectModules returned empty .module at index %d\n---input---\n%q", i, data)
			}
			// module names never contain spaces or newlines — if they do,
			// modprobe will fail with a confusing error at install time.
			if strings.ContainsAny(m.module, " \t\r\n") {
				t.Fatalf("parseSensorsDetectModules returned module with whitespace %q\n---input---\n%q", m.module, data)
			}
		}

		chips := parseSensorsDetectChips(data)
		for driver, chip := range chips {
			if driver == "" {
				t.Fatalf("parseSensorsDetectChips returned empty driver key\n---input---\n%q", data)
			}
			if chip == "" {
				t.Fatalf("parseSensorsDetectChips returned empty chip for driver %q\n---input---\n%q", driver, data)
			}
		}
	})
}
