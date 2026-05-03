# Attribution & Commit Identity

This project ships under a single human author. No AI attribution anywhere in the repo, in commits, or on the remote.

## Git identity

- All commits must be authored as `phoenixdnb` with the email associated with that GitHub account. Check `git config user.name` and `git config user.email` before the first commit in a session. If they don't match `phoenixdnb`, fix them with `git config user.name` / `git config user.email` (repo-scoped, not global) before committing.
- Never change the global git config.
- Never push with `--force` to `main`.
- Never skip hooks (`--no-verify`, `--no-gpg-sign`).

## Commit messages

- No `Co-Authored-By: Claude ...` trailer. No `Co-Authored-By` trailers of any kind unless a real human collaborator is named.
- No `🤖 Generated with [Claude Code]` footer. No mentions of Claude, Anthropic, Claude Code, AI, LLM, agent, assistant, copilot, or any similar term in commit subjects, bodies, or trailers.
- Write messages in the voice of the human author: first person or imperative, project-focused, no meta-commentary about how the change was produced.

## Pull requests, issues, and repo content

- PR titles, descriptions, and comments: no AI attribution footer, no "Generated with" line, no Claude branding, no emoji signature.
- README, docs, code comments, release notes: same rule. The project does not reference how it was built, only what it does.
- Do not add `CLAUDE.md`, `AGENTS.md`, or similar AI-oriented top-level files to the repo. Internal guidance files live under `.claude/rules/` which is gitignored where appropriate.

## Remote

- Remote is `github.com/ventd/ventd`. Never push to any other remote without explicit instruction in the chat.
- Before pushing, confirm `git remote -v` shows only the expected GitHub remote.

## Enforcement

- If a commit is about to be created with the wrong author or a forbidden trailer, stop and fix the identity or message before running `git commit`. Creating the commit and then amending is acceptable but not preferred — get it right the first time.
- If a prior commit in the current branch contains forbidden attribution, flag it to the human before pushing. Do not rewrite history without explicit instruction.
- `.github/workflows/no-ai-attribution.yml` is the hard CI gate. It scans commit messages, the PR body, and changed file content (excluding `.claude/rules/` and the workflow itself) for the line-anchored `Co-Authored-By:` trailer, the canonical `🤖 Generated with [Claude` footer, and `claude.com/claude-code`. The gate fires on every PR; a hit blocks merge. This catches anything that slips past local discipline.
