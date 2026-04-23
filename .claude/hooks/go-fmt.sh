#!/usr/bin/env bash
# Run gofmt + goimports on Go files after Edit/Write operations.
input=$(cat)
file=$(echo "$input" | jq -r '.tool_input.file_path // empty')
[[ "$file" == *.go ]] || exit 0
gofmt -w "$file"
goimports -w "$file" 2>/dev/null || true
exit 0
