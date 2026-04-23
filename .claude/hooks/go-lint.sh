#!/usr/bin/env bash
# Run golangci-lint on the package containing the edited file.
input=$(cat)
file=$(echo "$input" | jq -r '.tool_input.file_path // empty')
[[ "$file" == *.go ]] || exit 0
pkg_dir=$(dirname "$file")
cd "$pkg_dir" 2>/dev/null || exit 0
golangci-lint run --fast --out-format=line-number ./... 2>&1 | head -40
exit 0
