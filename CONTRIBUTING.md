# Contributing to ventd

Thanks for considering a contribution. This document covers what to expect, how to structure changes, and what blocks a merge.

## Scope

`ventd` has a single product promise: zero-config, zero-terminal fan control on any Linux box. Every change is measured against that promise. Contributions that add complexity a first-time user would have to understand are harder to merge than contributions that remove it.

## Before you start

- **Check the issue tracker** for duplicate work.
- **Open an issue first** for anything non-trivial. A five-minute chat saves rewriting a PR.
- **Hardware compatibility reports are contributions.** If `ventd` works or doesn't work on your hardware, open an issue with: motherboard / CPU / GPU / fan controller, distribution, kernel version, and the relevant excerpt from `journalctl -u ventd`.

## Development setup

```
git clone https://github.com/ventd/ventd
cd ventd
go build ./cmd/ventd/
go test -race ./...
```

Requires Go 1.25 or later. No other dependencies.

### Running the browser end-to-end suite (optional)

The tests under `internal/web/` that are gated with `//go:build e2e`
drive a real headless Chromium against the HTTP handler chain. They
catch the class of bug unit tests cannot — e.g. "CSP blocks our own
scripts, so the login button does nothing" — by actually executing
the UI in a browser. They are excluded from the default `go test`
run so contributors without a Chromium runtime pay nothing for them.

```
go test -tags=e2e ./internal/web/...
```

`rod` downloads its own Chromium into `~/.cache/rod` on first invocation
(~180 MB, cached thereafter), so you only need the system runtime
libraries Chromium links against. On Debian/Ubuntu:

```
sudo apt-get install -y libnss3 libatk1.0-0 libatk-bridge2.0-0 \
  libcups2 libxkbcommon0 libxcomposite1 libxdamage1 libxrandr2 \
  libgbm1 libpango-1.0-0 libasound2t64 libx11-xcb1 libxshmfence1 \
  fonts-liberation
```

If you already have a Chromium installed and would rather rod use
that, set `VENTD_E2E_CHROMIUM=/path/to/chrome`.

## What blocks a merge

- `go vet ./...` must pass.
- `go test -race ./...` must pass.
- `golangci-lint run` must pass (CI runs this).
- Any change touching hwmon writes must preserve the watchdog invariant: original `pwm_enable` state is restored on every exit path.
- Any change touching the single-static-binary invariant must not reintroduce build-tag splits, dual binaries, or CGO-linked releases. NVML is resolved at runtime via `dlopen`. This is non-negotiable.
- Any change to `calibration.json` schema must bump `schema_version` and handle older versions by surfacing a recalibration prompt, never silently applying new behaviour.
- New user-visible strings must be plain English and free of hwmon / PWM / sysfs jargon. Translate to "CPU Fan", "System Fan 1", "Automatic (speed increases with temperature)" — not chip-level terminology.
- New dependencies require justification in the PR description. The default answer is no.

## Coding conventions

- Logging: `log/slog`. No `fmt.Println`, no `log.Printf`.
- Error wrapping: `fmt.Errorf("context: %w", err)` on every error boundary.
- Tests: table-driven with `t.Run()` subtests.
- No `init()` functions. Explicit initialization in `main` or constructors.
- `internal/` packages are not importable outside the module — keep it that way.
- Context propagation (`context.Context`) for cancellation in long-running goroutines.

## Commit messages

- Present tense, imperative mood: "Add NVML refcount safety" not "Added" or "Adds".
- First line ≤72 characters.
- Body wraps at 72 characters, explains the why, references issues as `Fixes #123`.
- One logical change per commit. Squash trivial fixups before submitting.

## Pull requests

- Branch from `main`. Name the branch `<short-topic>` or `<issue-number>-<topic>`.
- Describe what changed, why, and how to verify. Paste relevant command output if hardware interaction is involved.
- Include a self-review checklist matching the "What blocks a merge" section.
- CI must be green before review.

## Reporting security issues

See [SECURITY.md](SECURITY.md). Do not open public issues for vulnerabilities.
