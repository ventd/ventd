# NixOS module for ventd.
#
# Mirrors deploy/ventd.service and deploy/ventd-recover.service bit-for-
# bit — sandbox parity with the upstream systemd units is intentional.
# If you update deploy/ventd.service, update this file in the same PR
# and re-check via:
#     diff <(systemd-analyze cat-config .../ventd.service) \
#          <(cat deploy/ventd.service)
#
# The NVIDIA-side consideration: the default package includes NVML
# support. If your host runs nvidia drivers you must set
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
    # Content copied verbatim from deploy/90-ventd-hwmon.rules. This
    # grants the ventd group write access to /sys/class/hwmon/*/pwmN
    # and pwmN_enable so the daemon can drive fans without running as
    # root. Chip-agnostic — matches every hwmon device and silently
    # no-ops on temp-only chips (coretemp, k10temp, acpitz, …).
    services.udev.extraRules = ''
      SUBSYSTEM=="hwmon", RUN+="/bin/sh -c 'chgrp ventd /sys%p/pwm[0-9]* /sys%p/pwm[0-9]*_enable 2>/dev/null; chmod g+w /sys%p/pwm[0-9]* /sys%p/pwm[0-9]*_enable 2>/dev/null; exit 0'"
    '';

    # ── Firewall ─────────────────────────────────────────────────────
    networking.firewall = lib.mkIf cfg.openFirewall {
      allowedTCPPorts = [ 9999 ];
    };

    # ── Main daemon unit ─────────────────────────────────────────────
    # Every directive below has a matching line in deploy/ventd.service.
    # Keep them in sync. The inline comments reference the safety
    # rationale documented in the upstream unit.
    systemd.services.ventd = {
      description = "Fan Control Daemon";
      documentation = [ "https://github.com/ventd/ventd" ];
      after = [ "local-fs.target" ];
      wantedBy = [ "multi-user.target" ];

      # The wait-hwmon ExecStartPre script calls awk and sort. On
      # NixOS those are not on the default systemd PATH; declare
      # them explicitly so the gate actually runs on cold boot.
      path = with pkgs; [ gawk coreutils ];

      # OnFailure lives in [Unit], not [Service]. systemd silently
      # ignores it under [Service]; scripts/check-unit-onfailure.sh
      # is the regression guard for the upstream unit.
      unitConfig = {
        OnFailure = "ventd-recover.service";
      };

      serviceConfig = {
        # Type=notify + WatchdogSec: ventd pings WATCHDOG=1 every
        # WATCHDOG_USEC/2; a hung main loop is killed and the
        # OnFailure= chain runs the recover oneshot.
        Type = "notify";
        NotifyAccess = "main";

        # ExecStartPre gate for the cold-boot hwmon race (#103).
        # Leading "-" = non-fatal, same as deploy/ventd.service.
        # Shipped inside the package at $out/libexec so the path is
        # stable across Nix store generations.
        ExecStartPre = "-${cfg.package}/libexec/ventd-wait-hwmon";

        ExecStart = "${cfg.package}/bin/ventd -config /etc/ventd/config.yaml";
        ExecReload = "/bin/kill -HUP $MAINPID";

        Restart = "on-failure";
        RestartSec = "1s";

        # Watchdog kick interval. systemd exports WATCHDOG_USEC
        # = 2_000_000; the daemon pings every ~1s.
        WatchdogSec = "2s";

        StandardOutput = "journal";
        StandardError = "journal";
        SyslogIdentifier = "ventd";

        User = "ventd";
        Group = "ventd";

        # ── Sandbox: process isolation ──────────────────────────
        NoNewPrivileges = true;
        CapabilityBoundingSet = "";
        AmbientCapabilities = "";

        # ── Sandbox: filesystem ─────────────────────────────────
        ProtectSystem = "strict";
        ProtectHome = true;
        PrivateTmp = true;

        # /run/ventd (0700) for the first-boot setup token.
        RuntimeDirectory = "ventd";
        RuntimeDirectoryMode = "0700";

        # /etc/ventd (0750) so the ventd group can read the config.
        ConfigurationDirectory = "ventd";
        ConfigurationDirectoryMode = "0750";

        # Every path ventd writes to. /sys/class/hwmon resolves to
        # /sys/devices/... symlinks; if a kernel surfaces an
        # unresolved hwmon inode, add the matching /sys/devices
        # prefix here.
        ReadWritePaths = [
          "/etc/ventd"
          "/run/ventd"
          "/sys/class/hwmon"
        ];

        # ── Sandbox: network ────────────────────────────────────
        # AF_UNIX (journal), AF_INET/AF_INET6 (web UI),
        # AF_NETLINK (hwmon uevent watcher — NETLINK_KOBJECT_UEVENT).
        # Dropping AF_NETLINK forces the 5-minute periodic rescan
        # fallback and loses hot-plug detection.
        RestrictAddressFamilies = [
          "AF_UNIX"
          "AF_INET"
          "AF_INET6"
          "AF_NETLINK"
        ];

        # ── Sandbox: kernel and syscalls ────────────────────────
        ProtectKernelTunables = true;
        ProtectKernelModules = true;
        ProtectKernelLogs = true;
        ProtectControlGroups = true;
        SystemCallFilter = [ "@system-service" ];
        SystemCallErrorNumber = "EPERM";
        SystemCallArchitectures = "native";

        # ── Sandbox: defence-in-depth ───────────────────────────
        LockPersonality = true;
        MemoryDenyWriteExecute = true;
        RestrictRealtime = true;
        RestrictSUIDSGID = true;
        RestrictNamespaces = true;
        RemoveIPC = true;
        ProtectHostname = true;
        ProtectClock = true;
        ProtectProc = "invisible";
        ProcSubset = "pid";
        UMask = "0077";
      };
    };

    # ── Recovery oneshot ─────────────────────────────────────────────
    # Mirrors deploy/ventd-recover.service. Fires once via the main
    # unit's OnFailure= when the daemon dies unexpectedly (SIGKILL,
    # OOM, watchdog, escaped panic). Resets every pwm_enable to the
    # kernel default (1 = automatic), handing fans back to firmware.
    # Runs as root outside the sandbox — the main unit's User=ventd
    # could only touch the chips it was configured for.
    systemd.services.ventd-recover = {
      description = "Reset hwmon pwm_enable to automatic on ventd failure";
      documentation = [ "https://github.com/ventd/ventd" ];

      unitConfig = {
        DefaultDependencies = false;
        Conflicts = "shutdown.target";
      };

      serviceConfig = {
        Type = "oneshot";
        User = "root";
        ExecStart = "${cfg.package}/bin/ventd --recover";
        TimeoutStartSec = "10s";
        Restart = "no";
        SyslogIdentifier = "ventd-recover";
      };
    };
  };
}
