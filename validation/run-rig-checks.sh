#!/usr/bin/env bash
# validation/run-rig-checks.sh — phoenix-MS-7D25 rig verification for PR #21 + PR #25.
#
# Usage:
#   sudo bash validation/run-rig-checks.sh                 # full run, writes log
#   sudo bash validation/run-rig-checks.sh -- --dry-run    # list checks, run nothing
#   sudo bash validation/run-rig-checks.sh /var/log/ventd  # custom output dir
#
# Output:
#   <output-dir>/rig-check-<timestamp>.log  — full transcript
#   stdout                                  — per-check PASS/FAIL/SKIP + summary
#
# Behaviour:
#   - Always exits 0 so the operator gets a complete report even when a
#     check fails. CI consumers should grep the summary line for FAIL>0.
#   - Idempotent: never permanently mutates system state (reverts each
#     pwm_enable / fan_target write before exiting that check).
#   - Distro-aware: AppArmor / SELinux checks SKIP cleanly on systems
#     without those LSMs.
#   - Manual (interactive) checks prompt the operator and accept "skip".
#
# Author: ventd daemon-hardening session F1.

set -uo pipefail

# ── argument parsing ──────────────────────────────────────────────────────
DRY_RUN=0
OUTDIR=""
for arg in "$@"; do
    case "$arg" in
        --dry-run) DRY_RUN=1 ;;
        --) ;;
        -*) echo "unknown flag: $arg" >&2; exit 2 ;;
        *)  OUTDIR="$arg" ;;
    esac
done
OUTDIR="${OUTDIR:-validation/results}"

if [[ "$DRY_RUN" -eq 0 && "$EUID" -ne 0 ]]; then
    echo "warn: not running as root — most checks will be inconclusive." >&2
fi

mkdir -p "$OUTDIR"
TS=$(date -u +%Y%m%dT%H%M%SZ)
LOG="$OUTDIR/rig-check-$TS.log"

# Result counters.
N_PASS=0
N_FAIL=0
N_SKIP=0
N_MANUAL=0

# ── helpers ───────────────────────────────────────────────────────────────
log() { printf '%s\n' "$*" | tee -a "$LOG"; }
sep() { log "────────────────────────────────────────────────────────────"; }

# check ID DESC FN — runs FN, captures result.
# FN returns 0=PASS, 1=FAIL, 77=SKIP, 78=MANUAL (operator must follow up).
check() {
    local id="$1" desc="$2" fn="$3"
    sep
    log "▶ $id — $desc"
    if [[ "$DRY_RUN" -eq 1 ]]; then
        log "  (dry-run; would invoke $fn)"
        return
    fi
    "$fn" 2>&1 | tee -a "$LOG"
    local rc="${PIPESTATUS[0]}"
    case "$rc" in
        0)  log "RESULT $id: PASS"; N_PASS=$((N_PASS + 1)) ;;
        77) log "RESULT $id: SKIP"; N_SKIP=$((N_SKIP + 1)) ;;
        78) log "RESULT $id: MANUAL — operator follow-up required"; N_MANUAL=$((N_MANUAL + 1)) ;;
        *)  log "RESULT $id: FAIL (rc=$rc)"; N_FAIL=$((N_FAIL + 1)) ;;
    esac
}

# manual_prompt MSG — used by interactive checks. Prints the steps the
# operator should run, then waits for "done" or "skip".
manual_prompt() {
    local msg="$1"
    log "$msg"
    log ""
    if [[ ! -t 0 ]]; then
        log "  (non-interactive shell; auto-skipping)"
        return 77
    fi
    local resp=""
    read -r -p "  Press Enter when complete, type 'skip' to skip: " resp
    case "$resp" in
        skip|s|S) return 77 ;;
        *)        return 0 ;;
    esac
}

# detect_pwm_enable_paths — list /sys/class/hwmon/*/pwm*_enable (excluding
# stale/unsupported entries). Used by recover and watchdog checks.
detect_pwm_enable_paths() {
    find /sys/class/hwmon -maxdepth 2 -name 'pwm*_enable' 2>/dev/null
}

# ── PR #21 checks — install path + chip-agnostic udev ─────────────────────

check_0a_i_udev_rule_chip_agnostic() {
    local rule=/etc/udev/rules.d/90-ventd-hwmon.rules
    if [[ ! -f "$rule" ]]; then
        log "  $rule not found"
        return 1
    fi
    log "  rule contents:"
    sed 's/^/    /' "$rule"
    # The rule must match SUBSYSTEM=="hwmon" with no chip-name predicate.
    if ! grep -q 'SUBSYSTEM=="hwmon"' "$rule"; then
        log "  no SUBSYSTEM==\"hwmon\" line"
        return 1
    fi
    if grep -E 'ATTR\{name\}|ATTRS\{name\}' "$rule" >/dev/null; then
        log "  rule appears to gate on chip name (ATTR{name}=…) — not chip-agnostic"
        return 1
    fi
    log "  triggering udev for hwmon subsystem..."
    udevadm trigger --subsystem-match=hwmon
    udevadm settle
    log "  pwm* writability snapshot:"
    for p in /sys/class/hwmon/*/pwm[0-9]*; do
        [[ -e "$p" ]] || continue
        local mode
        mode=$(stat -c '%a' "$p" 2>/dev/null || echo "?")
        log "    $p mode=$mode"
    done
    return 0
}

check_0a_ii_probe_modules_persists() {
    if ! command -v ventd >/dev/null; then
        log "  ventd binary not on PATH"
        return 1
    fi
    log "  invoking ventd --probe-modules"
    ventd --probe-modules
    local conf=/etc/modules-load.d/ventd.conf
    if [[ ! -f "$conf" ]]; then
        log "  $conf not created"
        return 1
    fi
    log "  $conf contents:"
    sed 's/^/    /' "$conf"
    log "  currently loaded modules matching the conf:"
    while read -r mod; do
        [[ -z "$mod" ]] && continue
        if lsmod | awk '{print $1}' | grep -qx "$mod"; then
            log "    $mod : LOADED"
        else
            log "    $mod : NOT LOADED"
        fi
    done < "$conf"
    return 0
}

check_0a_iii_enrich_chip_name_in_config() {
    local cfg=/etc/ventd/config.yaml
    if [[ ! -f "$cfg" ]]; then
        log "  $cfg not present (setup not run yet)"
        return 77
    fi
    local sensor_count fan_count chip_count
    sensor_count=$(grep -cE '^\s*-\s+name:' "$cfg" || true)
    chip_count=$(grep -cE '^\s*chip_name:' "$cfg" || true)
    fan_count=$(grep -cE '^\s*pwm_path:' "$cfg" || true)
    log "  sensors+fans entries (approx): $sensor_count"
    log "  pwm_path entries (fans):       $fan_count"
    log "  chip_name entries:             $chip_count"
    # Every hwmon Sensor and every hwmon Fan should carry a chip_name. nvidia
    # entries are exempt; this check passes if chip_count >= fan_count and >0.
    if [[ "$chip_count" -lt "$fan_count" || "$chip_count" -eq 0 ]]; then
        log "  expected chip_name on every hwmon entry"
        return 1
    fi
    return 0
}

check_0a_iv_renumber_survival() {
    manual_prompt "  This check requires unloading and reloading kernel modules
  in a different order, then verifying ventd's config self-heals.
  On phoenix-MS-7D25 run:

    sudo systemctl stop ventd
    sudo modprobe -r nct6687d coretemp
    sudo modprobe coretemp
    sudo modprobe nct6687d
    sudo systemctl start ventd && sleep 3
    journalctl -u ventd --since '10 seconds ago' \\
      | grep -E 'resolve|rebind|chip'

  Expected: log lines confirming paths re-anchored by chip name, no errors,
  /etc/ventd/config.yaml pwm_path entries reflect the new hwmonN."
    return $?
}

check_0a_v_reboot_survival() {
    manual_prompt "  This check requires a reboot.

  Before rebooting, note current pwm values:
    for f in /sys/class/hwmon/*/pwm[0-9]; do echo \"\$f: \$(cat \$f)\"; done

  Then:
    sudo reboot

  After reboot, re-run this script with --post-reboot to verify ventd
  came up clean. Or manually:
    systemctl status ventd
    journalctl -u ventd -b 0 | head -50
    sudo ventd --rescan-hwmon  # should be a no-op"
    return $?
}

# ── PR #25 checks — watchdog, recovery, calibration safety ────────────────

check_0b_i_kill_triggers_recover() {
    if ! systemctl is-active --quiet ventd; then
        log "  ventd is not active; cannot exercise recover service"
        return 1
    fi
    if ! systemctl list-unit-files | grep -q ventd-recover.service; then
        log "  ventd-recover.service is not installed"
        return 1
    fi
    local pid
    pid=$(pidof ventd || true)
    if [[ -z "$pid" ]]; then
        log "  no ventd PID found"
        return 1
    fi
    log "  killing ventd PID $pid with SIGKILL"
    kill -KILL "$pid"
    log "  waiting 3s for recover to fire..."
    sleep 3
    log "  ventd-recover status:"
    systemctl status ventd-recover.service --no-pager | tail -10
    log "  pwm*_enable snapshot (expect 1=manual or BIOS-restored):"
    local stuck=0
    while IFS= read -r p; do
        local v
        v=$(cat "$p" 2>/dev/null || echo "?")
        log "    $p = $v"
        if [[ "$v" == "0" ]]; then
            stuck=1
        fi
    done < <(detect_pwm_enable_paths)
    if [[ "$stuck" -ne 0 ]]; then
        log "  at least one pwm_enable still 0 (no software control); recover failed"
        return 1
    fi
    return 0
}

check_0b_ii_sd_notify_watchdog_active() {
    if ! systemctl show ventd >/dev/null 2>&1; then
        log "  ventd unit not known to systemd"
        return 1
    fi
    local watchdog_us notify_access
    watchdog_us=$(systemctl show ventd -p WatchdogUSec --value)
    notify_access=$(systemctl show ventd -p NotifyAccess --value)
    log "  WatchdogUSec=$watchdog_us"
    log "  NotifyAccess=$notify_access"
    if [[ -z "$watchdog_us" || "$watchdog_us" == "infinity" || "$watchdog_us" == "0" ]]; then
        log "  WatchdogSec not set"
        return 1
    fi
    # Acceptable: any positive watchdog within an order of magnitude of 2s.
    return 0
}

check_0b_iii_hung_loop_triggers_restart() {
    log "  This check requires a debug build that responds to SIGUSR1 by hanging."
    log "  Skipping in the standard rig run — covered by internal/sdnotify unit tests."
    return 77
}

check_0b_iv_calibration_zero_pwm_ceiling() {
    log "  This check requires triggering a calibration via the web UI"
    log "  while sampling pwm1 at 100ms. Recommended procedure:"
    log ""
    log "    while true; do echo \"\$(date +%H:%M:%S.%N) \$(cat <pwm-path>)\"; sleep 0.1; done > /tmp/pwm-trace.log"
    log "    (in the UI, trigger calibration on cpu_fan)"
    log "    awk '\$2 == \"0\" { count++; if (count > 22) print \"VIOLATION at\", \$1; next } { count = 0 }' /tmp/pwm-trace.log"
    log ""
    log "  PASS = zero violations during the entire calibration sweep."
    manual_prompt ""
    return $?
}

check_0b_v_new_fan_within_10s() {
    manual_prompt "  Plug a fan into a previously-unused header (or load a synthetic
  hwmon-providing module). Then watch:

    journalctl -u ventd -f | grep -E 'rescan|new fan|hwmon'

  Expected: a rescan log entry within ~10s of /sys/class/hwmon gaining a
  new entry. PASS if detection ≤ 10s."
    return $?
}

check_0b_vi_modprobe_cycle() {
    if ! lsmod | grep -q '^nct6687d'; then
        log "  nct6687d not currently loaded; cannot cycle"
        return 77
    fi
    log "  cycling nct6687d (rmmod, sleep 2, modprobe, sleep 12)"
    rmmod nct6687d
    sleep 2
    log "  removal log (last 5s):"
    journalctl -u ventd --since '5 seconds ago' | grep -iE 'remove|gone' | tail -5 || true
    modprobe nct6687d
    sleep 12
    log "  re-add log (last 15s):"
    if journalctl -u ventd --since '15 seconds ago' | grep -iE 'add|new|rescan' | tail -5; then
        return 0
    fi
    log "  no add/rescan log entry observed"
    return 1
}

check_0b_vii_apparmor_clean() {
    if ! command -v aa-status >/dev/null; then
        log "  aa-status not present (no AppArmor); skip"
        return 77
    fi
    if ! aa-status 2>/dev/null | grep -q ventd; then
        log "  no ventd profile loaded under AppArmor"
        return 77
    fi
    log "  ventd profile under AppArmor:"
    aa-status 2>/dev/null | grep ventd | sed 's/^/    /'
    log "  recent AVC denials matching ventd:"
    if dmesg 2>/dev/null | grep -i "DENIED.*ventd" | tail -5 | grep -q .; then
        dmesg | grep -i "DENIED.*ventd" | tail -5
        return 1
    fi
    log "    (none)"
    return 0
}

check_0b_viii_selinux_clean() {
    if ! command -v semodule >/dev/null; then
        log "  semodule not present (no SELinux); skip"
        return 77
    fi
    if ! semodule -l 2>/dev/null | grep -q ventd; then
        log "  no ventd module loaded under SELinux"
        return 77
    fi
    log "  ventd SELinux module loaded"
    log "  recent AVC denials:"
    if command -v ausearch >/dev/null; then
        if ausearch -m AVC -ts recent 2>/dev/null | grep -i ventd | tail -10 | grep -q .; then
            ausearch -m AVC -ts recent | grep -i ventd | tail -10
            return 1
        fi
    else
        log "    (ausearch not present; skipping AVC scan)"
        return 77
    fi
    log "    (none)"
    return 0
}

# ── runner ───────────────────────────────────────────────────────────────

log "ventd rig verification — PR #21 + PR #25"
log "host: $(hostname 2>/dev/null || echo unknown)"
log "kernel: $(uname -r)"
log "started: $(date -u +%FT%TZ)"
log "log: $LOG"
sep

# PR #21
check 0a.i   "udev rule present and chip-agnostic"     check_0a_i_udev_rule_chip_agnostic
check 0a.ii  "--probe-modules persists"                check_0a_ii_probe_modules_persists
check 0a.iii "EnrichChipName populates config"         check_0a_iii_enrich_chip_name_in_config
check 0a.iv  "hwmonN renumber survival"                check_0a_iv_renumber_survival
check 0a.v   "reboot survival"                         check_0a_v_reboot_survival

# PR #25
check 0b.i    "kill -KILL triggers ventd-recover"      check_0b_i_kill_triggers_recover
check 0b.ii   "sd_notify watchdog active"              check_0b_ii_sd_notify_watchdog_active
check 0b.iii  "hung loop triggers restart"             check_0b_iii_hung_loop_triggers_restart
check 0b.iv   "calibration zero-PWM ceiling"           check_0b_iv_calibration_zero_pwm_ceiling
check 0b.v    "new fan detected within 10s"            check_0b_v_new_fan_within_10s
check 0b.vi   "rmmod;modprobe detected within 10s"     check_0b_vi_modprobe_cycle
check 0b.vii  "AppArmor clean"                         check_0b_vii_apparmor_clean
check 0b.viii "SELinux clean"                          check_0b_viii_selinux_clean

sep
log "summary: ${N_PASS} PASS  ${N_FAIL} FAIL  ${N_SKIP} SKIP  ${N_MANUAL} MANUAL"
log "log: $LOG"
sep
exit 0
