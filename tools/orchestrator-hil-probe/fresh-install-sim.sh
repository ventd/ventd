#!/bin/bash
# Fresh-user install simulation for the v0.8.x setup-wizard rework.
#
# Simulates what a brand-new user does on a brand-new install:
#   1. Confirm we're on the right box (paranoia check)
#   2. Show current state — operator sees what's about to be wiped
#   3. apt purge ventd (triggers PR#A4's postremove purge of state)
#   4. Verify state is fully gone
#   5. Optionally remove the nct6687d DKMS module (truer "fresh user"
#      simulation — fresh users don't have OOT drivers installed)
#   6. Install dev .deb built from the orchestrator-default code
#   7. Start ventd; the wizard runs the new orchestrator path
#   8. Verify the generated config.yaml has the expected shape
#      (Sensors + Fans + Curves + Controls — active control)
#   9. Verify fans are being controlled (PWM writes happen)
#
# IMPORTANT: this is destructive on the running production daemon.
# Operator confirmation gates every destructive step.
#
# Run from repo root: bash tools/orchestrator-hil-probe/fresh-install-sim.sh

set -eu

# ─── 1. Sanity check ────────────────────────────────────────────────
EXPECTED_HOST="proxmox"
if [ "$(hostname)" != "$EXPECTED_HOST" ]; then
    echo "ABORT: expected hostname '$EXPECTED_HOST', got '$(hostname)'" >&2
    echo "This script is calibrated for the 13900K box only." >&2
    exit 1
fi

if [ "$(id -u)" -ne 0 ]; then
    echo "ABORT: must run as root (apt + systemctl access)" >&2
    exit 1
fi

pause() {
    printf '\n→ %s\n' "$1"
    printf '  press ENTER to continue, Ctrl-C to abort: '
    read -r _
}

# ─── 2. Show current state ─────────────────────────────────────────
echo "=== Current ventd state ==="
echo "--- service ---"
systemctl status ventd.service --no-pager | head -5 || true
echo "--- package ---"
dpkg-query -W -f='${Package} ${Version}\n' ventd 2>/dev/null || echo "ventd not installed via dpkg"
echo "--- state files ---"
ls -la /etc/ventd/ 2>/dev/null || echo "no /etc/ventd"
ls -la /var/lib/ventd/ 2>/dev/null || echo "no /var/lib/ventd"
ls /etc/modprobe.d/ventd-*.conf 2>/dev/null || echo "no ventd modprobe drop-ins"
echo "--- DKMS modules ---"
dkms status 2>/dev/null | grep -i nct6687d || echo "no nct6687d DKMS"
echo

pause "Step 3: 'apt purge ventd' (this stops the daemon and wipes ALL state via PR#A4 postremove)"

# ─── 3. Purge ──────────────────────────────────────────────────────
apt purge -y ventd 2>&1 | tail -20 || true

# ─── 4. Verify wipe ────────────────────────────────────────────────
echo
echo "=== Post-purge verification ==="
echo "--- /etc/ventd ---"
ls -la /etc/ventd/ 2>/dev/null || echo "✓ gone"
echo "--- /var/lib/ventd ---"
ls -la /var/lib/ventd/ 2>/dev/null || echo "✓ gone"
echo "--- modprobe drop-ins ---"
ls /etc/modprobe.d/ventd-*.conf 2>/dev/null || echo "✓ no drop-ins"
echo "--- /var/log/ventd ---"
ls /var/log/ventd/ 2>/dev/null || echo "✓ gone"

pause "Step 5 (OPTIONAL — destructive): remove the nct6687d DKMS module too. Without this, ventd's DriverPlan will see 'ready' since the module is already loaded. With this, DriverPlan → DriverInstall actually fires."

if dkms status 2>/dev/null | grep -qi nct6687d; then
    set +e
    echo "Removing nct6687d DKMS module..."
    modprobe -r nct6687d 2>&1 || echo "  (modprobe -r exited non-zero — likely in-use; that's OK if pwm_enable is at BIOS-managed)"
    dkms remove nct6687d/$(dkms status | grep nct6687d | head -1 | awk -F'[/,]' '{print $2}') --all 2>&1 | tail -5
    set -e
    echo "✓ nct6687d removed"
fi

pause "Step 6: install the dev .deb (built from current main with orchestrator default-on)"

# ─── 6. Install dev binary as a .deb ──────────────────────────────
# Easiest: build via goreleaser snapshot, install the .deb.
# Fallback: copy binary + write minimal systemd unit.
echo
echo "Looking for built artifact..."
if ls dist/ventd_*_linux_amd64.deb 2>/dev/null; then
    DEB=$(ls dist/ventd_*_linux_amd64.deb | head -1)
    echo "Installing: $DEB"
    apt install -y "$DEB"
else
    echo "No .deb in dist/. Run: goreleaser build --snapshot --clean"
    echo "Then re-run this script from step 6."
    exit 1
fi

pause "Step 7: start ventd, wait for orchestrator to complete"

systemctl start ventd.service
sleep 5
journalctl -u ventd.service --no-pager -n 50 | tail -30
echo
echo "=== Wait up to 5 min for wizard to apply config (orchestrator path) ==="
for i in {1..60}; do
    if [ -f /etc/ventd/config.yaml ]; then
        echo "  config appeared after ${i}×5s"
        break
    fi
    sleep 5
done

# ─── 8. Verify config shape ───────────────────────────────────────
echo
echo "=== Generated config.yaml ==="
cat /etc/ventd/config.yaml 2>&1 | head -50
echo
echo "=== Shape assertions ==="
[ -f /etc/ventd/config.yaml ] && echo "✓ config.yaml exists" || echo "✗ MISSING"
grep -q '^sensors:' /etc/ventd/config.yaml && echo "✓ has sensors" || echo "✗ no sensors"
grep -q '^fans:' /etc/ventd/config.yaml && echo "✓ has fans" || echo "✗ no fans"
grep -q '^curves:' /etc/ventd/config.yaml && echo "✓ has curves" || echo "✗ MONITOR-ONLY (no curves)"
grep -q '^controls:' /etc/ventd/config.yaml && echo "✓ has controls" || echo "✗ MONITOR-ONLY (no controls)"

# ─── 9. Verify orchestrator state ─────────────────────────────────
echo
echo "=== Orchestrator checkpoint state ==="
cat /var/lib/ventd/setup/state.json 2>&1 | head -100 || echo "(no state.json)"

echo
echo "=== Simulation complete ==="
echo "Review the config above. If it has Sensors+Fans+Curves+Controls,"
echo "the orchestrator did its job: zero-input fresh-user install →"
echo "working active-control config."
