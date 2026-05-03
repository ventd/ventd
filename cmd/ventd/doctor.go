package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ventd/ventd/internal/doctor"
	"github.com/ventd/ventd/internal/doctor/detectors"
	"github.com/ventd/ventd/internal/recovery"
)

// runDoctor implements the `ventd doctor` subcommand. The exit-code
// contract per RULE-DOCTOR-02:
//
//	0 = OK (no Warning, no Blocker)
//	1 = Warning (one or more Warning, no Blocker)
//	2 = Blocker (one or more Blocker)
//	3 = Doctor itself errored before producing a Report
//
// Caller (main.go) returns the corresponding os.Exit value.
func runDoctor(args []string, logger *slog.Logger) (exitCode int, err error) {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit schema-versioned JSON instead of human-readable text")
	quiet := fs.Bool("quiet", false, "suppress OK-severity facts in text output (warnings + blockers only)")
	skip := fs.String("skip", "", "comma-separated detector names to skip")
	only := fs.String("only", "", "comma-separated detector names to run exclusively (empty = all)")
	timeoutMs := fs.Int("per-detector-timeout-ms", 200, "per-detector timeout in milliseconds")
	if err := fs.Parse(args); err != nil {
		return 3, fmt.Errorf("doctor: parse args: %w", err)
	}

	// Auto-discover the OOT modules ventd installed by walking
	// /etc/modules-load.d/ventd-*.conf. This lets `ventd doctor`
	// produce useful output on a fresh install without flag-juggling.
	modules := discoverVentdModules("/etc/modules-load.d")

	// Construct the detector slice. Detectors with daemon-only data
	// sources (signguard, hwdiag.Store, calibration loader, hwmon
	// baseline) are not constructed here — the CLI invocation runs
	// out-of-process from the daemon and those would degrade to
	// no-ops anyway. The wiring layer (PR follow-up) registers the
	// full set on the daemon's periodic Runner.RunPeriodic.
	dets := []doctor.Detector{
		detectors.NewDKMSStatusDetector(nil),
		detectors.NewUserspaceConflictDetector(nil),
		detectors.NewBatteryTransitionDetector(nil),
		detectors.NewContainerPostbootDetector(nil),
	}
	if len(modules) > 0 {
		dets = append(dets,
			detectors.NewModulesLoadDetector(modules, nil),
			detectors.NewKmodLoadedDetector(modules, nil),
		)
	}

	runner := doctor.NewRunner(dets, nil, recovery.Classify, time.Now)

	opts := doctor.RunOptions{
		Skip:               splitCSV(*skip),
		Only:               splitCSV(*only),
		PerDetectorTimeout: time.Duration(*timeoutMs) * time.Millisecond,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	report, err := runner.RunOnce(ctx, opts)
	if err != nil {
		return 3, fmt.Errorf("doctor: runner: %w", err)
	}

	out := io.Writer(os.Stdout)
	if *jsonOut {
		if err := writeReportJSON(out, report); err != nil {
			return 3, err
		}
	} else {
		writeReportText(out, report, *quiet, modules)
	}

	_ = logger // logger reserved for verbose output in a future flag.
	return report.Severity.ExitCode(), nil
}

// discoverVentdModules walks the modules-load.d directory and
// returns the list of modules ventd registered via persistModuleLoad.
// File shape: ventd-<module>.conf containing the module name.
func discoverVentdModules(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "ventd-") || !strings.HasSuffix(name, ".conf") {
			continue
		}
		// Trust the filename — the wizard writes ventd-<module>.conf.
		// Reading the file content is more robust against operator
		// renames but the filename suffices for an install that
		// hasn't been hand-edited.
		mod := strings.TrimSuffix(strings.TrimPrefix(name, "ventd-"), ".conf")
		if mod == "" {
			continue
		}
		out = append(out, mod)
		_ = filepath.Join // silence unused-import warning when build trims; keeps the package usable for future drop-in helpers
	}
	sort.Strings(out)
	return out
}

// splitCSV is a tolerant comma-split: empty input → nil slice;
// trims whitespace around each entry; drops empty fields.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// writeReportText renders the human-readable form. Output shape:
//
//	ventd doctor — N facts (severity)
//
//	Detectors checked: dkms_status, userspace_conflict, ...
//
//	[BLOCKER] dkms_status: Title
//	          Detail line wrapped at 80 cols.
//	          Entity: <hash>
//
//	[WARNING] modules_load: Title
//	          ...
//
//	(Detectors with no facts are listed as "OK: <name>" in non-quiet
//	mode so the operator can see the full set of checks ran.)
func writeReportText(w io.Writer, r doctor.Report, quiet bool, modules []string) {
	_, _ = fmt.Fprintf(w, "ventd doctor — %d fact(s), %s\n\n", len(r.Facts), r.Severity)

	if len(modules) > 0 {
		_, _ = fmt.Fprintf(w, "Auto-discovered ventd modules: %s\n\n", strings.Join(modules, ", "))
	}

	if len(r.DetectorErrors) > 0 {
		_, _ = fmt.Fprintln(w, "Detector errors:")
		for _, de := range r.DetectorErrors {
			_, _ = fmt.Fprintf(w, "  - %s: %s\n", de.Detector, de.Err)
		}
		_, _ = fmt.Fprintln(w)
	}

	if len(r.Facts) == 0 {
		if !quiet {
			_, _ = fmt.Fprintln(w, "No facts emitted — every detector either passed or had no signal.")
		}
		return
	}

	for _, f := range r.Facts {
		if quiet && f.Severity == doctor.SeverityOK {
			continue
		}
		tag := strings.ToUpper(f.Severity.String())
		fmt.Fprintf(w, "[%s] %s: %s\n", tag, f.Detector, f.Title)
		if f.Detail != "" {
			fmt.Fprintf(w, "         %s\n", wrapText(f.Detail, 76, "         "))
		}
		fmt.Fprintf(w, "         Entity: %s\n\n", f.EntityHash)
	}
}

// writeReportJSON emits the full Report as schema-versioned JSON.
// Per RULE-DOCTOR-08 the schema_version field is the contract pin;
// downstream consumers (spec-11 wizard, web UI) gate on it.
func writeReportJSON(w io.Writer, r doctor.Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// wrapText is a minimal word-wrap to keep CLI output legible. Hand-
// rolled instead of pulling in a wrap library — the detail strings
// average 200-300 chars; performance isn't a concern.
func wrapText(s string, width int, indent string) string {
	if len(s) <= width {
		return s
	}
	var out strings.Builder
	words := strings.Fields(s)
	col := 0
	for i, word := range words {
		if i > 0 {
			if col+1+len(word) > width {
				out.WriteByte('\n')
				out.WriteString(indent)
				col = 0
			} else {
				out.WriteByte(' ')
				col++
			}
		}
		out.WriteString(word)
		col += len(word)
	}
	return out.String()
}
