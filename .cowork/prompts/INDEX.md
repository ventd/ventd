# Cowork prompts — index

One CC session per prompt. Paste the alias (left column) into a fresh Claude Code terminal; the alias resolver in `/CLAUDE.md` fetches the corresponding file from `.cowork/prompts/<alias>.md` on `cowork/state` and executes it verbatim.

| alias | task | status | model | notes |
|---|---|---|---|---|
| `unblock` | merge #247+#246, fix lint on #244+#245, install events.jsonl | ready | opus-4-7 | combined 4-part; CURRENT |
| `permpol` | P10-PERMPOL-01 | ready | sonnet-4-6 | Permissions-Policy header |
| `regresslint` | T0-META-02 | blocked on #244 merge | sonnet-4-6 | |
| `wd-safety` | T-WD-01 | ready after HAL | opus-4-7 | watchdog at 23% cov |
| `runner-smoke` | add self-hosted runner workflow | ready | sonnet-4-6 | verify phoenix-desktop routing |
| `hal` | P1-HAL-01 | MERGED | — | historical, for reference |
| `fp` | P1-FP-01 | MERGING | — | historical |
| `rulelint` | T0-META-01 | MERGING | — | historical |
| `faketime` | T0-INFRA-03 | MERGING | — | historical |

## Spinning up

1. Open a fresh CC terminal (web CC preferred).
2. Paste the alias. Example: `unblock`.
3. Wait for CC's `SUMMARY` at the end.
4. Say `done` in this Cowork thread and Cowork will move to the next queue item.

## Model assignment policy

- **Opus 4.7** — HAL, FP (except trivial data edits), IPMI, LIQUID, MPC, ACOUSTIC, UEFI, EXPR, MAC, WIN, FLEET, anything touching controller/watchdog/calibration safety, multi-part tasks crossing safety boundaries.
- **Sonnet 4.6** — HOT, MOD, UDEV, METRICS, HISTORY, SBOM, SIGN, REPRO, PERMPOL, I18N, T0-META-*, T0-INFRA-*, regression-replay, fuzz-corpus, TX-* evergreen, data-only profile edits.
- **Haiku 4.5** — P0-01/02/03, pure-docs, fixture skeletons, trivially bounded edits.

If a Sonnet/Haiku task fails the same rule twice, Cowork bumps the model automatically on dispatch-3 (not an escalation).
