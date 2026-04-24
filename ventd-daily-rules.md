# ventd — Daily Rules

## Before you type `claude`
- **Plan in claude.ai Opus chat** (flat-rate). **Implement in Claude Code** (per-token).
- **One spec = one session.** Close before switching topics.
- Open `ccwatch` in a second pane.

## Model discipline
- **Haiku** — tests, commits, lint fixes, mechanical edits
- **Sonnet** — implementation
- **Opus** — claude.ai chat ONLY. Never in CC.

## Session hygiene (memorize)
- Shift+Tab ×2 → **plan mode first**, always.
- `/clear` between unrelated tasks. Stale context = tokens every turn.
- `/compact focus on <topic>` only when warm (<5 min idle). Else `/clear`.
- **Never** edit CLAUDE.md mid-session.
- **Never** add/remove MCPs mid-session.
- **Never** swap models mid-session.
- End of session: dump state to `.claude/sessions/YYYY-MM-DD-<topic>.md`, then `/clear`.

## Budget reality
- **Target:** $10–30 per spec, $300/mo total.
- Any spec > $15 → post-mortem. Cause is almost always one of the four "nevers" above.
- Cache-read ratio > 90% or something is wrong.

## Red flag words — if a tool says any of these, DO NOT INSTALL
- **parallel · swarm · hive · orchestrator · agent team · fleet · farm**
- If you find yourself thinking "let's just spawn a few agents to..." — STOP.
- Hook already blocks >3 subagent spawns/session. If you hit that, the plan is wrong.

## Code discipline (ventd)
- CGO_ENABLED=0. Purego only.
- slog, not stdlib log.
- Every goroutine bound to a context.
- Wrap errors with `%w` + context.
- No panics in control loop — degrade to PWM=255.
- Hermetic tests. No real `/sys` in unit tests.
- Linear history. Conventional Commits. 1:1 rule↔subtest.

## When stuck
- Ask in claude.ai Opus chat. Don't spend Sonnet tokens flailing.
- Write a spec first. Implement second.
- If unsure whether to build or buy — write it yourself before installing an MCP.
## Manual file placement — check for duplicates first

Before placing any file manually in the repo (browser download, clipboard paste, scp, cp from another path): run `git ls-files <basename>` to check for an existing tracked copy at a different path.

If one exists, reconcile locations **before** adding new content. Place files at the canonical location the first time.

Origin: 2026-04-25. Two `TESTING.md` files ended up tracked (repo root + `docs/`) because a seed PR and a manual drop-in happened in different sessions and neither checked. Required a separate consolidation PR to fix.

Example:
```bash
# Before dropping a downloaded file in:
git ls-files TESTING.md           # checks root
git ls-files '*TESTING.md'        # checks all paths
# If output shows an existing tracked path, overwrite THAT file, not a new location.
```
## Weekly
- `npx ccusage@latest daily --breakdown`
- Any skill/MCP unused in 30 days → remove it.

---
**The $600 lesson:** deterministic tools (hooks, CLI, LSP) cost zero tokens.
LLM tools cost tokens every turn. Pick deterministic whenever possible.

