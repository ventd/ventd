package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequireTransportSecurity(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, []byte("cert"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte("key"), 0600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		web     Web
		wantErr bool
	}{
		{"loopback_plain_ok", Web{Listen: "127.0.0.1:9999"}, false},
		{"loopback_v6_plain_ok", Web{Listen: "[::1]:9999"}, false},
		{"localhost_plain_ok", Web{Listen: "localhost:9999"}, false},
		{"wildcard_plain_refused", Web{Listen: "0.0.0.0:9999"}, true},
		{"wildcard_v6_plain_refused", Web{Listen: "[::]:9999"}, true},
		{"lan_plain_refused", Web{Listen: "192.168.1.10:9999"}, true},
		{"wildcard_tls_ok", Web{Listen: "0.0.0.0:9999", TLSCert: certPath, TLSKey: keyPath}, false},
		{"wildcard_trust_proxy_ok", Web{Listen: "0.0.0.0:9999", TrustProxy: []string{"127.0.0.1/32"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.web.RequireTransportSecurity()
			if tc.wantErr && err == nil {
				t.Fatal("want error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErr && !strings.Contains(err.Error(), "plaintext") {
				t.Errorf("error lacks 'plaintext' keyword: %v", err)
			}
		})
	}
}

func TestVerifyTLSFiles(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, []byte("cert"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte("key"), 0600); err != nil {
		t.Fatal(err)
	}
	missingCert := filepath.Join(dir, "missing-cert.pem")
	missingKey := filepath.Join(dir, "missing-key.pem")
	subdir := filepath.Join(dir, "a-directory")
	if err := os.Mkdir(subdir, 0700); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name       string
		cert, key  string
		wantErr    bool
		wantSubstr string // substring the error must contain
	}{
		{"both_exist", certPath, keyPath, false, ""},
		{"cert_missing", missingCert, keyPath, true, "web.tls_cert"},
		{"key_missing", certPath, missingKey, true, "web.tls_key"},
		{"both_missing", missingCert, missingKey, true, "web.tls_cert"},
		{"cert_is_directory", subdir, keyPath, true, "directory"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := Web{Listen: "0.0.0.0:9999", TLSCert: tc.cert, TLSKey: tc.key}
			err := w.RequireTransportSecurity()
			if tc.wantErr && err == nil {
				t.Fatal("want error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErr && !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantSubstr)
			}
		})
	}
}

func TestTrustProxyCIDRValidation(t *testing.T) {
	cfg := &Config{
		Version: CurrentVersion,
		Web: Web{
			Listen:     "127.0.0.1:9999",
			TrustProxy: []string{"not-a-cidr"},
		},
	}
	if err := validate(cfg); err == nil || !strings.Contains(err.Error(), "trust_proxy") {
		t.Fatalf("want trust_proxy validation error, got %v", err)
	}
}
