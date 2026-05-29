package redactor

import (
	"regexp"
)

// P11Secret scrubs credential material that must never survive into a
// diagnostic bundle regardless of profile: bcrypt password hashes and the
// values of credential-bearing keys (password_hash, bcrypt_hash, token,
// secret, bearer) in JSON/YAML/env-style content.
//
// The architectural denylist (internal/diag/bundle.go) already drops
// /etc/ventd/auth.json wholesale; P11Secret is defence-in-depth that catches
// the same material when it appears inline elsewhere — e.g. a legacy
// config.yaml carrying web.password_hash, or a hash echoed into a log line.
type P11Secret struct{}

func (p *P11Secret) Name() string { return "secret_material" }

var secretPatterns = []*regexp.Regexp{
	// bcrypt hash: $2a$/$2b$/$2x$/$2y$, two-digit cost, then 53 base64-ish chars.
	regexp.MustCompile(`\$2[abxy]\$[0-9]{2}\$[./A-Za-z0-9]{53}`),
	// key: "value" / key = value / key: value for credential-bearing keys.
	// Group 1 keeps the key + separator (and opening quote when present);
	// the value up to the next quote, comma, brace, or whitespace is dropped.
	regexp.MustCompile(`(?i)("?(?:password_hash|bcrypt_hash|password|secret|token|bearer|api[_-]?key)"?\s*[:=]\s*"?)[^"\s,}]+`),
}

func (p *P11Secret) Redact(content []byte, _ *MappingStore) ([]byte, int) {
	total := 0
	// bcrypt: bare replacement (no capture group to preserve).
	if m := secretPatterns[0].FindAll(content, -1); m != nil {
		total += len(m)
		content = secretPatterns[0].ReplaceAll(content, []byte("[REDACTED]"))
	}
	// keyed values: preserve the key + separator, redact the value.
	if m := secretPatterns[1].FindAll(content, -1); m != nil {
		total += len(m)
		content = secretPatterns[1].ReplaceAll(content, []byte("${1}[REDACTED]"))
	}
	return content, total
}
