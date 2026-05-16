// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/ventd/ventd/internal/hwdb/lmsensors"
)

// runImportSensorsConf implements the `ventd import-sensors-conf
// <path>` subcommand. It parses a sensors.conf-format file (the
// lm-sensors community corpus, typically under
// /etc/sensors.d/*.conf or /usr/share/sensors.d/*.conf) and emits
// a ventd hwdb chip-overlay YAML document to stdout — or, with
// `--out <path>`, to the pending-overlay directory at
// /var/lib/ventd/profiles-pending/ alongside the existing
// `ventd hwdb capture` outputs.
//
// Exit codes:
//
//	0 — parse + emit succeeded
//	1 — usage error (missing input path, bad flags)
//	2 — I/O error reading the input or writing the output
//	3 — parse error (malformed sensors.conf)
//
// The subcommand is dispatched from main.go's argv router so it
// MUST NOT depend on the daemon config or any subsystem init. A
// fresh-install operator running `ventd import-sensors-conf
// /etc/sensors3.conf` before the daemon is configured should get
// a clean overlay on stdout.
func runImportSensorsConf(args []string, _ *slog.Logger) int {
	fs := flag.NewFlagSet("import-sensors-conf", flag.ContinueOnError)
	outPath := fs.String("out", "", "write the overlay to <path> instead of stdout (typically /var/lib/ventd/profiles-pending/<name>.yaml)")
	stdout := io.Writer(os.Stdout)
	stderr := io.Writer(os.Stderr)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintln(stderr, "usage: ventd import-sensors-conf [--out <path>] <sensors.conf>")
		return 1
	}
	inPath := fs.Arg(0)
	in, err := os.Open(inPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "import-sensors-conf: open %s: %v\n", inPath, err)
		return 2
	}
	defer func() { _ = in.Close() }()

	blocks, err := lmsensors.Parse(in)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "import-sensors-conf: parse %s: %v\n", inPath, err)
		return 3
	}

	out := stdout
	if *outPath != "" {
		// Ensure parent dir exists; honour mode 0750 to match the
		// existing profiles-pending convention (RULE-HWDB-CAPTURE-01).
		if err := os.MkdirAll(filepath.Dir(*outPath), 0o750); err != nil {
			_, _ = fmt.Fprintf(stderr, "import-sensors-conf: mkdir %s: %v\n", filepath.Dir(*outPath), err)
			return 2
		}
		f, err := os.OpenFile(*outPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o640)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "import-sensors-conf: create %s: %v\n", *outPath, err)
			return 2
		}
		defer func() { _ = f.Close() }()
		out = f
	}

	if err := lmsensors.EmitOverlay(out, inPath, blocks); err != nil {
		_, _ = fmt.Fprintf(stderr, "import-sensors-conf: emit: %v\n", err)
		return 2
	}
	if *outPath != "" {
		// Operator-facing breadcrumb on stderr so a script piping
		// stdout gets a clean overlay but interactive users see the
		// write location.
		_, _ = fmt.Fprintf(stderr, "import-sensors-conf: wrote overlay to %s (%d chip block(s))\n", *outPath, len(blocks))
	}
	return 0
}
