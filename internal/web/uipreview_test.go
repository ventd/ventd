//go:build uipreview

// Package web local UI preview harness. Not a real test — it stands
// up a web.Server on http://127.0.0.1:9990/ with a seeded dashboard
// (one control, a couple of fans, a couple of sensors) so responsive
// CSS work can be eyeballed at real phone/tablet/desktop viewports in
// a browser. Blocks on SIGINT/SIGTERM; kill with Ctrl-C.
//
// Run from the repo root:
//   go test -tags uipreview -run TestPreviewServer -count=1 -timeout 1h ./internal/web/
//
// Gated by the `uipreview` build tag so it never runs in CI or a
// normal `go test ./...`. Live in internal/web/ so it can reach the
// unexported HashPassword/New helpers without widening their
// visibility.

package web

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/ventd/ventd/internal/calibrate"
	"github.com/ventd/ventd/internal/config"
	"github.com/ventd/ventd/internal/hwdiag"
	setupmgr "github.com/ventd/ventd/internal/setup"
)

func TestPreviewServer(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	password := "preview"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	live := config.Empty()
	live.Web.PasswordHash = hash
	live.Controls = []config.Control{
		{Fan: "cpu-fan", Curve: ""},
		{Fan: "sys-fan-1", Curve: ""},
		{Fan: "sys-fan-2", Curve: ""},
	}
	live.Fans = []config.Fan{
		{Name: "cpu-fan", Type: "hwmon", PWMPath: "/tmp/nonexistent/pwm1"},
		{Name: "sys-fan-1", Type: "hwmon", PWMPath: "/tmp/nonexistent/pwm2"},
		{Name: "sys-fan-2", Type: "hwmon", PWMPath: "/tmp/nonexistent/pwm3"},
	}
	live.Sensors = []config.Sensor{
		{Name: "CPU Temperature", Type: "hwmon", Path: "/tmp/nonexistent/temp1_input"},
		{Name: "Motherboard Temperature", Type: "hwmon", Path: "/tmp/nonexistent/temp2_input"},
	}
	live.Curves = []config.CurveConfig{}

	var cfgPtr atomic.Pointer[config.Config]
	cfgPtr.Store(live)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cal := calibrate.New(t.TempDir()+"/cal.json", logger, nil)
	sm := setupmgr.New(cal, logger)
	restart := make(chan struct{}, 1)

	srv := New(ctx, &cfgPtr, t.TempDir()+"/config.yaml", "", logger, cal, sm, restart, "", hwdiag.NewStore())

	fmt.Println("==========================================")
	fmt.Println("ventd UI preview running at:")
	fmt.Println("  http://127.0.0.1:9990/")
	fmt.Printf("  password: %s\n", password)
	fmt.Println("Ctrl-C to stop.")
	fmt.Println("==========================================")

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe("127.0.0.1:9990", "", "")
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-stop:
		fmt.Println("stopping preview server")
	case err := <-errCh:
		t.Fatalf("ListenAndServe: %v", err)
	}
}
