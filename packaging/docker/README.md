# Docker packaging — ventd

This directory is a reference container image for running ventd on a
Linux host. It is additive to the native install paths (install.sh,
`.deb`/`.rpm`, AUR) and does not replace them. If you can install the
binary natively, that remains the recommended path — Docker around a
fan-control daemon has real limitations, documented below.

The image is **not published to a registry**. Build it yourself from
this directory. Pushing to Docker Hub / GHCR is a Phoenix-only
action; see `.claude/rules/collaboration.md`.

## Repository layout

```
packaging/docker/
├── README.md                 (this file)
├── Dockerfile                (multi-stage: golang:1.25-alpine → alpine:3.20)
├── Dockerfile.dockerignore   (BuildKit ignore, applies to repo-root context)
└── docker-compose.yml        (reference runtime configuration)
```

## Quick start

From the repo root:

```bash
mkdir -p packaging/docker/config
cp config.example.yaml packaging/docker/config/config.yaml

docker compose -f packaging/docker/docker-compose.yml up -d --build

# open http://localhost:9999
```

On first boot the daemon prints a one-time setup token to the
container log. Fetch it with:

```bash
docker compose -f packaging/docker/docker-compose.yml logs ventd | grep -i 'setup token'
```

This mirrors the native install flow documented in the top-level
README; the only difference is where you read the token from.

## What the image includes

- Alpine 3.20 base (musl libc).
- Single static `ventd` binary at `/usr/local/bin/ventd`, built with
  `CGO_ENABLED=0 -tags nonvidia -trimpath`.
- Unprivileged `ventd` user (UID 472, GID 472 by default; GID is
  overridable via `--build-arg VENTD_GID=...` — see "Permissions"
  below).
- `HEALTHCHECK` against the unauthenticated `/api/ping` endpoint.

The image does **not** include the systemd unit, the udev rule, the
AppArmor profile, or `ventd-recover.service`. Those are host-side
responsibilities; see "Limitations vs native install" below.

## Design spike: `/sys/class/hwmon` from a container

This section records the answers to the questions `release-notes/
v0.3.0-plan.md` flagged: "`/sys/class/hwmon` access from a container
is its own design question — budget a spike."

### What `/sys` actually is inside a container

`/sys` is a kernel-synthesised filesystem. It does not live on disk.
Every mount namespace that bind-mounts `/sys` sees the same set of
kernel-exported objects — there is no container-local hwmon subsystem
that can be provided separately. Consequence: the container sees the
same `hwmon0`, `hwmon1`, … devices as the host, and writing a PWM
value inside the container is indistinguishable at the kernel level
from writing it on the host.

The compose file therefore bind-mounts `/sys` read-write:

```yaml
- /sys:/sys:rw
```

Read-only will not work. PWM writes are writes to sysfs attribute
files, and kernel attribute files reject writes on a read-only mount
with `EROFS` before the driver's `store` callback is even invoked.

### Permissions: the udev rule runs on the host, not in the container

`deploy/90-ventd-hwmon.rules` `chgrp`s `pwm<N>` / `pwm<N>_enable` to
the `ventd` group and adds `g+w` whenever a hwmon device appears.
That rule is executed by udevd, which runs **on the host**. The
container has no udevd of its own (and even if it did, it could not
see kernel uevents from a separate network namespace — see "Uevent
namespace reality" below).

Two consequences:

1. The host must have `deploy/90-ventd-hwmon.rules` installed at
   `/etc/udev/rules.d/90-ventd-hwmon.rules` and reloaded with
   `sudo udevadm control --reload && sudo udevadm trigger
   --subsystem-match=hwmon` before the container is useful. If the
   host also has a native `ventd` install, this is already done.
2. The in-container `ventd` user must share a GID with the host's
   `ventd` group so the `g+w` bit applies. The Dockerfile defaults
   `ventd` to GID 472, and the compose file defaults `user` to
   `472:472`. If your host's `ventd` group has a different GID
   (typical when `useradd --system` picked the first free sub-500
   GID), align one of the two sides:

   ```bash
   # option A: move the host group (if no other service uses it)
   sudo groupmod -g 472 ventd
   sudo udevadm trigger --subsystem-match=hwmon

   # option B: rebuild the image with the host's GID
   #   sets both the build-arg (so the in-image group is created
   #   with that GID) and the runtime user: value (so the
   #   container process runs under it).
   VENTD_GID=$(getent group ventd | cut -d: -f3) \
     docker compose -f packaging/docker/docker-compose.yml up -d --build

   # or, with plain docker:
   docker build \
     --build-arg VENTD_GID=$(getent group ventd | cut -d: -f3) \
     --file packaging/docker/Dockerfile \
     --tag ventd:local \
     .
   ```

   The UID stays fixed at 472 regardless — only the group
   participates in the sysfs DAC check. Run
   `ls -l /sys/class/hwmon/hwmon*/pwm*` on the host to confirm the
   group on the pwm files matches the in-container GID before
   assuming permissions are wrong.

### Fallback: no udev rule on the host

If installing the udev rule is not an option (ephemeral host, read-
only distro image, policy), uncomment the following in
`docker-compose.yml`:

```yaml
cap_drop:
  - ALL
cap_add:
  - DAC_OVERRIDE
```

`CAP_DAC_OVERRIDE` lets the container bypass filesystem DAC checks,
which is strictly more privilege than the GID-alignment path. It is
documented here because some environments cannot modify host udev;
it is not the default, and you should not use it on a multi-tenant
host.

### Uevent namespace reality

ventd subscribes to `NETLINK_KOBJECT_UEVENT` (see
`internal/hwmon/uevent_linux.go`) for hot-plug topology changes.
Kernel uevents are delivered **per network namespace**, and only the
host namespace receives uevents from real hardware. A container on a
bridge network has its own netns; its netlink socket bind succeeds,
but no messages arrive.

`CAP_NET_ADMIN` alone does **not** fix this. The only way to receive
uevents from a container is to share the host's network namespace.

The compose file's default is therefore `network_mode: host`. The
web UI on `0.0.0.0:9999` is reachable at `http://<host-ip>:9999`
exactly as with a native install.

#### Bridged networking (no uevents)

If you must run on a bridge (because of reverse-proxy routing or
multi-tenant isolation), comment out `network_mode: host` and use
the `ports:` block instead. You will lose sub-second hot-plug
detection. ventd will then rely on its 5-minute periodic rescan (see
`internal/hwmon/watcher.go` — "two complementary loops: a periodic
rescan (safety net, 5 min) and a [uevent] stream"). Pulling a fan
cable, hot-plugging an AIO, or a driver rebind can take up to 5
minutes to be picked up. No PWM is written to a missing fan in the
meantime; the safety envelope in `.claude/rules/hwmon-safety.md`
(ENOENT/EIO graceful skip) is preserved.

Document this degradation to your users before choosing bridged
networking. It is a correctness fallback, not a silent downgrade.

## NVIDIA / NVML

The Docker image is built with `-tags nonvidia`, matching the
`ventd-musl` goreleaser build. Reason: NVML is loaded via purego's
runtime `dlopen` of `libnvidia-ml.so.1`, which pulls in glibc
SONAMEs that are not present on alpine/musl. The binary would panic
at first NVML call.

If you need GPU temperatures for fan curves, run ventd natively on a
glibc host (install.sh, .deb, .rpm, or AUR `ventd-bin`). There is no
supported path to NVML inside this image.

## Host prerequisites

Minimum viable host setup:

1. Kernel hwmon modules loaded for your platform (e.g.
   `nct6687d`, `it87`, `k10temp`, `coretemp`, `amdgpu`, …). This is
   a native-install concern and is not solved by the container —
   the container shares the host kernel, so the host must have
   loaded the drivers.
2. `deploy/90-ventd-hwmon.rules` installed at
   `/etc/udev/rules.d/90-ventd-hwmon.rules` and reloaded.
3. A `ventd` group on the host with GID 472, or the image rebuilt
   with `--build-arg VENTD_GID=<host-gid>` (see "Permissions" above).
4. `docker compose` v2 (the v1 `docker-compose` Python tool is end-
   of-life; the compose file is v2 syntax).

Optional but recommended:

- `hwmon.dynamic_rebind: true` in `config.yaml`, so the uevent
  stream (see above) can trigger a rebind without a container
  restart.

## Limitations vs native install

| Feature                         | Native install       | Docker image                      |
|---------------------------------|----------------------|-----------------------------------|
| Systemd `Type=notify` + sd_notify| Yes                  | No — Docker doesn't speak sd_notify|
| `WatchdogSec=2s`                | Yes, kernel-enforced | Replaced by `HEALTHCHECK` + `restart: unless-stopped` (coarser) |
| `OnFailure=ventd-recover.service`| Yes — resets pwm_enable to 1 | No — rely on container restart + hwmon driver defaults |
| AppArmor profile                 | Yes (see `deploy/apparmor.d/`) | No — container has its own confinement (seccomp, namespaces) |
| udev rule installation          | Done by installer    | Must be installed on the host separately |
| NVIDIA GPU temperatures (NVML)  | Yes (glibc hosts)    | No — built with `-tags nonvidia`  |
| Uevent hot-plug detection       | Yes                  | Yes, only with `network_mode: host` |
| Upgrade in place                | `install.sh` / distro pkg manager | `docker compose pull && up -d` (only if you publish yourself) |

Because Docker provides no in-kernel watchdog, a hung daemon will
not be forcibly killed at the 2-second mark the way systemd's
`WatchdogSec` does on the native install. `HEALTHCHECK` runs every
30 seconds and marks the container unhealthy after three
consecutive failures, which `restart: unless-stopped` then acts on.
That is a ~2-minute worst-case detection window rather than 2
seconds. If you need faster fault detection, use the native install.

None of this changes the hardware-safety envelope documented in
`.claude/rules/hwmon-safety.md`: PWM clamp, pump floor, allow_stop
gate, and watchdog-on-exit restore are enforced by the in-binary
controller regardless of how ventd is packaged.

## Multi-arch builds

The Dockerfile honours `TARGETARCH` and is tested for
`linux/amd64` + `linux/arm64`. To build a multi-arch image locally:

```bash
docker buildx create --name ventd-builder --use   # once

docker buildx build \
  --platform linux/amd64,linux/arm64 \
  --file packaging/docker/Dockerfile \
  --build-arg VERSION=dev \
  --build-arg COMMIT=$(git rev-parse --short HEAD) \
  --build-arg BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ) \
  --tag ventd:local \
  .
```

The build context is the repo root (`.`) so the Dockerfile can
`COPY go.mod go.sum ./` and `COPY . .` — do not run this from
`packaging/docker/`.

## Image publishing

Publishing images to Docker Hub / GHCR is a Phoenix-only action. No
Claude Code or Cowork session pushes to a registry. If / when images
start publishing:

- Source of truth stays on `github.com/ventd/ventd`; tags match
  release tags (e.g. `ventd/ventd:0.3.0`, `ventd/ventd:latest`).
- Image signing and SBOM generation are not wired yet — future work.

## Validation

The following are the checks Phoenix (or a reviewer) runs before
considering a change to this directory shippable. They are cheap:

```bash
# compose file syntax
docker compose -f packaging/docker/docker-compose.yml config >/dev/null

# image build (requires docker + buildx)
docker buildx build \
  --file packaging/docker/Dockerfile \
  --load \
  --tag ventd:test \
  .

# runtime smoke (requires the host udev rule above)
docker compose -f packaging/docker/docker-compose.yml up -d --build
curl -fsS http://localhost:9999/api/ping
docker compose -f packaging/docker/docker-compose.yml down
```

No automated CI coverage is wired for the Docker build yet; it is a
follow-up once the image has landed and a publish runbook exists.
