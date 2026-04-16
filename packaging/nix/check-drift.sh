#!/usr/bin/env bash
# check-drift.sh — guard against NixOS/deploy-file drift.
#
# packaging/nix/nixos-module.nix used to inline every directive from
# deploy/ventd.service and every byte of deploy/90-ventd-hwmon.rules.
# If either upstream file grew a new sandbox directive and the module
# didn't, NixOS users silently got a weaker unit. See #147 for the
# original incident report and #145 for the PR that introduced the
# duplication.
#
# The module now loads those files directly:
#
#   - deploy/ventd.service          → runCommand + substituteInPlace
#   - deploy/ventd-recover.service  → runCommand + substituteInPlace
#   - deploy/90-ventd-hwmon.rules   → builtins.readFile
#
# This script is the belt-and-braces CI guard that asserts the
# single-source-of-truth architecture is still in place. It is not
# running Nix — it is a static grep over the module source — so its
# job is to catch somebody quietly reinstating inlined content or
# introducing an extra `--replace*` rewrite that silently weakens the
# rendered unit (e.g. rewriting "WatchdogSec=2s" to "WatchdogSec=0").
#
# Content-level drift between deploy/ and the module cannot exist
# under this architecture: the module consumes deploy/ verbatim
# except for two FHS path rewrites, and --replace-fail aborts the
# Nix build if either rewrite stops matching.
#
# Usage:
#   packaging/nix/check-drift.sh [repo-root]
#
# Intended to run from the repo root; if invoked from elsewhere, pass
# the repo root explicitly.

set -euo pipefail

ROOT="${1:-.}"
UNIT="$ROOT/deploy/ventd.service"
RECOVER="$ROOT/deploy/ventd-recover.service"
RULES="$ROOT/deploy/90-ventd-hwmon.rules"
MODULE="$ROOT/packaging/nix/nixos-module.nix"

for f in "$UNIT" "$RECOVER" "$RULES" "$MODULE"; do
    if [[ ! -f "$f" ]]; then
        printf 'check-drift: %s not found\n' "$f" >&2
        exit 2
    fi
done

fail=0

# ─ 1. Module must reference each deploy/ unit file as a Nix path ────
if ! grep -q '\.\./\.\./deploy/ventd\.service' "$MODULE"; then
    printf 'FAIL: %s does not reference deploy/ventd.service\n' "$MODULE"
    fail=1
fi
if ! grep -q '\.\./\.\./deploy/ventd-recover\.service' "$MODULE"; then
    printf 'FAIL: %s does not reference deploy/ventd-recover.service\n' \
           "$MODULE"
    fail=1
fi

# ─ 2. Module must load the udev rule via readFile ───────────────────
if ! grep -q 'builtins\.readFile[[:space:]]\+\.\./\.\./deploy/90-ventd-hwmon\.rules' "$MODULE"; then
    printf 'FAIL: %s does not readFile deploy/90-ventd-hwmon.rules\n' "$MODULE"
    fail=1
fi

# ─ 3. Only known FHS paths may be rewritten. Extract every --replace*
#     line in the module and confirm the first quoted token is one of
#     the two allowlisted entries. Anything else — WatchdogSec=,
#     ProtectSystem=, a new random directive — is silent drift and
#     must fail the check.
expected_subst=$(cat <<'EOF'
/usr/local/bin/ventd
/usr/local/sbin/ventd-wait-hwmon
EOF
)

replace_lines=$(grep -E -- '--replace(-fail|-warn|-quiet)?\b' "$MODULE" || true)

if [[ -z "$replace_lines" ]]; then
    printf 'FAIL: %s no longer contains any --replace* lines — are deploy unit files still being loaded?\n' \
           "$MODULE"
    fail=1
else
    while IFS= read -r raw; do
        [[ -z "$raw" ]] && continue
        # Extract the first quoted token after --replace*.
        token=$(printf '%s\n' "$raw" | sed -nE 's/.*--replace[^[:space:]]*[[:space:]]+"([^"]+)".*/\1/p')
        [[ -z "$token" ]] && continue
        if ! grep -qxF "$token" <<<"$expected_subst"; then
            printf 'FAIL: %s rewrites an unexpected token: %q\n' \
                   "$MODULE" "$token"
            fail=1
        fi
    done <<<"$replace_lines"

    # Each expected substitution must appear at least once in the
    # module (i.e. ventd.service must still rewrite both paths).
    while IFS= read -r path; do
        if ! grep -qF -- "\"$path\"" <<<"$replace_lines"; then
            printf 'FAIL: %s no longer rewrites expected path %q\n' \
                   "$MODULE" "$path"
            fail=1
        fi
    done <<<"$expected_subst"
fi

if (( fail == 0 )); then
    printf 'PASS: nix module tracks deploy/ventd.service, deploy/ventd-recover.service, and deploy/90-ventd-hwmon.rules without drift\n'
fi

exit "$fail"
