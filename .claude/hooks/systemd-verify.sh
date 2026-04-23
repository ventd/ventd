#!/usr/bin/env bash
# Verify systemd unit files after edits.
input=$(cat)
file=$(echo "$input" | jq -r '.tool_input.file_path // empty')
[[ "$file" == *.service || "$file" == *.socket || "$file" == *.timer ]] || exit 0
systemd-analyze verify "$file" 2>&1 | head -40
exit 0
