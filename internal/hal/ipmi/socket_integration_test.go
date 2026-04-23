//go:build ipmi_integration

package ipmi_test

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ventd/ventd/internal/hal/ipmi/proto"
)

// TestSidecarIntegration_ProtoRoundtrip spawns ventd-ipmi under systemd-run
// with the hardening properties from deploy/ventd-ipmi.service, sends an
// ENUMERATE request over the Unix socket, and verifies the proto roundtrip.
// A negative subtest confirms that a process without device access cannot
// open /dev/ipmi0 directly.
//
// Skip conditions: must run as root; systemd-run must be in PATH; /dev/ipmi0
// must be present. In the CC sandbox none of these hold — the test skips
// cleanly.
func TestSidecarIntegration_ProtoRoundtrip(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skipf("requires root; uid=%d", os.Getuid())
	}
	if _, err := exec.LookPath("systemd-run"); err != nil {
		t.Skipf("systemd-run not available: %v", err)
	}
	if _, err := os.Stat("/dev/ipmi0"); err != nil {
		t.Skipf("/dev/ipmi0 not present: %v", err)
	}

	tmpDir := t.TempDir()
	binary := filepath.Join(tmpDir, "ventd-ipmi")

	build := exec.Command("go", "build", "-o", binary,
		"github.com/ventd/ventd/cmd/ventd-ipmi")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build ventd-ipmi: %v\n%s", err, out)
	}

	sockPath := filepath.Join(tmpDir, "ipmi.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Spawn sidecar under systemd-run --scope replicating the key hardening
	// from deploy/ventd-ipmi.service (capability grant + device allowlist).
	cmd := exec.CommandContext(ctx, "systemd-run",
		"--scope", "--quiet",
		"--property=AmbientCapabilities=CAP_SYS_RAWIO",
		"--property=CapabilityBoundingSet=CAP_SYS_RAWIO",
		"--property=NoNewPrivileges=yes",
		"--property=DeviceAllow=/dev/ipmi0 rw",
		"--",
		binary, "--socket", sockPath,
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sidecar under systemd-run: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	// Poll until the Unix socket appears (sidecar is ready to accept).
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sockPath); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if _, err := os.Stat(sockPath); err != nil {
		t.Fatalf("sidecar socket not created within 10s: %v", err)
	}

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial sidecar socket: %v", err)
	}
	defer conn.Close()

	codec := proto.NewCodec(conn, conn)

	t.Run("enumerate_roundtrip", func(t *testing.T) {
		req := &proto.Request{ReqID: 1, Op: proto.OpEnumerate}
		if err := codec.WriteRequest(req); err != nil {
			t.Fatalf("write ENUMERATE: %v", err)
		}
		resp, err := codec.ReadResponse()
		if err != nil {
			t.Fatalf("read ENUMERATE response: %v", err)
		}
		if resp.ReqID != 1 {
			t.Errorf("ReqID: got %d, want 1", resp.ReqID)
		}
		if !resp.OK {
			t.Errorf("ENUMERATE failed: %s", resp.Err)
		}
		var channels []proto.ChannelWire
		if err := json.Unmarshal(resp.Data, &channels); err != nil {
			t.Fatalf("unmarshal channel list: %v", err)
		}
		t.Logf("ENUMERATE returned %d channel(s)", len(channels))
	})

	// Negative test: a process without device access must not be able to open
	// /dev/ipmi0 directly. /dev/ipmi0 is typically mode 0600 root:root; a
	// process running as uid=65534 (nobody) is refused at open(), proving the
	// privilege boundary is enforced at the OS level and not merely cosmetic.
	t.Run("direct_open_without_privilege_returns_error", func(t *testing.T) {
		check := exec.Command("/bin/sh", "-c",
			"dd if=/dev/ipmi0 bs=1 count=1 of=/dev/null 2>&1; echo exit:$?")
		check.SysProcAttr = &syscall.SysProcAttr{
			Credential: &syscall.Credential{Uid: 65534, Gid: 65534},
		}
		out, _ := check.CombinedOutput()
		outStr := string(out)
		t.Logf("unprivileged open attempt: %q", outStr)
		if strings.Contains(outStr, "exit:0") {
			t.Errorf("expected failure opening /dev/ipmi0 as uid=65534, got success: %q", outStr)
		}
	})
}
