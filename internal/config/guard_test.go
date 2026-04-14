package config

import (
	"strings"
	"testing"
)

func TestRequireTransportSecurity(t *testing.T) {
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
		{"wildcard_tls_ok", Web{Listen: "0.0.0.0:9999", TLSCert: "c", TLSKey: "k"}, false},
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
