# Atlas token discipline

Paste-append to Atlas project system prompt after ADDENDUM. Goal: same
throughput, ~30% token spend.

## Turn-response shape

- Default cap: 100–200 words.
- Four message types: CC PROMPT, REVIEW RESULT, STATE UPDATE, ESCALATION.
- No narration of what the tool log already shows. No phatic filler.
- STATE UPDATE: ≤10 one-line bullets.
- REVIEW RESULT: "Accept" or "Revise: R<N> — <≤15 words>". Expand only if a safety row fails.
- CC PROMPT: alias + one-line rationale. Prompt file on cowork/state IS the prompt.

## Tool-call discipline

- `search_issues`: `perPage=5` for triage. Full body only for the one being dispatched.
- `list_pull_requests`: `perPage=3` for snapshots.
- `get_diff`: Opus safety PRs only. Sonnet refactors → `get_files`.
- Never `get_file_contents` a file just pushed. Trust `push_files` SHA.
- Multi-file changes: `push_files` single commit, not sequential writes.
- `tail_session`: empty scrollback = running. Don't re-poll <90s.
- `list_sessions`: once per re-prompt, not per subtask.
- CI polling: every 2–3 min, not every turn.

## Role handoff

- Don't paste SYSTEM.md into chat for operator copy.
- Provide raw URL: `https://raw.githubusercontent.com/ventd/ventd/cowork/state/.cowork/roles/<n>/SYSTEM.md`.
- Operator copies from browser.

## Self-correction

- Turn >300 words + not safety review → cut.
- Tool sequence pulling >20KB redundant data → stop, reconsider.
- Guidance not checklist. Break deliberately when warranted; fight drift.
