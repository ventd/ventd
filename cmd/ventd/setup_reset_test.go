package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/web/authpersist"
)

// seedAuth writes a real auth.json (and via authpersist.Save's overwrite
// path, optionally a .bak) into dir and returns its path.
func seedAuth(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "auth.json")
	auth := &authpersist.Auth{Admin: authpersist.AdminCreds{
		Username:   "admin",
		BcryptHash: "$2a$10$abcdefghijklmnopqrstuvabcdefghijklmnopqrstuvabcdefghi",
		CreatedAt:  time.Now(),
	}}
	if err := authpersist.Save(path, auth); err != nil {
		t.Fatal(err)
	}
	return path
}

func noRestart(t *testing.T) func() error {
	return func() error {
		t.Fatal("restart called for a daemon that is not running")
		return nil
	}
}

// TestRunSetupReset binds RULE-CLI-SETUP-RESET: `ventd setup --reset`
// removes auth.json + its .bak, scrubs a legacy config password_hash so
// migrateAuthToFile cannot resurrect the forgotten password, leaves the
// rest of the config alone, and only restarts a daemon that is running.
func TestRunSetupReset(t *testing.T) {
	t.Run("removes auth.json and .bak", func(t *testing.T) {
		dir := t.TempDir()
		authPath := seedAuth(t, dir)
		if err := os.WriteFile(authPath+".bak", []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		var out, errb bytes.Buffer
		exit := runSetupReset(filepath.Join(dir, "config.yaml"), authPath, dir, noRestart(t), &out, &errb)
		if exit != setupResetExitOK {
			t.Fatalf("exit = %d, want 0; stderr: %s", exit, errb.String())
		}
		for _, p := range []string{authPath, authPath + ".bak"} {
			if _, err := os.Stat(p); !os.IsNotExist(err) {
				t.Errorf("%s still exists after reset", p)
			}
		}
		if !strings.Contains(out.String(), "password cleared") {
			t.Errorf("stdout missing confirmation: %q", out.String())
		}
		if !strings.Contains(out.String(), "not running") {
			t.Errorf("stdout should report the daemon is not running: %q", out.String())
		}
	})

	t.Run("scrubs legacy config password_hash", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "config.yaml")
		cfg := &config.Config{}
		cfg.Web.PasswordHash = "$2a$10$legacyhashlegacyhashlegacyhashlegacyhashlegacyhashlegac"
		if _, err := config.Save(cfg, cfgPath); err != nil {
			t.Fatal(err)
		}
		var out, errb bytes.Buffer
		exit := runSetupReset(cfgPath, filepath.Join(dir, "auth.json"), dir, noRestart(t), &out, &errb)
		if exit != setupResetExitOK {
			t.Fatalf("exit = %d, want 0; stderr: %s", exit, errb.String())
		}
		reloaded, err := config.Load(cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		if reloaded.Web.PasswordHash != "" {
			t.Error("legacy web.password_hash survived the reset — migrateAuthToFile would resurrect the old password")
		}
	})

	t.Run("idempotent when nothing is set", func(t *testing.T) {
		dir := t.TempDir()
		var out, errb bytes.Buffer
		exit := runSetupReset(filepath.Join(dir, "config.yaml"), filepath.Join(dir, "auth.json"), dir, noRestart(t), &out, &errb)
		if exit != setupResetExitOK {
			t.Fatalf("exit = %d, want 0; stderr: %s", exit, errb.String())
		}
		if !strings.Contains(out.String(), "nothing to reset") {
			t.Errorf("stdout should say nothing to reset: %q", out.String())
		}
	})

	t.Run("restarts a running daemon", func(t *testing.T) {
		dir := t.TempDir()
		authPath := seedAuth(t, dir)
		// This test process's own PID is alive, so RunningPID sees a live daemon.
		if err := os.WriteFile(filepath.Join(dir, "ventd.pid"), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o640); err != nil {
			t.Fatal(err)
		}
		restarted := false
		var out, errb bytes.Buffer
		exit := runSetupReset(filepath.Join(dir, "config.yaml"), authPath, dir,
			func() error { restarted = true; return nil }, &out, &errb)
		if exit != setupResetExitOK {
			t.Fatalf("exit = %d, want 0; stderr: %s", exit, errb.String())
		}
		if !restarted {
			t.Error("running daemon was not restarted — it would keep the old hash in memory")
		}
		if !strings.Contains(out.String(), "restarted") {
			t.Errorf("stdout missing restart confirmation: %q", out.String())
		}
	})
}

// TestRunSetupResetCLI_Usage: bare `ventd setup` (no --reset) is a usage
// error pointing at both this command and the interactive wizard flag —
// it must never fall through to daemon startup.
func TestRunSetupResetCLI_Usage(t *testing.T) {
	var out, errb bytes.Buffer
	exit := runSetupResetCLI(nil, &out, &errb)
	if exit != setupResetExitUsage {
		t.Fatalf("exit = %d, want %d", exit, setupResetExitUsage)
	}
	if !strings.Contains(errb.String(), "--reset") || !strings.Contains(errb.String(), "ventd --setup") {
		t.Errorf("usage text should mention --reset and the interactive wizard: %q", errb.String())
	}
}
