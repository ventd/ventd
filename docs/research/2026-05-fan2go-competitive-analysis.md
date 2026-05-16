# fan2go — competitive analysis vs ventd (2026-05-16)

This document captures the architectural delta between
[fan2go](https://github.com/markusressel/fan2go) (Go, AGPL-3.0) and
ventd as of v0.6.1. fan2go is the closest existing single-binary
Go competitor to ventd and is therefore the most informative
reference point — but its **AGPL-3.0 licence is contagious**, so
this analysis is **study-only**. No code is copied; every
similar-shaped primitive in ventd is independently re-derived.

This file is the T4.1 deliverable from
`/root/.claude/plans/squishy-sparking-pearl.md`.

## Licence boundary

fan2go's AGPL-3.0 licence triggers on network use: anyone offering
fan2go as a network service must offer its full source code,
including any modifications. If ventd were to copy AGPL code, the
copyleft would propagate through the linked binary — ventd's
GPL-3.0 (without the network clause) would have to be upgraded to
AGPL-3.0 to comply. That is a one-way ratchet we are explicitly
choosing not to step onto.

Concrete rules for any ventd contributor consulting fan2go:

1. Reading fan2go source for design inspiration is fine. Reading
   fan2go's licence text + understanding the implication is
   mandatory before any deeper study.
2. Copying any code — even small snippets, even comments — would
   force a licence change on ventd. Don't.
3. Re-implementing similar behaviour in independently-written Go
   is fine and is what this document scaffolds.
4. Citing fan2go in a commit message or rule file is fine; it's
   prior art, not a code source.

## Architectural deltas (high-impact)

### Curve / control-law surface

fan2go offers three curve types: **linear**, **PID**, and **function**
(min/max/avg/delta over multiple input curves). Operators wire them
together via a YAML graph.

ventd offers **linear**, **fixed**, **mix** (min/max/avg over named
inputs), and **PI** (a calibrated form of fan2go's PID with explicit
anti-windup). ventd additionally has the **blended IMC-PI** controller
(`internal/controller/blended.go`) that mixes a model-driven
predictive arm with the reactive curve via Layer-A/B/C confidence —
a class of behaviour fan2go does not have.

Verdict: ventd's curve surface is a superset of fan2go's where it
overlaps and meaningfully ahead on the predictive arm. No gap to
close.

### Hysteresis + smoothing

fan2go has per-curve `hysteresis` (single threshold in °C; ramp-up
ignores it, ramp-down delays through it).

ventd has the same primitive at `Curve.Hysteresis` plus a separate
`Curve.Smoothing` (time-window low-pass filter), both validated and
tested in `internal/controller/hysteresis_test.go`. ventd's
Lipschitz clamp on the aggregator (RULE-AGG-LIPSCHITZ-01) adds a
second-order smoothness guarantee fan2go does not have.

Verdict: parity + extra on ventd's side. No gap.

### Sensor / fan auto-detect

fan2go's `fan2go detect` walks `/sys/class/hwmon`, writes PWM 50%
to every channel, and watches `fan*_input` for a stable RPM rise —
classifying the pwm↔tach pairing automatically. The output is a
ready-to-tune YAML config.

ventd's setup wizard (`internal/setup`) + the channel-validity
probe (`internal/validity`) + the bipolar polarity probe
(`internal/polarity`) collectively do the same job at higher
fidelity:

- ventd's bipolar probe uses 20% / 80% PWM stimuli, not 50%, which
  better distinguishes normal-polarity fans (whose BIOS auto-curve
  holds at high baseline) from inverted-polarity fans (where a
  midpoint write reads as "slower than baseline").
- ventd separately detects phantom channels (RULE-CAL-DETECT-HAPPY)
  and BIOS-override channels (RULE-CALIB-PR2B-06) — categories
  fan2go's detect does not distinguish.
- ventd's calibration sweep also detects `stall_pwm` per channel
  via a descending sweep (RULE-CALIB-PR2B-04), a piece of data
  fan2go does not produce.

Verdict: ventd's coverage is a strict superset. The `T4.2 pwmconfig
parity test` documents the cross-reference (different reference
implementation, same outcome). No gap.

### NVML / GPU support

fan2go integrates with NVML for NVIDIA fan reads + writes. It uses
the Go NVIDIA Management bindings (originally Cgo; recently
purego).

ventd ships a purego NVML backend (`internal/hal/nvml`) plus a
SUID-root helper (`cmd/ventd-nvml-helper`) for the R515+ write
methods. ventd's AMDGPU backend (`internal/hal/gpu/amdgpu`)
covers RDNA1..4 with kernel-version-gated dispatch (the kernel
6.15 OD-bit dance on RDNA4 in particular). fan2go does not have
an AMDGPU backend.

Verdict: GPU coverage on ventd is more comprehensive. No gap.

### Prometheus exporter

**fan2go ships a Prometheus exporter exposing per-channel PWM /
RPM / temp / target metrics via /metrics on a configurable port.**
ventd has no equivalent today — `/api/hardware` (the web UI's
inventory endpoint) is the closest surface, but it is JSON, not
the Prometheus exposition format.

Verdict: **gap on ventd's side.** Operators running a Prometheus
+ Grafana stack today need to wrap the JSON endpoint in a
text-format adapter. A future ventd spec slot — call it
`spec-prometheus` — could ship a `/metrics` endpoint emitting
the canonical exposition format, drawing inspiration from
fan2go's metric names + labels (which are themselves derived
from the established node_exporter conventions). The endpoint
implementation must be independently written.

### Liquid / AIO devices

fan2go does not have a USB-HID / liquidctl-equivalent path. Its
hwmon-only design covers AIOs only when the kernel exposes them
(e.g. via liquidtux).

ventd has `internal/hal/usbbase/hidraw` substrate and the Corsair
backend (`internal/hal/liquid/corsair`) over it. The
vendor_remediation doctor card (this PR) covers NZXT / Aquacomputer
/ Gigabyte Waterforce with PID-level recognition.

Verdict: ventd is ahead. No gap.

### IPMI / BMC

fan2go does not support BMC / IPMI fan control.

ventd ships `internal/hal/ipmi` covering Supermicro + Dell OEM
fan-control commands with a watchdog-routed restore path
(RULE-WD-IPMI-ROUTING). HPE iLO is gated behind the licence
requirement and refuses cleanly.

Verdict: ventd-only territory.

## Architectural deltas (low-impact / academic)

### Config file shape

fan2go uses YAML config; sections named `fans:` / `sensors:` /
`curves:` map directly onto its internal Go structs. Configuration
is human-edited by default.

ventd uses YAML config with the same section shapes. The web UI
is the canonical surface; manual YAML editing remains possible
but is not the recommended path. Field names differ slightly
(`fans[].pwm` vs `fans[].pwm_path`, etc.) but the conceptual
model is identical.

Verdict: surface parity. No gap.

### Service-management

fan2go ships systemd unit files for its own service, AND a
companion `fan2go control` CLI for runtime tweaks without a
restart.

ventd ships systemd unit files, an `/api/control` web surface,
and a split-daemon model (the `ventd-setup` privileged helper)
for operator-driven changes. No gap.

## What would be expensive to absorb (and isn't)

fan2go does **not** have:

- Calibration of stall-PWM, polarity, BIOS-override (RULE-CALIB-PR2B-*)
- Workload signature learning (RULE-SIG-*)
- Layer-B coupling / Layer-C marginal-benefit RLS estimators
- Confidence-gated controller (RULE-CTRL-PI-*)
- Acoustic cost gate / R30 mic calibration / R33 proxy
- IPMI / BMC fan control
- AMDGPU RDNA-aware backend
- Doctor / preflight surfaces
- Vendor catalog (driver / chip / board profiles, fingerprint matcher)

These are the genuine ventd ahead-of-state-of-art items. Operators
choosing between ventd and fan2go on the merits of these
capabilities reach for ventd; choosing on the merits of
"Prometheus / Grafana out of the box" reach for fan2go today.

## Closing observation

The valuable absorption target from fan2go is the **Prometheus
exposition format** specifically — every other capability either
already exists in ventd or is so architecturally entangled that
re-implementing is more honest than retro-fitting. The Prometheus
endpoint is mechanically simple (a `/metrics` HTTP handler in
`internal/web` rendering each per-channel metric with the standard
labels); the cost is design only, not architecture.

This analysis is the deliverable; the Prometheus-endpoint spec is
the natural follow-up.
