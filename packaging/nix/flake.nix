{
  description = "ventd — automatic Linux fan control daemon";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs = { self, nixpkgs }:
    let
      # Supported systems. ventd is Linux-only (hwmon + systemd); there
      # is no macOS/Windows story.
      systems = [ "x86_64-linux" "aarch64-linux" ];
      forAllSystems = f:
        nixpkgs.lib.genAttrs systems
          (system: f nixpkgs.legacyPackages.${system});

      # Build a ventd derivation. extraTags mirrors the .goreleaser.yml
      # split between the default glibc build (NVML via purego) and the
      # musl build tagged `nonvidia`.
      mkVentd = pkgs: { extraTags ? [ ] }: pkgs.buildGoModule rec {
        pname = "ventd";
        version = "0.3.0-dev";

        # Source is the repo root (two dirs up from this flake). Works
        # for local `nix build` from within packaging/nix and for
        # `github:ventd/ventd?dir=packaging/nix` remote refs — Nix
        # puts the full git tree in the store either way.
        src = ../..;

        # vendorHash is a placeholder. On first build, Nix prints the
        # real hash in the error output; paste that in and commit.
        # Regenerate when go.mod / go.sum change.
        vendorHash = nixpkgs.lib.fakeHash;

        subPackages = [ "cmd/ventd" ];

        # Mirror the .goreleaser.yml build env. CGO off keeps the
        # binary portable across libc variants; -trimpath strips
        # build-host absolute paths from the executable so the build
        # is reproducible regardless of Nix store location.
        env.CGO_ENABLED = "0";
        tags = extraTags;
        flags = [ "-trimpath" ];

        ldflags = [
          "-s"
          "-w"
          "-X main.version=${version}"
          "-X main.commit=${self.rev or "dirty"}"
          # Fixed epoch for reproducible builds. Matches how Nix
          # thinks about determinism; release tags should override
          # via an explicit override if a real timestamp is needed.
          "-X main.date=1970-01-01T00:00:00Z"
        ];

        # The ventd-wait-hwmon shell script is an ExecStartPre gate
        # invoked from the systemd unit. Ship it alongside the binary
        # at a stable Nix-store path; the module references
        # ${cfg.package}/libexec/ventd-wait-hwmon.
        #
        # patchShebangs rewrites "#!/usr/bin/env bash" to the bash in
        # the Nix store. Without this, the script fails to exec on
        # NixOS where /usr/bin/env doesn't resolve bash by that name.
        postInstall = ''
          install -Dm0755 scripts/ventd-wait-hwmon \
            $out/libexec/ventd-wait-hwmon
          patchShebangs $out/libexec/ventd-wait-hwmon
        '';

        meta = with nixpkgs.lib; {
          description = "Automatic Linux fan control daemon (auto-detects hardware, auto-calibrates, auto-recovers)";
          homepage = "https://github.com/ventd/ventd";
          license = licenses.gpl3Plus;
          mainProgram = "ventd";
          platforms = systems;
          maintainers = [ ];
        };
      };
    in
    {
      packages = forAllSystems (pkgs: rec {
        default = ventd;

        # Default glibc build. Includes NVML support via purego —
        # dynamically loads libnvidia-ml.so.1 at runtime when present.
        ventd = mkVentd pkgs { };

        # musl / no-NVIDIA variant. Matches the goreleaser `ventd-musl`
        # id and the `nonvidia` build tag. For Alpine-style
        # distributions or hosts that never have an NVIDIA driver.
        ventd-musl = mkVentd pkgs { extraTags = [ "nonvidia" ]; };
      });

      # NixOS module. Enable with:
      #   services.ventd.enable = true;
      # See nixos-module.nix for the full option tree.
      nixosModules.default = import ./nixos-module.nix self;
      nixosModules.ventd = self.nixosModules.default;
    };
}
