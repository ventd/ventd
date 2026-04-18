You are Claude Code. Investigating why a prior `cc-t-hal-01-a8c371` session died with no PR produced.

## Context

Session `cc-t-hal-01-a8c371` was spawned ~15 minutes ago via spawn-mcp with the `t-hal-01` alias (T-HAL-01 — HAL contract invariants). Its tmux session is no longer listed. No PR appeared on ventd/ventd.

The other three dispatched sessions all produced PRs (#253, #254, #255). Only this one failed silently.

## Steps

1. Check the persistent session log:
   ```
   sudo cat /var/log/spawn-mcp/sessions/cc-t-hal-01-a8c371.log
   ```
   Copy-paste the FULL contents (or last 300 lines if it's huge).

2. Report what you find:
   - Was there an auth error?
   - Did `claude -p` exit with rc != 0?
   - Was there a Go compile error during the work?
   - Was there a git push failure (old PAT cached)?
   - Was there a prompt interpretation problem?

3. Don't attempt to fix it yet. Just report the log contents + your hypothesis about the failure mode.

## Out of scope
- Don't re-run the task.
- Don't touch any ventd code.
- Don't modify spawn-mcp.
