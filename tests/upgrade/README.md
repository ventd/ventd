# Upgrade-Path Test Harness

Automated v0.2.0 → v0.3.0 upgrade-path test covering the five acceptance
gates from issue #183. Runs across Ubuntu 24.04, Fedora 41, Arch Linux, and
Alpine 3.20 inside Docker containers on the same amd64 runner used by
[`.github/workflows/upgrade-path.yml`](../../.github/workflows/upgrade-path.yml).

## Five gates

| Gate | What is checked |
|------|-----------------|
| 1 config | `config.yaml` survives upgrade; `hwmon.dynamic_rebind` is unchanged (present → preserved, absent → still absent) |
| 2 calibration | `calibration.json` is present and parseable (correct `schema_version`) |
| 3 curves | Fan curve returned by `GET /api/config` is byte-equivalent on user-facing fields |
| 4 bcrypt | Admin `web.password_hash` in config is byte-identical pre/post upgrade |
| 5 no-wizard | First `GET /` post-restart returns a non-wizard response (no 3xx to `/setup` or `/wizard`) |

## How to run locally

Prerequisites: Docker, Bash ≥ 4, Go (only if building the candidate from source).

```bash
# Run against a single distro with a pre-built candidate binary:
bash tests/upgrade/harness.sh ubuntu:24.04 \
    --candidate-binary /path/to/ventd-linux-amd64

# Build the candidate from the current tree and test Ubuntu:
bash tests/upgrade/harness.sh ubuntu:24.04

# Test all four distros sequentially:
for d in ubuntu:24.04 fedora:41 archlinux:latest alpine:3.20; do
    bash tests/upgrade/harness.sh "$d" \
        --candidate-binary /tmp/ventd-candidate
done

# Keep the container on failure for inspection:
bash tests/upgrade/harness.sh fedora:41 \
    --candidate-binary /tmp/ventd-candidate \
    --keep-on-failure
# Then: docker exec -it ventd-upgrade-fedora-41-<ts> bash
```

The harness logs to `/tmp/ventd-upgrade-<distro>-<ts>/` by default.
Pass `--log-dir /your/path` to override.

## File layout

```
tests/upgrade/
├── harness.sh          Host driver: starts Docker container, mounts files,
│                       collects logs. One invocation per distro.
├── inner-test.sh       Runs INSIDE the container: installs v0.2.0, seeds
│                       state, upgrades to v0.3.0-candidate, runs assertions.
├── fixtures/
│   ├── config.tmpl.yaml   Fixture config with ADMIN_HASH_PLACEHOLDER (replaced
│   │                       at runtime by inner-test.sh using bcrypt).
│   └── calibration.json   Pre-generated calibration artifact (schema_version 2).
└── assertions/
    ├── gate1-config.sh
    ├── gate2-calibration.sh
    ├── gate3-curves.sh
    └── gate4-bcrypt.sh
    └── gate5-no-wizard.sh
```

## Systemd variance

Systemd is not available inside Docker containers. `inner-test.sh` starts
`ventd` directly as a background process instead of using `systemctl restart`.
The `VENTD_TEST_MODE=1` environment variable instructs `scripts/install.sh`
to skip service activation, account creation, and hwmon module probing, but
still runs all file-copy and config-directory steps.

The five data-integrity gates do not depend on systemd: they verify that
config/calibration/auth files and the HTTP API response are unchanged after
a binary swap, regardless of how the daemon is started. For systemd-specific
upgrade behaviours (unit file refresh, Restart= policy across upgrades) see
`validation/fresh-vm-smoke/` which exercises full Proxmox VMs.

## CI integration

The workflow [`.github/workflows/upgrade-path.yml`](../../.github/workflows/upgrade-path.yml)
triggers on:
- Pull requests touching `scripts/install.sh`, `scripts/postinstall.sh`,
  `deploy/**`, `packaging/**`, `.goreleaser.yml`, `CHANGELOG.md`, or
  `tests/upgrade/**`
- `workflow_dispatch` (manual, optional `distro` input to narrow the matrix)

The CI job matrix runs all four distros with `fail-fast: false` so a single
distro's failure does not mask the others. Logs are uploaded as artifacts on
failure.

## Adding a new distro

1. Add a new `matrix.include` row to `.github/workflows/upgrade-path.yml`.
2. Add the distro's package manager to the `install_deps()` function in
   `inner-test.sh` under the appropriate `case` branch.
3. Update the distro-family detection `case` in `harness.sh` if the image
   tag prefix is not already handled.
4. Verify that `python3`, `python3-yaml`, and `python3-bcrypt` are available
   under the new distro's package name.
5. Run `bash tests/upgrade/harness.sh <new-distro>` locally to confirm.

## When a gate fails

A failing gate signals a **real upgrade-path bug**, not a harness defect.
Do not silence the failure. The correct response is:

1. Look at the log artifact uploaded by the CI job.
2. File a separate issue describing the regression (title: "upgrade: <gate>
   fails on <distro>").
3. Leave the gate RED until the underlying runtime fix lands. The harness
   is the signal; suppressing it hides the bug.
