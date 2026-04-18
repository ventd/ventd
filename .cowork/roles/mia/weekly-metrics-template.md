# Weekly metrics template

Copy-paste into `.cowork/roles/mia/worklog.md` as a new worklog entry each Monday. Fill fresh numbers from live queries. Replace every `[FILL]` placeholder before committing.

Source queries for each number are in a comment block beneath the template. Commit the filled version with a commit message like `mia/worklog: weekly metrics rollup <ISO-week>`.

---

```
## [YYYY-MM-DD] Weekly metrics rollup — <ISO-week-identifier>

**Reporting period**: [YYYY-MM-DD] to [YYYY-MM-DD] (7 days)

### Throughput

- **Issues closed this week**: [FILL] (of which [FILL] as `completed`, [FILL] as `duplicate`, [FILL] as `not_planned`)
- **Issues filed this week**: [FILL]
- **Net backlog delta**: [FILL] (open this Monday - open last Monday)
- **Issues commented this week**: [FILL]
- **Labels applied this week**: [FILL]

### Quality

- **Stale-issue ratio**: [FILL-A] / [FILL-B] = [FILL-%] (open issues >30 days idle / total open). **Target: <15%.**
- **Regresslint compliance**: [FILL] closed-bug issues with neither a `TestRegression_Issue<N>_*` test nor a `no-regression-test` exemption. **Target: 0.**
- **Milestone hygiene**: [FILL] open milestones with closed PRs still attached. **Target: 0.**

### Queue depth (end-of-week)

- `role:atlas`: [FILL] open
- `role:cassidy`: [FILL] open
- `role:mia`: [FILL] open

### Notable

- [One-liner per week of anything notable — release tag landed, milestone closed, unusual pattern in the backlog, cross-role friction point. Delete if nothing.]

### Followups moved to next week

- [One-liner per followup that rolled forward. Delete if none.]
```

---

## Source queries

Run these against the `cowork/state` branch and the GitHub API to get the numbers.

### Issues closed this week

```
search_issues(query="is:issue is:closed closed:>=YYYY-MM-DD", owner="ventd", repo="ventd", perPage=100)
```

Where `closed:>=YYYY-MM-DD` is the start of the reporting period (Monday a week ago). Count results. Group by `state_reason`:

- `completed` → regular closures.
- `duplicate` → dedup work.
- `not_planned` → scrub closures or declined features.

### Issues filed this week

```
search_issues(query="is:issue created:>=YYYY-MM-DD", owner="ventd", repo="ventd", perPage=100)
```

Count results. Subtract any that Mia filed and later closed as self-duplicates (those don't count as net new).

### Net backlog delta

```
search_issues(query="is:issue is:open", owner="ventd", repo="ventd", perPage=1)
```

The `total_count` is this Monday's open-issue count. Compare against last Monday's recorded count in the prior weekly rollup.

### Issues commented this week

Count comments I made this week. If Mia is consistently about one comment-per-close, approximate as `issues closed × 1.2`. If a more precise number is needed, this would need a per-issue read of comment timestamps — skip unless pattern drift is suspected.

### Labels applied this week

Count from my own worklog entries. Sum the "Labels applied" lines in all session entries within the reporting period.

### Stale-issue ratio

```
search_issues(query="is:issue is:open updated:<YYYY-MM-DD", owner="ventd", repo="ventd", perPage=100)
```

Where `YYYY-MM-DD` is 30 days before this Monday. `total_count` is the numerator (stale issues). Denominator is the total-open count from the backlog-delta query above.

### Regresslint compliance

```
search_issues(query="is:issue is:closed label:bug -label:no-regression-test", owner="ventd", repo="ventd", perPage=100)
```

`total_count` is closed bugs with no exemption. Against the actual regresslint tool behaviour, the real compliance count is this minus any that have a `TestRegression_Issue<N>_*` function or `t.Run("Issue<N>_` subtest in `internal/` or `cmd/`. Until lesson #290 (magic-comment binding) lands, there are approximately zero matching functions, so the exempted-only count is effectively the compliance count. Verify by running `tools/regresslint` locally if uncertain.

### Milestone hygiene

```
list_milestones(state="open", owner="ventd", repo="ventd")
```

For each open milestone, list the PRs referencing it. If any referenced PR is closed/merged but the milestone is still open with unclosed issues, the milestone is in violation. Number of violations = milestone hygiene metric.

### Queue depth

```
search_issues(query="is:issue is:open label:role:atlas", owner="ventd", repo="ventd", perPage=1)
search_issues(query="is:issue is:open label:role:cassidy", owner="ventd", repo="ventd", perPage=1)
search_issues(query="is:issue is:open label:role:mia", owner="ventd", repo="ventd", perPage=1)
```

`total_count` from each.

---

## When to use this template

- **Weekly**: every Monday at session start, before any triage work. If Monday is missed, the rollup still applies but note "Delayed [N] days" in the header.
- **First weekly rollup**: 2026-04-20 (first Monday after ensemble bootstrap on 2026-04-18).
- **Format updates**: if the template shape doesn't capture something useful, edit this file (not the worklog entry) — the template is a live document; individual rollup entries are historical.
