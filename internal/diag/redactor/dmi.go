package redactor

import (
	"regexp"
)

// P2DMI redacts DMI serial numbers, UUIDs, and asset tags.
// One-shot replacement (not consistent-mapped — no analytical value).
type P2DMI struct{}

func (p *P2DMI) Name() string { return "dmi_serial" }

var dmiPatterns = []struct {
	re  *regexp.Regexp
	tag string
}{
	{regexp.MustCompile(`(?i)(Serial Number\s*:\s*)(\S+)`), "DMI_SERIAL"},
	{regexp.MustCompile(`(?i)(UUID\s*:\s*)([0-9A-Fa-f-]{32,36})`), "DMI_UUID"},
	{regexp.MustCompile(`(?i)(Asset Tag\s*:\s*)(\S+)`), "DMI_ASSET_TAG"},
	// /sys/class/dmi/id/ style: single-value lines after a known path context.
	{regexp.MustCompile(`(?m)(product_uuid=)([^\s\n]+)`), "DMI_UUID"},
	{regexp.MustCompile(`(?m)(product_serial=)([^\s\n]+)`), "DMI_SERIAL"},
	{regexp.MustCompile(`(?m)(board_serial=)([^\s\n]+)`), "DMI_SERIAL"},
	{regexp.MustCompile(`(?m)(chassis_serial=)([^\s\n]+)`), "DMI_SERIAL"},
}

func (p *P2DMI) Redact(content []byte, _ *MappingStore) ([]byte, int) {
	total := 0
	for _, pat := range dmiPatterns {
		matches := pat.re.FindAll(content, -1)
		for range matches {
			total++
		}
		replacement := []byte("${1}[REDACTED:" + pat.tag + "]")
		content = pat.re.ReplaceAll(content, replacement)
	}
	return content, total
}
