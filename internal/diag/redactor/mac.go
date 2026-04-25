package redactor

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// P3MAC redacts MAC addresses with consistent mapping.
// Broadcast and zero MACs are preserved. Output: aa:bb:cc:dd:ee:NN.
type P3MAC struct {
	mu  sync.Mutex
	seq map[string]int // cleartext → sequence number
}

func (p *P3MAC) Name() string { return "mac_address" }

var macRE = regexp.MustCompile(`\b([0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2}\b`)

var macPassthrough = map[string]struct{}{
	"ff:ff:ff:ff:ff:ff": {},
	"00:00:00:00:00:00": {},
}

func (p *P3MAC) macToken(norm string, store *MappingStore) string {
	p.mu.Lock()
	if p.seq == nil {
		p.seq = make(map[string]int)
	}
	n, ok := p.seq[norm]
	if !ok {
		n = len(p.seq) + 1
		p.seq[norm] = n
	}
	p.mu.Unlock()
	// Also register in global store for cross-bundle consistency key.
	_ = store.mac(norm)
	return fmt.Sprintf("aa:bb:cc:dd:ee:%02x", n)
}

func (p *P3MAC) Redact(content []byte, store *MappingStore) ([]byte, int) {
	total := 0
	result := macRE.ReplaceAllFunc(content, func(match []byte) []byte {
		norm := strings.ToLower(string(match))
		if _, skip := macPassthrough[norm]; skip {
			return match
		}
		total++
		return []byte(p.macToken(norm, store))
	})
	return result, total
}
