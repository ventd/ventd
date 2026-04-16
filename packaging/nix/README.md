# NixOS flake + module — ventd

A flake and NixOS module for building and running ventd on NixOS.
Lives under `packaging/nix/` to keep the repo root clean; the
flake references the repo source via `../..`.

## What it provides

- `packages.${system}.default` — the ventd binary, built from source
  via `buildGoModule`. Includes NVML support for NVIDIA GPU readings.
- `packages.${system}.ventd-musl` — build with the `nonvidia` tag
  (mirrors the `.goreleaser.yml` musl variant). Pick this if the host
  has no NVIDIA driver or you need a smaller surface.
- `nixosModules.default` / `nixosModules.ventd` — declarative service
  module that installs `deploy/ventd.service`,
  `deploy/ventd-recover.service`, and `deploy/90-ventd-hwmon.rules`
  directly (see *Single source of truth* below).

Supported systems: `x86_64-linux`, `aarch64-linux`.

## Quick start

Add the flake to your system flake's inputs:

```nix
{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

    ventd.url = "github:ventd/ventd?dir=packaging/nix";
    ventd.inputs.nixpkgs.follows = "nixpkgs";
  };

  outputs = { self, nixpkgs, ventd, ... }: {
    nixosConfigurations.myhost = nixpkgs.lib.nixosSystem {
      system = "x86_64-linux";
      modules = [
        ventd.nixosModules.default
        ./configuration.nix
      ];
    };
  };
}
```

Then in `configuration.nix`:

```nix
{ config, ... }:
{
  services.ventd = {
    enable = true;
    openFirewall = false;     # open 9999/tcp? only if UI is LAN-exposed

    # Optional: declare the config entirely in Nix. If omitted, ventd
    # starts in first-run setup mode — open http://host:9999 and the
    # web wizard writes /etc/ventd/config.yaml itself.
    settings = {
      poll_interval = "2s";
      web = {
        listen = "127.0.0.1:9999";
        session_ttl = "24h";
      };
      sensors = [
        {
          name = "cpu_package";
          type = "hwmon";
          path = "/sys/class/hwmon/hwmon4/temp1_input";
        }
      ];
      fans = [ ];
      curves = [ ];
    };
  };
}
```

`nixos-rebuild switch` and the service comes up under its own
unprivileged `ventd` user with the full sandbox from
`deploy/ventd.service`.

## vendorHash — one-time bootstrap

The shipped `flake.nix` has `vendorHash = nixpkgs.lib.fakeHash;`. This
is standard practice for a freshly packaged Go project: Nix computes
the real hash on first build and prints it in the error output.

Replace it once and commit:

```console
$ cd packaging/nix
$ nix build .#default
error: hash mismatch in fixed-output derivation '/nix/store/…-ventd-0.3.0-dev-go-modules.drv':
         specified: sha256-AAAAAAAA…
            got:    sha256-abc123…xyz
$ sed -i 's|fakeHash|"sha256-abc123…xyz"|' flake.nix
```

Regenerate whenever `go.mod` or `go.sum` change.

Long-term we may ship the real hash in-tree and bump it in the same PR
as go.sum edits; for now the placeholder mirrors the AUR `SKIP` pattern
and keeps the repo reviewable without a remote fetch.

## Building without installing

From within `packaging/nix/`:

```console
$ nix build .#default          # glibc build
$ nix build .#ventd-musl       # nonvidia tag
$ ./result/bin/ventd --help
```

Or remote, without cloning:

```console
$ nix build github:ventd/ventd?dir=packaging/nix
```

## Running on a non-NixOS host

The flake's package output is usable on any Linux with Nix installed:

```console
$ nix run github:ventd/ventd?dir=packaging/nix -- -config /tmp/my.yaml
```

However the module only applies on NixOS. For Arch, Debian, Fedora,
etc., use the native install path (`scripts/install.sh`, the AUR
`ventd` / `ventd-bin` packages, or the .deb/.rpm artefacts) — the
packaging there is more idiomatic than a Nix store build.

## What the module configures

Enabling `services.ventd.enable = true;` wires up:

- `systemd.services.ventd` — `Type=notify`, `WatchdogSec=2s`,
  `User=ventd`, full sandbox (`ProtectSystem=strict`, `ProtectHome`,
  `PrivateTmp`, `ReadWritePaths`, `RestrictAddressFamilies` including
  `AF_NETLINK` for hwmon uevents, `OnFailure=ventd-recover.service`).
- `systemd.services.ventd-recover` — oneshot that resets every
  `pwm_enable` to automatic when the main daemon dies unexpectedly.
- `users.users.ventd` + `users.groups.ventd` — system user, no login.
- `services.udev.extraRules` — the chip-agnostic `hwmon` rule that
  grants the `ventd` group DAC write on `pwm<N>` / `pwm<N>_enable`.
- `environment.etc."ventd/config.yaml"` — generated from
  `services.ventd.settings` when that attribute is non-empty.
- `networking.firewall.allowedTCPPorts` — adds 9999 only when
  `openFirewall = true`.

## Not handled here

- **TLS cert paths** — set `web.tls_cert` / `web.tls_key` in
  `services.ventd.settings.web` if you want ventd to terminate TLS
  directly. For Let's Encrypt, front with nginx or Caddy instead.
- **AppArmor / SELinux** — the shipped profiles in `deploy/apparmor.d`
  and `deploy/selinux` target the `/usr/local/bin/ventd` install path
  used by the tarball and `.deb`/`.rpm`. NixOS installs the binary at
  a Nix store path, so those profiles would need rewriting to attach.
  The systemd sandbox (`NoNewPrivileges`, `ProtectSystem=strict`,
  the syscall filter, etc.) is the primary confinement here.
- **NVIDIA driver** — you own the driver. ventd only reads NVML. If
  you want GPU fan / temp readings, ensure libnvidia-ml is available
  to the daemon.
- **Automatic nixpkgs inclusion** — this flake is not in nixpkgs.
  Upstream the package if you want `nixpkgs#ventd`; until then, pin
  the flake in your system inputs.

## Single source of truth

The module does not duplicate the systemd unit text or the udev rule.
Everything comes from `deploy/`:

- `deploy/ventd.service` and `deploy/ventd-recover.service` are each
  handed to `systemd.packages` verbatim via `runCommand` +
  `substituteInPlace`. Two separate `runCommand` derivations, because
  the main unit references `ventd-wait-hwmon` and the recover unit
  doesn't — `--replace-fail` would abort the build on a missed match
  if both units shared one substitution recipe. The only rewrites
  applied are these FHS-path fixes:

  |     deploy/ ships             |   Nix store path                              |   applied to                     |
  |-------------------------------|-----------------------------------------------|----------------------------------|
  | `/usr/local/bin/ventd`        | `${cfg.package}/bin/ventd`                    | both units                       |
  | `/usr/local/sbin/ventd-wait-hwmon` | `${cfg.package}/libexec/ventd-wait-hwmon` | `ventd.service` only             |

  Every other directive (sandbox fields, `WatchdogSec=`, `OnFailure=`,
  `After=`, etc.) flows through unchanged. Tightening the sandbox in
  `deploy/ventd.service` automatically tightens the NixOS unit too.

- `deploy/90-ventd-hwmon.rules` is loaded with `builtins.readFile` into
  `services.udev.extraRules`. No inline copy.

The `nix-drift` CI job runs `packaging/nix/check-drift.sh`, which
asserts:

1. The module references `deploy/ventd.service`,
   `deploy/ventd-recover.service`, and `deploy/90-ventd-hwmon.rules`
   through the mechanisms above.
2. The only path rewrites applied by the module's `--replace*`
   invocations are the two FHS-path fixes in the table above; any
   other token is flagged as suspicious (e.g. a silent rewrite of
   `WatchdogSec=2s` to `WatchdogSec=0`).

Content-level drift between `deploy/` and the module cannot exist
under this architecture: the Nix build literally copies
`deploy/ventd.service` into the store and applies the two path
rewrites. `--replace-fail` aborts the build if a path rewrite stops
matching, which is itself a deploy-side drift signal.

If a future nixpkgs change ever breaks the `systemd.packages`
merging we rely on and the module has to fall back to inlined
attributes, update `check-drift.sh` to re-parse the attrset and keep
the CI gate. Never land a version of this module that silently
duplicates `deploy/ventd.service` content without `nix-drift`
catching it.

## Development

`nix flake check ./packaging/nix` from the repo root will type-check
the flake. Requires the `nix` CLI with flake support enabled
(`experimental-features = nix-command flakes` in `nix.conf`).
