package main

import (
	"os"
	"path/filepath"
	"testing"
)

// RULE-INSTALL-FIRSTBOOT-LOOPBACK: the loopback default is promoted to the LAN
// wildcard only once the daemon is locked (admin password set) AND TLS is
// active — never during the open first-boot window, and never over plaintext.
func TestShouldPromoteToWildcard(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name               string
		listen             string
		tlsActive, hasPass bool
		want               bool
	}{
		{"first-boot: TLS up, no password, loopback -> hold", defaultLoopbackListen, true, false, false},
		{"locked: TLS up, password set, loopback -> promote", defaultLoopbackListen, true, true, true},
		{"never plaintext: no TLS, password set, loopback -> hold", defaultLoopbackListen, false, true, false},
		{"no TLS, no password -> hold", defaultLoopbackListen, false, false, false},
		{"already LAN: password set, TLS up, 0.0.0.0 -> no-op", "0.0.0.0:9999", true, true, false},
		{"operator custom bind left untouched", "192.168.1.5:9999", true, true, false},
		{"non-default loopback port left untouched", "127.0.0.1:8443", true, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldPromoteToWildcard(tc.listen, tc.tlsActive, tc.hasPass); got != tc.want {
				t.Fatalf("shouldPromoteToWildcard(%q, tls=%v, pass=%v) = %v, want %v",
					tc.listen, tc.tlsActive, tc.hasPass, got, tc.want)
			}
		})
	}
}

func TestFileExists(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f := filepath.Join(dir, "tls.crt")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !fileExists(f) {
		t.Error("existing file should report true")
	}
	if fileExists(dir) {
		t.Error("directory should report false")
	}
	if fileExists(filepath.Join(dir, "missing")) {
		t.Error("missing path should report false")
	}
}
