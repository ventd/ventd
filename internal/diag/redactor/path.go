package redactor

import (
	"bytes"
)

// P6Path redacts home directory paths containing a redacted username.
// It relies on P5 having already run (or runs independently using the
// same store for consistency). Replaces /home/<user>/ with /home/<obf_user_N>/.
type P6Path struct {
	users []string
}

// NewP6Path builds a P6 primitive from the same user list as P5.
func NewP6Path(users []string) *P6Path { return &P6Path{users: users} }

func (p *P6Path) Name() string { return "user_path_home" }

func (p *P6Path) Redact(content []byte, store *MappingStore) ([]byte, int) {
	total := 0
	for _, u := range p.users {
		if _, skip := wellKnownSystem[u]; skip {
			continue
		}
		pattern := []byte("/home/" + u + "/")
		if !bytes.Contains(content, pattern) {
			continue
		}
		token := store.username(u)
		replacement := []byte("/home/" + token + "/")
		n := bytes.Count(content, pattern)
		content = bytes.ReplaceAll(content, pattern, replacement)
		total += n
	}
	return content, total
}
