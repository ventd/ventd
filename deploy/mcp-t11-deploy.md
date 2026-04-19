# MCP-T11 Deployment: 12 Cassidy + Drew Tools

Branch: `mcp-t11-cassidy-drew-tools-mcp-t11-1776566533-1079277`
Repo: `/home/cc-runner/github-mcp-server/`
BEFORE_SHA: `569a48d847236e7ed8d2b46ca57af8af7f768b36`
AFTER_SHA: `3c44ddba48ade297157196d66d82e40fad689f74`

## What was built

12 new MCP tools for Cassidy and Drew:

**Cassidy (6):**
- `grep_repo` — regex grep over repo tree via Contents API
- `get_issues_bulk` — batch fetch issues by number (parallel)
- `my_pending_close_requests` — find issues where commenter signalled close intent
- `prs_touching_paths` — list PRs that touched specific paths/globs
- `diff_summary` — summarise PR diff by file (hunks, additions, deletions)
- `rule_bindings` — parse `.claude/rules/*.md` and verify Bound: targets

**Drew (6):**
- `issue_label_flip` — add or remove a single label on an issue
- `file_patch` — fetch/edit/push file content with find-replace edits
- `drew_status_snapshot` — snapshot of open PRs and role-labelled issues
- `sha_audit` — audit workflow files for pinned-SHA action references
- `bulk_issue_comment` — post a comment to multiple issues with stagger
- `pr_branch_clean_assertion` — assert a PR branch is ahead by expected commits

## Build status (on phoenix-desktop)

```
CGO_ENABLED=0 go build ./...     → ok (exit 0)
go test -short ./pkg/github/...  → ok (exit 0)
go vet ./...                     → ok (exit 0)
gofmt -l .                       → clean (exit 0)
```

## Push status

Push to `github/github-mcp-server` origin was denied (PhoenixDnB does not have
write access to the upstream repo). The commit is at:

```
/home/cc-runner/github-mcp-server (branch mcp-t11-cassidy-drew-tools-mcp-t11-1776566533-1079277)
```

To deploy from phoenix-desktop with credentials:

```bash
cd /home/cc-runner/github-mcp-server
git push <YOUR_FORK_REMOTE> mcp-t11-cassidy-drew-tools-mcp-t11-1776566533-1079277
```

Or build and redeploy directly from the local branch:

```bash
cd /home/cc-runner/github-mcp-server
git checkout mcp-t11-cassidy-drew-tools-mcp-t11-1776566533-1079277
docker build -t github-mcp-server:mcp-t11 .
# Then update the systemd service to use the new image tag
systemctl restart github-mcp
```

## Docker smoke

Could not run Docker build in this session (cc-runner not in docker group).
Phoenix should run: `docker build -t github-mcp-server:mcp-t11-smoke .` on phoenix-desktop
to verify the image builds before deploying.
