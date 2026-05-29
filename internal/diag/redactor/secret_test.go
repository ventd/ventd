package redactor

import (
	"strings"
	"testing"
)

func TestP11Secret_RedactsBcryptAndKeyedSecrets(t *testing.T) {
	p := &P11Secret{}
	store := NewMappingStore()

	cases := []struct {
		name           string
		in             string
		mustNotContain []string
		mustContain    []string
	}{
		{
			name:           "bare bcrypt hash",
			in:             "hash=$2b$12$abcdefghijklmnopqrstuuWZ1234567890abcdefghijklmnopqrs",
			mustNotContain: []string{"$2b$12$abcdefghijklmnopqrstuu"},
			mustContain:    []string{"[REDACTED]"},
		},
		{
			name:           "yaml password_hash",
			in:             "web:\n  password_hash: $2y$12$abcdefghijklmnopqrstuuWZ1234567890abcdefghijklmnopqrs\n",
			mustNotContain: []string{"$2y$12$"},
			mustContain:    []string{"password_hash:", "[REDACTED]"},
		},
		{
			name:           "json bcrypt_hash field",
			in:             `{"admin":{"bcrypt_hash":"$2a$12$abcdefghijklmnopqrstuuWZ1234567890abcdefghijklmnopqrs"}}`,
			mustNotContain: []string{"$2a$12$"},
			mustContain:    []string{`"bcrypt_hash"`, "[REDACTED]"},
		},
		{
			name:           "bearer token",
			in:             "token = deadbeefcafef00d1234567890",
			mustNotContain: []string{"deadbeefcafef00d1234567890"},
			mustContain:    []string{"token", "[REDACTED]"},
		},
		{
			name:           "benign content untouched",
			in:             "fan1_pwm: 128\ntemp1_input: 42000\n",
			mustNotContain: []string{"[REDACTED]"},
			mustContain:    []string{"fan1_pwm: 128"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, _ := p.Redact([]byte(c.in), store)
			got := string(out)
			for _, s := range c.mustNotContain {
				if strings.Contains(got, s) {
					t.Errorf("output still contains secret %q:\n%s", s, got)
				}
			}
			for _, s := range c.mustContain {
				if !strings.Contains(got, s) {
					t.Errorf("output missing expected %q:\n%s", s, got)
				}
			}
		})
	}
}

// TestP11Secret_WiredIntoProfiles ensures the secret scrubber is part of
// both redaction profiles that ship content (conservative + trusted),
// so a legacy config.yaml password_hash can never survive the pipeline.
func TestP11Secret_WiredIntoProfiles(t *testing.T) {
	for _, profile := range []string{ProfileConservative, ProfileTrustedRecipient} {
		prims := buildPrimitives(Config{Profile: profile})
		found := false
		for _, p := range prims {
			if p.Name() == (&P11Secret{}).Name() {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("profile %q does not include P11Secret", profile)
		}
	}
}
