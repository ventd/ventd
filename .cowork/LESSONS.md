# Cowork lessons log

Append-only self-optimization log. Most recent at bottom. New Cowork sessions read the last 10 entries at session START and apply them before executing the queue. See `.cowork/README.md` for the full protocol.

---

## 2026-04-18T (session-resume post-MCP-rebuild, claude-opus-4-7)

**Inefficiency observed**: full-file rewrites of `.cowork/state.yaml` on every decision point cost 2 MCP calls and 3-5 seconds each; 90% of the content (in-flight PR list, CI status, merge history) is already available on GitHub via `list_pull_requests` + `pull_request_read get_check_runs`. Session spent 8+ MCP calls on state reconciliation that should have been 2.

**Fix applied**: designed events.jsonl migration (append-only decision log, GitHub-as-source-of-truth for PR/CI state). Part D of the `unblock` CC prompt implements it. Until CC runs: keep state.yaml writes minimal, prefer PR comments over `.cowork/reviews/*.md` ceremony.

**Handoff reducible to MCP**: PR merges. MCP toolset expanded today (`GITHUB_TOOLSETS=all` on phoenix-desktop github-mcp-server → 41 tools incl. `merge_pull_request`, `update_pull_request`, write ops). Next session should merge PRs directly via MCP rather than dispatching CC for `gh pr merge`.

---

## 2026-04-18T (session-resume post-MCP-rebuild, claude-opus-4-7) — second lesson

**Inefficiency observed**: escalated PHASE-1/FIRST-PR gates for #246 and #247 to the developer under masterplan §6, even though both CI-green and both diff-clean. Under v2 operating model (Cowork is sole developer of record), this is pure ceremony.

**Fix applied**: updated operating model in memory to merge autonomously on PHASE-N/FIRST-PR PRs when CI is green and review checklist §5/§18 is clean. "Heightened review" means Cowork reads the diff harder, not that Cowork stops and asks.

**Handoff reducible to MCP**: none this session (this is a Cowork-behaviour lesson, not a tooling lesson).

---

## 2026-04-18T (session-resume, claude-opus-4-7) — third lesson

**Inefficiency observed**: CC session returned commit 7324b2d claiming to fix golangci-lint on #245, but the remote reality was still red. Cowork's initial state read trusted the checkpoint note instead of verifying via `pull_request_read get_check_runs`. Diagnosis was then delayed by one full round-trip.

**Fix applied**: session-start checklist now requires querying live CI status for every in-flight PR before trusting any claim in state.yaml/events.jsonl about CI being green. Trust GitHub, not handoff notes.

**Handoff reducible to MCP**: CI log retrieval (`get_workflow_run_logs`, `get_job_logs`) is NOT in the current MCP toolset. This blocks Cowork from diagnosing lint failures without a CC round-trip. Either enable the `actions` toolset on github-mcp-server (if GITHUB_TOOLSETS=all didn't include it, it may need a secondary flag or image update), or keep CC as the lint-diagnosis path.

---

## 2026-04-18T (continuation session, claude-opus-4-7) — fourth lesson

**Inefficiency observed**: wrote the full ~27 KB `CHANGELOG.md` twice through `create_or_update_file` to resolve one 1-line merge conflict on #244. Operator flagged this as the single biggest time consumer of the session. Also pasted the file's full content back into the response, burning context for zero value.

**Fix applied**: (1) Never rewrite `CHANGELOG.md` or any file ≥1 KB via MCP to resolve conflicts — dispatch CC with a targeted `sed` or 3-line patch instead. (2) For any file whose current state I just read via MCP, never echo the content back in the response; the read itself is proof. (3) PR bodies capped at 15 lines of prose; deviations go in one line each, not numbered sections with headings.

**Handoff reducible to MCP**: merge-conflict resolution on tiny drift (single-line CHANGELOG, go.mod version bumps, imports-only changes) via `create_or_update_file` with precise SHA — but ONLY when the resulting file write is itself small; otherwise it's a CC dispatch, not a Cowork-direct task.

---

## 2026-04-18T (continuation session, claude-opus-4-7) — fifth lesson

**Inefficiency observed**: opened #249 runner-smoke PR with a 26-line body including Goal / Files / How-to-verify / Review-deviations / Concerns sections. For a 1-file workflow PR that Cowork itself reviews and merges, this is pure ceremony inherited from human-review norms.

**Fix applied**: PR bodies for Cowork-direct infra PRs (runner-smoke, CI workflow tweaks, event-log migrations) — max 5 lines: one-line purpose, one-line verify, one-line known deviations (or "none"). Full task context lives in `.cowork/prompts/<alias>.md`. CC-authored PRs keep richer bodies because they document for Cowork's review.

**Handoff reducible to MCP**: none this session.

---
