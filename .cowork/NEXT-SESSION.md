# Wake-up brief — 2026-04-18 S5 night

Read this first on wake. ~5 min to act, then S6 can start.

## What works

- `spawn-mcp` is fully operational end-to-end. Verified via `spawn_cc("smoke-test")` → `tail_session` → clean output with `rc=0`. Theme-picker deadlock is resolved.
- OAuth token seeded in `/etc/spawn-mcp/env` (`sk-ant-oat01-...`). Service log shows `oauth_token_set=True`.
- Deploy clean: cc-runner uid 986/976, CapEff 0, NoNewPrivs 1.
- MCP round-trip works: Cowork → claude.ai connector → phoenix-desktop spawn-mcp → tmux → `claude -p` → session log persisted.

## What doesn't work

- **cc-runner's gh PAT has no push access to `ventd/ventd`.** Authenticated as `PhoenixDnB`, token is `github_pat_11AYEKLWA0npT7sw5PfOMz_***`, but `git push` returns `403: Permission denied`. Confirmed via `cc-auth-diag` CC session.
- This blocks every real CC dispatch. Every PR-producing session would produce a local commit that can't reach GitHub.

## Action on wake (required before S6 starts)

Fix cc-runner's PAT. Two common causes for this 403:

**Cause A — PAT scoped to the wrong repositories:**
Fine-grained PATs specify which repos they can touch. This PAT probably lists only `PhoenixDnB/*` repos (or no repos) in its "Repository access" field, not `ventd/ventd`.

Fix: Mint a new fine-grained PAT at https://github.com/settings/tokens?type=beta with:
  - Resource owner: `ventd` (the org, not your personal account)
  - Repository access: select `ventd/ventd` (and `ventd/hardware-profiles` for Phase 5)
  - Permissions: `Contents: Read and write`, `Pull requests: Read and write`, `Issues: Read and write`, `Workflows: Read and write` (for P3, P10), `Metadata: Read`
  - Expiration: whatever you're comfortable with (90 days is common for bot accounts)

If `ventd` org doesn't show up as a resource owner, you need to accept org access for fine-grained PATs under org settings first.

Then on phoenix-desktop:
```
sudo -u cc-runner -i
echo <NEW_PAT> | gh auth login --with-token
gh auth status  # verify
exit
```

**Cause B — `PhoenixDnB` isn't a collaborator on `ventd/ventd`:**
If you're using a personal account but haven't added it to the ventd org. Fix: add yourself as org member with write access to the repo.

(Cause A is more likely since the PAT exists and authenticates fine.)

## Verify the fix

After rotating the PAT, dispatch the diagnostic again to prove:

    spawn_cc("cc-auth-diag")

Then `tail_session(...)`. Expect `PUSH_RESULT: PUSH_OK`.

## Then S6 can start

Queue is primed, 4 aliases ready, all with non-overlapping allowlists:
- `wd-safety` (T-WD-01, Opus 4.7)
- `permpol` (P10-PERMPOL-01, Sonnet 4.6)
- `T0-META-02` (T0-META-02, Sonnet 4.6)
- `t-hal-01` (T-HAL-01, Opus 4.7)

Dispatch all four concurrently via one Cowork turn. Phase 2–3 MAX_PARALLEL=4.

## Merged this session (S5)

- `#251` chore(spawn-mcp): collapse service user to cc-runner
- `#252` fix(spawn-mcp): print-mode + onboarding bypass + per-session logs
- cowork/state direct commits: `9f03d86` smoke-test prompt, `6fd43a2` cc-auth-diag prompt, `a75b9db` INDEX.md refresh, `bf05c83` THROUGHPUT.md tracker, `4b30b0d` LESSONS.md #9 + #10, `5ce57ab` ESCALATIONS.md

## S5 throughput

2 PRs merged + 5 cowork/state direct commits, in ~3h wall-clock = **0.67 PR/hr merged**. Below human baseline (5 PR/hr), below S4 (2.7 PR/hr). Cause: one full session consumed unblocking spawn-mcp from its #251 deploy gap. Neither PR advances the ventd roadmap. Infrastructure tax paid; S6 is where the parallel-dispatch architecture finally earns its keep — or gets cut.

## What I did with the rest of the night

Deep-dive on ventd's competitive position. Document at `.cowork/STRATEGY.md` (will be committed shortly). Lists every known fan control tool, where ventd already wins, where it loses, and the concrete technical wedges to dominate each.

## Outstanding local state on cc-runner

- Local commit `abea31b` on cc-runner's `/home/cc-runner/ventd` cowork/state branch has same content as my MCP commit `9f03d86`. Harmless. Next `git reset --hard origin/cowork/state` in cc-runner's worktree clears it if needed.
- `/tmp/spawn-mcp-smoke.md` exists (600, cc-runner). Can be deleted or left.
- `/var/log/spawn-mcp/sessions/cc-smoke-test-c9c497.log` exists — evidence.
- `/var/log/spawn-mcp/sessions/cc-cc-auth-diag-899bdb.log` exists — evidence.
