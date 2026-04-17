#!/usr/bin/env bash
# diagnose-tests.sh — structured test runner for ventd.
#
# This is the single entry point that both humans and Claude Code
# should use to run the diagnostic suite. It invokes a fixed set of
# test groups in a predictable order, captures per-group exit codes,
# and prints a summary section a tool can parse by looking for the
# line `DIAGNOSE-SUMMARY BEGIN`/`DIAGNOSE-SUMMARY END`.
#
# Exit code is 0 iff every group passed. Individual group failures do
# NOT abort the run — the point is to surface everything that is
# broken in one pass, not to bail on the first red.
#
# Usage:
#   scripts/diagnose-tests.sh              # run all groups (default)
#   scripts/diagnose-tests.sh safety       # run only safety-critical
#   scripts/diagnose-tests.sh fuzz         # run seed corpora only
#   scripts/diagnose-tests.sh fuzz-long    # run -fuzz for 30s/target
#   scripts/diagnose-tests.sh -v           # verbose test output
#
# Environment:
#   DIAGNOSE_FUZZTIME   override the fuzz-long duration (default 30s)
#   DIAGNOSE_RACE       set to "0" to skip -race (not recommended)
#
# Reference for future sessions:
#
#   When a new package test file lands that is worth running as a
#   dedicated diagnostic group, add it to the `groups` associative
#   array below. Keep group names short and action-oriented; Claude
#   Code's workflow docs in docs/TESTING.md reference them by name.
#
#   The script deliberately avoids running `go vet` or `staticcheck`
#   here — those live in the Makefile and have different failure
#   semantics. This file is about test results only.

set -u

cd "$(dirname "$0")/.."

VERBOSE=""
MODE="all"
FUZZTIME="${DIAGNOSE_FUZZTIME:-30s}"
RACE="-race"
if [[ "${DIAGNOSE_RACE:-1}" == "0" ]]; then
	RACE=""
fi

for arg in "$@"; do
	case "$arg" in
		-v|--verbose) VERBOSE="-v" ;;
		all|safety|fuzz|fuzz-long|web|cmd) MODE="$arg" ;;
		*) echo "unknown arg: $arg" >&2; exit 2 ;;
	esac
done

# Declare groups as parallel arrays so the script works under bash 3.2
# (macOS) as well as bash 5. Each group has a name, a short label used
# in the summary, and the go test invocation.
group_names=(
	safety_watchdog
	safety_controller
	safety_calibrate
	hwmon_parsers
	nvidia_unavailable
	web_handlers
	cmd_preflight
	config_fuzz_seed
	hwmon_fuzz_seed
)
group_labels=(
	"watchdog restore matrix"
	"controller safety invariants"
	"calibrate detect + abort"
	"hwmon autoload parsers"
	"nvidia unavailable paths"
	"web setup / detect / abort handlers"
	"cmd/ventd preflight subcommand"
	"config.Parse fuzz seeds"
	"hwmon sensors-detect fuzz seeds"
)
group_cmds=(
	"go test $RACE -count=1 $VERBOSE ./internal/watchdog/..."
	"go test $RACE -count=1 $VERBOSE -run TestSafety ./internal/controller/..."
	"go test $RACE -count=1 $VERBOSE -run 'TestDetectRPMSensor|TestAbort' ./internal/calibrate/..."
	"go test $RACE -count=1 $VERBOSE -run 'TestAutoload|TestParseSensorsDetect|TestIdentifyDriverNeeds|TestKoBasename|TestModuleFromPath' ./internal/hwmon/..."
	"go test $RACE -count=1 $VERBOSE -run 'TestAvailable|TestPublicFunctions|TestZeroValue|TestGPUName|TestReadMetric_Unknown|TestNvmlErrorString|TestGoStringFromC|TestShutdown_Idempotent|TestInit_Concurrent' ./internal/nvidia/..."
	"go test $RACE -count=1 $VERBOSE -run 'TestHandle(Setup|DetectRPM|CalibrateAbort)' ./internal/web/..."
	"go test $RACE -count=1 $VERBOSE -run 'TestRunPreflightCheck|TestPreflightReasonString|TestPrintVersion' ./cmd/ventd/..."
	"go test -count=1 -run FuzzParseConfig ./internal/config/..."
	"go test -count=1 -run FuzzParseSensorsDetect ./internal/hwmon/..."
)

# Optional: fuzz-long adds an extra group that runs -fuzz for real.
fuzz_long_names=(fuzz_config fuzz_hwmon)
fuzz_long_labels=(
	"config.Parse fuzz (-fuzz $FUZZTIME)"
	"sensors-detect fuzz (-fuzz $FUZZTIME)"
)
fuzz_long_cmds=(
	"go test -run ^$ -fuzz FuzzParseConfig -fuzztime $FUZZTIME ./internal/config/..."
	"go test -run ^$ -fuzz FuzzParseSensorsDetect -fuzztime $FUZZTIME ./internal/hwmon/..."
)

run_group() {
	local label="$1" cmd="$2"
	echo
	echo "=== $label ==="
	echo "\$ $cmd"
	# shellcheck disable=SC2086 # word splitting is intentional
	bash -c "$cmd"
}

selected_names=()
selected_labels=()
selected_cmds=()
case "$MODE" in
	all)
		selected_names=("${group_names[@]}")
		selected_labels=("${group_labels[@]}")
		selected_cmds=("${group_cmds[@]}")
		;;
	safety)
		selected_names=("${group_names[@]:0:3}")
		selected_labels=("${group_labels[@]:0:3}")
		selected_cmds=("${group_cmds[@]:0:3}")
		;;
	web)
		selected_names=("web_handlers")
		selected_labels=("${group_labels[5]}")
		selected_cmds=("${group_cmds[5]}")
		;;
	cmd)
		selected_names=("cmd_preflight")
		selected_labels=("${group_labels[6]}")
		selected_cmds=("${group_cmds[6]}")
		;;
	fuzz)
		selected_names=("${group_names[@]:7:2}")
		selected_labels=("${group_labels[@]:7:2}")
		selected_cmds=("${group_cmds[@]:7:2}")
		;;
	fuzz-long)
		selected_names=("${fuzz_long_names[@]}")
		selected_labels=("${fuzz_long_labels[@]}")
		selected_cmds=("${fuzz_long_cmds[@]}")
		;;
esac

declare -a results
overall=0

for i in "${!selected_names[@]}"; do
	run_group "${selected_labels[$i]}" "${selected_cmds[$i]}"
	rc=$?
	results[$i]=$rc
	if [[ $rc -ne 0 ]]; then
		overall=1
	fi
done

echo
echo "=============================================================="
echo "DIAGNOSE-SUMMARY BEGIN"
echo "mode: $MODE"
echo "race: ${RACE:-off}"
for i in "${!selected_names[@]}"; do
	if [[ "${results[$i]}" -eq 0 ]]; then
		status="PASS"
	else
		status="FAIL"
	fi
	printf '%-8s %-22s %s\n' "$status" "${selected_names[$i]}" "${selected_labels[$i]}"
done
echo "overall: $([[ $overall -eq 0 ]] && echo PASS || echo FAIL)"
echo "DIAGNOSE-SUMMARY END"
echo "=============================================================="

exit $overall
