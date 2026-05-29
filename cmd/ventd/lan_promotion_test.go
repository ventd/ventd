package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/ventd/ventd/internal/config"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// RULE-INSTALL-LAN-PROMOTION-PERSISTS: the loopback default is promoted to the
// LAN wildcard only when TLS is active and only from the default address, and
// the same promotion is applied on every start so the LAN bind survives reboot.
func TestPromoteLoopbackDefaultToWildcard(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		listen     string
		cert       string
		wantPromo  bool
		wantListen string
	}{
		{"loopback default + TLS -> promote", defaultLoopbackListen, "/etc/ventd/tls.crt", true, "0.0.0.0:9999"},
		{"loopback default, no TLS -> hold", defaultLoopbackListen, "", false, defaultLoopbackListen},
		{"operator wildcard already set -> no-op", "0.0.0.0:9999", "/etc/ventd/tls.crt", false, "0.0.0.0:9999"},
		{"operator custom bind left untouched", "192.168.1.5:9999", "/etc/ventd/tls.crt", false, "192.168.1.5:9999"},
		{"non-default loopback port untouched", "127.0.0.1:8443", "/etc/ventd/tls.crt", false, "127.0.0.1:8443"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Web.Listen = tc.listen
			if tc.cert != "" {
				cfg.Web.TLSCert = tc.cert
				cfg.Web.TLSKey = "/etc/ventd/tls.key"
			}
			got := promoteLoopbackDefaultToWildcard(cfg, quietLogger())
			if got != tc.wantPromo {
				t.Fatalf("promoted = %v, want %v", got, tc.wantPromo)
			}
			if cfg.Web.Listen != tc.wantListen {
				t.Fatalf("listen = %q, want %q", cfg.Web.Listen, tc.wantListen)
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
	if fileExists(filepath.Join(dir, "nope")) {
		t.Error("missing path should report false")
	}
}
