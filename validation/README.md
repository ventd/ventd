# validation/ — release-gate checks for ventd

This directory holds the harnesses Phoenix (and eventually CI) runs before
cutting a release tag. Two levers live here today:

- `run-rig-checks.sh` — rig verification that runs on the physical test
  host. Covers live PWM/hwmon behaviour. Interactive; needs real hardware.
- `fresh-vm-smoke.sh` — fresh-VM install smoke harness. Spins up a
  throwaway Incus system container per target distro, runs the install
  script against a locally-built binary, and asserts the daemon starts
  clean. **Gate for v0.2.0: every target in the matrix must PASS.**

Everything else (`install-smoke/`, `tier-*/`, `phoenix-*.md`) is captured
output from prior manual runs.

## fresh-vm-smoke.sh

### What it does

For each target distro:

1. Builds `ventd` from the current working tree (`go build ./cmd/ventd`).
2. Bundles binary + `scripts/` + `deploy/` + `config.example.yaml` into a
   release-shape tarball. Serves it from the host's Incus bridge with
   `python3 -m http.server`.
3. Launches an Incus system container from a fresh upstream image.
4. Inside the container: `curl` the tarball, extract, run
   `scripts/install.sh` (exercising the tarball-extraction path).
5. Runs seven assertions (see below). Writes
   `validation/fresh-vm-smoke-<target>-<date>.md` with PASS/FAIL per
   assertion and log tails for any FAIL.
6. Deletes the container. Repeats for the next target.

### Assertions

| ID | Check                                                                                         |
|----|-----------------------------------------------------------------------------------------------|
| A1 | `scripts/install.sh` exits 0                                                                  |
| A2 | Service active (`systemctl is-active ventd` or `rc-service ventd status`)                     |
| A3 | `curl -sf http://127.0.0.1:9999/api/ping` → 200                                               |
| A4 | Setup token present in `journalctl -u ventd` (systemd) or `/var/log/ventd/current` (OpenRC)   |
| A5 | `/etc/ventd/config.yaml` is `ventd:ventd 0600` after daemon restart (PR #38 regression gate)  |
| A6 | `ps -o user= -p $(pidof ventd)` = `ventd` (the hardened unit's `User=ventd` took effect)      |
| A7 | `systemctl stop ventd && rm -rf /etc/ventd /usr/local/bin/ventd /etc/systemd/system/ventd.*`  |
|    | leaves no on-disk orphans and no lingering `ventd` process                                    |

A5 seeds `/etc/ventd/config.yaml` from `config.example.yaml` (the install
script doesn't create it — the setup wizard does, which needs real
hardware), restarts the daemon, then asserts the daemon didn't drift the
file's mode or owner on load. That is the PR #38 regression surface.

### Target matrix (v0.2.0 gate)

Must pass: `ubuntu-24.04`, `debian-12`, `fedora-40`, `arch`.
Nice-to-have (data, not blockers): `opensuse-tumbleweed`, `alpine`.

Alpine is expected to expose OpenRC-specific edge cases and — per the
prior `install-smoke/install-smoke-alpine.md` notes — needs the musl
variant of the binary. The harness builds one glibc binary per run, so
Alpine will only pass when a musl-compatible binary is served or when
ventd's static build covers both libcs. Document the failure mode in
the generated report rather than working around it in the harness.

### Prerequisites (host, one-time)

The harness itself runs as an unprivileged user. Incus setup needs one
`sudo` step per host; pick the command for the host distro:

| Distro                    | Install                                                                                            |
|---------------------------|----------------------------------------------------------------------------------------------------|
| Ubuntu 24.04 / Debian 13+ | `sudo apt install incus`                                                                           |
| Debian 12                 | `sudo apt install -t bookworm-backports incus` (or use the Zabbly backports repo)                  |
| Fedora 40+                | `sudo dnf copr enable ganto/lxc4 && sudo dnf install incus incus-tools`                            |
| Arch                      | `sudo pacman -S incus`                                                                             |
| openSUSE Tumbleweed       | `sudo zypper install incus`                                                                        |

Then, once:

```
sudo incus admin init --auto
sudo usermod -a -G incus-admin "$USER"   # log out + back in, or `newgrp incus-admin`
incus remote add images https://images.linuxcontainers.org --protocol simplestreams  # if not preconfigured
```

Host tooling the harness itself needs: `go`, `python3`, `tar`, `curl`.
All ship in the default dev setup; no extra install.

### Running

From the repo root:

```
# whole matrix (~3–5 min on a laptop; container launch dominates)
validation/fresh-vm-smoke.sh

# one target
validation/fresh-vm-smoke.sh ubuntu-24.04

# several
validation/fresh-vm-smoke.sh ubuntu-24.04 debian-12 fedora-40 arch
```

Reports land next to the script:

```
validation/fresh-vm-smoke-ubuntu-24.04-20260416-0900.md
validation/fresh-vm-smoke-debian-12-20260416-0900.md
...
```

Raw install logs (in case the report's tail is too terse) land under
`validation/.build/logs/install-<target>.log` and are cleaned on the
next run.

### Runtime

Back-of-envelope on a laptop with a warm image cache:

- First-ever run per target image: +20 s for the image download.
- Subsequent runs: ~30–45 s per target (launch + install + assertions + teardown).
- Full 6-target run cold: ~5 min. Warm: ~3–4 min.

### Adding a target

1. Add `[<key>]="<images-remote>:<alias>"` to the `IMAGES` map.
2. Add `<key>` to `ALL_TARGETS`.
3. Extend `bootstrap_tools` with the distro's package-manager invocation
   for `curl` and `tar` (only needed if the base image ships without
   them).
4. Run once; iterate on any init-system-specific edges in the
   `assert_*` helpers.

### Environment overrides

All documented in the script header (`validation/fresh-vm-smoke.sh`).
The ones you'll likely touch:

- `VENTD_SMOKE_KEEP=1` — skip instance teardown on success. Useful when
  debugging why an assertion FAILs; `incus exec <name> -- bash` drops
  you into the container. Ctrl-C still cleans up.
- `VENTD_SMOKE_BRIDGE` — override the bridge name if you don't use the
  default `incusbr0`.
- `VENTD_SMOKE_PORT` — override the HTTP port if 8089 is taken.

### Limitations / known gaps

- Runs sequentially; parallel launches would speed up cold runs but
  complicate bridge-IP/port bookkeeping for unclear gain.
- No VM-backed fallback. If the host can't run Incus (restricted
  environment, embedded CI runner), fall back to the prior manual
  QEMU+cloud-init flow documented in `install-smoke/`.
- CI integration is deferred. GitHub-hosted runners don't have Incus,
  and setting up a self-hosted runner with Incus is a separate
  F4-bucket task. For v0.2.0 this harness runs on Phoenix's laptop.
- The setup wizard (which creates the real `config.yaml`) is not
  driven; A5 is a narrower regression check that verifies the daemon
  doesn't stomp on mode/owner of a seeded config.
