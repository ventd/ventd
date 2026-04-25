package redactor

import (
	"bytes"
	"net"
	"os"
	"regexp"
	"strings"
)

// P1Hostname redacts the system hostname and its common variants.
// Consistent-mapped: same host → same obf_host_N token.
type P1Hostname struct {
	variants [][]byte // all forms to replace (FQDN, short, dhcp-*)
}

// NewP1Hostname builds a P1 primitive from the current machine hostname.
func NewP1Hostname() *P1Hostname {
	host, _ := os.Hostname()
	return NewP1HostnameFrom(host)
}

// NewP1HostnameFrom builds a P1 primitive from an explicit hostname string.
// Used in tests to inject a synthetic hostname.
func NewP1HostnameFrom(host string) *P1Hostname {
	seen := make(map[string]struct{})
	var variants []string
	addVariant := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		if _, ok := seen[v]; ok {
			return
		}
		seen[v] = struct{}{}
		variants = append(variants, v)
	}
	addVariant(host)
	// Short name (before first dot).
	if idx := strings.IndexByte(host, '.'); idx > 0 {
		addVariant(host[:idx])
	}
	// FQDN via reverse-DNS lookup (best-effort).
	addrs, _ := net.LookupHost(host)
	for _, addr := range addrs {
		names, _ := net.LookupAddr(addr)
		for _, n := range names {
			addVariant(strings.TrimSuffix(n, "."))
		}
	}
	// DHCP hostname pattern: dhcp-W-X-Y-Z (per sosreport issue #3388).
	dhcpPat := regexp.MustCompile(`^dhcp-[\d-]+$`)
	if dhcpPat.MatchString(host) {
		addVariant(host)
	}
	p := &P1Hostname{}
	for _, v := range variants {
		p.variants = append(p.variants, []byte(v))
	}
	return p
}

func (p *P1Hostname) Name() string { return "hostname" }

func (p *P1Hostname) Redact(content []byte, store *MappingStore) ([]byte, int) {
	total := 0
	for _, v := range p.variants {
		if !bytes.Contains(content, v) {
			continue
		}
		token := store.hostname(string(v))
		n := bytes.Count(content, v)
		content = bytes.ReplaceAll(content, v, []byte(token))
		total += n
	}
	return content, total
}
