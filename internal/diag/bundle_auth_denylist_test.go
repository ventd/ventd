package diag

import (
	"testing"
)

// TestAuthJSONNeverCaptured guards the security fix that keeps the ventd
// admin credential store (/etc/ventd/auth.json, which holds the admin
// bcrypt hash) out of diagnostic bundles. The state collector walks every
// file under /etc/ventd/, so without this denylist entry the hash would
// ship in any bundle an operator downloads or sends to the maintainer
// ingest — a credential-disclosure path (SECURITY.md "password hashes").
func TestAuthJSONNeverCaptured(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		// The canonical credential store path and its bundle-relative form.
		{"/etc/ventd/auth.json", true},
		{"etc/ventd/auth.json", true},
		{"/etc/ventd/auth.json.bak", true},
		// Non-credential config under the same dir is still captured.
		{"etc/ventd/config.yaml", false},
		// TLS private key remains denied (regression guard).
		{"etc/ventd/tls.key", true},
		// Public cert is fine to capture.
		{"etc/ventd/tls.crt", false},
	}
	for _, c := range cases {
		if got := isDenied(c.path); got != c.want {
			t.Errorf("isDenied(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}
