package web

import (
	"crypto/tls"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEnsureSelfSignedCertGeneratesWhenMissing(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "tls.crt")
	keyPath := filepath.Join(dir, "tls.key")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	fp, err := EnsureSelfSignedCert(certPath, keyPath, logger)
	if err != nil {
		t.Fatalf("EnsureSelfSignedCert: %v", err)
	}
	if fp == "" {
		t.Fatal("fingerprint empty")
	}

	// Loadable as a TLS keypair? That's the contract.
	kp, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatalf("LoadX509KeyPair: %v", err)
	}
	if len(kp.Certificate) == 0 {
		t.Fatal("no certificate loaded")
	}
	// Key file must be 0600.
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat key: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("key perms=%o want 0600", info.Mode().Perm())
	}
}

func TestEnsureSelfSignedCertLeavesExistingUntouched(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "tls.crt")
	keyPath := filepath.Join(dir, "tls.key")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	if _, err := EnsureSelfSignedCert(certPath, keyPath, logger); err != nil {
		t.Fatalf("first gen: %v", err)
	}
	certBefore, _ := os.ReadFile(certPath)
	mtimeBefore, _ := os.Stat(certPath)

	// Second call must not rewrite.
	time.Sleep(10 * time.Millisecond)
	if _, err := EnsureSelfSignedCert(certPath, keyPath, logger); err != nil {
		t.Fatalf("second gen: %v", err)
	}
	certAfter, _ := os.ReadFile(certPath)
	mtimeAfter, _ := os.Stat(certPath)

	if string(certBefore) != string(certAfter) {
		t.Fatal("cert was rewritten on second call")
	}
	if !mtimeBefore.ModTime().Equal(mtimeAfter.ModTime()) {
		t.Fatal("cert mtime changed on second call")
	}
}

func TestEnsureSelfSignedCertRefusesHalfPair(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "tls.crt")
	keyPath := filepath.Join(dir, "tls.key")
	if err := os.WriteFile(certPath, []byte("dummy"), 0o644); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := EnsureSelfSignedCert(certPath, keyPath, logger); err == nil {
		t.Fatal("want error when only cert exists, got nil")
	}
}
