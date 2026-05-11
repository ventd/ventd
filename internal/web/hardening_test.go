package web

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/config"
)

// TestHandleInstall_BodyValidation pins issue #1062 (web H1): the install
// handlers (kernel-headers, DKMS, AppArmor, reset-and-reinstall, grub-cmdline-
// add) must validate Content-Type and JSON shape; a malformed body must
// surface as 400 rather than silently falling through to defaults.
func TestHandleInstall_BodyValidation(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	cases := []struct {
		name        string
		path        string
		handler     http.HandlerFunc
		body        string
		contentType string
		wantStatus  int
	}{
		{
			name:        "kernel_headers_rejects_nonjson",
			path:        "/api/hwdiag/install-kernel-headers",
			handler:     srv.handleInstallKernelHeaders,
			body:        "not json",
			contentType: "application/json",
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "kernel_headers_rejects_wrong_content_type",
			path:        "/api/hwdiag/install-kernel-headers",
			handler:     srv.handleInstallKernelHeaders,
			body:        `{}`,
			contentType: "text/plain",
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "dkms_rejects_unknown_fields",
			path:        "/api/hwdiag/install-dkms",
			handler:     srv.handleInstallDKMS,
			body:        `{"module": "it87"}`,
			contentType: "application/json",
			wantStatus:  http.StatusBadRequest, // handler takes no body fields
		},
		{
			name:        "reset_and_reinstall_rejects_malformed_json",
			path:        "/api/hwdiag/reset-and-reinstall",
			handler:     srv.handleResetAndReinstall,
			body:        `{module: it87`,
			contentType: "application/json",
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "reset_and_reinstall_rejects_unknown_fields",
			path:        "/api/hwdiag/reset-and-reinstall",
			handler:     srv.handleResetAndReinstall,
			body:        `{"module":"it87","extra":"oops"}`,
			contentType: "application/json",
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "grub_cmdline_rejects_malformed_json",
			path:        "/api/hwdiag/grub-cmdline-add",
			handler:     srv.handleGrubCmdlineAdd,
			body:        `garbage`,
			contentType: "application/json",
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "load_apparmor_rejects_body_fields",
			path:        "/api/hwdiag/load-apparmor",
			handler:     srv.handleLoadAppArmor,
			body:        `{"profile":"ventd"}`,
			contentType: "application/json",
			wantStatus:  http.StatusBadRequest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", tc.contentType)
			req.ContentLength = int64(len(tc.body))
			w := httptest.NewRecorder()
			tc.handler(w, req)
			if got := w.Result().StatusCode; got != tc.wantStatus {
				t.Errorf("%s: status = %d, want %d (body=%q)", tc.name, got, tc.wantStatus, w.Body.String())
			}
		})
	}
}

// TestHandleSetupReset_KVWipeFailure_ReturnsErrorAndKeepsConfig pins issue
// #1063 (web H2): when kvWiper returns an error the handler MUST return 500
// AND MUST NOT have deleted the config file. The previous order deleted
// config first then logged the wipe failure as a Warn — leaving the system
// in a half-reset state.
func TestHandleSetupReset_KVWipeFailure_ReturnsErrorAndKeepsConfig(t *testing.T) {
	srv, configPath, cancel := newHandlerHarness(t)
	defer cancel()

	if err := os.WriteFile(configPath, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	srv.SetKVWiper(func() error { return errors.New("simulated KV wipe failure") })

	req := httptest.NewRequest(http.MethodPost, "/api/setup/reset", nil)
	w := httptest.NewRecorder()
	srv.handleSetupReset(w, req)

	if got := w.Result().StatusCode; got != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 (body=%q)", got, w.Body.String())
	}
	if _, err := os.Stat(configPath); err != nil {
		t.Errorf("config was removed despite KV wipe failure: %v", err)
	}
}

// TestHandleFactoryReset_KVWipeFailure_ReturnsErrorAndKeepsConfig pins the
// same web H2 invariant on the factory-reset endpoint.
func TestHandleFactoryReset_KVWipeFailure_ReturnsErrorAndKeepsConfig(t *testing.T) {
	srv, configPath, cancel := newHandlerHarness(t)
	defer cancel()

	if err := os.WriteFile(configPath, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	srv.SetKVWiper(func() error { return errors.New("simulated factory-reset KV wipe failure") })

	req := httptest.NewRequest(http.MethodPost, "/api/admin/factory-reset", nil)
	w := httptest.NewRecorder()
	srv.handleFactoryReset(w, req)

	if got := w.Result().StatusCode; got != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 (body=%q)", got, w.Body.String())
	}
	if _, err := os.Stat(configPath); err != nil {
		t.Errorf("config was removed despite KV wipe failure: %v", err)
	}
}

// TestHandleSystemReboot_CallsRestoreBeforeReboot pins issue #1064 (web H3):
// the reboot goroutine MUST invoke the watchdog Restore hook before firing
// systemctl reboot. RULE-HWMON-RESTORE-EXIT says every documented exit path
// restores firmware control before the kernel-init sequence — the reboot
// path is one of those exits.
func TestHandleSystemReboot_CallsRestoreBeforeReboot(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	// Override the environment guard so the handler doesn't refuse on a
	// CI container.
	srv.rebootBlocker = func() string { return "" }

	var order []string
	srv.SetRebootRestore(func(_ context.Context) { order = append(order, "restore") })
	srv.rebootExec = func() { order = append(order, "reboot") }

	req := httptest.NewRequest(http.MethodPost, "/api/system/reboot", nil)
	w := httptest.NewRecorder()
	srv.handleSystemReboot(w, req)

	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%q)", got, w.Body.String())
	}

	// Wait for the goroutine to complete: 300 ms initial delay + Restore
	// callback. Bounded by 2 s.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(order) == 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(order) != 2 {
		t.Fatalf("reboot goroutine did not complete; observed=%v", order)
	}
	if order[0] != "restore" || order[1] != "reboot" {
		t.Errorf("ordering wrong: got %v, want [restore reboot] (RULE-HWMON-RESTORE-EXIT)", order)
	}
}

// TestHandleSystemReboot_PanicInExec_RecoveredCleanly pins the panic-recover
// half of issue #1064: a panic inside the reboot goroutine MUST be recovered
// so the daemon continues rather than crashing with no diagnostic surface.
func TestHandleSystemReboot_PanicInExec_RecoveredCleanly(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	srv.rebootBlocker = func() string { return "" }

	srv.SetRebootRestore(func(_ context.Context) {})
	panicked := make(chan struct{}, 1)
	srv.rebootExec = func() {
		defer func() {
			// Signal that the panic actually fired (and was caught) so
			// the test can observe the recovery rather than time out.
			panicked <- struct{}{}
		}()
		panic("simulated reboot exec panic")
	}

	req := httptest.NewRequest(http.MethodPost, "/api/system/reboot", nil)
	w := httptest.NewRecorder()
	srv.handleSystemReboot(w, req)

	// Wait for the goroutine to land in the panicking branch. If recover
	// fails this test panics out via the goroutine's runtime crash; the
	// 2s deadline is the upper-bound safety net.
	select {
	case <-panicked:
	case <-time.After(2 * time.Second):
		t.Fatal("reboot goroutine never executed the rebootExec stub")
	}

	// Daemon survives: the test goroutine is still here, so the recover
	// worked. Nothing else to assert structurally.
}

// TestHandleSystemReboot_RefusedWithActiveManualOverride pins the manual-
// override guard half of issue #1064: rebooting while an operator has a
// fan pinned via Control.ManualPWM silently discards that intent. The
// handler returns 409 so the UI can surface a human explanation.
func TestHandleSystemReboot_RefusedWithActiveManualOverride(t *testing.T) {
	srv, _, cancel := newHandlerHarness(t)
	defer cancel()

	srv.rebootBlocker = func() string { return "" }

	manual := uint8(128)
	cfg := &config.Config{
		Fans: []config.Fan{{Name: "cpu fan", Type: "hwmon", PWMPath: "/sys/class/hwmon/hwmon0/pwm1"}},
		Controls: []config.Control{
			{Fan: "cpu fan", Curve: "cpu_curve", ManualPWM: &manual},
		},
	}
	var p atomic.Pointer[config.Config]
	p.Store(cfg)
	srv.cfg = &p

	req := httptest.NewRequest(http.MethodPost, "/api/system/reboot", nil)
	w := httptest.NewRecorder()
	srv.handleSystemReboot(w, req)

	if got := w.Result().StatusCode; got != http.StatusConflict {
		t.Errorf("status = %d, want 409 (body=%q)", got, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("manual override")) {
		t.Errorf("body = %q, want substring %q", w.Body.String(), "manual override")
	}
}
