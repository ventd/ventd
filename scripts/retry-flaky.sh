#!/usr/bin/env bash
# retry-flaky.sh — wrap `go test ./...` with a single automatic retry for
# tests listed in .github/flaky-tests.yaml.
#
# Behaviour:
#   1. Run the full test suite once with the args passed to this script.
#   2. If exit code is 0, propagate success.
#   3. If exit code is non-zero, parse the JSON output for FAIL events.
#      a. If EVERY failure is a registered flake, re-run only those tests
#         with `go test -run "^TestX$|^TestY$"` and propagate the re-run's
#         exit code (second failure is real).
#      b. If ANY failure is NOT in the registry, exit with the original
#         exit code (no retry).
#
# Usage:
#   ./scripts/retry-flaky.sh -race -count=1 ./...
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
registry="$repo_root/.github/flaky-tests.yaml"

if [[ ! -f "$registry" ]]; then
	echo "retry-flaky: registry not found: $registry" >&2
	exec go test "$@"
fi

# Parse the registry into a sorted list of "<pkg>:<TopLevelTest>" lines.
# We deliberately use `awk` not yq — keeps CI dep-free.
mapfile -t flaky_keys < <(awk '
	/^[[:space:]]*-[[:space:]]*test:[[:space:]]*/ {
		sub(/^[[:space:]]*-[[:space:]]*test:[[:space:]]*/, "")
		gsub(/[[:space:]]+$/, "")
		gsub(/^["'"'"']|["'"'"']$/, "")
		# strip a leading "./" — go test -json reports "./pkg" but its
		# "Package" field is the import path or "./pkg"; we normalise.
		print
	}
' "$registry" | sort -u)

if [[ ${#flaky_keys[@]} -eq 0 ]]; then
	exec go test "$@"
fi

json_log=$(mktemp -t retry-flaky.XXXXXX.json)
trap 'rm -f "$json_log"' EXIT

# Run once, tee the json output. PIPESTATUS[0] = go test exit code.
set +e
go test -json "$@" | tee "$json_log" >/dev/null
first_exit=${PIPESTATUS[0]}
set -e

if [[ $first_exit -eq 0 ]]; then
	exit 0
fi

# Extract FAILs from the JSON stream. Each line is one JSON event;
# we want `Action=="fail"` events that have a `Test` field. Subtest
# events arrive as Test:"TestFoo/subname" — we strip after the first '/'
# because the registry tracks top-level tests, and a subtest fail also
# yields a top-level fail event in the same stream.
mapfile -t failed_keys < <(
	python3 - "$json_log" <<'PY'
import json, sys
seen = set()
with open(sys.argv[1]) as f:
    for line in f:
        line = line.strip()
        if not line or not line.startswith('{'):
            continue
        try:
            ev = json.loads(line)
        except json.JSONDecodeError:
            continue
        if ev.get("Action") != "fail":
            continue
        test = ev.get("Test", "")
        pkg = ev.get("Package", "")
        if not test or not pkg:
            continue
        # strip subtest suffix
        if "/" in test:
            test = test.split("/", 1)[0]
        seen.add(f"{pkg}:{test}")
for k in sorted(seen):
    print(k)
PY
)

if [[ ${#failed_keys[@]} -eq 0 ]]; then
	# Suite failed but no FAIL events — likely a build error or panic.
	exit "$first_exit"
fi

# Are all failures registered as flakes?
declare -A flake_set
for k in "${flaky_keys[@]}"; do flake_set["$k"]=1; done

unregistered=()
for k in "${failed_keys[@]}"; do
	if [[ -z "${flake_set[$k]:-}" ]]; then
		unregistered+=("$k")
	fi
done

if [[ ${#unregistered[@]} -gt 0 ]]; then
	echo "retry-flaky: NOT retrying — unregistered failure(s):" >&2
	printf '  %s\n' "${unregistered[@]}" >&2
	exit "$first_exit"
fi

echo "retry-flaky: all failures are registered flakes, retrying once:" >&2
printf '  %s\n' "${failed_keys[@]}" >&2

# Build a retry invocation: -run regex over the failed test names, scoped
# to the failed packages. We re-run each failed package independently so
# `-run` can be tightly anchored.
declare -A pkg_to_tests
for k in "${failed_keys[@]}"; do
	pkg=${k%%:*}
	test=${k#*:}
	if [[ -z "${pkg_to_tests[$pkg]:-}" ]]; then
		pkg_to_tests[$pkg]="^${test}$"
	else
		pkg_to_tests[$pkg]="${pkg_to_tests[$pkg]}|^${test}$"
	fi
done

retry_failed=0
for pkg in "${!pkg_to_tests[@]}"; do
	regex=${pkg_to_tests[$pkg]}
	echo "retry-flaky: go test -run '$regex' -count=1 $pkg" >&2
	if ! go test -run "$regex" -count=1 "$pkg"; then
		retry_failed=1
	fi
done

if [[ $retry_failed -ne 0 ]]; then
	echo "retry-flaky: retry FAILED — second failure is real" >&2
	exit 1
fi

echo "retry-flaky: retry passed — flake absorbed" >&2
exit 0
