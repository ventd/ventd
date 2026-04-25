package redactor

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
)

// SelfCheckResult holds the outcome of the post-bundle scan.
type SelfCheckResult struct {
	Leaks []SelfCheckLeak // non-empty when leaks were detected
}

// SelfCheckLeak identifies one detected cleartext occurrence.
type SelfCheckLeak struct {
	File   string
	String string
}

// Ok returns true when no leaks were detected.
func (r *SelfCheckResult) Ok() bool { return len(r.Leaks) == 0 }

// SelfCheck scans every file in the gzip-tar bundle at bundlePath for
// occurrences of any cleartext string in needles.
// RULE-DIAG-PR2C-02: detects un-redacted hostname strings.
func SelfCheck(bundlePath string, needles []string) (*SelfCheckResult, error) {
	f, err := os.Open(bundlePath)
	if err != nil {
		return nil, fmt.Errorf("self_check: open bundle: %w", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("self_check: gzip: %w", err)
	}
	defer gz.Close()

	result := &SelfCheckResult{}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("self_check: tar: %w", err)
		}
		// Skip the REDACTION_REPORT and manifest — they legitimately
		// contain metadata that may reference class names.
		switch hdr.Name {
		case "REDACTION_REPORT.json", "manifest.json", "README.md":
			continue
		}
		content, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("self_check: read %s: %w", hdr.Name, err)
		}
		for _, needle := range needles {
			if needle == "" {
				continue
			}
			if bytes.Contains(content, []byte(needle)) {
				result.Leaks = append(result.Leaks, SelfCheckLeak{
					File:   hdr.Name,
					String: needle,
				})
			}
		}
	}
	return result, nil
}
