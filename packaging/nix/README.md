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
  module that mirrors `deploy/ventd.service` and
  `deploy/ventd-recover.service` exactly, plus the udev rule from
  `deploy/90-ventd-hwmon.rules`.

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

## Development

`nix flake check ./packaging/nix` from the repo root will type-check
the flake. Requires the `nix` CLI with flake support enabled
(`experimental-features = nix-command flakes` in `nix.conf`).
