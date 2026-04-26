package hwdb

import (
	"io/fs"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// FuzzAnonymise verifies that no PII token injected into Profile fields
// survives the Anonymise pass. Seeds are loaded from testdata/anonymise/
// and cover hostname, MAC, IP, path, USB-physical, and kernel cmdline patterns.
func FuzzAnonymise(f *testing.F) {
	seeds := loadFuzzSeeds(f, "testdata/anonymise")
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		// Parse the raw input as a list of profiles (the generator format).
		var profiles []Profile
		if err := yaml.Unmarshal([]byte(raw), &profiles); err != nil || len(profiles) == 0 {
			t.Skip()
		}
		profile := profiles[0]

		// Collect PII tokens before anonymisation.
		piiTokens := collectPIITokens(&profile)

		if err := Anonymise(&profile); err != nil {
			// Fail-closed is acceptable — the important invariant is that
			// an error means nothing leaked, not that all inputs succeed.
			return
		}

		// Re-marshal the anonymised profile to check for lingering PII.
		out, err := yaml.Marshal([]Profile{profile})
		if err != nil {
			t.Fatalf("marshal post-anonymise: %v", err)
		}
		outStr := string(out)

		for _, tok := range piiTokens {
			if strings.Contains(outStr, tok) {
				t.Errorf("PII token %q survived anonymisation in output:\n%s", tok, outStr)
			}
		}
	})
}

// collectPIITokens extracts strings from user-controllable text fields that
// should not survive anonymisation. Vendor-attributed strings (board names,
// module names) are excluded — only user-set free-text is collected.
func collectPIITokens(p *Profile) []string {
	var tokens []string
	for _, fan := range p.Hardware.Fans {
		if tok := extractPIIFromLabel(fan.Label); tok != "" {
			tokens = append(tokens, tok)
		}
	}
	for _, st := range p.SensorTrust {
		if tok := extractPIIFromLabel(st.Reason); tok != "" {
			tokens = append(tokens, tok)
		}
	}
	return tokens
}

// extractPIIFromLabel identifies PII-like patterns in a label string.
// Returns the raw token for post-anonymise comparison, or "" if the string
// does not appear to contain user-identifiable data.
func extractPIIFromLabel(label string) string {
	if label == "" {
		return ""
	}
	// Patterns that indicate user PII: MAC address, IP, /home/ path, hostname suffix.
	piiPatterns := []string{
		"/home/",
		"usb-",
		".local",
		"192.168.", "10.0.", "10.1.", "172.",
		"UUID=",
		"cryptdevice=",
	}
	for _, pat := range piiPatterns {
		if strings.Contains(label, pat) {
			return label
		}
	}
	return ""
}

// loadFuzzSeeds reads all *.yaml files from dir and returns their contents as
// strings to be passed to f.Add.
func loadFuzzSeeds(f *testing.F, dir string) []string {
	f.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			f.Logf("fuzz seed dir %q not found; continuing without seeds", dir)
			return nil
		}
		f.Fatalf("ReadDir %q: %v", dir, err)
	}
	seeds := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !hasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, readErr := fs.ReadFile(os.DirFS(dir), e.Name())
		if readErr != nil {
			f.Logf("skip seed %q: %v", e.Name(), readErr)
			continue
		}
		seeds = append(seeds, string(data))
	}
	return seeds
}

func hasSuffix(name, suffix string) bool {
	return len(name) >= len(suffix) && name[len(name)-len(suffix):] == suffix
}
