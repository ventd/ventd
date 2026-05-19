package redactor

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestP9UserLabel_OutputIsYAMLStringScalar guards against #652: the
// previous bare `[REDACTED:USER_LABEL_N]` shape parses as a YAML
// flow-sequence on string-typed fields. Single-quoting forces scalar
// decode.
func TestP9UserLabel_OutputIsYAMLStringScalar(t *testing.T) {
	in := []byte(strings.Join([]string{
		"fan:",
		"    name: cpu fan",
		"    label: top intake",
		"sensor:",
		"    name: cpu temp",
		"    description: package id 0",
	}, "\n") + "\n")

	p := &P9UserLabel{}
	out, n := p.Redact(in, nil)
	if n != 4 {
		t.Fatalf("redacted %d fields, want 4", n)
	}

	var doc struct {
		Fan struct {
			Name  string `yaml:"name"`
			Label string `yaml:"label"`
		} `yaml:"fan"`
		Sensor struct {
			Name        string `yaml:"name"`
			Description string `yaml:"description"`
		} `yaml:"sensor"`
	}
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("redacted output failed to decode as YAML with string fields: %v\n---\n%s", err, out)
	}
	if !strings.HasPrefix(doc.Fan.Name, "[REDACTED:USER_LABEL_") || !strings.HasPrefix(doc.Fan.Label, "[REDACTED:USER_LABEL_") {
		t.Errorf("fan field not redacted as expected: name=%q label=%q", doc.Fan.Name, doc.Fan.Label)
	}
	if !strings.HasPrefix(doc.Sensor.Name, "[REDACTED:USER_LABEL_") || !strings.HasPrefix(doc.Sensor.Description, "[REDACTED:USER_LABEL_") {
		t.Errorf("sensor field not redacted as expected: name=%q description=%q", doc.Sensor.Name, doc.Sensor.Description)
	}
}
