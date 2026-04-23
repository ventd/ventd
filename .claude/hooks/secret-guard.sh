#!/usr/bin/env bash
path=$(jq -r '.tool_input.file_path // .tool_input.path // ""')
case "$path" in
  *.env|*/.env|*/id_rsa|*/id_ed25519|*.key|*.pem|*kubeconfig*|*/.aws/credentials)
    echo "BLOCKED: refuse to touch secret file: $path" >&2
    exit 2 ;;
esac
exit 0
