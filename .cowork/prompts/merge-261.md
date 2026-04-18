You are Claude Code. Run exactly these commands and exit.

1. `gh pr ready 261 --repo ventd/ventd`
   (marks PR #261 as ready-for-review, no code changes)

2. `gh pr view 261 --repo ventd/ventd --json mergeable,mergeStateStatus,isDraft`
   (confirm isDraft=false before merging)

3. `gh pr merge 261 --repo ventd/ventd --squash --subject "fix(hwmon): append-not-overwrite in persistModule (#261)" --delete-branch`
   (squash-merge and delete branch)

4. `gh pr view 261 --repo ventd/ventd --json merged,mergeCommit`
   (confirm merged=true, capture mergeCommit.oid)

Report:
- STATUS: done | partial | blocked
- MERGE_SHA: <oid from step 4>
- BRANCH_DELETED: <bool>

Do NOT edit any files. Do NOT push anything. This is a pure gh-API-call task.

If any step fails:
- STATUS: blocked
- FAILED_STEP: <step number>
- ERROR: <verbatim error>
