---
name: commit-via-file
description: |
  Use when committing a message that contains shell-meta characters
  (parentheses, backticks, dollars, exclamation marks, smart quotes) — i.e.
  whenever the body would be at risk under heredoc / -m. Writes the commit
  body to a tmp file and runs `git commit -F`. Optionally scps to a remote
  dev VM and runs the commit there. Do NOT use for: simple one-line
  commits where -m is safe; pushing; tagging; amending.
disable-model-invocation: true
argument-hint: [optional remote host alias]
allowed-tools: Bash(git add *) Bash(git status *) Bash(git diff *) Bash(git commit -F *) Bash(scp *) Bash(ssh *) Write Read
---

# commit-via-file

User-invoked. Drafts the commit body, persists it to a tmp file, then runs
`git commit -F <file>` (locally or on a named remote dev VM). Eliminates the
heredoc-with-parens / heredoc-with-backticks failure class.

## When to use

- Commit body contains: `()`, `\`\`\``, `$()`, `$VAR`, `!`, `"smart" quotes`, em-dashes,
  multiline backticked code blocks, or any nested quoting.
- Remote target is a dev VM where `ssh remote 'git commit -m "..."'` would
  be at risk of double-shell expansion.

## When NOT to use

- One-liner commits with no shell metacharacters → use `-m` directly.
- Pushing, amending, tagging, force-pushing — out of scope.

## Procedure

1. Draft the commit body in a buffer (Conventional Commits, per
   `.claude/skills/conventional-commit/SKILL.md` for the message itself).
2. Write the body to `/tmp/ventd-commit-msg.txt` via the Write tool.
   - Keep filename stable so a re-run overwrites instead of accumulating.
   - Never put the body in a Bash heredoc — that's the failure mode this
     skill avoids.
3. Stage explicitly with named files (no `git add -A`).
4. Commit:
   - **Local:** `git commit -F /tmp/ventd-commit-msg.txt`
   - **Remote dev VM:** `scp /tmp/ventd-commit-msg.txt $REMOTE:/tmp/`
     then `ssh $REMOTE 'cd <repo> && git commit -F /tmp/ventd-commit-msg.txt'`
5. Verify with `git log -1 --format=fuller` (local) or
   `ssh $REMOTE 'git log -1 --format=fuller'` (remote).

## Failure modes this avoids

- Backticks inside a heredoc → command substitution fires inside the
  intended message, garbage commit recorded.
- Single-quoted SSH command with a `'` inside the body → quoting balance
  breaks, ssh exits with parser error.
- `!` in interactive bash → history expansion mangles the message.
- Smart quotes (curly `"..."`) — survive a file write but break heredoc
  detection on some shells.

## Cleanup

- The tmp file is left in place after the commit. Re-running overwrites.
- If multiple commits are about to happen in one session, suffix with the
  scope: `/tmp/ventd-commit-msg-<scope>.txt`.

## Constraints

- Never bypass hooks (`--no-verify`).
- Never commit to `main` directly (per collaboration.md).
- Author identity is `phoenixdnb` — verify before commit.
- No AI-attribution trailers (per attribution.md).
