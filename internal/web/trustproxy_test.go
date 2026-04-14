package web

import (
	"io"
	"log/slog"
	"net"
	"net/http/httptest"
	"testing"
)

func mustCIDR(t *testing.T, s string) *net.IPNet {
	t.Helper()
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return n
}

func TestResolveClientIP(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	_ = logger
	trusted := []*net.IPNet{
		mustCIDR(t, "127.0.0.1/32"),
		mustCIDR(t, "10.0.0.0/8"),
	}

	cases := []struct {
		name       string
		remoteAddr string
		xff        string
		proxies    []*net.IPNet
		want       string
	}{
		{"no_proxies_ignore_xff", "1.2.3.4:5555", "9.9.9.9", nil, "1.2.3.4"},
		{"untrusted_peer_ignore_xff", "8.8.8.8:5555", "9.9.9.9", trusted, "8.8.8.8"},
		{"trusted_peer_no_xff", "127.0.0.1:5555", "", trusted, "127.0.0.1"},
		{"trusted_peer_single_untrusted_xff", "127.0.0.1:5555", "203.0.113.5", trusted, "203.0.113.5"},
		{"trusted_peer_chain_real_client", "127.0.0.1:5555", "203.0.113.5, 10.0.0.7", trusted, "203.0.113.5"},
		{"trusted_peer_attacker_prepends", "127.0.0.1:5555", "1.1.1.1, 203.0.113.5, 10.0.0.7", trusted, "203.0.113.5"},
		{"trusted_peer_all_trusted_fallback_leftmost", "127.0.0.1:5555", "10.0.0.1, 10.0.0.2", trusted, "10.0.0.1"},
		{"trusted_peer_malformed_xff_falls_back", "127.0.0.1:5555", "not-an-ip", trusted, "127.0.0.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tc.remoteAddr
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			got := resolveClientIP(r, tc.proxies)
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestParseTrustedProxiesSkipsInvalid(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	out := parseTrustedProxies([]string{"127.0.0.1/32", "bogus", "10.0.0.0/8"}, logger)
	if len(out) != 2 {
		t.Fatalf("got %d entries, want 2", len(out))
	}
}
