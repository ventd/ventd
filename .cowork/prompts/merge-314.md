# merge-314 \u2014 flip #314 draft\u2192ready and merge

You are Claude Code. One-shot task: flip PR #314 (refactor(testfixture): extract shared Base struct) from draft to ready-for-review so Atlas can auto-merge via MCP.

## Task

```bash
cd /home/cc-runner/ventd
gh pr ready 314
```

That's it. No code changes, no commits, no pushes. Just the one gh command.

## Definition of done

- `gh pr view 314 --json isDraft` returns `{"isDraft": false}`.

## Reporting

- STATUS: done | blocked
- Output of `gh pr view 314 --json isDraft,state` after the flip.
- If `gh pr ready 314` fails, report the error and STOP \u2014 do not retry.

## Constraints

- Do NOT merge the PR. Atlas merges via MCP.
- Do NOT touch any file.
- Do NOT create any branch.
- The ONLY permitted command is `gh pr ready 314`.
