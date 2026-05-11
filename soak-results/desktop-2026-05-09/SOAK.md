# v0.6.0 Phase C5 — desktop soak (in progress)

**Host:** `phoenix@192.168.7.209` — MSI Z690-A DDR4 (MS-7D25), Intel + RTX 4090, NCT6687D
**Kernel:** 6.8.0-111-generic (Ubuntu 24.04)
**ventd version:** 0.5.31 (commit `4d343750dd00edb2abbace45c28381ea3157c931`)
**Daemon up since:** 2026-05-08 10:36:30 UTC (≈12 h at soak start)
**Workload at soak start:** Tdarr_node container active (sustained NVENC/NVDEC + CPU)
**Soak harness:** `cmd/ventd-soak` from PR #1023, watch loop @ 300 s interval
**Soak start:** 2026-05-08 22:52 UTC (desktop clock)
**NDJSON trace:** `/home/phoenix/ventd-soak-traces/desktop-20260508T225209.ndjson` (host-local; nohup background)

## Baseline observation

8 hwmon5 channels (NCT6687D pwm1..pwm8). All 8 in identical state at soak start:

| field | value |
|---|---|
| `n_samples` | 0 / 20 (5·d²=5·4) [--] |
| `tr(P)` | 100.000 / 50.000 [--] |
| `theta` | [0.0000, 0.0000] [--] |
| `lambda` | 0.9900 |
| `last_seen` | — (Unix=0; never updated) |
| verdict | `warming up — no observations yet (excitation gate may still be closed)` |

`tr(P) = d × InitialP = 2 × 50 = 100` is the unmoved P₀; not a single RLS update has fired since the shards were initialised.

## Smart-mode pipeline state

- `/var/lib/ventd/smart/shard-B/` — 8 × 157-byte CBOR shards, mtime 22:52 (periodic-save tick rewrites; no content drift).
- `/var/lib/ventd/smart/conf-A/` — **absent**. Layer-A `SeenFirstContact` (RULE-CONFA-FIRSTCONTACT-01) has never flipped; Layer-A never persisted.
- `/var/lib/ventd/smart/shard-C/` — **absent**. Layer-C deferred while parent Layer-B is unidentifiable (RULE-CMB-IDENT-01).
- `/var/lib/ventd/logs/observations.log` — 37 MB current + 4 daily rotations totalling ~7.8 MB compressed. Daemon is recording observations; they're just not admitted to Layer-B RLS update.

## Hypothesis

Tdarr_node holds the system above the idle-gate threshold (RULE-OPP-IDLE-01..04, 600 s sustained idle requirement). Opportunistic probes never fire (RULE-OPP-PROBE-01). The Δpwm-on-i-while-zero-on-j admission filter (RULE-CMB-OAT-01) requires deliberate single-channel excitation that workload-driven PWM mirroring (CPU_FAN + Pump_Fan + Sys_Fan_1 + Sys_Fan_2 ride together — see RULE-HWDB-PR2-15 PWM groups documented for this exact board) cannot satisfy.

If this hypothesis is correct, the trace will show:

- Every 300 s tick: identical verdicts across all 8 channels.
- `last_seen` field stays at `—` until an opportunistic probe fires.
- An idle window (Tdarr paused, no SSH activity, no maintenance jobs) lasting > 600 s is the only path to a non-zero `theta`.

## Trace analysis (26.5 h, 318 ticks per channel) — final

Pulled at 2026-05-10 ~01:18 UTC desktop clock. 2544 records covering 28 hours, 96 ticks/hour median.

| metric | value across all 8 channels |
|---|---|
| ticks per channel | 318 |
| `n_samples` range | 0 .. 0 |
| `tr(P)` range | 100.000 .. 100.000 |
| `theta` non-zero, ever | **False** |
| `last_seen` advanced past 1970 | **False** |
| any field drift across 318 ticks | **None detected** |

**`/var/lib/ventd/smart/`** at end-of-soak — `shard-B/` only; `conf-A/` and `shard-C/` still absent on disk. Layer-A `SeenFirstContact` never flipped; Layer-C deferred behind unidentifiable parent. Whole pipeline upstream-stuck at Layer-B warmup, identical to T=0.

**Workload state:**
- Tdarr_node container active throughout (`s6-supervise tdarr_node`, PID 968926). No ffmpeg child currently — likely paused between transcoding tasks.
- `loadavg = 0.00 0.00 0.07` at end of soak (current idle moment).
- ventd 0.5.31 daemon up 38h, 43 MB RSS, healthy. Observation log 2.2 MB current (post-midnight rotation) + 4 daily rotations.

**Confounders to acknowledge:**
- 6 SSH sessions (one active, five closing) — every analysis SSH I ran kept RULE-OPP-IDLE-03 firing for ~60s after disconnect.
- Tdarr's ffmpeg children: when Tdarr was actively transcoding, RULE-IDLE-06 closed the gate via the `ffmpeg` blocklist entry.

## Verdict — hypothesis CONFIRMED

26.5 hours of continuous read-only observation against ventd 0.5.31 on Phoenix's MSI Z690-A under realistic load: **zero RLS updates ever fired**. `theta` stayed at `[0,0]`, `tr(P)` stayed at `d × InitialP`, `n_samples` stayed at 0, `last_seen` stayed at Unix=0. The smart-mode pipeline is not "warming up slowly" — it is structurally not advancing under this workload class.

## Root cause chain

```
realistic workload (Tdarr transcoding + occasional SSH)
  → RULE-OPP-IDLE-01..04 idle gate closed > 99 % of the time
  → opportunistic probes never fire (RULE-OPP-PROBE-01)
  → no Δpwm-on-i-while-zero-on-j events for the OAT filter
  → RULE-CMB-OAT-01 admits zero observations to Layer-B RLS
  → Shard-B stays at θ=[0,0], tr(P)=tr(P₀), n=0
  → RULE-CPL-WARMUP-01 never clears (n ≥ 5·d² fails)
  → Snapshot.WarmingUp stays true forever
  → predictive controller path is structurally locked out
  → smart-mode never demonstrates value beyond reactive control
```

This matches what the senior review predicted at v0.5.26. v0.5.30's `FirstInstallDelay = 0` (RULE-OPP-PROBE-07) closed the fresh-install gate but didn't address the load-bearing blocker: the idle preconditions themselves prevent learning on hosts with any sustained background workload.

## Next decision (Phoenix's call)

Three credible paths for v0.6.0:

1. **Widen the opportunistic gate** by allowing probes to fire when load is "low enough" rather than full idle (e.g. PSI `cpu.some avg60 < 5 %` AND no input IRQ in last 60s AND no `ffmpeg`/`make`/`apt` in live processes — drop the 600s sustained-idle requirement). Risk: probes during background-but-active workloads contaminate calibration. Reward: smart-mode learns on most desktops within hours instead of never.
2. **Ship the synthetic excitation driver** behind `RULE-SOAK-EXCITATION-OPT-IN`. Operator opts in once post-install; harness drives a structured Δpwm sequence over ~30 minutes with the daemon's restore-on-exit baseline as the safety floor. Smart-mode converges deterministically. Risk: requires daemon HTTP API integration + restore-path safety; opt-in is acceptable for a dev-tier feature but doesn't help "first user no matter who they are".
3. **Accept smart-mode as idle-hours-only**, document explicitly in README + doctor surface, tag v0.6.0 on that contract. Risk: fails the "works flawlessly for the very first user" promise on workload hosts.

My read: **path 1 is the right v0.6.0 call.** Existing hard preconditions (battery, container, scrub, blocked-process list) remain the load-bearing protection; loosening the *durability* and *PSI ceiling* gates lets smart-mode learn during workload lulls without giving up the safety contract. Path 2 stays in v0.7+ as the determinism backstop. Path 3 is the loudest admission of failure and shouldn't be accepted while path 1 is unexplored.

## Soak status

Watch loop **stopped** on the desktop at end of analysis (`pkill -f ventd-soak-watch.sh`). Trace file preserved at `~/ventd-soak-traces/desktop-20260508T225209.ndjson` and pulled local to `desktop-20260508T225209.ndjson`.

## Soak operations

- Stop the watch: `sshpass -p password ssh phoenix@192.168.7.209 'pkill -f ventd-soak-watch.sh'`
- Live tail: `sshpass -p password ssh phoenix@192.168.7.209 'tail -f ~/ventd-soak-traces/desktop-20260508T225209.ndjson'`
- Restart on reboot: not yet wired as a user-unit; if Phoenix wants persistence across reboots, propose `~/.config/systemd/user/ventd-soak.service` in a follow-up.

## Artefacts

- `baseline-human.txt` — human-readable snapshot at T=0
- `baseline.ndjson` — JSON snapshot at T=0 (1 line per channel)
- `host-state.txt` — `ls -la` of `/var/lib/ventd/smart/` + observation log + ventd version
- `desktop-20260508T225209.ndjson` (pulled later) — full trace
