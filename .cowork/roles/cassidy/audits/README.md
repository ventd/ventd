# Cassidy per-directory audit checklists

Each file in this directory is a mechanical checklist Cassidy runs on PRs touching the named directory. Files named after the directory: `hal.md` → `internal/hal/`, `controller.md` → `internal/controller/`, etc.

Each checklist is priority-ordered — skim-pass in 2 minutes on low-budget sessions, full pass on deep audits.

## Files

| File | Covers | Cross-refs |
|---|---|---|
| `hal.md` | `internal/hal/` (interface, registry, all backends) | CAUGHT.md #4, #5, #6, #9 |
| `controller.md` | `internal/controller/` (hot loop, curve eval, clamps) | CAUGHT.md #1, #3 |
| `watchdog.md` | `internal/watchdog/` (restore, lifecycle invariants) | `.claude/rules/watchdog-safety.md` 7 rules |
| `calibrate.md` | `internal/calibrate/` (sweep + detect + abort) | hwmon-safety PWM=0 gate |

## Usage

When auditing a PR:

1. `pull_request_read method:get_files` to see which paths changed
2. For each safety-critical path in the list, open the matching checklist here
3. Run the always-run checks in priority order
4. If a check fails, grep TEMPLATES.md for a matching bug-class template and file via that

When the PR touches a path without a checklist (e.g. `internal/web/`, `internal/config/`), audit freeform. If the same class of bug shows up twice in that path, write a checklist for it on the second catch.

## Update discipline

- Add a check when a real bug surfaces that the existing checks wouldn't have caught
- Reorder priorities when catch-rate data says so (e.g. if check 4 catches nothing for 10 PRs, consider demoting)
- Don't grow these beyond ~10 always-run checks per file — longer lists don't get run

Cross-reference philosophy: CAUGHT.md for the pattern library (what-to-look-for), TEMPLATES.md for the issue bodies (how-to-file), audits/ for the procedural sequences (when-to-look-for-what).
