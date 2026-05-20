// Copyright the ventd authors.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/ventd/ventd/internal/hal"
	"github.com/ventd/ventd/internal/hal/msiec"
)

// runPowerProfile implements `ventd power-profile [get|set <value>]`.
//
// The subcommand surfaces the optional hal.PowerProfileBackend
// capability (#1166) to operators without needing the full daemon
// running. msi-ec's shift_mode (eco / comfort / turbo) is the only
// implementing backend today; thinkpad / asus / Legion follow when
// their HAL backends add the same optional interface.
//
// Forms:
//
//	ventd power-profile             # print current + available
//	ventd power-profile get         # same, explicit
//	ventd power-profile set <name>  # write the profile
//
// Exit codes:
//
//	0 — success (read or write completed)
//	1 — no power-profile-capable backend on this host
//	2 — invalid usage / unknown profile name
//	3 — backend error
func runPowerProfile(args []string, logger *slog.Logger, stdout, stderr io.Writer) int {
	cmd := "get"
	var value string
	switch len(args) {
	case 0:
		// default: get
	case 1:
		cmd = strings.ToLower(args[0])
	case 2:
		cmd = strings.ToLower(args[0])
		value = args[1]
	default:
		_, _ = fmt.Fprintln(stderr, "Usage: ventd power-profile [get | set <eco|comfort|turbo>]")
		return 2
	}

	backend, channel, ok := findPowerProfileChannel(logger)
	if !ok {
		_, _ = fmt.Fprintln(stderr, "no power-profile-capable backend on this host")
		_, _ = fmt.Fprintln(stderr, "  (msi-ec is the only supported backend today; check /sys/devices/platform/msi-ec/)")
		return 1
	}
	pp, ok := any(backend).(hal.PowerProfileBackend)
	if !ok {
		// Should not happen — Caps & CapWritePowerProfile implies the
		// backend satisfies the interface. Defensive.
		_, _ = fmt.Fprintln(stderr, "internal error: backend has CapWritePowerProfile but does not satisfy hal.PowerProfileBackend")
		return 3
	}

	switch cmd {
	case "get", "":
		current, err := pp.ReadPowerProfile(channel)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "read shift_mode: %v\n", err)
			return 3
		}
		avail, err := pp.AvailablePowerProfiles(channel)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "read available profiles: %v\n", err)
			return 3
		}
		_, _ = fmt.Fprintf(stdout, "backend:   %s\n", backend.Name())
		_, _ = fmt.Fprintf(stdout, "channel:   %s\n", channel.ID)
		_, _ = fmt.Fprintf(stdout, "current:   %s\n", current)
		_, _ = fmt.Fprintf(stdout, "available: %s\n", strings.Join(avail, ", "))
		return 0

	case "set":
		if value == "" {
			_, _ = fmt.Fprintln(stderr, "Usage: ventd power-profile set <eco|comfort|turbo>")
			return 2
		}
		if err := pp.WritePowerProfile(channel, value); err != nil {
			_, _ = fmt.Fprintf(stderr, "write shift_mode %q: %v\n", value, err)
			return 3
		}
		_, _ = fmt.Fprintf(stdout, "set power-profile=%s on %s\n", value, channel.ID)
		return 0

	default:
		_, _ = fmt.Fprintf(stderr, "unknown subcommand %q (expected get or set)\n", cmd)
		return 2
	}
}

// findPowerProfileChannel discovers the first channel from any
// registered HAL backend that advertises CapWritePowerProfile. The
// daemon's HAL registry would normally handle this, but the CLI runs
// out-of-process — we enumerate msi-ec directly. Future backends
// (thinkpad / asus / Legion) get appended to the if-chain when they
// land the optional interface.
func findPowerProfileChannel(logger *slog.Logger) (hal.FanBackend, hal.Channel, bool) {
	b := msiec.NewBackend(logger)
	channels, err := b.Enumerate(context.Background())
	if err != nil {
		logger.Debug("power-profile: msiec Enumerate", "err", err)
		return nil, hal.Channel{}, false
	}
	for _, ch := range channels {
		if ch.Caps&hal.CapWritePowerProfile != 0 {
			return b, ch, true
		}
	}
	return nil, hal.Channel{}, false
}

// powerProfileMain is the package-level glue the main.go subcommand
// switch calls; isolated for testability (the test file injects
// alternate writers and asserts exit codes without going through
// os.Exit).
func powerProfileMain() {
	logger := buildLogger("info")
	exit := runPowerProfile(os.Args[2:], logger, os.Stdout, os.Stderr)
	os.Exit(exit)
}
