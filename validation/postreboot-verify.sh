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

set -uo pipefail

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
        0) log "PASS  $id  $desc — $detail"; pass=$((pass + 1)) ;;
        *) log "FAIL  $id  $desc — $detail"; fail=$((fail + 1)) ;;
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
    /^fans:/{in_fans=1}
    in_fans && /^\s*-\s*name:/{name=$0; got_chip=0; got_device=0}
    in_fans && /chip_name: nct6687/{got_chip=1}
    in_fans && /hwmon_device:/{got_device=1}
    in_fans && /^\S/ && !/^fans:/{in_fans=0}
    END{exit 0}
' /etc/ventd/config.yaml 2>/dev/null; awk '
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

# 4g (new, for the reboot gate specifically) — hwmon renumber survived
wait_line="$(journalctl -u ventd -b 0 --no-pager 2>/dev/null | grep -E 'chips=' | head -1)"
[[ -n "$wait_line" ]]
check 4g "ventd reported hwmon chip binding this boot" "$?" "${wait_line:-no chips= log line}"

log ""
log "summary: $pass PASS / $fail FAIL"
log "log: $LOG"
[[ "$fail" -eq 0 ]]
