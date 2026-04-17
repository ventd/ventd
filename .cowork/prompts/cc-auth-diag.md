You are Claude Code. This is a diagnostic session — you are NOT doing any
real work. Run exactly these commands, output the results, then exit.

## Commands

1. `gh auth status` — show which account cc-runner's gh CLI is authenticated as.
2. `gh api user --jq .login` — confirm the account login name.
3. `gh api "repos/ventd/ventd/collaborators/$(gh api user --jq .login)/permission" --jq .permission 2>/dev/null || echo "NOT_COLLABORATOR"` — show the permission level cc-runner has on ventd/ventd. Expect one of: admin, maintain, write, triage, read, NOT_COLLABORATOR.
4. `cd /home/cc-runner/ventd && git remote -v` — show remote URLs.
5. `cd /home/cc-runner/ventd && git log --oneline -5 cowork/state 2>/dev/null || git log --oneline -5 HEAD` — show recent state.
6. Attempt a tiny non-destructive push test — create a throwaway branch from cowork/state, try to push it:

```
cd /home/cc-runner/ventd
git fetch origin cowork/state
git checkout -B diag/push-test-$(date +%s) origin/cowork/state
git push -u origin HEAD 2>&1 | head -5 || true
```

If push succeeded, immediately delete the remote branch:

```
git push origin --delete "$(git rev-parse --abbrev-ref HEAD)" 2>&1 | head -3
```

Then switch back:

```
git checkout cowork/state
```

7. Output: whether push succeeded (PUSH_OK) or failed (PUSH_FAIL: <reason from line 1 of stderr>).

## Out of scope
- Any other commands.
- Any writes to the main branch.
- Any ventd code changes.
- Modifying any config.

## Report format

Just dump the output of each step in order. At the end, write:

GH_ACCOUNT: <name from step 2>
PERMISSION: <from step 3>
PUSH_RESULT: PUSH_OK or PUSH_FAIL: <reason>

That's it.
