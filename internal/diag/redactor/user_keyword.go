package redactor

import (
	"bytes"
)

// P10UserKeyword redacts caller-supplied keywords (--redact-keyword=foo,bar).
// Consistent-mapped: same keyword → same obf_keyword_N token.
type P10UserKeyword struct {
	keywords []string
}

// NewP10UserKeyword builds a P10 from the list of keywords to redact.
func NewP10UserKeyword(keywords []string) *P10UserKeyword {
	return &P10UserKeyword{keywords: keywords}
}

func (p *P10UserKeyword) Name() string { return "user_keyword" }

func (p *P10UserKeyword) Redact(content []byte, store *MappingStore) ([]byte, int) {
	total := 0
	for _, kw := range p.keywords {
		b := []byte(kw)
		if !bytes.Contains(content, b) {
			continue
		}
		token := store.keyword(kw)
		n := bytes.Count(content, b)
		content = bytes.ReplaceAll(content, b, []byte(token))
		total += n
	}
	return content, total
}
