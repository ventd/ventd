package config

// Fuzz coverage for the YAML parser boundary.
//
// FuzzParseConfig hands arbitrary bytes to Parse(). The contract we
// fuzz for is weak but load-bearing:
//
//   * Parse never panics on any input. A yaml.Unmarshal panic inside
//     the daemon would take the whole process down at startup.
//   * Parse either returns a *Config + nil error, or nil + non-nil
//     error. No half-built config leaks.
//   * Validate catches ill-formed values. If Parse succeeds, every
//     safety invariant in validate() must hold on the returned Config
//     (MinPWM <= MaxPWM, AllowStop gate on MinPWM=0, pump_minimum
//     nonzero for is_pump:true).
//
// Seed corpus: the testdata fixtures + a handful of pathological
// shapes known to have been problems in the past (duplicate keys,
// unicode in names, empty document).
//
// Running the fuzzer:
//
//   go test -run ^$ -fuzz FuzzParseConfig -fuzztime 30s ./internal/config/...
//
// In CI we only run the seed corpus as a regular test (no fuzz
// iterations). That keeps the CI wall clock short but still catches
// any new regression that trips an existing seed.
//
// Reference note for future sessions:
//
//   To reproduce a failure found by -fuzz, copy the offending
//   testdata/fuzz/FuzzParseConfig/* file into that directory (Go
//   does this automatically) and the next `go test` run will
//   replay it deterministically.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func FuzzParseConfig(f *testing.F) {
	// Seed with every fixture on disk.
	fixtures, err := filepath.Glob("testdata/*.yaml")
	if err != nil {
		f.Fatalf("glob testdata: %v", err)
	}
	for _, p := range fixtures {
		data, err := os.ReadFile(p)
		if err != nil {
			f.Fatalf("read %s: %v", p, err)
		}
		f.Add(data)
	}

	// Pathological shapes.
	f.Add([]byte{})
	f.Add([]byte("version: 1\n"))
	f.Add([]byte("version: 1\nfans: []\ncontrols: []\n"))
	f.Add([]byte("version: 1\nfans:\n  - name: a\n    name: b\n"))  // duplicate key
	f.Add([]byte("\xff\xfeUTF-16-BOM"))                             // invalid UTF-8
	f.Add([]byte("version: 1\nfans:\n  - pwm_path: \"\\u0000\"\n")) // embedded NUL
	f.Add([]byte(strings.Repeat("a: 1\n", 200)))                    // deep duplicate-key stream
	// YAML anchor bombs: a small file that expands exponentially.
	// We only guard against the panic, not against memory usage —
	// yaml.v3 has its own depth/anchor limits.
	f.Add([]byte("a: &a [1,2,3]\nb: *a\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		// Catch panics explicitly; the fuzzer will report them, but
		// recover() here lets us enrich the failure with the input.
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Parse panic on input (%d bytes): %v\n---input---\n%q", len(data), r, data)
			}
		}()

		cfg, err := Parse(data)
		if err != nil {
			if cfg != nil {
				t.Fatalf("Parse returned (non-nil cfg, err=%v): callers assume nil cfg on error\n---input---\n%q", err, data)
			}
			return
		}
		if cfg == nil {
			t.Fatalf("Parse returned (nil cfg, nil err)\n---input---\n%q", data)
		}

		// Every safety invariant validate() enforces must still hold.
		// Re-run it so we catch any regression where Parse stops
		// calling validate().
		if err := validate(cfg); err != nil {
			t.Fatalf("Parse accepted a config that validate now rejects: %v\n---input---\n%q", err, data)
		}
	})
}
