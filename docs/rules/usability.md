# Usability — Universal Linux Compatibility

This program targets every Linux user from complete beginners to sysadmins. Every design decision must pass this test: "Would a person who has never opened a terminal be able to use this?"

## Distribution Support

- Must work on: Ubuntu/Debian, Fedora/RHEL/CentOS, Arch/Manjaro, openSUSE, Void, Alpine.
- **NixOS is currently not in the supported list** — `/etc/modprobe.d/*.conf` drop-ins are ignored on NixOS (system module config has to live in `configuration.nix`). Until ventd emits a NixOS fragment in the install path, installs will appear to succeed but module options won't apply. Surface a doctor card directing the operator to copy the modprobe options into their `configuration.nix` (R28 §F).
- Package manager detection: apt, dnf, yum, pacman, zypper, apk, xbps-install. (nix-env intentionally omitted — see NixOS note above.)
- Systemd is primary service manager but support OpenRC and runit as fallback
- Never assume a specific distro. Test for capabilities, not distro names.
- Binary should be statically linked or have minimal dynamic deps (libc only, NVML optional runtime load)

## Installation

- Single binary + systemd unit file. No Python, no Node, no Ruby, no Java runtime.
- Install script: curl-pipe-bash one-liner that detects arch (amd64/arm64), downloads binary, installs service file, enables and starts
- The install command is the LAST terminal command the user ever runs. After that, everything happens in the browser.
- Provide .deb and .rpm packages for distro repos (future)
- Cross-compile for amd64 and arm64 at minimum (arm64 covers Raspberry Pi, ARM servers)

## Zero Terminal After Install

After the install script completes, the user never touches the terminal again. Everything is in the web UI:

- Hardware detection → web UI shows results, not stdout
- Dependency installation (missing kernel modules, drivers) → web UI detects, explains, and offers a one-click "Fix this" button that runs the commands server-side
- Calibration → initiated and monitored entirely through the web UI with live progress
- Config changes → web UI form, not YAML editing
- Service control (start/stop/restart) → web UI buttons
- Log viewing → web UI live log stream
- Updates → web UI checks for new version, one-click update
- Re-running setup/recalibration → web UI button, not CLI flags

The terminal install command should print ONE thing: the URL to open. e.g.:
  "ventd installed. Open http://192.168.7.209:9999 to set up."

## First Run Experience

- If no config exists, daemon starts web UI on port 9999 and prints the URL to stdout/journald. The setup-token bootstrap was eliminated in v0.5.8.1 (#765, #794) — first-boot now just shows the password-set form when no `auth.json` exists.
- Web UI setup wizard walks the user through everything with plain English — no jargon
- Step-by-step flow: Welcome → Hardware Scan (automatic) → Fan Detection → Calibration (with live progress) → Review & Apply
- Each step has a clear explanation of what's happening and why
- "We found 3 fans and 2 temperature sensors. Click Start Calibration to measure your fan speeds."
- Never expose raw hwmon paths or PWM values in the UI. Translate to: "CPU Fan", "System Fan 1", "GPU Fan"
- Provide sensible defaults for everything. User should be able to click through the wizard without changing anything and get a reasonable result.
- The web UI must feel like a native desktop app, not a sysadmin tool. Think: router setup page, not Grafana.

## Error Handling

- If a required kernel module can't be loaded: explain what happened in plain English, suggest the fix, offer to attempt it
- If no controllable fans found: don't crash. Show a clear message: "No controllable fans detected. Your BIOS may be managing fans directly."
- If NVIDIA driver not installed: silently skip GPU detection, don't error
- All errors in the web UI must be human-readable. Never show stack traces, hwmon paths, or Go error strings to the user.

## Naming Conventions (user-facing)

- Use motherboard header names where detectable (CPU_FAN1, SYS_FAN1, CHA_FAN1)
- Fall back to "Fan 1", "Fan 2" etc when header names aren't available
- Temperature sensors: "CPU Temperature", "GPU Temperature", "Motherboard Temperature" — not "coretemp-isa-0000 temp1"
- Curve types in UI: "Automatic (speed increases with temperature)", "Fixed Speed", "Custom" — not "linear", "fixed", "mix"

## Documentation

- README should explain: what it does, one-command install, how to access the web UI, how to report issues
- No assumed knowledge of Linux internals, hwmon, sysfs, PWM, or fan control concepts
