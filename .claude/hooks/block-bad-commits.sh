#!/usr/bin/env bash
# Block git commits that don't follow Conventional Commits format.
input=$(cat)
cmd=$(echo "$input" | jq -r '.tool_input.command // empty')
[[ "$cmd" == *"git commit"* ]] || exit 0

# Extract -m "message" from the command (handles both single and double quotes)
msg=$(echo "$cmd" | grep -oP '(?<=-m ")[^"]+' || echo "$cmd" | grep -oP "(?<=-m ')[^']+")

if [[ -n "$msg" ]] && ! echo "$msg" | grep -qE '^(feat|fix|docs|style|refactor|test|chore|perf|build|ci|revert)(\([a-z0-9/-]+\))?!?: .+'; then
  echo "Commit message does not match Conventional Commits format." >&2
  echo "Expected: <type>(<optional scope>): <subject>" >&2
  echo "Types: feat, fix, docs, style, refactor, test, chore, perf, build, ci, revert" >&2
  exit 2
fi
exit 0
