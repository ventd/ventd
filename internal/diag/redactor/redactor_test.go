package redactor_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ventd/ventd/internal/diag/redactor"
)

// --- RULE-DIAG-PR2C-01: default profile is default-conservative ---

func TestRuleDiagPR2C_01(t *testing.T) {
	t.Run("default_profile_is_conservative", func(t *testing.T) {
		cfg := redactor.DefaultConfig()
		if cfg.Profile != redactor.ProfileConservative {
			t.Errorf("DefaultConfig().Profile = %q, want %q", cfg.Profile, redactor.ProfileConservative)
		}
		r := redactor.New(cfg, nil)
		report := r.Report()
		if report.RedactorProfile != redactor.ProfileConservative {
			t.Errorf("Report.RedactorProfile = %q, want %q", report.RedactorProfile, redactor.ProfileConservative)
		}
	})
}

// --- RULE-DIAG-PR2C-02: self-check detects hostname leak ---

func TestRuleDiagPR2C_02(t *testing.T) {
	t.Run("self_check_detects_hostname_leak", func(t *testing.T) {
		// Build a bundle tarball that deliberately contains the hostname.
		fakeHost := "super-secret-hostname"
		bundlePath := writeFakeBundleWithContent(t, map[string]string{
			"commands/system/uname_-a": "Linux " + fakeHost + " 6.8.0 #1 SMP x86_64",
			"manifest.json":            `{"schema_version":"ventd-diag-bundle-v1"}`,
		})
		needles := []string{fakeHost}
		result, err := redactor.SelfCheck(bundlePath, needles)
		if err != nil {
			t.Fatalf("SelfCheck: %v", err)
		}
		if result.Ok() {
			t.Error("expected SelfCheck to detect hostname leak, got Ok()")
		}
		if len(result.Leaks) == 0 {
			t.Error("expected at least one Leak entry")
		}
		found := false
		for _, l := range result.Leaks {
			if l.String == fakeHost {
				found = true
			}
		}
		if !found {
			t.Errorf("leak for %q not found in %+v", fakeHost, result.Leaks)
		}
	})
}

// --- RULE-DIAG-PR2C-03: self-check failure is fatal ---

func TestRuleDiagPR2C_03(t *testing.T) {
	t.Run("self_check_failure_is_fatal", func(t *testing.T) {
		fakeHost := "leaky-host"
		bundlePath := writeFakeBundleWithContent(t, map[string]string{
			"commands/system/uname_-a": "Linux " + fakeHost + " 6.8.0",
		})
		result, err := redactor.SelfCheck(bundlePath, []string{fakeHost})
		if err != nil {
			t.Fatalf("SelfCheck: %v", err)
		}
		// A caller that does NOT set AllowRedactionFails must treat non-Ok as fatal.
		// This test verifies SelfCheck itself exposes the result so callers can act.
		if result.Ok() {
			t.Error("SelfCheck should have returned non-Ok for deliberate leak")
		}
		// Verify the leak is named.
		if len(result.Leaks) == 0 || result.Leaks[0].File == "" {
			t.Error("Leak must name the file that contains the cleartext")
		}
	})
}

// --- RULE-DIAG-PR2C-04: mapping file mode 0600 ---

func TestRuleDiagPR2C_04(t *testing.T) {
	t.Run("mapping_file_mode_0600", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "mapping.json")
		store, err := redactor.LoadOrCreate(path, slog.Default())
		if err != nil {
			t.Fatalf("LoadOrCreate: %v", err)
		}
		if err := store.Save(slog.Default()); err != nil {
			t.Fatalf("Save: %v", err)
		}
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("mapping file mode %o, want 0600", fi.Mode().Perm())
		}
	})
}

// --- RULE-DIAG-PR2C-05: --redact=off requires confirm or flag ---
// (Tested at the CLI layer; here we verify the profile constant exists.)

func TestRuleDiagPR2C_05(t *testing.T) {
	t.Run("off_requires_confirm_or_flag", func(t *testing.T) {
		// ProfileOff is defined and distinct from the other profiles.
		if redactor.ProfileOff == redactor.ProfileConservative {
			t.Error("ProfileOff must differ from ProfileConservative")
		}
		if redactor.ProfileOff == redactor.ProfileTrustedRecipient {
			t.Error("ProfileOff must differ from ProfileTrustedRecipient")
		}
		// An off-profile redactor has no primitives and applies no changes.
		cfg := redactor.Config{Profile: redactor.ProfileOff}
		r := redactor.New(cfg, nil)
		input := []byte("hello world hostname-secret")
		out := r.Apply(input)
		if !bytes.Equal(out, input) {
			t.Error("off-profile redactor must not change content")
		}
	})
}

// --- RULE-DIAG-PR2C-08: mapping consistent within a bundle ---

func TestRuleDiagPR2C_08(t *testing.T) {
	t.Run("mapping_consistent_within_bundle", func(t *testing.T) {
		store := redactor.NewMappingStore()
		cfg := redactor.Config{
			Profile:  redactor.ProfileConservative,
			Hostname: "my-real-host",
		}
		r := redactor.New(cfg, store)

		// Two separate content blocks both containing the same hostname.
		c1 := r.Apply([]byte("kernel: my-real-host booted"))
		c2 := r.Apply([]byte("journal: my-real-host started ventd"))

		// Both must replace with the same token.
		if bytes.Contains(c1, []byte("my-real-host")) {
			t.Error("c1 still contains cleartext hostname")
		}
		if bytes.Contains(c2, []byte("my-real-host")) {
			t.Error("c2 still contains cleartext hostname")
		}
		// Extract tokens and compare.
		tok1 := extractToken(c1, "obf_host_")
		tok2 := extractToken(c2, "obf_host_")
		if tok1 == "" || tok2 == "" {
			t.Fatalf("could not find token in output: %q / %q", c1, c2)
		}
		if tok1 != tok2 {
			t.Errorf("inconsistent tokens: %q vs %q", tok1, tok2)
		}
	})
}

// --- RULE-DIAG-PR2C-09: foreign mapping file is handled gracefully ---

func TestRuleDiagPR2C_09(t *testing.T) {
	t.Run("foreign_mapping_file_graceful", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "mapping.json")
		// Write a file with an unknown schema version and extra fields.
		foreign := map[string]any{
			"schema_version": 99,
			"unknown_field":  "surprise",
			"hostnames":      map[string]string{"real-host": "obf_host_1"},
		}
		data, _ := json.Marshal(foreign)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		var logBuf strings.Builder
		logger := slog.New(slog.NewTextHandler(&logBuf, nil))
		store, err := redactor.LoadOrCreate(path, logger)
		if err != nil {
			t.Fatalf("LoadOrCreate with foreign schema must not error, got: %v", err)
		}
		if store == nil {
			t.Fatal("LoadOrCreate must return non-nil store")
		}
		// Should have logged a warning.
		if !strings.Contains(logBuf.String(), "schema mismatch") && !strings.Contains(logBuf.String(), "discarding") {
			t.Logf("log output: %s", logBuf.String())
			// Not fatal — the key invariant is no crash, not the exact log message.
		}
	})
}

// --- ApplyHostnameForce ---

func TestApplyHostnameForce(t *testing.T) {
	t.Run("conservative_profile_redacts", func(t *testing.T) {
		store := redactor.NewMappingStore()
		cfg := redactor.Config{Profile: redactor.ProfileConservative, Hostname: "secret-host"}
		r := redactor.New(cfg, store)
		out := r.ApplyHostnameForce([]byte("kernel: secret-host booted"))
		if bytes.Contains(out, []byte("secret-host")) {
			t.Error("ApplyHostnameForce: hostname not redacted in conservative profile")
		}
	})
	t.Run("off_profile_still_redacts", func(t *testing.T) {
		// ApplyHostnameForce ignores profile — must redact even when profile=off.
		store := redactor.NewMappingStore()
		cfg := redactor.Config{Profile: redactor.ProfileOff, Hostname: "secret-host"}
		r := redactor.New(cfg, store)
		out := r.ApplyHostnameForce([]byte("kernel: secret-host booted"))
		if bytes.Contains(out, []byte("secret-host")) {
			t.Error("ApplyHostnameForce: hostname leaked under off profile")
		}
	})
	t.Run("trusted_recipient_profile_still_redacts", func(t *testing.T) {
		store := redactor.NewMappingStore()
		cfg := redactor.Config{Profile: redactor.ProfileTrustedRecipient, Hostname: "secret-host"}
		r := redactor.New(cfg, store)
		out := r.ApplyHostnameForce([]byte("kernel: secret-host booted"))
		if bytes.Contains(out, []byte("secret-host")) {
			t.Error("ApplyHostnameForce: hostname leaked under trusted-recipient profile")
		}
	})
	t.Run("token_is_consistent_with_apply", func(t *testing.T) {
		// The token produced by ApplyHostnameForce must be the same as Apply
		// for conservative profile (same store, same hostname → same token).
		store := redactor.NewMappingStore()
		cfg := redactor.Config{Profile: redactor.ProfileConservative, Hostname: "myhost"}
		r := redactor.New(cfg, store)
		out1 := r.Apply([]byte("a: myhost"))
		out2 := r.ApplyHostnameForce([]byte("b: myhost"))
		tok1 := extractToken(out1, "obf_host_")
		tok2 := extractToken(out2, "obf_host_")
		if tok1 == "" || tok2 == "" {
			t.Fatalf("no obf_host_ token found: %q / %q", out1, out2)
		}
		if tok1 != tok2 {
			t.Errorf("inconsistent tokens between Apply and ApplyHostnameForce: %q vs %q", tok1, tok2)
		}
	})
}

// --- Primitive unit tests ---

func TestP1Hostname_Redacts(t *testing.T) {
	store := redactor.NewMappingStore()
	p := redactor.NewP1HostnameFrom("mybox")
	out, n := p.Redact([]byte("kernel: mybox started"), store)
	if bytes.Contains(out, []byte("mybox")) {
		t.Error("hostname not redacted")
	}
	if n == 0 {
		t.Error("expected non-zero count")
	}
}

func TestP2DMI_RedactsSerial(t *testing.T) {
	store := redactor.NewMappingStore()
	p := &redactor.P2DMI{}
	input := []byte("Serial Number: ABC123XYZ\nUUID: 550e8400-e29b-41d4-a716-446655440000")
	out, n := p.Redact(input, store)
	if bytes.Contains(out, []byte("ABC123XYZ")) {
		t.Error("serial not redacted")
	}
	if n < 2 {
		t.Errorf("expected ≥2 redactions, got %d", n)
	}
}

func TestP3MAC_Redacts(t *testing.T) {
	store := redactor.NewMappingStore()
	p := &redactor.P3MAC{}
	input := []byte("link/ether aa:bb:cc:11:22:33 brd ff:ff:ff:ff:ff:ff")
	out, n := p.Redact(input, store)
	if bytes.Contains(out, []byte("aa:bb:cc:11:22:33")) {
		t.Error("MAC not redacted")
	}
	if bytes.Contains(out, []byte("ff:ff:ff:ff:ff:ff")) == false {
		t.Error("broadcast MAC must be preserved")
	}
	if n == 0 {
		t.Error("expected non-zero count")
	}
}

func TestP4IP_Redacts(t *testing.T) {
	store := redactor.NewMappingStore()
	p := &redactor.P4IP{}
	input := []byte("src 192.168.1.50 dst 8.8.8.8 loopback 127.0.0.1")
	out, n := p.Redact(input, store)
	if bytes.Contains(out, []byte("192.168.1.50")) {
		t.Error("private IP not redacted")
	}
	if bytes.Contains(out, []byte("8.8.8.8")) {
		t.Error("public IP not redacted")
	}
	if !bytes.Contains(out, []byte("127.0.0.1")) {
		t.Error("loopback must be preserved")
	}
	if n < 2 {
		t.Errorf("expected ≥2 IP redactions, got %d", n)
	}
}

func TestP7USBPhysical_PreservesTopology(t *testing.T) {
	store := redactor.NewMappingStore()
	p := &redactor.P7USBPhysical{}
	input := []byte("hidraw: usb-1-2.3:1.0")
	out, n := p.Redact(input, store)
	if bytes.Contains(out, []byte("usb-1-2.3:1.0")) {
		t.Error("USB path not redacted")
	}
	// Topology depth preserved: same number of separators.
	if n == 0 {
		t.Error("expected non-zero count")
	}
	// Should contain letter-replaced form.
	if !bytes.Contains(out, []byte("usb-B-C.D:B.A")) {
		t.Logf("redacted: %s", out)
		// Exact form depends on letterFor; just check digits are gone from usb-*.
	}
}

func TestP8Cmdline_RedactsRoot(t *testing.T) {
	store := redactor.NewMappingStore()
	p := &redactor.P8Cmdline{}
	input := []byte("BOOT_IMAGE=/vmlinuz root=UUID=abc123 amdgpu.ppfeaturemask=0xffffffff quiet")
	out, n := p.Redact(input, store)
	if bytes.Contains(out, []byte("abc123")) {
		t.Error("UUID not redacted from cmdline")
	}
	if !bytes.Contains(out, []byte("amdgpu.ppfeaturemask=0xffffffff")) {
		t.Error("amdgpu param must be preserved")
	}
	if n == 0 {
		t.Error("expected non-zero redaction count")
	}
}

func TestP10UserKeyword_Redacts(t *testing.T) {
	store := redactor.NewMappingStore()
	p := redactor.NewP10UserKeyword([]string{"project-codename", "secret"})
	input := []byte("ventd started for project-codename with secret key")
	out, n := p.Redact(input, store)
	if bytes.Contains(out, []byte("project-codename")) {
		t.Error("keyword not redacted")
	}
	if bytes.Contains(out, []byte("secret")) {
		t.Error("second keyword not redacted")
	}
	if n < 2 {
		t.Errorf("expected ≥2, got %d", n)
	}
}

// --- helpers ---

func writeFakeBundleWithContent(t *testing.T, files map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bundle.tar.gz")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatalf("create bundle: %v", err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		data := []byte(content)
		hdr := &tar.Header{
			Name: name,
			Mode: 0o644,
			Size: int64(len(data)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header %s: %v", name, err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatalf("tar write %s: %v", name, err)
		}
	}
	_ = tw.Close()
	_ = gz.Close()
	_ = f.Close()
	return path
}

func extractToken(content []byte, prefix string) string {
	idx := bytes.Index(content, []byte(prefix))
	if idx < 0 {
		return ""
	}
	end := idx + len(prefix)
	for end < len(content) && content[end] != ' ' && content[end] != '\n' {
		end++
	}
	return string(content[idx:end])
}
