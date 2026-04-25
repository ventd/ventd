package redactor

import (
	"regexp"
)

// P7USBPhysical redacts USB physical paths (usb-X-Y-Z.W:U.V style).
// Topology shape (hub depth) is preserved; specific numbers replaced with letters.
type P7USBPhysical struct{}

func (p *P7USBPhysical) Name() string { return "usb_physical_path" }

// Matches: usb-1-2.3.4:1.0 style paths (variable depth, colon-separated interface).
var usbPhysRE = regexp.MustCompile(`usb-[\d][\d.\-:]+`)

// letterFor maps a digit character to a letter for topology-preserving obfuscation.
func letterFor(b byte) byte {
	if b >= '0' && b <= '9' {
		return 'A' + (b - '0')
	}
	return b
}

func (p *P7USBPhysical) Redact(content []byte, _ *MappingStore) ([]byte, int) {
	total := 0
	result := usbPhysRE.ReplaceAllFunc(content, func(match []byte) []byte {
		total++
		out := make([]byte, len(match))
		for i, b := range match {
			if b >= '0' && b <= '9' {
				out[i] = letterFor(b)
			} else {
				out[i] = b
			}
		}
		return out
	})
	return result, total
}
