# Cowork prompts — index

One CC session per prompt. Cowork dispatches via `spawn_cc(alias)` through the spawn-mcp MCP server; spawn-mcp fetches `.cowork/prompts/<alias>.md` from this branch over HTTPS at invocation time. Prompt files are the canonical source; this INDEX is a human-readable map.

## Active (ready, not yet dispatched or mid-flight)

| alias | task | status | model | notes |
|---|---|---|---|---|
| `wd-safety` | T-WD-01 | ready | opus-4-7 | watchdog safety invariants (23% → 80% cov target) |
| `permpol` | P10-PERMPOL-01 | ready | sonnet-4-6 | Permissions-Policy header + ETag on embedded UI |
| `T0-META-02` | T0-META-02 | ready | sonnet-4-6 | regression-replay lint (every closed bug issue has a replay test) |
| `t-hal-01` | T-HAL-01 | ready | opus-4-7 | HAL contract invariants, bound to new .claude/rules/hal-contract.md |

## Diagnostic / utility

| alias | purpose |
|---|---|
| `smoke-test` | minimal "output one line and exit" — proves spawn-mcp → tmux → claude -p wiring |
| `cc-auth-diag` | checks cc-runner's gh auth account + ventd repo permission + push ability (one-shot diagnostic, not real work) |

## Historical (merged; kept for reference)

| alias | task | merged |
|---|---|---|
| `unblock` | combined 4-part Phase 1 unblock | ✓ |
| `unblock-partD` | events.jsonl + coworkstatus + workflow | ✓ as #248 |
| `fp` | P1-FP-01 | ✓ as #246 |
| `hal` | P1-HAL-01 | ✓ as #247 |
| `rulelint` | T0-META-01 | ✓ as #244 |
| `P1-FP-01` (duplicate of `fp`) | — | ✓ |
| `P1-HAL-01` (duplicate of `hal`) | — | ✓ |
| `P10-PERMPOL-01` (duplicate of `permpol`) | — | same file, two names |
| `T0-INFRA-03` + `T0-INFRA-03-revise` | faketime fixture | ✓ as #245 |
| `T0-META-01` | T0-META-01 | ✓ as #244 |

## Spinning up

Cowork calls `spawn_cc("<alias>")` via MCP. The server returns a session name; Cowork polls `tail_session("<session>", N)` to track progress. The session log persists at `/var/log/spawn-mcp/sessions/<session>.log` on phoenix-desktop.

To attach interactively (on phoenix-desktop):

    sudo -u cc-runner tmux attach -t cc-<alias>-<shortid>

## Model assignment policy

- **Opus 4.7** — HAL, FP (except trivial data edits), IPMI, LIQUID, MPC, ACOUSTIC, UEFI, EXPR, MAC, WIN, FLEET, anything touching controller/watchdog/calibration safety, multi-part tasks crossing safety boundaries.
- **Sonnet 4.6** — HOT, MOD, UDEV, METRICS, HISTORY, SBOM, SIGN, REPRO, PERMPOL, I18N, T0-META-*, T0-INFRA-*, regression-replay, fuzz-corpus, TX-* evergreen, data-only profile edits.
- **Haiku 4.5** — P0-01/02/03, pure-docs, fixture skeletons, trivially bounded edits.

If a Sonnet/Haiku task fails the same rule twice, Cowork bumps the model automatically on dispatch-3 (not an escalation).
