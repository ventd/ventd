package redactor

import (
	"regexp"
)

// P8Cmdline redacts sensitive tokens from kernel command lines (/proc/cmdline).
// Keeps fan/hwmon-relevant parameters intact.
type P8Cmdline struct{}

func (p *P8Cmdline) Name() string { return "kernel_cmdline_token" }

// Patterns matching sensitive cmdline tokens. Each replaces only the value part.
var cmdlinePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(root=UUID=)[^\s]+`),
	regexp.MustCompile(`(root=PARTUUID=)[^\s]+`),
	regexp.MustCompile(`(root=)[^\s]+`),
	regexp.MustCompile(`(cryptdevice=UUID=)[^\s]+`),
	regexp.MustCompile(`(cryptdevice=)[^\s]+`),
	regexp.MustCompile(`(ip=)[^\s]+`),
	regexp.MustCompile(`(hostname=)[^\s]+`),
	regexp.MustCompile(`(BOOT_IMAGE=)[^\s]+`),
	regexp.MustCompile(`(resume=UUID=)[^\s]+`),
	regexp.MustCompile(`(resume=)[^\s]+`),
}

func (p *P8Cmdline) Redact(content []byte, _ *MappingStore) ([]byte, int) {
	total := 0
	for _, re := range cmdlinePatterns {
		matches := re.FindAll(content, -1)
		total += len(matches)
		content = re.ReplaceAll(content, []byte("${1}[REDACTED]"))
	}
	return content, total
}
