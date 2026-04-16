#!/usr/bin/env bash
# cc-issue-logger.sh — sourceable shell library for CC terminals.
# Provides structured issue filing to GitHub + local backlog,
# an exhaustive audit checklist, and automatic output scanning.
#
# Usage: source this at the top of any CC terminal session:
#   source scripts/cc-issue-logger.sh
#   issue_init "PR #130" "test/monitor-coverage"
#
# Then call issue_* functions as you work. At session end:
#   issue_scan_output /tmp/go-test.log /tmp/go-vet.log
#   issue_audit
#   issue_flush
#
# Local backlog: .claude/issue-backlog.jsonl (gitignored, persists
# across sessions, reviewable by Cowork).

set -euo pipefail

# ─── State ──────────────────────────────────────────────────────────
_ISSUE_PR=""           # e.g. "PR #130"
_ISSUE_BRANCH=""       # e.g. "test/monitor-coverage"
_ISSUE_COUNT=0
_ISSUE_FILED=()        # array of "type: title → #NNN" or "type: title → BACKLOG"
_ISSUE_BACKLOG="${_ISSUE_BACKLOG:-.claude/issue-backlog.jsonl}"

# ─── Init ───────────────────────────────────────────────────────────
# Call once at the start of a session.
#   issue_init "PR #130" "test/monitor-coverage"
issue_init() {
    _ISSUE_PR="${1:?usage: issue_init 'PR #NNN' 'branch-name'}"
    _ISSUE_BRANCH="${2:-}"
    _ISSUE_COUNT=0
    _ISSUE_FILED=()
    mkdir -p "$(dirname "$_ISSUE_BACKLOG")"
    echo "issue-logger: initialised for ${_ISSUE_PR} (branch: ${_ISSUE_BRANCH:-unknown})"
}

# ─── Core filing function ───────────────────────────────────────────
# issue_file TYPE TITLE BODY [LABELS]
#
# TYPE:   bug | surprise | skip | cleanup | flake | ci | coverage | wish
# TITLE:  single-line imperative (will be prefixed with type emoji)
# BODY:   multi-line markdown body; "Surfaced by:" line auto-appended
# LABELS: comma-separated; empty by default (no --label flag passed)
#
# Behaviour:
#   1. Dedup check via `gh issue list --search` (if gh available)
#   2. Create GH issue (if gh available and no dup found)
#   3. Always append to local backlog JSONL
#   4. Print one-line summary to stdout
issue_file() {
    local type="${1:?usage: issue_file TYPE TITLE BODY [LABELS]}"
    local title="${2:?}"
    local body="${3:?}"
    local labels="${4:-}"
    local ts gh_issue="" status="pending"

    ts="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

    # Append standard footer
    body="${body}

---
Surfaced by: ${_ISSUE_PR}
Branch: ${_ISSUE_BRANCH:-unknown}"

    # Try GitHub first
    if command -v gh &>/dev/null && gh auth status &>/dev/null 2>&1; then
        # Dedup: search for existing issue with similar title
        local search_title
        search_title="$(echo "$title" | head -c 60)"
        local existing
        existing="$(gh issue list --search "\"${search_title}\" in:title" --state open --limit 3 --json number,title 2>/dev/null || echo "[]")"

        if echo "$existing" | grep -qi "$(echo "$search_title" | head -c 30)"; then
            local dup_num
            dup_num="$(echo "$existing" | grep -o '"number":[0-9]*' | head -1 | grep -o '[0-9]*')"
            if [ -n "$dup_num" ]; then
                echo "issue-logger: SKIP duplicate — #${dup_num} matches: ${title}"
                status="duplicate"
                gh_issue="#${dup_num}"
            fi
        fi

        if [ "$status" = "pending" ]; then
            local label_args=""
            if [ -n "$labels" ]; then
                IFS=',' read -ra label_arr <<< "$labels"
                for l in "${label_arr[@]}"; do
                    label_args="${label_args} --label $(printf '%q' "$(echo "$l" | xargs)")"
                done
            fi

            gh_issue="$(eval gh issue create \
                --title "\"${title}\"" \
                --body "\"${body}\"" \
                ${label_args} 2>/dev/null || echo "")"

            if [ -n "$gh_issue" ]; then
                status="filed"
                echo "issue-logger: FILED ${type} → ${gh_issue}: ${title}"
            else
                status="gh-failed"
                echo "issue-logger: GH FAILED, saved to backlog: ${title}"
            fi
        fi
    else
        echo "issue-logger: NO GH AUTH, saved to backlog: ${title}"
    fi

    # Always write to local backlog
    local json_title json_body json_pr json_branch
    json_title="$(echo "$title" | sed 's/"/\\"/g')"
    json_body="$(echo "$body" | sed ':a;N;$!ba;s/\n/\\n/g' | sed 's/"/\\"/g')"
    json_pr="$(echo "$_ISSUE_PR" | sed 's/"/\\"/g')"
    json_branch="$(echo "$_ISSUE_BRANCH" | sed 's/"/\\"/g')"

    echo "{\"ts\":\"${ts}\",\"type\":\"${type}\",\"title\":\"${json_title}\",\"body\":\"${json_body}\",\"pr\":\"${json_pr}\",\"branch\":\"${json_branch}\",\"gh_issue\":\"${gh_issue}\",\"status\":\"${status}\"}" \
        >> "$_ISSUE_BACKLOG"

    _ISSUE_COUNT=$((_ISSUE_COUNT + 1))
    _ISSUE_FILED+=("${type}: ${title} → ${gh_issue:-BACKLOG}")
}

# ─── Convenience wrappers ───────────────────────────────────────────

# Bug: actual behaviour mismatch against spec or safety rules.
issue_bug() {
    local title="${1:?}" body="${2:?}" labels="${3:-bug}"
    issue_file "bug" "$title" "$body" "$labels"
}

# Surprise: non-obvious behaviour worth remembering.
issue_surprise() {
    local title="${1:?}" body="${2:?}" labels="${3:-}"
    issue_file "surprise" "$title" "$body" "$labels"
}

# Skip: a test was skipped with t.Skip or a subtest was deferred.
issue_skip() {
    local title="${1:?}" body="${2:?}" labels="${3:-}"
    issue_file "skip" "$title" "$body" "$labels"
}

# Cleanup: deferred refactor, dead code, naming inconsistency.
issue_cleanup() {
    local title="${1:?}" body="${2:?}" labels="${3:-}"
    issue_file "cleanup" "$title" "$body" "$labels"
}

# Flake: CI flake or non-deterministic test.
issue_flake() {
    local title="${1:?}" body="${2:?}" labels="${3:-}"
    issue_file "flake" "$title" "$body" "$labels"
}

# CI: CI infrastructure issue.
issue_ci() {
    local title="${1:?}" body="${2:?}" labels="${3:-}"
    issue_file "ci" "$title" "$body" "$labels"
}

# Coverage: test coverage gap identified but not addressed in this PR.
issue_coverage() {
    local title="${1:?}" body="${2:?}" labels="${3:-}"
    issue_file "coverage" "$title" "$body" "$labels"
}

# Wish: feature idea or improvement surfaced during work.
issue_wish() {
    local title="${1:?}" body="${2:?}" labels="${3:-enhancement}"
    issue_file "wish" "$title" "$body" "$labels"
}

# ─── Automatic output scanner ──────────────────────────────────────
# Parses go test, go vet, gofmt, and golangci-lint output files for
# actionable findings and auto-files issues for each unique finding.
#
# Usage:
#   go test -race -cover ./... 2>&1 | tee /tmp/go-test.log
#   go vet ./... 2>&1 | tee /tmp/go-vet.log
#   gofmt -l ./internal ./cmd 2>&1 | tee /tmp/gofmt.log
#   issue_scan_output /tmp/go-test.log /tmp/go-vet.log /tmp/gofmt.log
#
# Each file is optional — pass only the ones you captured.
issue_scan_output() {
    local files=("$@")
    local scan_count=0

    echo "issue-logger: scanning ${#files[@]} output file(s)..."

    for f in "${files[@]}"; do
        if [ ! -f "$f" ]; then
            echo "issue-logger: skip missing file: $f"
            continue
        fi

        local basename
        basename="$(basename "$f")"

        # ── go test failures ────────────────────────────────────
        # Lines like: --- FAIL: TestFoo/subtest (0.01s)
        # or:         FAIL	github.com/ventd/ventd/internal/foo	0.123s
        while IFS= read -r line; do
            local test_name pkg
            if [[ "$line" =~ ^---\ FAIL:\ ([^ ]+) ]]; then
                test_name="${BASH_REMATCH[1]}"
                issue_file "bug" \
                    "Test failure: ${test_name}" \
                    "Test \`${test_name}\` failed during this session.\n\nFull line:\n\`\`\`\n${line}\n\`\`\`\n\nProposed fix: investigate and fix the failing test" \
                    "bug"
                scan_count=$((scan_count + 1))
            elif [[ "$line" =~ ^FAIL[[:space:]]+(github\.com/ventd/ventd/[^ ]+) ]]; then
                pkg="${BASH_REMATCH[1]}"
                # Only file if we didn't already catch the specific test above
                # (package-level FAIL often follows --- FAIL lines)
                :
            fi
        done < <(grep -E '^(--- FAIL:|FAIL\t)' "$f" 2>/dev/null || true)

        # ── go test race detector warnings ──────────────────────
        # Lines like: WARNING: DATA RACE
        local race_count
        race_count="$(awk '/WARNING: DATA RACE/{c++}END{print c+0}' "$f" 2>/dev/null)"
        race_count="${race_count:-0}"
        if [ "$race_count" -gt 0 ]; then
            # Extract the first race's goroutine stacks (up to 30 lines after WARNING)
            local race_context
            race_context="$(grep -A 30 'WARNING: DATA RACE' "$f" | head -40)"
            issue_file "bug" \
                "Data race detected (${race_count} occurrence(s))" \
                "The race detector found ${race_count} data race(s) during \`go test -race\`.\n\nFirst occurrence:\n\`\`\`\n${race_context}\n\`\`\`\n\nProposed fix: add synchronisation or restructure the concurrent access" \
                "bug"
            scan_count=$((scan_count + 1))
        fi

        # ── go test panics ──────────────────────────────────────
        while IFS= read -r line; do
            local panic_msg
            panic_msg="$(grep -A 5 'panic:' "$f" | head -6)"
            issue_file "bug" \
                "Panic in test: ${line:0:80}" \
                "A panic occurred during testing.\n\n\`\`\`\n${panic_msg}\n\`\`\`\n\nProposed fix: add nil check or guard against the panic condition" \
                "bug"
            scan_count=$((scan_count + 1))
            break  # one issue per file for panics — they cascade
        done < <(grep '^panic:' "$f" 2>/dev/null || true)

        # ── go test skipped tests ───────────────────────────────
        # Lines like: --- SKIP: TestFoo/subtest (0.00s)
        while IFS= read -r line; do
            if [[ "$line" =~ ^---\ SKIP:\ ([^ ]+) ]]; then
                local skip_name="${BASH_REMATCH[1]}"
                # Extract the skip reason (usually the next line with t.Skip message)
                local skip_reason
                skip_reason="$(grep -A 1 "--- SKIP: ${skip_name}" "$f" | tail -1 | sed 's/^[[:space:]]*//')"
                issue_file "skip" \
                    "Skipped test: ${skip_name}" \
                    "Test \`${skip_name}\` was skipped.\n\nReason: ${skip_reason}\n\nProposed fix: remove skip condition or implement the missing dependency"
                scan_count=$((scan_count + 1))
            fi
        done < <(grep '^--- SKIP:' "$f" 2>/dev/null || true)

        # ── go vet warnings ─────────────────────────────────────
        # Lines like: ./foo.go:42:3: printf-style format verb
        while IFS= read -r line; do
            if [[ "$line" =~ ^\.?/?([^:]+):([0-9]+):.*:\ (.+) ]]; then
                local vet_file="${BASH_REMATCH[1]}"
                local vet_line="${BASH_REMATCH[2]}"
                local vet_msg="${BASH_REMATCH[3]}"
                issue_file "cleanup" \
                    "go vet: ${vet_file}:${vet_line} — ${vet_msg:0:60}" \
                    "go vet reported:\n\n\`\`\`\n${line}\n\`\`\`\n\nFile: \`${vet_file}\` line ${vet_line}\n\nProposed fix: address the vet diagnostic"
                scan_count=$((scan_count + 1))
            fi
        done < <(grep -E '^\.?/?[^:]+:[0-9]+:[0-9]+:' "$f" 2>/dev/null | grep -v '^#' | head -20 || true)

        # ── gofmt dirty files ───────────────────────────────────
        # gofmt -l outputs one filename per line for files that need formatting
        local dirty_files
        dirty_files="$(grep -E '\.go$' "$f" 2>/dev/null | head -20 || true)"
        if [ -n "$dirty_files" ] && [[ "$basename" == *gofmt* ]]; then
            local dirty_count
            dirty_count="$(printf '%s\n' "$dirty_files" | wc -l | tr -d '[:space:]')"
            dirty_count="${dirty_count:-0}"
            issue_file "cleanup" \
                "gofmt: ${dirty_count} file(s) need formatting" \
                "The following files are not gofmt-clean:\n\n\`\`\`\n${dirty_files}\n\`\`\`\n\nProposed fix: run \`gofmt -w\` on the listed files"
            scan_count=$((scan_count + 1))
        fi

        # ── test coverage below threshold ───────────────────────
        # Lines like: ok  	github.com/ventd/ventd/internal/foo	0.5s	coverage: 12.3% of statements
        while IFS= read -r line; do
            if [[ "$line" =~ coverage:\ ([0-9]+\.[0-9]+)%\ of\ statements ]]; then
                local cov="${BASH_REMATCH[1]}"
                local cov_int="${cov%.*}"
                if [ "$cov_int" -lt 20 ]; then
                    local cov_pkg
                    cov_pkg="$(echo "$line" | awk '{print $2}')"
                    issue_file "coverage" \
                        "Low coverage: ${cov_pkg} at ${cov}%" \
                        "Package \`${cov_pkg}\` has only ${cov}% statement coverage.\n\nProposed fix: add test cases for the uncovered paths"
                    scan_count=$((scan_count + 1))
                fi
            fi
        done < <(grep 'coverage:' "$f" 2>/dev/null || true)

        # ── build failures ──────────────────────────────────────
        while IFS= read -r line; do
            if [[ "$line" =~ ^#\ (github\.com/ventd/ventd/[^ ]+) ]]; then
                local fail_pkg="${BASH_REMATCH[1]}"
                local build_errors
                build_errors="$(grep -A 5 "^# ${fail_pkg}" "$f" | head -6)"
                issue_file "bug" \
                    "Build failure: ${fail_pkg}" \
                    "Package \`${fail_pkg}\` failed to compile.\n\n\`\`\`\n${build_errors}\n\`\`\`\n\nProposed fix: resolve the compilation error" \
                    "bug"
                scan_count=$((scan_count + 1))
                break  # one issue per build failure — they cascade
            fi
        done < <(grep '^# github.com/ventd/ventd/' "$f" 2>/dev/null || true)

    done

    echo "issue-logger: scan complete — ${scan_count} finding(s) from output files"
}

# ─── Exhaustive audit checklist ─────────────────────────────────────
# Call at the end of every session BEFORE issue_flush. Walks CC through
# factual yes/no questions. CC must answer each one; if yes, CC calls
# the appropriate issue_* function before moving to the next question.
#
# This is NOT interactive — it prints the checklist. CC reads it and
# files issues for every "yes" answer. The function itself doesn't
# prompt for input; CC is the agent that reads and acts.
#
# Usage:
#   issue_audit    # prints checklist, CC acts on each item
issue_audit() {
    cat <<'CHECKLIST'

═══ Issue Audit Checklist ═══

Answer each question. If YES, call the indicated function before
moving to the next question. If NO, move on. Do not skip any question.

1. Did any test take more than one attempt to pass?
   → YES: issue_flake "test: <name> needed N attempts" "<what happened>"

2. Did any file or directory structure differ from what you expected
   based on the codebase conventions or the task spec?
   → YES: issue_surprise "unexpected layout: <what>" "<expected vs actual>"

3. Did any default value, config constant, or magic number surprise you?
   → YES: issue_surprise "unexpected default: <what>" "<value and why it's odd>"

4. Did any function behave differently than its name or docstring implies?
   → YES: issue_surprise "misleading API: <func>" "<expected vs actual behaviour>"

5. Did you read a comment referencing something unimplemented, a TODO,
   a FIXME, a HACK, or a "see issue #N" for an issue that's closed?
   → YES: issue_cleanup "stale comment: <file:line>" "<what it says>"

6. Did you skip or defer anything for any reason (test, cleanup, fix)?
   → YES: issue_skip "skipped: <what>" "<why and what's needed to unblock>"

7. Did the build, lint, or vet surface anything you chose not to fix
   in this PR?
   → YES: issue_cleanup "unfixed lint: <what>" "<why deferred>"

8. Did any error message (from ventd code, not from tools) seem wrong,
   misleading, or missing context?
   → YES: issue_cleanup "bad error message: <where>" "<what's wrong with it>"

9. Did you notice a naming inconsistency (variable, function, file,
   package, test) compared to the rest of the codebase?
   → YES: issue_cleanup "naming inconsistency: <what>" "<convention vs actual>"

10. Did any test or code path have a race-condition risk you didn't
    address? (Even if -race didn't trigger it this run.)
    → YES: issue_bug "potential race: <where>" "<description>"

11. Did anything take significantly longer than expected (compile,
    test, CI)?
    → YES: issue_ci "slow <what>: <N>s" "<expected vs actual time>"

12. Did you encounter any behaviour that would break on a different
    distro, kernel version, or architecture (arm64, Alpine/musl)?
    → YES: issue_bug "portability: <what>" "<which platform and why>"

13. Did you think of a feature, API improvement, or refactor that
    would help but is out of scope?
    → YES: issue_wish "<idea>" "<brief rationale>"

14. Did any test fixture or testdata feel fragile, likely to break
    if the production code changes?
    → YES: issue_cleanup "fragile fixture: <what>" "<why it's brittle>"

15. Did you copy-paste code or logic that should be extracted into
    a shared helper?
    → YES: issue_cleanup "duplication: <what>" "<where it should live>"

═══════════════════════════════

Walk through every question. File or skip. Then call issue_flush.
CHECKLIST
}

# ─── Session summary ───────────────────────────────────────────────
# Call at the end of the session. Prints a summary and returns
# the count for use in PR comments.
issue_flush() {
    echo ""
    echo "═══ Issue Logger Summary (${_ISSUE_PR}) ═══"
    echo "Total: ${_ISSUE_COUNT} issue(s)"
    echo ""
    if [ ${#_ISSUE_FILED[@]} -gt 0 ]; then
        for entry in "${_ISSUE_FILED[@]}"; do
            echo "  • ${entry}"
        done
    else
        echo "  (no issues filed this session)"
    fi
    echo ""
    echo "Backlog: ${_ISSUE_BACKLOG}"
    echo "═══════════════════════════════════════════"
}

# ─── PR comment helper ─────────────────────────────────────────────
# Posts a single comment on the current PR linking all filed issues.
#   issue_comment_on_pr 130
issue_comment_on_pr() {
    local pr_num="${1:?usage: issue_comment_on_pr PR_NUMBER}"

    if [ ${#_ISSUE_FILED[@]} -eq 0 ]; then
        echo "issue-logger: no issues to comment about"
        return 0
    fi

    local comment="Issues filed from this PR:\n"
    for entry in "${_ISSUE_FILED[@]}"; do
        comment="${comment}\n- ${entry}"
    done

    if command -v gh &>/dev/null && gh auth status &>/dev/null 2>&1; then
        echo -e "$comment" | gh pr comment "$pr_num" --body-file - 2>/dev/null \
            && echo "issue-logger: commented on PR #${pr_num}" \
            || echo "issue-logger: failed to comment on PR #${pr_num}"
    else
        echo "issue-logger: no gh auth — comment not posted"
        echo -e "$comment"
    fi
}

# ─── Review helper (for Cowork) ────────────────────────────────────
# Pretty-prints the backlog. Not used by CC — used by Cowork to
# review what sessions filed.
issue_review() {
    local backlog="${1:-$_ISSUE_BACKLOG}"
    if [ ! -f "$backlog" ]; then
        echo "No backlog found at ${backlog}"
        return 0
    fi

    echo "═══ Issue Backlog Review ═══"
    echo ""

    local pending=0 filed=0 dup=0 failed=0
    while IFS= read -r line; do
        local status type title gh_issue
        status="$(echo "$line" | grep -o '"status":"[^"]*"' | cut -d'"' -f4)"
        type="$(echo "$line" | grep -o '"type":"[^"]*"' | cut -d'"' -f4)"
        title="$(echo "$line" | grep -o '"title":"[^"]*"' | cut -d'"' -f4)"
        gh_issue="$(echo "$line" | grep -o '"gh_issue":"[^"]*"' | cut -d'"' -f4)"
        ts="$(echo "$line" | grep -o '"ts":"[^"]*"' | cut -d'"' -f4)"

        case "$status" in
            filed)    filed=$((filed + 1));   printf "  + [%s] %s: %s -> %s\n" "$ts" "$type" "$title" "$gh_issue" ;;
            pending)  pending=$((pending + 1)); printf "  o [%s] %s: %s (NOT FILED)\n" "$ts" "$type" "$title" ;;
            duplicate) dup=$((dup + 1));      printf "  = [%s] %s: %s -> dup of %s\n" "$ts" "$type" "$title" "$gh_issue" ;;
            gh-failed) failed=$((failed + 1)); printf "  x [%s] %s: %s (GH FAILED)\n" "$ts" "$type" "$title" ;;
        esac
    done < "$backlog"

    echo ""
    echo "Filed: ${filed}  Pending: ${pending}  Duplicates: ${dup}  GH-failed: ${failed}"
    echo "═══════════════════════════"
}
