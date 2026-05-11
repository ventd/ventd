# v0.6.0 Phase C5 — Proxmox host soak (in progress)

**Host:** `root@192.168.7.10` (`pve`) — Proxmox VE bare-metal hypervisor host
**Distro / kernel:** Debian 13 (trixie), 6.17.13-2-pve
**Chip:** IT8688 (5 fans on hwmon3)
**ventd version:** **v0.5.35** (upgraded from v0.5.26 immediately before soak start; commit `50d861271d4dc2f189036dc6c1bf8a109a868e1c`)
**Daemon up since:** 2026-05-10 11:45:04 AEST
**Workload at soak start:** Proxmox host carrying LXC containers (radarr / sonarr stack per `project_arr_stack.md`); host loadavg `0.45 1.55 1.66` at start. No vzdump / backup / qemu-img / rsync running on the host itself.
**Sysclass:** `mid_desktop` (sysclass detected from `pwm_channels_present`; the Proxmox host doesn't read as virtualised because the hypervisor host itself is bare-metal — only the guest VMs would trip RULE-PROBE-02's virt detection).
**Preset:** `performance`
**Soak harness:** `cmd/ventd-soak` from PR #1023, watch loop @ 300 s interval
**Soak start:** 2026-05-10 11:47:54 UTC (2026-05-10 01:47:54 AEST + 10h)
**NDJSON trace:** `/root/ventd-soak-traces/proxmox-20260510T014754.ndjson` (host-local; nohup background, PID 515942)

## Update path executed

1. Pulled `https://github.com/ventd/ventd/releases/latest/download/install.sh` to `/tmp/ventd-install.sh`.
2. First run blocked on `dkms_missing` per RULE-PREFLIGHT-BUILD-03 (v0.5.34's preflight orchestrator).
3. Installed `dkms 3.2.2-1~deb13u1` via `apt-get install -y dkms`.
4. Re-ran `bash /tmp/ventd-install.sh` — preflight cleared, binary swapped to v0.5.35, daemon restarted.
5. Stopped daemon, removed `/var/lib/ventd/smart/{shard-B,conf-A,shard-C}` (fresh smart-mode state per Phoenix's "recalibrate"), restarted daemon.
6. Waited for first persist tick to write 5 fresh shard-B CBOR files.

## Baseline observation

5 hwmon3 channels (IT8688 pwm1..pwm5). All 5 in identical state at soak start:

| field | value |
|---|---|
| `n_samples` | 0 / 20 (5·d²=5·4) [--] |
| `tr(P)` | **2000.000** / 50.000 [--] |
| `theta` | [0.0000, 0.0000] [--] |
| `lambda` | 0.9900 |
| `last_seen` | — (Unix=0; never updated) |
| verdict | `warming up — no observations yet (excitation gate may still be closed)` |

**Note on `tr(P)=2000`:** the v0.5.35 daemon's default `InitialP` jumped from 50 (on v0.5.31) to 1000 (on v0.5.35) — `tr(P_0) = d × 1000 = 2000` for d=2. The soak harness's hardcoded `tr_p_initial = d × 50` cosmetically mis-displays the gate threshold, but the motion-detection logic (n_samples>0, theta non-zero, last_seen!=1970) still works correctly. **Follow-up:** make the harness derive `InitialP` from the first-save bucket. Does NOT block this soak.

## Smart-mode pipeline state

- `/var/lib/ventd/smart/shard-B/` — 5 fresh CBOR shards (created 11:47, written by the daemon's first persist tick after restart at 11:45).
- `/var/lib/ventd/smart/conf-A/` — **absent**. Layer-A `SeenFirstContact` (RULE-CONFA-FIRSTCONTACT-01) hasn't flipped yet.
- `/var/lib/ventd/smart/shard-C/` — **absent**. Layer-C deferred while parent Layer-B is unidentifiable (RULE-CMB-IDENT-01).

## Hypothesis for this host

The Proxmox host runs LXC containers but the host itself is mostly idle (the workload runs inside containers, not as host-visible processes). RULE-IDLE-06's blocklist (`ffmpeg`, `rsync`, `make`, `apt`, etc.) operates against host-process names, not container processes. So the opportunistic-gate idle preconditions may actually fire on this host where they never fired on the desktop. If Layer-B converges on the Proxmox host within 24 h, that's the first counter-evidence to the desktop hypothesis — opportunistic probing CAN work in production, just not on a host running tdarr-class workload directly.

If the trace shows convergence on Proxmox under realistic-but-host-idle conditions, the v0.6.0 RFC at #1024 strengthens (path 1 widening helps EVERY host, not just hypervisor-light ones). If the trace ALSO stays flat on Proxmox, the conclusion is even stronger (issue #1024 is load-bearing for v0.6.0).

## Operations

- Live tail: `sshpass -p password ssh root@192.168.7.10 'tail -f /root/ventd-soak-traces/proxmox-20260510T014754.ndjson'`
- Stop: `sshpass -p password ssh root@192.168.7.10 'pkill -f ventd-soak-watch.sh'`
- Pull: `sshpass -p password scp root@192.168.7.10:/root/ventd-soak-traces/proxmox-20260510T014754.ndjson /root/ventd-work/soak-results/proxmox-2026-05-10/`

## Artefacts

- `baseline-human.txt` — human-readable snapshot at T=0
- `baseline.ndjson` — JSON snapshot at T=0 (1 line per channel)
- `proxmox-20260510T014754.ndjson` (pulled later) — full trace
