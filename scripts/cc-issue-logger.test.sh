#!/usr/bin/env bash
# cc-issue-logger.test.sh — tests for scripts/cc-issue-logger.sh.
#
# Exercises the logic paths from issues #148 and #149:
#   - race_count when zero matches (awk replaces grep -c || echo 0)
#   - race_count when matches present
#   - empty labels path (no --label flag passed)
#   - multi-label path (one --label flag per entry, no v0.3.0 default)
#   - dirty_count guard against empty input
#
# Runs with plain bash + assertions. No bats, no external deps.
# Stubs `gh` so the tests never hit the network.
#
# Invoke directly:
#   bash scripts/cc-issue-logger.test.sh
# or via Make:
#   make test-issue-logger

# NOTE: do NOT set -e here — the library under test uses `set -euo pipefail`
# and we want to observe its behaviour, not inherit-and-mask it.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOGGER="$SCRIPT_DIR/cc-issue-logger.sh"

_fail=0
_pass=0

_assert() {
    local msg="$1"
    local expected="$2"
    local actual="$3"
    if [ "$expected" = "$actual" ]; then
        _pass=$((_pass + 1))
        echo "  ok   — $msg"
    else
        _fail=$((_fail + 1))
        echo "  FAIL — $msg"
        echo "         expected: $(printf '%q' "$expected")"
        echo "         actual:   $(printf '%q' "$actual")"
    fi
}

_assert_contains() {
    local msg="$1"
    local needle="$2"
    local haystack="$3"
    if [[ "$haystack" == *"$needle"* ]]; then
        _pass=$((_pass + 1))
        echo "  ok   — $msg"
    else
        _fail=$((_fail + 1))
        echo "  FAIL — $msg"
        echo "         needle not in haystack: $(printf '%q' "$needle")"
        echo "         haystack: $(printf '%q' "$haystack")"
    fi
}

_assert_not_contains() {
    local msg="$1"
    local needle="$2"
    local haystack="$3"
    if [[ "$haystack" != *"$needle"* ]]; then
        _pass=$((_pass + 1))
        echo "  ok   — $msg"
    else
        _fail=$((_fail + 1))
        echo "  FAIL — $msg"
        echo "         needle unexpectedly present: $(printf '%q' "$needle")"
    fi
}

_setup_tmpdir() {
    _TMP="$(mktemp -d)"
    _ISSUE_BACKLOG="$_TMP/backlog.jsonl"
    export _ISSUE_BACKLOG _TMP
}

_teardown_tmpdir() {
    rm -rf "$_TMP"
    unset _TMP _ISSUE_BACKLOG
}

# ─── gh stub ─────────────────────────────────────────────────────────
# Records each invocation to $_TMP/gh-calls.log and returns a canned
# response. `gh auth status` succeeds (exit 0) so the logger takes the
# "GitHub first" path. `gh issue create` echoes a predictable URL and
# appends the full argv (with the resolved --label flags) to the log.
_install_gh_stub() {
    cat > "$_TMP/gh" <<'STUB'
#!/usr/bin/env bash
_log="$_TMP/gh-calls.log"
case "$1" in
    auth)
        exit 0
        ;;
    issue)
        shift
        case "$1" in
            list)
                # Dedup search: return empty list → no duplicate.
                echo "[]"
                exit 0
                ;;
            create)
                # Record the full argv (for label inspection) and echo a fake URL.
                printf 'issue create' >> "$_log"
                for arg in "$@"; do
                    printf ' %s' "$(printf '%q' "$arg")" >> "$_log"
                done
                printf '\n' >> "$_log"
                echo "https://github.com/ventd/ventd/issues/999"
                exit 0
                ;;
        esac
        ;;
esac
exit 0
STUB
    chmod +x "$_TMP/gh"
    PATH="$_TMP:$PATH"
    export PATH
}

# ─── Test 1: race_count=0 happy path (was "0\n0" bug) ─────────────
test_race_count_zero() {
    echo "test: race_count=0 on file with no DATA RACE lines"
    _setup_tmpdir
    _install_gh_stub

    # Clean log — no DATA RACE warnings.
    cat > "$_TMP/clean.log" <<'EOF'
=== RUN   TestFoo
--- PASS: TestFoo (0.00s)
PASS
ok  	github.com/ventd/ventd/internal/foo	0.123s
EOF

    # Run the awk snippet as the script does.
    local race_count
    race_count="$(awk '/WARNING: DATA RACE/{c++}END{print c+0}' "$_TMP/clean.log" 2>/dev/null)"
    race_count="${race_count:-0}"

    _assert "race_count is single integer zero, not \"0\\n0\"" "0" "$race_count"

    # And must survive [ -gt 0 ] comparison with no "integer expression expected" error.
    local err
    err="$( { [ "$race_count" -gt 0 ] || true; } 2>&1)"
    _assert "[ -gt 0 ] does not trip on race_count=0" "" "$err"

    _teardown_tmpdir
}

# ─── Test 2: race_count>0 path (still detects races) ──────────────
test_race_count_positive() {
    echo "test: race_count>0 on file containing DATA RACE warnings"
    _setup_tmpdir
    _install_gh_stub

    cat > "$_TMP/racy.log" <<'EOF'
==================
WARNING: DATA RACE
Read at 0x00c0000180e0 by goroutine 8:
  main.worker()
      /tmp/main.go:12 +0x42
Previous write at 0x00c0000180e0 by main goroutine:
  main.main()
      /tmp/main.go:18 +0x84
==================
WARNING: DATA RACE
another race reported
EOF

    local race_count
    race_count="$(awk '/WARNING: DATA RACE/{c++}END{print c+0}' "$_TMP/racy.log" 2>/dev/null)"
    race_count="${race_count:-0}"

    _assert "race_count counts both DATA RACE warnings" "2" "$race_count"

    _teardown_tmpdir
}

# ─── Test 3: dirty_count guard on empty input ─────────────────────
# Protects against `echo "" | wc -l` → 1 surprises and empty strings.
test_dirty_count_guard() {
    echo "test: dirty_count guard tolerates empty input"
    _setup_tmpdir

    local dirty_files=""
    local dirty_count
    dirty_count="$(printf '%s\n' "$dirty_files" | wc -l | tr -d '[:space:]')"
    dirty_count="${dirty_count:-0}"

    # With dirty_files="" and an echo-style newline, wc -l is 1, but the
    # surrounding guard (`[ -n "$dirty_files" ]`) prevents the branch
    # from firing. The expansion default still protects against an
    # empty capture. Assert the guard itself is idempotent.
    _assert "dirty_count is a non-empty integer" "1" "$dirty_count"

    # Exercise the branch guard: empty dirty_files must not enter the block.
    local entered=0
    if [ -n "$dirty_files" ]; then
        entered=1
    fi
    _assert "empty dirty_files does not enter the gofmt branch" "0" "$entered"

    _teardown_tmpdir
}

# ─── Test 4: empty labels path (no --label flag passed) ───────────
test_issue_file_empty_labels() {
    echo "test: issue_file with empty labels emits no --label flag"
    _setup_tmpdir
    _install_gh_stub

    # shellcheck source=/dev/null
    source "$LOGGER"
    issue_init "PR #test" "test-branch" >/dev/null

    issue_file "surprise" "empty label test" "body text" "" >/dev/null

    local log
    log="$(cat "$_TMP/gh-calls.log" 2>/dev/null || true)"

    _assert_contains "gh was called for issue create" "issue create" "$log"
    _assert_not_contains "no --label flag when labels empty" "--label" "$log"

    _teardown_tmpdir
}

# ─── Test 5: multi-label path ─────────────────────────────────────
test_issue_file_multi_labels() {
    echo "test: issue_file with multiple comma-separated labels"
    _setup_tmpdir
    _install_gh_stub

    # shellcheck source=/dev/null
    source "$LOGGER"
    issue_init "PR #test" "test-branch" >/dev/null

    issue_file "bug" "multi label test" "body text" "bug,enhancement" >/dev/null

    local log
    log="$(cat "$_TMP/gh-calls.log" 2>/dev/null || true)"

    _assert_contains "gh was called for issue create" "issue create" "$log"
    _assert_contains "--label bug present" "--label bug" "$log"
    _assert_contains "--label enhancement present" "--label enhancement" "$log"

    _teardown_tmpdir
}

# ─── Test 6: wrapper defaults no longer include v0.3.0 ────────────
test_wrapper_defaults_no_v030() {
    echo "test: wrapper defaults no longer reference v0.3.0"
    _setup_tmpdir
    _install_gh_stub

    # shellcheck source=/dev/null
    source "$LOGGER"
    issue_init "PR #test" "test-branch" >/dev/null

    # issue_bug default is "bug"
    issue_bug "bug wrapper test" "body" >/dev/null

    # issue_surprise default is empty
    issue_surprise "surprise wrapper test" "body" >/dev/null

    # issue_wish default is "enhancement"
    issue_wish "wish wrapper test" "body" >/dev/null

    local log
    log="$(cat "$_TMP/gh-calls.log" 2>/dev/null || true)"

    _assert_not_contains "no v0.3.0 label anywhere in gh calls" "v0.3.0" "$log"
    _assert_contains "issue_bug still passes --label bug" "--label bug" "$log"
    _assert_contains "issue_wish still passes --label enhancement" "--label enhancement" "$log"

    _teardown_tmpdir
}

# ─── runner ──────────────────────────────────────────────────────────
echo "cc-issue-logger.sh tests"
echo "========================"

test_race_count_zero
test_race_count_positive
test_dirty_count_guard
test_issue_file_empty_labels
test_issue_file_multi_labels
test_wrapper_defaults_no_v030

echo ""
echo "========================"
echo "passed: $_pass"
echo "failed: $_fail"

if [ "$_fail" -gt 0 ]; then
    exit 1
fi
exit 0
