package redactor

import (
	"regexp"
)

// P9UserLabel redacts free-form user-supplied labels in active_profile.yaml.
// Default-conservative replaces all label/name/comment YAML values.
type P9UserLabel struct{}

func (p *P9UserLabel) Name() string { return "user_label" }

// YAML fields that may contain user-chosen names.
var labelFieldRE = regexp.MustCompile(`(?m)^(\s*(?:name|label|comment|description|alias|title)\s*:\s*)(.+)$`)

func (p *P9UserLabel) Redact(content []byte, _ *MappingStore) ([]byte, int) {
	total := 0
	n := 0
	result := labelFieldRE.ReplaceAllFunc(content, func(match []byte) []byte {
		n++
		total++
		// Single-quote the scalar so YAML decodes it as a string. Bare
		// `[REDACTED:USER_LABEL_N]` is a YAML flow-sequence and breaks
		// string-typed fields on the consumer side (#652).
		repl := labelFieldRE.ReplaceAll(match, []byte("${1}'[REDACTED:USER_LABEL_"+itoa(n)+"]'"))
		return repl
	})
	return result, total
}

func itoa(n int) string {
	var buf [20]byte
	pos := len(buf)
	for n >= 10 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	pos--
	buf[pos] = byte('0' + n)
	return string(buf[pos:])
}
