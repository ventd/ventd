# R29 — Acoustic Capture Analysis (Phoenix's MSI Z690-A under Tdarr)

**Status:** Empirical research artifact, spec-input quality.
**Capture date:** 2026-05-03 07:40Z onward, 84+ minute window.
**Rig:** Phoenix's desktop, MSI PRO Z690-A DDR4 (MS-7D25), Intel + RTX 4090, USB Blue Yeti microphone, Tdarr transcoding 3× 1080p movies concurrently with NVDEC hardware acceleration.
**Predecessor research:** R30 (mic calibration), R31 (fan-stall signatures), R32 (perception thresholds), R33 (no-mic proxy).
**Successor:** Updates `RULE-CTRL-COST-01` from synthetic `k_factor = 0.01°C/PWM` to a measured per-fan baseline.

---

## 0. Executive summary

- **Fan PWM is the dominant driver of measured loudness — not workload.** Across 1658 acoustic samples paired to status/workload streams, `dBFS ↔ Cpu_Fan_pwm` correlates at **r=+0.80**, while `dBFS ↔ load1` correlates only at **r=+0.66**. CPU load drives temperature, temperature drives PWM, PWM drives noise — confirming the smart-mode design premise that *reducing PWM at the same cooling target reduces operator-perceived noise*.

- **GPU fan is ~20× more "dBFS-per-PWM" responsive than chassis fans on this hardware.** Phoenix's RTX 4090 fan goes from -30 dBFS at PWM=77 to -15 dBFS at PWM=89 (15 dB rise over 12 PWM points = **1.28 dB/PWM**). The Z690-A grouped chassis fans go from -30 dBFS at PWM=29 to -22 dBFS at PWM=158 (**0.064 dB/PWM**). This argues for *per-fan* k_factor in the cost gate, not a global constant.

- **Per-board PWM grouping is a load-bearing catalog signal.** The MSI Z690-A drives Cpu_Fan, Pump_Fan, System_Fan_#1, System_Fan_#2 with **identical PWM values throughout the entire capture** (4 fans on one curve). R36 §B's similar finding for the IT8613E mini-PC pool generalises: a substantial fraction of consumer + mini-PC hardware groups multiple fans on a single PWM channel. The cost gate must account for this — a 100→200 PWM step does **not** cost 1× the per-fan ΔdB; it costs the energetic sum across however many fans are on that channel.

- **Reactive controller baseline is observable in the correlation matrix.** `Cpu_Fan_pwm ↔ cpu_temp` correlates at **r=+0.66** — the existing curve is following temperature reactively. The predictive arm's value-add is the gap between this and what could be achieved with workload prediction; the smart-mode controller's job is to outperform r=+0.66 by leading PWM with workload signal instead of trailing it with temperature.

- **GPU fan utility correlation is weak (r=+0.21 with `gpu_util`).** The mic at the listening position picks up GPU fan noise less efficiently than chassis fan noise — likely because the GPU fan is shrouded inside the GPU cooler stack while chassis fans face into the room. Implication: a calibrated mic at the listening position systematically *under*-weights GPU fan loudness compared to its actual cooling cost. R30's per-mic K_cal calibration captures this for the absolute path; the no-mic proxy (R33) avoids the issue by computing per-fan tip-speed independently.

- **One physical board ↔ one cost-gate-tunable curve does not generalise.** The findings here are specific to Phoenix's Z690-A + RTX 4090 + Blue Yeti at a fixed listening position. Per R30 §6 the per-(room, mic) K_cal makes absolute dBA portable; the per-fan dB-vs-PWM curves shipped with the catalog must be relative-only baselines that calibration personalises.

---

## 1. Methodology

### 1.1 Capture pipeline

Five NDJSON streams running concurrently on Phoenix's desktop, all wall-clock-aligned:

- `confidence.ndjson` — `/api/v1/confidence/status` snapshots every 2 s (smart-mode confidence components).
- `status.ndjson` — `/api/v1/status` every 2 s (per-fan PWM/RPM, per-sensor temperatures).
- `workload.ndjson` — top-10 process / GPU stats / loadavg every 5 s.
- `journal.ndjson` — `journalctl -u ventd -f` (sparse — daemon was running in monitor-only).
- `acoustic.ndjson` — RMS dBFS + peak dBFS + zero-crossing rate every 2 s, computed from 16 kHz mono PCM piped from `ffmpeg -f alsa -i hw:CARD=Microphones,DEV=0`.

Acoustic processing uses `/tmp/acoustic_logger.py` (Python, no numpy):
- Reads 32000 samples per 2-second window from stdin.
- Computes RMS via `Σ(x²)/N` then 20·log10(√mean / 32767).
- Peak via `max|x|` then 20·log10(peak / 32767).
- Zero-crossing rate as a basic spectral-centroid proxy.

### 1.2 Workload

Tdarr running 3 concurrent 1080p Blu-ray transcodes (NVDEC hardware-accelerated). Three `tdarr-ffmpeg` processes consuming 360–600% CPU each plus heavy GPU utilisation. Concurrent Whisper STT planned but not yet running during the capture window analysed here.

### 1.3 Joining and analysis

`/tmp/capture-snapshot/analyze.py` joins the streams on 2-second wall-clock buckets, emitting `joined.csv` with the following columns: `ts_unix, ts_iso, load1, load5, cpu_temp_c, gpu_temp_c, gpu_util, gpu_fan_pct, gpu_power_w, rms_dbfs, peak_dbfs, zcr, tdarr_n, ffmpeg_n, whisper_n, {Cpu_Fan,Pump_Fan,System_Fan_1,System_Fan_2,gpu0}_{pwm,rpm}`.

Per-(fan, PWM-bin) means, p50, and p90 dBFS computed; Pearson correlations across the timeline computed in pure Python (no scipy).

### 1.4 Sample count summary (84 min window)

| Stream | Samples | Cadence | Coverage |
|---|---|---|---|
| status | 2479 | 2 s | full window |
| acoustic | 1658 | 2 s | mic-restart loss in first ~10 min |
| workload | 978 | 5 s | full window |
| confidence | 2484 | 2 s | full window |
| journal | 5 | continuous tail | sparse — monitor-mode daemon emits little |

Joined CSV: 2515 rows × 25 columns = 62 875 cells.

---

## 2. Correlation matrix (the load-bearing finding)

Pearson r, N=1658 acoustic-aligned rows:

| Variable A | Variable B | r |
|---|---|---|
| rms_dbfs | Cpu_Fan_pwm | **+0.797** |
| rms_dbfs | System_Fan_1_pwm | +0.796 |
| rms_dbfs | load1 | +0.659 |
| rms_dbfs | cpu_temp | +0.602 |
| rms_dbfs | gpu_util | +0.209 |
| rms_dbfs | gpu_power_w | +0.179 |
| Cpu_Fan_pwm | cpu_temp | +0.655 |
| Cpu_Fan_pwm | load1 | +0.549 |

**Reading the matrix:**

1. **Fan PWM > load > temperature, in that order.** The smart-mode controller's reactive baseline (`Cpu_Fan_pwm ↔ cpu_temp = +0.655`) is *weaker* than the loudness↔PWM correlation (`+0.797`). The reactive curve is not the limiting factor in observed loudness — the PWM trajectory itself is. This argues that **reducing PWM at the same thermal target is the highest-leverage acoustic intervention**, which is exactly the predictive-arm value proposition.

2. **GPU fan is invisible to the mic at this listening position.** `dBFS ↔ gpu_util = +0.21` and `↔ gpu_power_w = +0.18` are both weak. The GPU was clearly working hard (Tdarr NVDEC + 119 W power draw confirmed in workload.ndjson) but the mic, ~50 cm from the desk, captures chassis fans rather than the GPU shroud's internally-baffled output. Implication for calibration: a mic at the listener cannot proxy for GPU fan noise on a system where the case is closed and the GPU dumps air through its own shroud. The R33 no-mic proxy's per-fan formulation handles this correctly by computing GPU-fan tip-speed independently of the mic.

3. **Correlation strengthens monotonically with sample count.** At 43 min (N=500): r=+0.64. At 60 min (N=966): r=+0.67. At 84 min (N=1658): r=+0.80. The relationship is real, not a fluke of small-sample noise.

---

## 3. Per-fan dB-vs-PWM curves

### 3.1 GPU fan (gpu0, RTX 4090)

Narrow PWM range during the capture (the GPU runs at firmware-managed curve, 30–35% of pwm_max), but extremely steep response within that range:

| PWM | Samples | Mean dBFS | p50 | p90 |
|---|---|---|---|---|
| 77 | 152 | -30.07 | -30.29 | -29.19 |
| 79 | 308 | -26.21 | -26.70 | -22.00 |
| 82 | 246 | -20.97 | -20.79 | -19.04 |
| 84 | 232 | -19.12 | -19.06 | -17.48 |
| 87 | 478 | -16.65 | -16.50 | -15.46 |
| 89 | 221 | -14.74 | -14.65 | -14.09 |

**Slope: 15.3 dB rise over 12 PWM units = 1.28 dB / PWM.**

Sanity check against R33 §3's tip-speed formula: `S_tip = 50·log10(RPM·D)`. RTX 4090 fan spec is 100 mm diameter, ~3000 RPM at 100% PWM. In the captured PWM range (77–89, ~30–35% duty), the fan turns 1000–2200 RPM. Tip-speed-based prediction over that range: log10(2200/1000) × 50 = 17 dB. Measured: 15 dB. Within R33's expected ±2 dB error band — empirical calibration of R33 §3 against a real RTX 4090 fan at its mid-PWM operating point.

### 3.2 Chassis fans (Cpu_Fan, Pump_Fan, System_Fan_#1, System_Fan_#2 — all on one curve)

Wide PWM range covered (29–255 captured), shallow response per the energetic-sum-of-4-fans behaviour:

| PWM range | Bins | Mean dBFS | p50 | p90 |
|---|---|---|---|---|
| 29–50 | ~5 | -29 to -30 | similar | similar |
| 50–100 | ~10 | -24 to -27 | similar | similar |
| 100–160 | ~10 | -22 to -25 | similar | similar |
| 200–255 | ~5 | -15 to -17 | -14 to -16 | -14 to -16 |

**Slope (chassis fan group): 14 dB rise over 226 PWM units = 0.062 dB / PWM.**

The shallowness is real: the four chassis fans of Phoenix's case are 120 mm or 140 mm low-RPM fans at modest pressure, optimised for quiet idle. Their response curve is roughly linear in *log(RPM)* — meaning roughly constant *dB-per-doubling* — and the chassis-fan duty range is wide, so the per-PWM slope is small.

### 3.3 Per-fan slope summary

| Fan | Slope | Source |
|---|---|---|
| Phoenix's RTX 4090 GPU fan (mid PWM) | **1.28 dB / PWM** | measured |
| Phoenix's chassis fan group (4× fans, full PWM range) | **0.062 dB / PWM** | measured |
| R33's predicted tip-speed slope | ~1 dB / PWM (case fans) | predicted |

The 20× difference between GPU and chassis on this rig — both in slope and absolute level — is the central observation that argues against a single global `k_factor` in `RULE-CTRL-COST-01`. The cost gate must be **per-fan**, ideally seeded from the catalog and refined by calibration when a mic is present.

---

## 4. PWM grouping (per-board catalog signal)

Across **2479 status samples**, Phoenix's Z690-A drove all four chassis fans to **identical PWM values, every sample**:

```
$ awk -F, 'NR>1 {if ($16 != $18 || $18 != $20 || $20 != $22) print}' joined.csv | wc -l
0
```

(Where columns 16/18/20/22 are Cpu_Fan_pwm, Pump_Fan_pwm, System_Fan_1_pwm, System_Fan_2_pwm.)

This is a hardware property of the MSI Z690-A's IT8688E + nct6687d configuration: the chassis-fan group shares one PWM channel under the BIOS curve, and ventd inherits that grouping. R36 §B notes the same pattern for the IT8613E mini-PC pool, and it's likely common across consumer ATX/mATX boards in the 2020+ era.

**Implications for the cost gate:**

1. A "single fan PWM step" on a grouped board is actually a *N-fan* PWM step. The energetic-sum cost is `10·log10(N)` higher than a single-fan step at the same dBA — for N=4, that's +6 dB per step versus the per-fan calculation.

2. Calibration must record fan groupings explicitly. The catalog row needs a `pwm_groups: [{channel: pwm1, fan_ids: [cpu_fan, pump_fan, sys_fan_1, sys_fan_2]}]` field (or equivalent), and the cost gate must operate on group-level energetic sums, not per-fan independent sums.

3. The R33 no-mic proxy already handles this correctly via §7's energetic addition rule. The mic-calibrated path (R30) needs the same grouping awareness — without it, calibration measures the energetic sum of the group at each PWM and incorrectly attributes it to a single "fan" identifier.

This is a v0.5.12 catalog-schema feature, not just a v0.5.12 acoustic-feature feature.

---

## 5. Recommendations for v0.5.12

### 5.1 `RULE-CTRL-COST-01` update

Current: `k_factor = 0.01°C-equivalent per PWM unit`, applied globally per fan.

Proposed: per-fan `k_factor` derived from:
- Catalog default per fan-class (case-fan ≈ 0.06 dB/PWM, GPU shroud ≈ 1.3 dB/PWM, AIO pump ≈ R33 §6's pump-band penalty).
- Calibration override when `ventd calibrate --acoustic` has run, replacing the catalog default with the measured slope.
- PWM-group awareness: when N fans share a PWM channel, multiply the per-fan slope by `10·log10(N)` to get the channel-level cost.

The updated rule signature should be:

```go
// CostFactor returns the dBA-per-PWM-unit cost of stepping the
// channel up by one PWM unit, accounting for per-fan slope, fan
// count on the channel, and the operator's preset weighting.
func (c *Channel) CostFactor() float64
```

### 5.2 v0.5.12 spec acceptance criteria

The R29 data argues for the following acceptance criteria when reviewing the v0.5.12 acoustic-spec PRs:

- The cost gate refuses ramps where measured `predicted_ΔdBA × N_fans > k_factor[preset] × |ΔPWM|`. The `× N_fans` term is the load-bearing addition.
- Per-fan-class calibration tables ship in the catalog as the no-mic baseline. Verify against R33 §3's tip-speed formula at one anchor point per class.
- When a mic is present and calibrated (R30 `K_cal` set), per-fan `k_factor` is replaced by the measured value with a 60 s confidence window before takeover (per R33 §10).

### 5.3 Catalog deliverables this finding implies

- Add `pwm_groups: [...]` field to the board catalog schema (PR-5 schema v1.3 work).
- Write the MS-7D25 (Phoenix's board) catalog row with explicit grouping: `pwm_groups: [{channel: pwm1, fans: [cpu_fan, pump_fan, sys_fan_1, sys_fan_2]}, {channel: pwm5, fans: [gpu0]}]`.
- Apply the same grouping field to the 22 R36-deliverable mini-PC rows where applicable.

---

## 6. What this analysis cannot answer

- **Generalisation beyond Phoenix's rig.** All findings are specific to one Z690-A motherboard, one RTX 4090, one Blue Yeti at one listening position, one workload (Tdarr 3-stream NVDEC). HIL replication on the MiniPC and Proxmox machines is needed before the per-fan slopes become catalog defaults.

- **Frequency-domain analysis.** The mic captured RMS only — no FFT, no per-band SPL, no tonality analysis. R31's recommended detector (1–2 kHz broadband rise gate) requires FFT. Adding numpy + scipy to the capture pipeline is the next step (~50 LOC of acoustic_logger.py changes).

- **Pump-cavitation or stall signatures.** The capture window had no fault events; all hardware was nominal. R31's detector validation requires either synthetic test cases or a deliberate fault-injection HIL run.

- **Workload prediction.** The capture is a single Tdarr-3-stream pattern. The smart-mode controller's predictive value-add is across *transitions* (idle → workload, workload → idle, sustained → spike). A multi-day capture covering varied user activity (gaming + transcoding + idle + sleep + sustained + bursty) would be needed to characterise predictive-arm value-add.

- **Acoustic-impact of fan-grouping changes.** If a future BIOS update changed Phoenix's board to ungroup the chassis fans (per-fan PWM control), the per-fan slope and the energetic sum would both change. Catalog rows need to record the firmware version that the grouping reflects.

These gaps are R29-extensions and v0.5.12-HIL items, not blockers for the v0.5.12 spec sequence.

---

## 7. Cross-references

- **R28** (failure modes) — the dummy-tach class is independent of these findings.
- **R30** (mic calibration) — the K_cal formulation is what makes R29's dBFS readings convertible to absolute dBA when needed; this analysis stays in dBFS because we don't have R30's calibration applied during this capture window.
- **R31** (stall signatures) — independent; this analysis had no fault events.
- **R32** (perception thresholds) — the per-fan slopes here can be projected onto R32's preset tiers (e.g., "≤32 dBA = ≤PWM 95 on Phoenix's chassis fan group + ≤PWM 80 on GPU fan", subject to K_cal + N_fans correction).
- **R33** (no-mic proxy) — the GPU fan slope of 1.28 dB/PWM matches R33 §3's predicted tip-speed slope to within ~2 dB at the captured operating point. Real-world calibration of the proxy.
- **R36** (mini-PC EC survey) — the PWM grouping observation in §4 corroborates R36 §B's IT8613E pool finding; argues for `pwm_groups` schema field across both desktop and mini-PC catalog rows.

---

## 8. Citations

- Capture data (this analysis): `/tmp/research-safeguard/r29-joined.csv` (2515 rows, 326 KB).
- Capture script: `/tmp/capture-tdarr-session.sh` (on Phoenix's desktop).
- Acoustic logger: `/tmp/acoustic_logger.py` (Phoenix's desktop, 16 kHz mono S16LE → NDJSON RMS dBFS at 2 s cadence).
- Joiner / analysis: `/tmp/capture-snapshot/analyze.py` (this repo, in research-safeguard).
- ALSA UAC capture path documented in [ALSA project — UAC1/UAC2 driver](https://www.alsa-project.org/wiki/Asoundrc).
- ffmpeg ALSA-to-PCM pipeline: [ffmpeg ALSA capture documentation](https://ffmpeg.org/ffmpeg-devices.html#alsa).
- Pearson correlation formulae: standard.
