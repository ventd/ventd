package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"github.com/ventd/ventd/internal/preflight"
	"github.com/ventd/ventd/internal/preflight/checks"
)

// runPreflight implements the `ventd preflight` subcommand. It is the
// orchestrator-driven counterpart of the legacy `ventd
// --preflight-check` flag (which still works for back-compat: it
// emits a single Reason in JSON and exits 0). The new subcommand:
//
//   - Runs every check in the catalogue, not just the first blocker.
//   - In --interactive mode, walks the operator through Y/N prompts
//     and runs auto-fixes inline.
//   - Emits schema-versioned JSON with --json so install.sh can
//     consume the structured result.
//   - Exits 0 when all blockers cleared, 2 when blockers remain, 1
//     on internal error.
//
// Flags:
//
//	--interactive               Y/N prompt loop with inline auto-fix.
//	--auto-yes                  Answer Yes to every prompt (smoke tests).
//	--json                      Emit schema-versioned JSON to stdout.
//	--skip <name1,name2,...>    Exclude named checks.
//	--only <name1,name2,...>    Run only the named checks.
//	--target-module <name>      Override the OOT module name (default: nct6687).
//	--max-kernel <ver>          Set the driver's MaxSupportedKernel ceiling.
//	--port-addr <addr>          host:port the daemon will bind on first start.
//	--apparmor-profile <path>   Path to the shipped AppArmor profile (warn check).
//	--max-attempts <n>          Cap on per-check fix retries (default: 1).
func runPreflight(args []string, logger *slog.Logger) int {
	var (
		interactive     bool
		autoYes         bool
		emitJSON        bool
		skipList        string
		onlyList        string
		targetModule    string
		maxKernel       string
		portAddr        string
		apparmorProfile string
		maxAttempts     int
	)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--interactive":
			interactive = true
		case arg == "--auto-yes":
			autoYes = true
			interactive = true
		case arg == "--json":
			emitJSON = true
		case arg == "--skip" && i+1 < len(args):
			i++
			skipList = args[i]
		case strings.HasPrefix(arg, "--skip="):
			skipList = strings.TrimPrefix(arg, "--skip=")
		case arg == "--only" && i+1 < len(args):
			i++
			onlyList = args[i]
		case strings.HasPrefix(arg, "--only="):
			onlyList = strings.TrimPrefix(arg, "--only=")
		case arg == "--target-module" && i+1 < len(args):
			i++
			targetModule = args[i]
		case strings.HasPrefix(arg, "--target-module="):
			targetModule = strings.TrimPrefix(arg, "--target-module=")
		case arg == "--max-kernel" && i+1 < len(args):
			i++
			maxKernel = args[i]
		case strings.HasPrefix(arg, "--max-kernel="):
			maxKernel = strings.TrimPrefix(arg, "--max-kernel=")
		case arg == "--port-addr" && i+1 < len(args):
			i++
			portAddr = args[i]
		case strings.HasPrefix(arg, "--port-addr="):
			portAddr = strings.TrimPrefix(arg, "--port-addr=")
		case arg == "--apparmor-profile" && i+1 < len(args):
			i++
			apparmorProfile = args[i]
		case strings.HasPrefix(arg, "--apparmor-profile="):
			apparmorProfile = strings.TrimPrefix(arg, "--apparmor-profile=")
		case arg == "--max-attempts" && i+1 < len(args):
			i++
			fmt.Sscanf(args[i], "%d", &maxAttempts)
		case strings.HasPrefix(arg, "--max-attempts="):
			fmt.Sscanf(strings.TrimPrefix(arg, "--max-attempts="), "%d", &maxAttempts)
		case arg == "--help", arg == "-h":
			printPreflightUsage()
			return 0
		default:
			fmt.Fprintf(os.Stderr, "ventd preflight: unknown arg %q\n", arg)
			printPreflightUsage()
			return 1
		}
	}

	checkList := checks.Default(checks.DefaultOptions{
		TargetModule:        targetModule,
		MaxSupportedKernel:  maxKernel,
		PortAddr:            portAddr,
		AppArmorProfilePath: apparmorProfile,
	})

	var prompter preflight.Prompter
	if autoYes {
		prompter = &preflight.AutoYesPrompter{Out: os.Stdout}
	} else if interactive {
		prompter = preflight.NewStdPrompter()
	} else {
		// Non-interactive runs still need a prompter for the
		// summary writer; route output to stdout.
		prompter = preflight.NewIOPrompter(os.Stdin, os.Stdout)
	}

	report, runErr := preflight.Run(context.Background(), checkList, preflight.Options{
		Interactive:    interactive,
		Skip:           preflight.ParseList(skipList),
		Only:           preflight.ParseList(onlyList),
		MaxFixAttempts: maxAttempts,
		Prompter:       prompter,
		Logger:         logger,
	})

	if emitJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintf(os.Stderr, "encode: %v\n", err)
			return 1
		}
	}

	// In interactive mode, surface the reboot prompt once at the end
	// when any successful AutoFix queued one. The orchestrator
	// captures this via report.NeedsReboot.
	if interactive && report.NeedsReboot && !autoYes {
		fmt.Println()
		fmt.Println("One or more fixes require a reboot to take effect.")
		fmt.Println("After rebooting, confirm MOK enrollment at the blue MOK Manager screen:")
		fmt.Println("  1. Choose 'Enroll MOK'")
		fmt.Println("  2. Choose 'Continue'")
		fmt.Println("  3. Choose 'Yes' to enroll")
		fmt.Println("  4. Type the password you supplied during enrollment")
		fmt.Println("  5. Choose 'Reboot'")
		resp := preflight.NewStdPrompter().AskYN("Reboot now?")
		if resp == preflight.PromptYes {
			// Trigger the reboot via systemctl. We use --no-wall to
			// avoid spamming logged-in TTYs with the wall message
			// (the operator who answered Y is presumably aware they
			// asked for it). exec.Command without a context: we
			// want the reboot to outlive this process.
			fmt.Println("Initiating reboot in 3 seconds — Ctrl-C to cancel...")
			rebootCmd := exec.Command("systemctl", "reboot", "--no-wall")
			rebootCmd.Stdout = os.Stdout
			rebootCmd.Stderr = os.Stderr
			if err := rebootCmd.Start(); err != nil {
				// Fallback to /sbin/reboot for non-systemd hosts.
				if err2 := exec.Command("reboot").Run(); err2 != nil {
					fmt.Fprintf(os.Stderr, "could not reboot: systemctl: %v / reboot: %v\n", err, err2)
					fmt.Fprintln(os.Stderr, "Please reboot manually: sudo reboot")
					return 3
				}
			}
			return 3 // signals "preflight done, reboot requested"
		}
		fmt.Println("Reboot deferred. Run `sudo reboot` when ready, then confirm MOK at firmware.")
	}

	if runErr != nil {
		// Blockers remain. Exit 2 so install.sh can distinguish
		// "checks ran but found problems" from "internal error" (exit 1).
		return 2
	}
	return 0
}

func printPreflightUsage() {
	fmt.Print(`Usage: ventd preflight [flags]

Runs install-time precondition checks and (in --interactive mode) walks
the operator through Y/N-gated auto-fixes. Designed to run from
scripts/install.sh before the systemd unit is installed.

Flags:
  --interactive              Prompt Y/N for each fixable blocker.
  --auto-yes                 Answer Yes to every prompt (testing).
  --json                     Emit schema-versioned JSON to stdout.
  --skip <a,b,...>           Exclude named checks from the run.
  --only <a,b,...>           Restrict the run to the named checks.
  --target-module <name>     OOT module to install (default: nct6687).
  --max-kernel <ver>         Driver's MaxSupportedKernel ceiling.
  --port-addr <host:port>    Daemon's first-start listen address.
  --apparmor-profile <path>  Shipped AppArmor profile path (warn check).
  --max-attempts <n>         Per-check AutoFix retry cap (default: 1).

Exit codes:
  0  All checks passed.
  1  Internal error.
  2  One or more blockers remain (operator declined or fix failed).
  3  Fix queued; reboot requested.
`)
}
