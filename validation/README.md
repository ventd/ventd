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
5. Runs nine assertions (see below). Writes
   `validation/fresh-vm-smoke-<target>-<date>.md` with PASS/FAIL per
   assertion and log tails for any FAIL.
6. Deletes the container. Repeats for the next target.

### Assertions

| ID | Check                                                                                                    |
|----|----------------------------------------------------------------------------------------------------------|
| A1 | `scripts/install.sh` exits 0                                                                             |
| A2 | Service active (`systemctl is-active ventd` or `rc-service ventd status`)                                |
| A3 | `curl -sfm 3 -k https://127.0.0.1:9999/api/ping` → 200                                                   |
| A4 | `GET /api/auth/state` → `{"first_boot":true}` — daemon is in wizard mode, not dashboard mode             |
| A5 | Setup token present in `journalctl -u ventd` (systemd) or `/var/log/ventd/current` (OpenRC)              |
| A6 | `/etc/ventd` (if present) owned by `ventd:ventd` — regression gate for PR #38                            |
| A7 | No `level=error ... fatal`, `Failed with result`, or `matches multiple hwmon` lines in journal last 2 min |
| A8 | `ps -o user= -p $(pidof ventd)` = `ventd` (the hardened unit's `User=ventd` took effect)                 |
| A9 | `systemctl stop ventd && rm -rf /etc/ventd /usr/local/bin/ventd /etc/systemd/system/ventd.*` leaves no on-disk or process orphans |

#### Why `-k` on A3 and A4

ventd refuses to serve plaintext HTTP on non-loopback listens — see
`Web.RequireTransportSecurity()` in `internal/config/config.go`. The
first-boot flow auto-generates a self-signed cert under `/etc/ventd/`
(`cmd/ventd/main.go:132-162`) and serves HTTPS on `0.0.0.0:9999`. That
is the documented security posture per `.claude/rules/web-ui.md` and
`.claude/rules/usability.md`: the daemon is LAN-reachable by default,
so the setup wizard (admin password + token in flight) has to run over
TLS even before the operator has configured anything. `-k` is
necessary because the cert has no trust chain; it is not laziness —
it is testing the designed posture. `-m 3` caps the probe timeout so
a non-listening port fails fast instead of hanging.

#### Why first-boot, not post-config, is what A5–A8 test

The install script does **not** write `/etc/ventd/config.yaml` — the
setup wizard does, and the setup wizard needs real hwmon hardware.
An earlier iteration of this harness tried to seed `config.example.yaml`
to stand in for wizard output and restart the daemon. That fought the
install flow on two axes: (1) `config.example.yaml` references
`chip_name: nct6687` and fixed `/sys/class/hwmon/hwmonN/…` paths, and
(2) Incus system containers share `/sys` with the host, so the config
resolver saw the host's real chips and tripped PR #42's multi-match
guard. The daemon then died, every downstream assertion failed, and
none of them were actually checking what they were named after. The
redesign tests what the install one-liner genuinely leaves on disk:
a daemon running in first-boot wizard mode under `User=ventd`, ready
for the operator to point a browser at. Driving the wizard and
verifying the persisted config belongs in e2e tests, not here.

### Target matrix (v0.2.0 gate)

Must pass: `ubuntu-24.04`, `debian-12`, `fedora-42`, `arch`.
Nice-to-have (data, not blockers): `opensuse-tumbleweed`, `alpine`.

Fedora 42 (not 40) because Fedora 40 reached EOL in Dec 2025 and was
pruned from `images.linuxcontainers.org`; 42 is the current stable as
of this file's last update. Bump when Fedora 43 is out and 41 is EOL.

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

### Flags

- `--refresh-images` — opt-in. Deletes the locally cached Incus image
  for each selected target before launch so the next `incus launch`
  re-pulls from the `images:` remote. Adds ~20 s per target; use it
  when the local squashfs cache is suspect (see _Recovery: corrupted
  cached image_ below). Normal runs reuse the warm cache.

- `--migration-smoke` — opt-in. Upgrade-path smoke for issue #59. For
  each selected target, pre-seeds `/etc/ventd/config.yaml` from
  `validation/fixtures/pre-tls-config.yaml` plus an openssl-generated
  self-signed `tls.crt` / `tls.key` pair, then runs `scripts/install.sh`.
  The new `M1` assertion verifies `config.Load`'s TLS-path migration
  populated `web.tls_cert` / `web.tls_key` and persisted the result —
  the regression gate for pre-F3 configs that would otherwise make a
  post-F2 daemon crashloop on `RequireTransportSecurity()`.

  The seeded config has `password_hash` set, so `first_boot` is false and
  the daemon generates no setup token. The `A4` (wizard-mode), `A5`
  (setup token), and `A9` (uninstall) assertions are skipped when this
  flag is active; they test fresh-install invariants, not the upgrade
  path. The remaining six assertions still run.

  ```
  validation/fresh-vm-smoke.sh --migration-smoke                    # all targets
  validation/fresh-vm-smoke.sh --migration-smoke fedora-42 arch     # subset
  ```

  Reports land under `validation/fresh-vm-smoke-migration-<target>-<date>.md`
  so a normal run and a migration run on the same day don't clobber.

### Environment overrides

All documented in the script header (`validation/fresh-vm-smoke.sh`).
The ones you'll likely touch:

- `VENTD_SMOKE_KEEP=1` — skip instance teardown on success. Useful when
  debugging why an assertion FAILs; `incus exec <name> -- bash` drops
  you into the container. Ctrl-C still cleans up.
- `VENTD_SMOKE_BRIDGE` — override the bridge name if you don't use the
  default `incusbr0`.
- `VENTD_SMOKE_PORT` — override the HTTP port if 8089 is taken.

### Recovery: corrupted cached image

Incus's local image store occasionally corrupts a cached squashfs — the
canonical symptom is an `unsquashfs` error partway through
`incus launch`, e.g.:

```
unsquashfs: xz uncompress failed with error code 9 on .../locale-archive
```

This has been observed once on `images:fedora/42` during a matrix run
and did not reproduce on a second attempt. It is an Incus storage-pool
issue, not a harness or ventd bug, but the recovery step is worth
knowing:

1. Find the cached image's fingerprint:

   ```
   incus image list --format=csv -c fd
   ```

   The `d` column carries a description like `Fedora 42 amd64 (…)`;
   the `f` column is the fingerprint.

2. Delete by fingerprint prefix (12 chars is enough):

   ```
   incus image delete <fingerprint-prefix>
   ```

3. Re-run the harness. The next `incus launch` re-pulls the image from
   `images.linuxcontainers.org` automatically.

The `--refresh-images` flag wraps all three steps for the targets on
the command line, so a one-liner recovery is:

```
validation/fresh-vm-smoke.sh --refresh-images fedora-42
```

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
  driven; the harness verifies the pre-wizard state only, which is
  the install script's actual responsibility. Wizard-driven config
  creation belongs in e2e tests with real or simulated hwmon, not
  in install smoke.
- UFW + Incus bridge: if the host runs UFW with the default
  `deny (incoming)` policy, DHCPOFFERs from the Incus-managed
  dnsmasq get dropped and containers never get an IPv4. `ufw allow
  in on incusbr0` alone is not sufficient on Ubuntu 24.04's UFW
  build; for local runs the workaround is `sudo ufw disable`.
  Proper coexistence is a host-level firewall task outside this
  harness's scope.
