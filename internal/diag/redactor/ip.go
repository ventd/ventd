package redactor

import (
	"fmt"
	"net"
	"regexp"
	"strings"
	"sync"
)

// P4IP redacts IPv4 and IPv6 addresses with topology-preserving consistent mapping.
// Loopback, link-local, and unspecified are preserved.
type P4IP struct {
	mu        sync.Mutex
	v4subnets map[string]int // /24 → subnet index
	v4hosts   map[string]int // last octet within subnet
	v6seq     map[string]int // full v6 → sequence
}

func (p *P4IP) Name() string { return "ipv4" }

// IPv4: matches bare and CIDR forms.
var ipv4RE = regexp.MustCompile(`\b(\d{1,3}\.){3}\d{1,3}(/\d{1,2})?\b`)

// IPv6: simplified pattern covering common forms. Over-matches by
// design — we accept any colon-separated hex shape here and rely on
// net.ParseIP in Redact() to reject the false positives. Without
// the ParseIP guard the regex also matches ISO-8601 time-of-day
// shapes (`T01:08:33` looks like an IPv6 to this pattern), which
// historically corrupted every slog timestamp in diag bundles.
var ipv6RE = regexp.MustCompile(`\b([0-9a-fA-F]{0,4}:){2,7}[0-9a-fA-F]{0,4}\b`)

var ipv4Passthrough = []string{
	"127.", "0.0.0.0", "255.255.255.255",
}
var ipv6Passthrough = []string{
	"::1", "fe80:", "fd00:obf:",
}

func (p *P4IP) Redact(content []byte, store *MappingStore) ([]byte, int) {
	p.mu.Lock()
	if p.v4subnets == nil {
		p.v4subnets = make(map[string]int)
		p.v4hosts = make(map[string]int)
		p.v6seq = make(map[string]int)
	}
	p.mu.Unlock()

	total := 0
	// IPv4 first.
	content = ipv4RE.ReplaceAllFunc(content, func(match []byte) []byte {
		s := string(match)
		for _, prefix := range ipv4Passthrough {
			if strings.HasPrefix(s, prefix) {
				return match
			}
		}
		// Strip CIDR suffix for mapping, preserve it in output.
		cidr := ""
		bare := s
		if idx := strings.IndexByte(s, '/'); idx >= 0 {
			cidr = s[idx:]
			bare = s[:idx]
		}
		_ = store.ip(bare)
		token := p.v4Token(bare)
		total++
		return []byte(token + cidr)
	})
	// IPv6.
	content = ipv6RE.ReplaceAllFunc(content, func(match []byte) []byte {
		s := string(match)
		// Reject anything net.ParseIP can't accept as a v6 address.
		// The regex over-matches ISO-8601 time-of-day shapes
		// (T01:08:33 looks like a 3-group IPv6) and other
		// punctuation-delimited hex runs; ParseIP is the canonical
		// gate. ParseIP also accepts dotted-IPv4 — reject those
		// here so v4 doesn't double-redact.
		ip := net.ParseIP(s)
		if ip == nil || ip.To4() != nil {
			return match
		}
		for _, prefix := range ipv6Passthrough {
			if strings.HasPrefix(strings.ToLower(s), prefix) {
				return match
			}
		}
		// Skip if it looks like a MAC (already redacted).
		if strings.HasPrefix(s, "aa:") {
			return match
		}
		_ = store.ip(s)
		token := p.v6Token(s)
		total++
		return []byte(token)
	})
	return content, total
}

func (p *P4IP) v4Token(bare string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	// Subnet: first 3 octets.
	parts := strings.Split(bare, ".")
	if len(parts) != 4 {
		return "100.64.0.1"
	}
	subnet := strings.Join(parts[:3], ".")
	sn, ok := p.v4subnets[subnet]
	if !ok {
		sn = len(p.v4subnets) + 1
		p.v4subnets[subnet] = sn
	}
	// Host: last octet preserved for topology.
	return fmt.Sprintf("100.64.%d.%s", sn, parts[3])
}

func (p *P4IP) v6Token(addr string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	n, ok := p.v6seq[addr]
	if !ok {
		n = len(p.v6seq) + 1
		p.v6seq[addr] = n
	}
	return fmt.Sprintf("fd00:obf::%d", n)
}
