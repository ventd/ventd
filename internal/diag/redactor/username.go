package redactor

import (
	"bytes"
	"os/user"
	"strconv"
	"strings"
)

// P5Username redacts login names for non-system users (UID ≥ 1000).
// Consistent-mapped: same username → same obf_user_N token.
type P5Username struct {
	users []string // cleartext usernames to redact
}

// NewP5Username reads /etc/passwd for UID ≥ 1000 users.
// Falls back gracefully if passwd is unreadable.
func NewP5Username() *P5Username {
	return &P5Username{users: collectHumanUsers()}
}

// NewP5UsernameFrom builds a P5 from an explicit list (for tests).
func NewP5UsernameFrom(users []string) *P5Username {
	return &P5Username{users: users}
}

func (p *P5Username) Name() string { return "username" }

// wellKnown system users to skip even if somehow UID ≥ 1000 on this system.
var wellKnownSystem = map[string]struct{}{
	"root": {}, "nobody": {}, "daemon": {}, "bin": {}, "sys": {},
	"sync": {}, "games": {}, "man": {}, "lp": {}, "mail": {},
	"news": {}, "uucp": {}, "proxy": {}, "www-data": {}, "backup": {},
	"list": {}, "irc": {}, "gnats": {}, "messagebus": {}, "syslog": {},
}

func collectHumanUsers() []string {
	var out []string
	// user.Current is the reliable fallback.
	if u, err := user.Current(); err == nil {
		uid, _ := strconv.Atoi(u.Uid)
		if uid >= 1000 {
			out = append(out, u.Username)
		}
	}
	return out
}

func (p *P5Username) Redact(content []byte, store *MappingStore) ([]byte, int) {
	total := 0
	for _, u := range p.users {
		if _, skip := wellKnownSystem[u]; skip {
			continue
		}
		b := []byte(u)
		if !bytes.Contains(content, b) {
			continue
		}
		token := store.username(u)
		n := bytes.Count(content, b)
		content = bytes.ReplaceAll(content, b, []byte(token))
		total += n
		// Also redact /home/<user> → /home/<token> style occurrences
		// that weren't caught by P6 (belt-and-suspenders).
		homeVariant := []byte("/home/" + u)
		if bytes.Contains(content, homeVariant) {
			homeToken := []byte("/home/" + strings.TrimPrefix(token, "obf_user_"))
			n2 := bytes.Count(content, homeVariant)
			content = bytes.ReplaceAll(content, homeVariant, homeToken)
			total += n2
		}
	}
	return content, total
}
