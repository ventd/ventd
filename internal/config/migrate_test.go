package config

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// silentLogger discards every record — keeps test output clean while
// still exercising the log/slog plumbing the production call path takes.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// withFakeTLSFS replaces the file-reading + keypair-parsing hook used by
// migrateTLSPaths for the duration of t. Returns the restore in case a
// caller needs to sequence multiple overrides.
func withFakeTLSFS(t *testing.T, fn func(certPath, keyPath string) ([]byte, []byte, error)) {
	t.Helper()
	prev := SetTLSMigrationFS(fn)
	t.Cleanup(func() { SetTLSMigrationFS(prev) })
}

// TestMigrate_TLSBothPresent covers the canonical upgrade path:
// first-boot produced a keypair at <configDir>/tls.{crt,key}, a later
// Save() stripped the fields, the v0.2.0 binary with
// RequireTransportSecurity now refuses to start. Migrate repopulates.
func TestMigrate_TLSBothPresent(t *testing.T) {
	withFakeTLSFS(t, func(certPath, keyPath string) ([]byte, []byte, error) {
		if certPath != "/etc/ventd/tls.crt" {
			t.Fatalf("unexpected certPath %q", certPath)
		}
		if keyPath != "/etc/ventd/tls.key" {
			t.Fatalf("unexpected keyPath %q", keyPath)
		}
		return []byte("fake-cert"), []byte("fake-key"), nil
	})

	cfg := &Config{Web: Web{Listen: "0.0.0.0:9999"}}
	mutated, err := Migrate(cfg, "/etc/ventd/config.yaml", silentLogger())
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !mutated {
		t.Fatal("expected mutated=true when keypair is present and fields are empty")
	}
	if got, want := cfg.Web.TLSCert, "/etc/ventd/tls.crt"; got != want {
		t.Errorf("TLSCert = %q, want %q", got, want)
	}
	if got, want := cfg.Web.TLSKey, "/etc/ventd/tls.key"; got != want {
		t.Errorf("TLSKey = %q, want %q", got, want)
	}
}

// TestMigrate_TLSNeitherPresent covers the fresh-install / no-keypair
// case. Migrate should not invent a path, must leave the cfg untouched,
// and must not return an error — "nothing to migrate" is a normal
// steady state, not a failure.
func TestMigrate_TLSNeitherPresent(t *testing.T) {
	withFakeTLSFS(t, func(certPath, keyPath string) ([]byte, []byte, error) {
		return nil, nil, os.ErrNotExist
	})

	cfg := &Config{Web: Web{Listen: "0.0.0.0:9999"}}
	mutated, err := Migrate(cfg, "/etc/ventd/config.yaml", silentLogger())
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if mutated {
		t.Error("expected mutated=false when neither keypair file exists")
	}
	if cfg.Web.TLSCert != "" || cfg.Web.TLSKey != "" {
		t.Errorf("TLS fields populated when they should have stayed empty: cert=%q key=%q",
			cfg.Web.TLSCert, cfg.Web.TLSKey)
	}
}

// TestMigrate_TLSOnlyCertPresent is the half-pair footgun case: the key
// file is missing (or vice versa). Migrate must not populate either
// field — leaving an orphan TLSCert would cause the daemon to crash at
// keypair load time with a worse error message than the transport-
// security guard's message.
func TestMigrate_TLSOnlyCertPresent(t *testing.T) {
	withFakeTLSFS(t, func(certPath, keyPath string) ([]byte, []byte, error) {
		// Simulate: cert readable, key missing. defaultTLSMigrationFS
		// returns the aggregate "can't validate the pair" error.
		return nil, nil, os.ErrNotExist
	})

	cfg := &Config{Web: Web{Listen: "0.0.0.0:9999"}}
	mutated, err := Migrate(cfg, "/etc/ventd/config.yaml", silentLogger())
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if mutated {
		t.Error("expected mutated=false when only one of the two files exists")
	}
	if cfg.Web.TLSCert != "" || cfg.Web.TLSKey != "" {
		t.Errorf("partial TLS fields populated: cert=%q key=%q",
			cfg.Web.TLSCert, cfg.Web.TLSKey)
	}
}

// TestMigrate_TLSAlreadySetNoOp guards against overwriting an operator-
// or wizard-written value. If TLSCert or TLSKey is already set, Migrate
// must leave the cfg alone and must not even probe the filesystem — we
// should see no call-through to the injected reader.
func TestMigrate_TLSAlreadySetNoOp(t *testing.T) {
	called := false
	withFakeTLSFS(t, func(certPath, keyPath string) ([]byte, []byte, error) {
		called = true
		return []byte("cert"), []byte("key"), nil
	})

	for _, tc := range []struct {
		name string
		cert string
		key  string
	}{
		{name: "both_set", cert: "/my/cert.pem", key: "/my/key.pem"},
		{name: "cert_only_set", cert: "/my/cert.pem", key: ""},
		{name: "key_only_set", cert: "", key: "/my/key.pem"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called = false
			cfg := &Config{Web: Web{
				Listen:  "0.0.0.0:9999",
				TLSCert: tc.cert,
				TLSKey:  tc.key,
			}}
			mutated, err := Migrate(cfg, "/etc/ventd/config.yaml", silentLogger())
			if err != nil {
				t.Fatalf("Migrate: %v", err)
			}
			if mutated {
				t.Errorf("mutated=true; expected no-op when operator already set cert=%q key=%q",
					tc.cert, tc.key)
			}
			if cfg.Web.TLSCert != tc.cert || cfg.Web.TLSKey != tc.key {
				t.Errorf("operator-set fields overwritten: cert=%q→%q, key=%q→%q",
					tc.cert, cfg.Web.TLSCert, tc.key, cfg.Web.TLSKey)
			}
			if called {
				t.Error("filesystem probed when TLS field(s) were already set — waste of a syscall")
			}
		})
	}
}

// TestMigrate_TLSFileUnreadable covers the malformed-keypair and
// permission-denied cases. Both should be "nothing to migrate" rather
// than Migrate returning an error: the config file itself is fine, and
// the downstream transport-security guard is a better place to surface
// the real problem.
func TestMigrate_TLSFileUnreadable(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "permission_denied", err: os.ErrPermission},
		{name: "parse_error", err: errors.New("x509: malformed certificate")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withFakeTLSFS(t, func(certPath, keyPath string) ([]byte, []byte, error) {
				return nil, nil, tc.err
			})
			cfg := &Config{Web: Web{Listen: "0.0.0.0:9999"}}
			mutated, err := Migrate(cfg, "/etc/ventd/config.yaml", silentLogger())
			if err != nil {
				t.Fatalf("Migrate should not propagate %v", err)
			}
			if mutated {
				t.Errorf("mutated=true for unreadable/unparsable keypair (%v)", tc.err)
			}
			if cfg.Web.TLSCert != "" || cfg.Web.TLSKey != "" {
				t.Errorf("TLS fields populated despite unreadable keypair: cert=%q key=%q",
					cfg.Web.TLSCert, cfg.Web.TLSKey)
			}
		})
	}
}

// TestMigrate_NilConfigIsSafe mirrors the nil-guard in other config
// entry points (ResolveHwmonPaths, CheckResolvable). Callers that
// Parse()'d a file and got a pointer back shouldn't need to nil-check
// before handing it off.
func TestMigrate_NilConfigIsSafe(t *testing.T) {
	mutated, err := Migrate(nil, "/etc/ventd/config.yaml", silentLogger())
	if err != nil {
		t.Fatalf("Migrate(nil): %v", err)
	}
	if mutated {
		t.Error("Migrate(nil) reported mutated=true")
	}
}

// TestMigrate_DefaultLoggerAcceptsNil verifies the logger=nil branch
// doesn't nil-deref. This is the call shape cmd/ventd uses: Load()
// hands Migrate a nil logger, which falls back to slog.Default().
func TestMigrate_DefaultLoggerAcceptsNil(t *testing.T) {
	withFakeTLSFS(t, func(certPath, keyPath string) ([]byte, []byte, error) {
		return []byte("cert"), []byte("key"), nil
	})
	cfg := &Config{Web: Web{Listen: "0.0.0.0:9999"}}
	if _, err := Migrate(cfg, "/etc/ventd/config.yaml", nil); err != nil {
		t.Fatalf("Migrate with nil logger: %v", err)
	}
}

// TestMigrate_DerivesConfigDir is a sanity test against hand-written
// paths: Migrate builds tls.crt / tls.key paths from filepath.Dir of
// the configPath, not from a hardcoded /etc/ventd. A config in
// /tmp/testrig/foo.yaml should look for /tmp/testrig/tls.{crt,key}.
func TestMigrate_DerivesConfigDir(t *testing.T) {
	var sawCert, sawKey string
	withFakeTLSFS(t, func(certPath, keyPath string) ([]byte, []byte, error) {
		sawCert, sawKey = certPath, keyPath
		return nil, nil, os.ErrNotExist
	})
	cfgPath := filepath.Join("/tmp/testrig", "foo.yaml")
	cfg := &Config{Web: Web{Listen: "0.0.0.0:9999"}}
	if _, err := Migrate(cfg, cfgPath, silentLogger()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if got, want := sawCert, filepath.Join("/tmp/testrig", "tls.crt"); got != want {
		t.Errorf("certPath = %q, want %q", got, want)
	}
	if got, want := sawKey, filepath.Join("/tmp/testrig", "tls.key"); got != want {
		t.Errorf("keyPath = %q, want %q", got, want)
	}
}
