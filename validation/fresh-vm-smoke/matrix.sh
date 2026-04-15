#!/usr/bin/env bash
# ventd fresh-VM install smoke — matrix driver.
#
# Runs run.sh serially across every distro listed in the DISTROS array that
# has a corresponding cloud-init template on the Proxmox host. Aggregates
# per-distro PASS/FAIL into validation/fresh-vm-smoke-<date>.md.
#
# Serial, not parallel, so the tail of each run's output stays readable and
# the per-distro report section matches its own log tail.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

PVE_HOST="${PVE_HOST:-root@pve}"

# Default sweep. Edit to narrow the matrix for a faster local run.
DISTROS=("${@:-ubuntu-24.04 debian-12 fedora-40 arch}")
# Flatten if passed as a single space-separated argument.
read -r -a DISTROS <<< "${DISTROS[*]}"

BINARY="${BINARY:-}"
if [[ -z "$BINARY" ]]; then
    echo "building ventd from current tree..." >&2
    BINARY="$(mktemp -t ventd-smoke-bin-XXXX)"
    ( cd "$REPO_ROOT" && go build -o "$BINARY" ./cmd/ventd ) || {
        echo "ERROR: go build failed" >&2
        exit 1
    }
fi

REPORT="$REPO_ROOT/validation/fresh-vm-smoke-$(date +%Y-%m-%d).md"
{
    echo "# ventd fresh-VM install smoke — $(date +%Y-%m-%d)"
    echo ""
    echo "- Proxmox host: \`$PVE_HOST\`"
    echo "- Binary: \`$BINARY\` ($(stat -c %s "$BINARY" 2>/dev/null || wc -c <"$BINARY") bytes)"
    echo "- Repo HEAD: $(git -C "$REPO_ROOT" rev-parse --short HEAD 2>/dev/null || echo 'unknown')"
    echo ""
    echo "| Distro | Result | Wall clock |"
    echo "|--------|--------|-----------:|"
} > "$REPORT"

overall_rc=0
for distro in "${DISTROS[@]}"; do
    [[ -n "$distro" ]] || continue
    echo "========================================"
    echo "== $distro"
    echo "========================================"
    result_dir="$(mktemp -d -t "ventd-smoke-${distro}-XXXX")"
    start=$(date +%s)
    if "$SCRIPT_DIR/run.sh" \
        --distro "$distro" \
        --binary "$BINARY" \
        --pve-host "$PVE_HOST" \
        --result-dir "$result_dir" \
        2>&1 | tee "$result_dir/run.log"; then
        verdict="PASS"
    else
        verdict="FAIL"
        overall_rc=1
    fi
    end=$(date +%s)
    wall=$(( end - start ))

    {
        echo "| $distro | $verdict | ${wall}s |"
    } >> "$REPORT"

    {
        echo ""
        echo "---"
        echo ""
        echo "## $distro — $verdict (${wall}s)"
        echo ""
        echo "**Result JSON**"
        echo ""
        echo '```json'
        cat "$result_dir/result.json" 2>/dev/null || echo "(missing)"
        echo '```'
        echo ""
        echo "**Install log tail (last 40 lines)**"
        echo ""
        echo '```'
        tail -n 40 "$result_dir/install.log" 2>/dev/null || echo "(missing)"
        echo '```'
    } >> "$REPORT"
done

echo ""
echo "Report written to: $REPORT"
exit "$overall_rc"
