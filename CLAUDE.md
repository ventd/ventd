# Claude Code Instructions for ventd

This repository is orchestrated by "Cowork" — a Claude instance that
drafts tasks for you to execute. Cowork lives in the conversation
layer; you live in the terminal. Your job is to run the task
Cowork hands you, follow the reporting contract, and not wander
off.

## Task activation

If the user pastes one of the following patterns, fetch the Cowork
prompt from the `cowork/state` branch and execute it exactly as
written:

- `<alias>` — a short name like `hal`, `fp`, `rulelint`. Current
  aliases live in `.cowork/aliases.yaml` on `cowork/state`.
- `<task-id>` — the full ID, e.g. `P1-HAL-01`, `T0-META-01`.
- `run <alias-or-id>` or `cowork <alias-or-id>` — same, with a
  verb.

Resolve like this:

```bash
git fetch origin cowork/state
# If the user gave an alias, look it up first:
git show origin/cowork/state:.cowork/aliases.yaml
# Then load the prompt for the resolved task-id:
git show origin/cowork/state:.cowork/prompts/<TASK-ID>.md
```

Read the prompt file in full. Everything you need — branch name,
allowlist, model assignment, reporting contract, definition of done
— is inside it. Do not deviate from any of those.

## Ground rules for every Cowork task

- Use the prompt's specified branch name exactly.
- Touch only files in the prompt's allowlist. If you must deviate,
  justify it inline in the PR body — Cowork's review will otherwise
  flag it as an R4 failure.
- Obey the prompt's model assignment. If you are not running the
  model the prompt asks for, stop and flag it to the user.
- Conventional commits for all commit messages.
- `CGO_ENABLED=0` unless the task explicitly says otherwise.
- Open a **draft PR** when done. Fill out the full reporting
  contract in the PR body (STATUS, BEFORE-SHA, AFTER-SHA, build /
  test / gofmt outputs, and everything else the prompt lists).
- Do not modify `.claude/rules/*.md` unless the task explicitly
  targets a rule file.
- Do not modify `.cowork/**` from a task branch — that's Cowork's
  territory.
- Do not touch test files outside the scope the task declares.

## If resolution fails

If the alias or ID does not exist on `cowork/state`, stop and ask
the user for clarification. Do not guess; do not pick a similar
name.

## Escalation

If you hit a blocker — plan ambiguity, missing capability, hardware
that isn't reachable — push your work-in-progress on the task
branch, open a draft PR with `[BLOCKED]` in the title prefix and a
"Blocker" section at the top of the body, and stop. Cowork picks
it up from there.

## Reporting contract template

Every PR body Cowork reviews must include:

- `STATUS`: `done` / `partial` / `blocked`
- `BEFORE-SHA` / `AFTER-SHA`: branch tip before and after your work
- `POST-PUSH GIT LOG`: `git log --oneline main..HEAD`
- `BUILD`: full output of `go build ./...`
- `TEST`: full output of `go test -race ./...` (or the narrower
  scope the task specifies)
- `GOFMT`: output of `gofmt -l <paths>` — must be empty
- Any additional task-specific outputs named in the prompt
- `CONCERNS`: second-guessing you had while working
- `FOLLOWUPS`: work you noticed that wasn't in scope

Truncated output means the review will ask for a rerun. Paste
literal stdout / stderr, not summaries.
