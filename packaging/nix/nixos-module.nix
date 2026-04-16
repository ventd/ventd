# NixOS module for ventd.
#
# Single source of truth for the systemd units and the udev rule lives
# under deploy/. This module reads those files at evaluation time and
# hands them to systemd verbatim, so the Nix packaging can never drift
# from the deb/rpm/tarball packaging.
#
# - deploy/ventd.service          → systemd.packages (path-substituted)
# - deploy/ventd-recover.service  → systemd.packages (path-substituted)
# - deploy/90-ventd-hwmon.rules   → services.udev.extraRules via readFile
#
# The only transformation applied is rewriting the FHS-style install
# paths that the tarball and distro packages ship with to the package's
# Nix store paths:
#
#     /usr/local/bin/ventd           → ${cfg.package}/bin/ventd
#     /usr/local/sbin/ventd-wait-hwmon → ${cfg.package}/libexec/ventd-wait-hwmon
#
# Everything else (sandbox, watchdog, OnFailure=, After=, sockets) is
# taken from deploy/*.service byte-for-byte. If a sandbox directive is
# tightened there, NixOS picks it up automatically on the next rebuild.
# packaging/nix/check-drift.sh is the belt-and-braces CI guard that
# asserts every critical directive from deploy/ventd.service made it
# into the rendered unit installed at /etc/systemd/system/ventd.service.
#
# NVIDIA note: the default package includes NVML support. If the host
# runs the nvidia driver you still must set
# `hardware.nvidia.modesetting.enable = true;` or equivalent at the
# system level — ventd only reads NVML, it does not install or
# configure the driver.

self:
{ config, lib, pkgs, ... }:

let
  cfg = config.services.ventd;

  yamlFormat = pkgs.formats.yaml { };

  # Serialise the user's settings attrset to /etc/ventd/config.yaml.
  # The upstream config schema is documented in config.example.yaml at
  # the repo root; anything valid there is valid here.
  configFile = yamlFormat.generate "ventd-config.yaml" cfg.settings;

  # Rewrite the FHS install paths in each shipped unit to the Nix
  # store paths exposed by cfg.package. The source text is read from
  # deploy/ so nothing else drifts — any new directive added upstream
  # flows through unchanged. Each unit gets only the substitutions it
  # actually needs: --replace-fail aborts the build on a missing
  # match, so ventd-recover.service (no wait-hwmon reference) must
  # not ask for the wait-hwmon rewrite.
  ventdServiceUnit = pkgs.runCommand "ventd.service" { } ''
    cp ${../../deploy/ventd.service} $out
    chmod +w $out
    substituteInPlace $out \
      --replace-fail "/usr/local/bin/ventd" "${cfg.package}/bin/ventd" \
      --replace-fail "/usr/local/sbin/ventd-wait-hwmon" "${cfg.package}/libexec/ventd-wait-hwmon"
  '';

  ventdRecoverUnit = pkgs.runCommand "ventd-recover.service" { } ''
    cp ${../../deploy/ventd-recover.service} $out
    chmod +w $out
    substituteInPlace $out \
      --replace-fail "/usr/local/bin/ventd" "${cfg.package}/bin/ventd"
  '';

  # systemd.packages expects units at $out/lib/systemd/system/.
  ventdUnits = pkgs.runCommand "ventd-systemd-units" { } ''
    install -Dm644 ${ventdServiceUnit} $out/lib/systemd/system/ventd.service
    install -Dm644 ${ventdRecoverUnit} $out/lib/systemd/system/ventd-recover.service
  '';
in
{
  options.services.ventd = {
    enable = lib.mkEnableOption "ventd fan control daemon";

    package = lib.mkOption {
      type = lib.types.package;
      default = self.packages.${pkgs.stdenv.hostPlatform.system}.default;
      defaultText = lib.literalExpression ''
        ventd.packages.''${system}.default
      '';
      description = ''
        The ventd package to use. Defaults to the flake's default
        build (glibc + NVML). Override with ventd-musl for musl
        targets or when NVIDIA support is undesired.
      '';
    };

    settings = lib.mkOption {
      type = yamlFormat.type;
      default = { };
      description = ''
        ventd configuration as a Nix attribute set, serialised to
        /etc/ventd/config.yaml on activation. See
        <https://github.com/ventd/ventd/blob/main/config.example.yaml>
        for the full schema.

        If this is left at the default empty set, ventd boots into
        first-run setup mode and the one-time setup token is printed
        to the journal. Open the web UI on port 9999 to complete
        configuration; the wizard writes the resulting config back
        to /etc/ventd/config.yaml.

        Note: if you let the web UI write the config, switching to
        declaring `settings` later will overwrite those web-wizard
        changes on the next activation. Pick one source of truth.
      '';
      example = lib.literalExpression ''
        {
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
        }
      '';
    };

    openFirewall = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = ''
        Whether to open TCP port 9999 (the web UI listener) in the
        NixOS firewall. Leave disabled if you front ventd with a
        reverse proxy or only access it over tailnet/VPN.
      '';
    };
  };

  config = lib.mkIf cfg.enable {
    # ── Config file ──────────────────────────────────────────────────
    # Rendered only when the user supplied a non-empty settings attrset.
    # Otherwise ventd runs in first-boot setup mode and creates the
    # config itself once the web wizard finishes.
    environment.etc = lib.mkIf (cfg.settings != { }) {
      "ventd/config.yaml".source = configFile;
    };

    # ── System user ──────────────────────────────────────────────────
    # Matches scripts/_ventd_account.sh: system user, no login shell,
    # no home, dedicated group. NixOS picks a stable UID from the
    # systemusers range.
    users.users.ventd = {
      isSystemUser = true;
      group = "ventd";
      description = "ventd fan control daemon";
    };
    users.groups.ventd = { };

    # ── Udev rule ────────────────────────────────────────────────────
    # Read verbatim from deploy/90-ventd-hwmon.rules — no duplication.
    services.udev.extraRules =
      builtins.readFile ../../deploy/90-ventd-hwmon.rules;

    # ── Firewall ─────────────────────────────────────────────────────
    networking.firewall = lib.mkIf cfg.openFirewall {
      allowedTCPPorts = [ 9999 ];
    };

    # ── Systemd units ────────────────────────────────────────────────
    # Install the deploy/*.service files directly (path-substituted).
    # NixOS's systemd module merges the augmentations below with the
    # literal unit content, so the sandbox, watchdog, OnFailure=, and
    # every other directive stays single-sourced in deploy/.
    systemd.packages = [ ventdUnits ];

    # wait-hwmon's ExecStartPre invokes awk and sort. NixOS does not
    # populate /usr/bin on the systemd PATH, so make them available
    # through the unit's own PATH. This augments — does not override —
    # the unit loaded from systemd.packages.
    systemd.services.ventd = {
      wantedBy = [ "multi-user.target" ];
      path = with pkgs; [ gawk coreutils ];
    };

    # ventd-recover is an OnFailure= target, not wanted by any regular
    # target. The [Install] stanza in deploy/ventd-recover.service sets
    # WantedBy=multi-user.target to keep `systemctl enable` a no-op;
    # restate it here so systemd.packages' symlink farm matches.
    systemd.services.ventd-recover = {
      wantedBy = [ "multi-user.target" ];
    };
  };
}
