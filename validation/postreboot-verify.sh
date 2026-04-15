#!/usr/bin/env bash
# validation/postreboot-verify.sh — runs the pre-reboot 4a–4f assertions
# again after a fresh boot, to prove that the hwmonN → chip_name
# resolution survives kernel renumbering and the daemon starts clean
# from a cold cache.
#
# Designed to be invoked from validation/ventd-postreboot-verify.service
# (systemd Type=oneshot, After=ventd.service, WantedBy=multi-user.target).
#
# Output goes to a timestamped log under /var/log/ventd. Exit code is
# 0 if every assertion passed, 1 otherwise, so the enabling systemd
# unit's result reflects the gate state.
#
# The assertion helpers below (assert_chip_binding in particular) are
# also exercised from validation/test-rig-checks.sh against fixtures.
# A BASH_SOURCE guard at the bottom keeps `source`'ing the file
# side-effect-free; only direct invocation runs the full matrix.

set -uo pipefail

# ── assertion helpers (callable by test-rig-checks.sh) ────────────────────

# assert_chip_binding CONFIG_PATH [SYSFS_ROOT]
#
# For every hwmon fan in CONFIG_PATH that carries a non-empty hwmon_device,
# derive the /hwmonN/ segment from its pwm_path, read SYSFS_ROOT/hwmonN/device,
# and assert the resolved target matches the configured hwmon_device (after
# canonicalisation). Closes the verifier gap #86 flagged: 4e (journal clean)
# and 4f (hwmon_device set in config) both PASSed on phoenix-MS-7D25 while the
# daemon was writing to the wrong chip for ~4 hours.
#
# Exits 0 if every bound fan matches its configured chip, 1 on any mismatch,
# 77 when there are no hwmon fans with hwmon_device to verify (vacuous pass).
#
# Prints per-fan FAIL detail to stderr; stdout carries the summary line.
#
# SYSFS_ROOT defaults to /sys/class/hwmon. Tests override it by pointing at
# a fixture tree under validation/fixtures/chip-binding*/sysfs/.
assert_chip_binding() {
    local cfg="${1:?config path required}"
    local sysfs="${2:-/sys/class/hwmon}"

    if [[ ! -f "$cfg" ]]; then
        printf 'assert_chip_binding: config %q not found\n' "$cfg" >&2
        return 1
    fi

    # Extract (name, pwm_path, hwmon_device) triples for fans whose
    # type is hwmon AND which carry a non-empty hwmon_device. Fans
    # without hwmon_device set are out of scope for this check — 4f
    # already asserts every hwmon fan has the field.
    local triples
    triples="$(awk '
        BEGIN { in_fans = 0; in_block = 0 }
        function emit() {
            if (in_block && cur_type == "hwmon" && cur_pwm != "" && cur_device != "" && cur_name != "") {
                printf "%s\t%s\t%s\n", cur_name, cur_pwm, cur_device
            }
            cur_name = ""; cur_pwm = ""; cur_device = ""; cur_type = ""
            in_block = 0
        }
        function strip_quotes(s) {
            gsub(/^[[:space:]]+|[[:space:]]+$/, "", s)
            if (length(s) >= 2) {
                first = substr(s, 1, 1)
                last  = substr(s, length(s), 1)
                if ((first == "\"" && last == "\"") || (first == "\x27" && last == "\x27")) {
                    s = substr(s, 2, length(s) - 2)
                }
            }
            return s
        }
        /^fans:[[:space:]]*$/      { in_fans = 1; next }
        /^[a-zA-Z_]+:[[:space:]]*$/ {
            if (in_fans) { emit(); in_fans = 0 }
            next
        }
        !in_fans                   { next }
        /^[[:space:]]*-[[:space:]]*name:[[:space:]]*/ {
            emit()
            v = $0; sub(/^.*name:[[:space:]]*/, "", v)
            cur_name = strip_quotes(v)
            in_block = 1; next
        }
        in_block && /^[[:space:]]*type:[[:space:]]*/ {
            v = $0; sub(/^.*type:[[:space:]]*/, "", v)
            cur_type = strip_quotes(v); next
        }
        in_block && /^[[:space:]]*pwm_path:[[:space:]]*/ {
            v = $0; sub(/^.*pwm_path:[[:space:]]*/, "", v)
            cur_pwm = strip_quotes(v); next
        }
        in_block && /^[[:space:]]*hwmon_device:[[:space:]]*/ {
            v = $0; sub(/^.*hwmon_device:[[:space:]]*/, "", v)
            cur_device = strip_quotes(v); next
        }
        END { emit() }
    ' "$cfg")"

    local total=0 mismatches=0
    while IFS=$'\t' read -r name pwm_path want_device; do
        [[ -z "$name" ]] && continue
        total=$((total + 1))

        # Derive hwmonN from the pwm_path. The daemon normalises to
        # /sys/class/hwmon/hwmonN/pwmN after ResolveHwmonPaths, but the
        # regex also survives device-tree-style paths like
        # /sys/devices/platform/nct6687.2592/hwmon/hwmon6/pwm1.
        local hwmonN
        hwmonN="$(printf '%s' "$pwm_path" | sed -nE 's|.*/(hwmon[0-9]+)/.*|\1|p')"
        if [[ -z "$hwmonN" ]]; then
            printf '  4h  fan %q: no /hwmonN/ segment in pwm_path %q\n' "$name" "$pwm_path" >&2
            mismatches=$((mismatches + 1))
            continue
        fi

        local device_link="$sysfs/$hwmonN/device"
        if [[ ! -e "$device_link" ]]; then
            printf '  4h  fan %q: %s missing — daemon likely cannot resolve this chip at all\n' \
                "$name" "$device_link" >&2
            mismatches=$((mismatches + 1))
            continue
        fi

        # realpath on both sides canonicalises symlinks, trailing slashes,
        # and embedded "." / ".." segments so stable paths compare equal.
        # If realpath can't resolve (fixture paths not reachable on test
        # host), fall back to the raw string so the check still runs.
        local actual_device want_canonical
        actual_device="$(realpath -q "$device_link" 2>/dev/null)"
        want_canonical="$(realpath -q "$want_device" 2>/dev/null)"
        [[ -z "$actual_device" ]] && actual_device="$device_link"
        [[ -z "$want_canonical" ]] && want_canonical="$want_device"

        if [[ "$actual_device" != "$want_canonical" ]]; then
            printf '  4h  fan %q: %s/device -> %q but config hwmon_device = %q\n' \
                "$name" "$sysfs/$hwmonN" "$actual_device" "$want_canonical" >&2
            mismatches=$((mismatches + 1))
        fi
    done <<< "$triples"

    if [[ "$total" -eq 0 ]]; then
        printf 'assert_chip_binding: no hwmon fans with hwmon_device to verify (vacuous pass)\n'
        return 77
    fi
    printf 'assert_chip_binding: %d fans checked, %d mismatches\n' "$total" "$mismatches"
    if [[ "$mismatches" -gt 0 ]]; then
        return 1
    fi
    return 0
}

# ── runner ────────────────────────────────────────────────────────────────
#
# Guarded so validation/test-rig-checks.sh can source this file to call
# assert_chip_binding directly without running the full verifier.
if [[ "${BASH_SOURCE[0]:-}" != "${0}" ]]; then
    return 0 2>/dev/null || :
fi

TS="$(date -u +%Y%m%dT%H%M%SZ)"
LOGDIR="/var/log/ventd"
mkdir -p "$LOGDIR"
LOG="$LOGDIR/postreboot-$TS.log"

pass=0
fail=0

log() { printf '%s\n' "$*" | tee -a "$LOG" >&2; }
check() {
    local id="$1" desc="$2" status="$3" detail="$4"
    case "$status" in
        0)  log "PASS  $id  $desc — $detail"; pass=$((pass + 1)) ;;
        77) log "SKIP  $id  $desc — $detail" ;;
        *)  log "FAIL  $id  $desc — $detail"; fail=$((fail + 1)) ;;
    esac
}

log "post-reboot verify — $(uname -r) — boot at $(uptime -s)"
log "ventd commit: $(systemctl show ventd -p ExecStart --value 2>/dev/null | head -1)"
log ""

# 4a — active
active="$(systemctl is-active ventd 2>&1 || true)"
[[ "$active" == "active" ]]
check 4a "systemctl is-active ventd" "$?" "$active"

# 4b — process user
pid="$(pidof ventd 2>/dev/null || true)"
if [[ -n "$pid" ]]; then
    user="$(ps -o user= -p "$pid" | tr -d ' ')"
    [[ "$user" == "ventd" ]]
    check 4b "ventd runs as User=ventd" "$?" "$user"
else
    check 4b "ventd runs as User=ventd" 1 "no pidof ventd"
fi

# 4c — config ownership/perms
perm="$(stat -c '%U:%G %a' /etc/ventd/config.yaml 2>/dev/null || echo "missing")"
[[ "$perm" == "ventd:ventd 600" ]]
check 4c "/etc/ventd/config.yaml is ventd:ventd 600" "$?" "$perm"

# 4d — API ping (HTTPS, since PR #5 enforces no plaintext on non-loopback binds)
tls_crt="$(grep -E '^\s*tls_cert:' /etc/ventd/config.yaml 2>/dev/null | awk '{print $2}')"
if [[ -n "$tls_crt" ]]; then
    code="$(curl -sfm 3 -k -o /dev/null -w '%{http_code}' https://127.0.0.1:9999/api/ping 2>&1 || echo "curl-err")"
    [[ "$code" == "200" ]]
    check 4d "HTTPS /api/ping = 200" "$?" "code=$code"
else
    code="$(curl -sfm 3 -o /dev/null -w '%{http_code}' http://127.0.0.1:9999/api/ping 2>&1 || echo "curl-err")"
    [[ "$code" == "200" ]]
    check 4d "HTTP /api/ping = 200" "$?" "code=$code"
fi

# 4e — journal clean since boot
bad="$(journalctl -u ventd -b 0 --no-pager 2>/dev/null | grep -Ei 'no hwmon device|resolve hwmon|fatal' | wc -l)"
[[ "$bad" -eq 0 ]]
check 4e "journal free of hwmon-resolve / fatal entries this boot" "$?" "matches=$bad"

# 4f — config still carries hwmon_device for every nct6687 fan
missing="$(awk '
    BEGIN{in_fans=0; in_block=0; got_chip=0; got_device=0; miss=0}
    /^fans:/{in_fans=1; next}
    in_fans && /^\s*-\s*name:/{
        if (in_block && got_chip && !got_device) miss++
        in_block=1; got_chip=0; got_device=0
    }
    in_fans && /^\S/ && !/^\s/{
        if (in_block && got_chip && !got_device) miss++
        in_fans=0; in_block=0
    }
    in_fans && /chip_name: nct6687/{got_chip=1}
    in_fans && /hwmon_device:/{got_device=1}
    END{
        if (in_block && got_chip && !got_device) miss++
        print miss
    }
' /etc/ventd/config.yaml)"
[[ "$missing" -eq 0 ]]
check 4f "every nct6687 fan carries hwmon_device" "$?" "missing=$missing"

# 4g — hwmon chip binding reported at boot (renumber-survival sanity)
wait_line="$(journalctl -u ventd -b 0 --no-pager 2>/dev/null | grep -E 'chips=' | head -1)"
[[ -n "$wait_line" ]]
check 4g "ventd reported hwmon chip binding this boot" "$?" "${wait_line:-no chips= log line}"

# 4h — resolved pwm_path lives under the configured hwmon_device.
#
# 4e passes when the daemon's journal is silent; 4f passes when the YAML
# carries the disambiguation field. Neither catches the situation where
# the resolver picked the wrong single-candidate chip at boot (#86). This
# check closes that gap by following <hwmon_dir>/device and comparing to
# the configured hwmon_device for every fan.
chip_err="$(assert_chip_binding /etc/ventd/config.yaml /sys/class/hwmon 2>&1 1>/dev/null)"
chip_rc=$?
chip_summary="$(assert_chip_binding /etc/ventd/config.yaml /sys/class/hwmon 2>/dev/null | tail -1)"
case "$chip_rc" in
    0)  check 4h "resolved pwm_path lives under configured hwmon_device" 0 "$chip_summary" ;;
    77) check 4h "resolved pwm_path lives under configured hwmon_device" 77 "$chip_summary" ;;
    *)  check 4h "resolved pwm_path lives under configured hwmon_device" "$chip_rc" \
            "$(printf '%s' "$chip_err" | tr '\n' ';' | sed 's/  */ /g')" ;;
esac

log ""
log "summary: $pass PASS / $fail FAIL"
log "log: $LOG"
[[ "$fail" -eq 0 ]]
