package main

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/ventd/ventd/internal/diag"
	"github.com/ventd/ventd/internal/diag/redactor"
)

// runDiagBundle implements `ventd diag bundle`.
// RULE-DIAG-PR2C-01: default profile is default-conservative.
// RULE-DIAG-PR2C-05: --redact=off requires confirm or --i-understand-this-is-not-redacted.
func runDiagBundle(args []string, logger *slog.Logger) error {
	// Parse flags manually (flag package conflicts with main's FlagSet).
	var (
		outputDir        string
		redactProfile    = redactor.ProfileConservative // RULE-DIAG-PR2C-01
		extraKeywords    []string
		noConfirm        bool
		allowRedactFails bool
		resetMapping     bool
		noMapping        bool
		includeTrace     bool
	)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--output" && i+1 < len(args):
			i++
			outputDir = args[i]
		case strings.HasPrefix(arg, "--output="):
			outputDir = strings.TrimPrefix(arg, "--output=")
		case strings.HasPrefix(arg, "--redact="):
			redactProfile = strings.TrimPrefix(arg, "--redact=")
		case arg == "--redact" && i+1 < len(args):
			i++
			redactProfile = args[i]
		case strings.HasPrefix(arg, "--redact-keyword="):
			kw := strings.TrimPrefix(arg, "--redact-keyword=")
			extraKeywords = append(extraKeywords, strings.Split(kw, ",")...)
		case arg == "--i-understand-this-is-not-redacted":
			noConfirm = true
		case arg == "--allow-redaction-failures":
			allowRedactFails = true
		case arg == "--reset-redactor-mapping":
			resetMapping = true
		case arg == "--no-mapping":
			noMapping = true
		case arg == "--include-trace":
			includeTrace = true
		case arg == "--help", arg == "-h":
			printDiagBundleHelp()
			return nil
		}
	}

	// RULE-DIAG-PR2C-05: --redact=off requires interactive confirm or flag.
	if redactProfile == redactor.ProfileOff && !noConfirm {
		fmt.Fprintln(os.Stderr, "WARNING: --redact=off will produce an un-redacted bundle.")
		fmt.Fprintln(os.Stderr, "This bundle may contain hostnames, MAC addresses, and serial numbers.")
		fmt.Fprint(os.Stderr, "Type 'confirm' to proceed: ")
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Scan()
		if strings.TrimSpace(scanner.Text()) != "confirm" {
			return fmt.Errorf("diag bundle: aborted — confirmation not received")
		}
	}

	// Build mapping store.
	var store *redactor.MappingStore
	if !noMapping {
		mappingPath := mappingFilePath()
		if resetMapping {
			_ = os.Remove(mappingPath)
		}
		var err error
		store, err = redactor.LoadOrCreate(mappingPath, logger)
		if err != nil {
			return fmt.Errorf("diag bundle: load mapping: %w", err)
		}
	} else {
		store = redactor.NewMappingStore()
	}

	cfg := redactor.Config{
		Profile:             redactProfile,
		ExtraKeywords:       extraKeywords,
		AllowRedactionFails: allowRedactFails,
	}

	opts := diag.Options{
		OutputDir:           outputDir,
		RedactorCfg:         cfg,
		MappingStore:        store,
		VentdVersion:        version,
		IncludeTrace:        includeTrace,
		AllowRedactionFails: allowRedactFails,
	}

	bundlePath, err := diag.Generate(context.Background(), opts)
	if err != nil {
		return fmt.Errorf("diag bundle: %w", err)
	}

	// Persist updated mapping.
	if !noMapping {
		_ = store.Save(logger)
	}

	fmt.Fprintf(os.Stdout, "Bundle written: %s\n", bundlePath)
	return nil
}

// mappingFilePath returns the path for the persistent redactor mapping file.
func mappingFilePath() string {
	if os.Getegid() == 0 {
		return "/var/lib/ventd/redactor-mapping.json"
	}
	xdg := os.Getenv("XDG_STATE_HOME")
	if xdg == "" {
		home, _ := os.UserHomeDir()
		xdg = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(xdg, "ventd", "redactor-mapping.json")
}

func printDiagBundleHelp() {
	fmt.Print(`Usage: ventd diag bundle [flags]

Generates a privacy-redacted diagnostic bundle for bug reports.

Flags:
  --output <dir>                Output directory (default: /var/lib/ventd/diag-bundles/ or $XDG_STATE_HOME/ventd/diag-bundles/)
  --redact <profile>            Redaction profile: default-conservative (default), trusted-recipient, off
  --redact-keyword=<kw,...>     Additional keywords to redact from bundle content
  --i-understand-this-is-not-redacted  Skip confirmation prompt when --redact=off
  --allow-redaction-failures    Downgrade self-check failures to warnings instead of fatal errors
  --reset-redactor-mapping      Delete the persistent mapping file before this run
  --no-mapping                  Do not persist or load the redactor mapping file
  --include-trace               Include the rolling trace ring buffer snapshot (~2 MB extra)
  --help                        Show this help

Default behaviour: default-conservative profile, mapping persisted for cross-bundle consistency.
`)
}
