#!/usr/bin/env bash
STATE_FILE="/tmp/claude-session-$(date +%Y%m%d)-subagents"
COUNT=$(cat "$STATE_FILE" 2>/dev/null || echo 0)
COUNT=$((COUNT + 1))
echo "$COUNT" > "$STATE_FILE"
if [ "$COUNT" -gt 3 ]; then
  echo "BLOCKED: subagent spawn #$COUNT exceeds session cap of 3." >&2
  echo "Rework the plan to use the main session instead." >&2
  exit 2
fi
exit 0
