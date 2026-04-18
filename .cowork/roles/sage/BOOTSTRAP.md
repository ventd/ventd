# Sage bootstrap — first-session briefing

Generated 2026-04-18 by Atlas. Accurate as of ~12:50 UTC; verify at session start.

## Current dispatch queue

~18 open `role:atlas` issues at the time of role creation. Atlas will triage and label with `role:sage` the ones ready for prompt-writing. When you start, if `role:sage` queue is empty, Atlas hasn't handed off yet — read the `role:atlas` queue and propose a batch Atlas should transfer to you.

### Prioritized backlog (Atlas's current read)

**Opus cluster (serial; all touch `internal/controller/controller.go`):**
- #288 controller perm-err semantics regression from #247 (SAFETY)
- #272 manual-mode retry/RestoreOne follow-up to #263
- #298 curveSig + maxRPM cache hardening
- #305 usbbase Handle.mu serialisation

**Sonnet cluster (parallelizable across distinct lanes):**
- #311 `mergeModuleLoadFile` fsync before rename
- #306 crosec Write-failure-counter log spam
- #268 cleanup/dead-code (hwmon pruning)
- #316 halhwmon/halasahi dedup on Apple Silicon
- #304 regresslint annotations — already in-flight as `fix-304-regresslint-annotations`

**Serial umbrella (own session):**
- #296 mutateConfig umbrella — wide-blast-radius refactor across `internal/web/`

**Medium complexity:**
- #307 IPMI Restore cc + Supermicro zone + response bounds
- #308 hwdb verified-first match precedence
- #312 tlsSniff peek blocks Accept + redirect XSS
- #313 hwmon-safety.md rule bindings
- #317 settings.json hardening (gh api bypass + shell redirects)
- #318 hwdb CLI-only refresh gap

**Escalation (first-of-kind rule file):**
- #235 calibration-safety.md (T-CAL-01) — first-precedent set by T-HAL-01 merged as #258

## Reference prompts to read before writing

On `cowork/state`, read these for shape:

- `.cowork/prompts/fix-293-sensor-fan-collision.md` — clean Sonnet config-validation template.
- `.cowork/prompts/fix-304-regresslint-annotations.md` — Sonnet test-only annotation sweep.
- `.cowork/prompts/rebase-315.md` — one-shot rebase/conflict prompt.
- `.cowork/prompts/P4-PI-01-v2.md` — richest Opus safety-critical template. Read this to understand what a thorough prompt looks like; don't match its length for simpler fixes.
- `.cowork/prompts/merge-314.md` — minimal one-shot (`gh pr ready N`) for reference on how terse a prompt can legitimately be.

## Active in-flight CC work (don't write prompts that would conflict)

At bootstrap time:
- **PR #315** (P4-PI-01 PI curve) — CI running on rebased head `43eed4fb`. Touches `internal/controller/controller.go` + `internal/config/config.go`. Any new prompt touching those files should wait until #315 merges.
- **Session `cc-fix-304-regresslint-annotations-...`** — test-file annotations for 7 closed bugs. Touches `_test.go` files only.

## What I want from your first session

1. Read SYSTEM.md + this BOOTSTRAP.md + LESSONS.md top 5 + the 5 reference prompts listed above.
2. Write prompts for **up to 3 Sonnet-class items** from the queue. Recommended first batch:
   - #311 (`mergeModuleLoadFile` fsync) — small, self-contained, `internal/hwmon/autoload.go` only.
   - #306 (crosec log spam) — small, self-contained, `internal/hal/crosec/crosec.go` only.
   - #313 (hwmon-safety.md rule bindings) — more nuanced, requires understanding which tests cover which invariants; include explicit instruction that CC must NOT invent bindings for missing tests (flag unresolved instead).
3. Push the 3 prompt files to `.cowork/prompts/<alias>.md` on `cowork/state` via one `push_files` commit.
4. File one `role:atlas` summary issue: `prompts ready: fix-311-fsync, fix-306-crosec-spam, fix-313-hwmon-rule-bindings` with the model-recommendation table.
5. Write your worklog entry.

**Do NOT write more than 3 prompts in your first session.** Atlas reviews the first batch and gives you feedback before you scale up.

**Do NOT write prompts for the Opus cluster** (#288, #272, #298, #305) until Atlas signals go — those need controller.go access which is currently blocked by #315.

## Commit discipline

Follow `.cowork/roles/atlas/TOKEN-DISCIPLINE.md` principles. Specifically:
- One `push_files` commit for the batch of prompt files, not sequential `create_or_update_file` calls.
- Don't re-read a prompt file you just pushed to verify — trust the SHA.
- Don't paste prompt content back into chat unless the operator asks.

## Who to ask if stuck

- **Ambiguous issue body** — file `role:cassidy` with one concrete question. Do NOT write a speculative prompt.
- **Uncertain allowlist** — file `role:atlas` asking for clarification. Do NOT guess.
- **Conflicting in-flight work** — skip the item; flag in your summary issue under "deferred pending <reason>".
