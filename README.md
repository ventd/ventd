# ventd

**Automatic Linux fan control. Auto-detects hardware, auto-calibrates, auto-recovers — set and forget.**

`ventd` is a zero-config fan control daemon for Linux. Install it once, open the web UI, click Apply. After that, you never open a terminal again. Calibration runs server-side, survives restarts and disconnects, and persists across reboots. New hardware is detected automatically.

One static binary. One install command. Works on any distribution with any fan controller the kernel exposes.

## Features

- **Automatic hardware detection.** Enumerates every writable fan control the kernel exposes, regardless of chip identity. Works with motherboard Super I/O chips, BMC/IPMI controllers, AIO pumps, NVIDIA GPUs (via runtime-loaded NVML), and AMD GPUs.
- **Automatic calibration.** Measures start PWM, stop PWM, max RPM, and full PWM→RPM curve for every fan. Runs server-side; survives browser disconnect and daemon restart. Abortable from the UI at any time.
- **Automatic safety.** Hardware watchdog restores fan state on any exit path — signal, crash, panic, or power loss — within two seconds.
- **Automatic hardware change detection.** Plug a new fan or GPU in; `ventd` notices within ten seconds and offers to add it.
- **Zero terminal after install.** Hardware scan, dependency install, calibration, curve editing, and service control all happen in the web UI.
- **Single static binary.** `CGO_ENABLED=0`. NVML loaded at runtime via `dlopen`; GPU features silently disable if the library is absent. No Python, no Node, no runtime dependencies beyond libc.

## Install

```
curl -sSL https://raw.githubusercontent.com/ventd/ventd/main/scripts/install.sh | sudo bash
```

The script detects your architecture and init system (systemd, OpenRC, or runit), drops the binary at `/usr/local/bin/ventd`, installs the service file, enables it, and starts the daemon. It prints one thing: the URL to open in your browser.

Open that URL. The setup wizard prompts for a one-time token on first run; if you didn't catch it from the install output, run `sudo journalctl -u ventd -n 50` (or check `/var/log/ventd/current` on runit) to recover it.

## Supported platforms

- **Distributions:** Ubuntu, Debian, Fedora, RHEL, CentOS, Arch, Manjaro, openSUSE, Alpine, Void, NixOS
- **Init systems:** systemd, OpenRC, runit
- **Architectures:** amd64, arm64
- **C library:** glibc and musl
- **GPU:** NVIDIA (via NVML), AMD (via amdgpu hwmon). Intel Arc fan control is read-only at the kernel level; monitoring only.

## How it compares

| | ventd | CoolerControl | fan2go | thinkfan | lm-sensors fancontrol |
|---|---|---|---|---|---|
| Zero-config first boot | yes | no | no | no | no |
| Web UI | yes | yes (Qt GUI) | no | no | no |
| Automatic calibration | yes | manual | manual | manual | manual |
| Single static binary | yes | no | yes | yes | script |
| Runtime NVML dlopen | yes | no | no | no | no |
| Hardware change detection | yes | no | no | no | no |
| Remote (browser-only) setup | yes | no | no | no | no |

## Documentation

- [Installation guide](docs/install.md)
- [Configuration reference](docs/config.md)
- [Hardware compatibility](docs/hardware.md)
- [Troubleshooting](docs/troubleshooting.md)

## Safety

`ventd` controls physical hardware. It is engineered conservatively — the watchdog always restores original `pwm_enable` state on exit, pump fans have a hard minimum floor, and calibration never leaves a fan at PWM=0 for more than two seconds. But no software can guarantee safety on every hardware permutation. If you run exotic hardware (server chassis, custom loop, unusual AIO), validate calibration results before leaving the daemon unattended.

Report any case where `ventd` leaves a fan in an unsafe state as a [SECURITY.md](SECURITY.md) issue, not a regular bug.

## Building from source

```
git clone https://github.com/ventd/ventd
cd ventd
go build ./cmd/ventd/
```

Requires Go 1.25 or later. No other build dependencies.

## License

GPL-3.0. See [LICENSE](LICENSE).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Pull requests, issues, and hardware compatibility reports are welcome.
