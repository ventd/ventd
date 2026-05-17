// Command orchestrator-hil-probe runs the v0.8.x setup orchestrator's
// read-only phases (Inventory, ConflictHunt, Probe) plus DiscoverCPUSensor
// against the live host and prints each phase's artifact as JSON.
//
// Purpose: confirm on real hardware that the orchestrator code paths
// see the host's hwmon topology, DMI fields, and competing daemons
// the same way the legacy Manager.run path does. The probe is
// read-only — it never writes to /sys, never modprobes, never touches
// /etc/ventd/config.yaml. State directory defaults to
// /tmp/orchestrator-hil-state so the run does not collide with the
// production daemon's /var/lib/ventd/setup/.
//
// Run:
//
//	go run ./tools/orchestrator-hil-probe
//
// Flags:
//
//	-hwmon-root  override /sys/class/hwmon (for fixture-driven testing)
//	-state-dir   override /tmp/orchestrator-hil-state
//	-timeout     overall timeout (default 30s)
//
// The Polarity, Calibrate, Verify, and Apply phases are intentionally
// excluded — those drive real fans and would conflict with the running
// production daemon. Use `ventd --setup` (with VENTD_USE_ORCHESTRATOR=1
// once the env-gate flips on) to exercise the full chain.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/ventd/ventd/internal/setup/orchestrator"
)

type stdoutSink struct{}

func (stdoutSink) Emit(level, tag, text string) {
	fmt.Printf("  [%s/%s] %s\n", level, tag, text)
}

func main() {
	hwmonRoot := flag.String("hwmon-root", "/sys/class/hwmon",
		"sysfs hwmon root (override for fixture-driven testing)")
	stateDir := flag.String("state-dir", "/tmp/orchestrator-hil-state",
		"orchestrator state directory; do NOT point at /var/lib/ventd/setup/ on a host running ventd")
	timeout := flag.Duration("timeout", 30*time.Second,
		"overall probe timeout")
	flag.Parse()

	if err := os.MkdirAll(*stateDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "mkdir state:", err)
		os.Exit(1)
	}

	rc := &orchestrator.RunContext{
		Logger:    slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})),
		HwmonRoot: *hwmonRoot,
		ProcRoot:  "/proc",
		StateDir:  *stateDir,
		Events:    stdoutSink{},
	}

	// Read-only phases only. Polarity/Calibrate/Verify/Apply would
	// drive real fans and write /etc/ventd/config.yaml.
	o, err := orchestrator.New(rc,
		orchestrator.InventoryPhase{},
		orchestrator.ConflictHuntPhase{AutoStop: false, AutoStopVendor: false},
		orchestrator.ProbePhase{},
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orchestrator.New:", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	fmt.Println("=== HIL probe: Inventory + ConflictHunt + Probe + DiscoverCPUSensor ===")
	fmt.Println("    (read-only: no fan writes, no modprobes, no config writes)")
	fmt.Println()

	outs, err := o.Run(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Run:", err)
		os.Exit(1)
	}

	for _, out := range outs {
		fmt.Printf("\n--- Phase: %s ---\n", out.Phase)
		fmt.Printf("  Status: %s\n", out.Status)
		if out.Detail != "" {
			fmt.Printf("  Detail: %s\n", out.Detail)
		}
		if out.Class != "" {
			fmt.Printf("  Class:  %s\n", out.Class)
		}
		if len(out.Artifact) > 0 {
			var pretty map[string]any
			_ = json.Unmarshal(out.Artifact, &pretty)
			b, _ := json.MarshalIndent(pretty, "  ", "  ")
			fmt.Printf("  Artifact:\n  %s\n", string(b))
		}
	}

	fmt.Println("\n--- DiscoverCPUSensor ---")
	sensor := orchestrator.DiscoverCPUSensor(*hwmonRoot)
	b, _ := json.MarshalIndent(sensor, "  ", "  ")
	fmt.Printf("  %s\n", string(b))

	fmt.Println()
	fmt.Println("HIL probe complete. State dir:", *stateDir)
}
