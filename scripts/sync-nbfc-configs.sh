#!/usr/bin/env bash
# Sync the vendored nbfc-linux catalogue at the named upstream tag.
#
# Usage:
#   scripts/sync-nbfc-configs.sh [TAG]
#
# Defaults to the tag currently named in internal/hwdb/nbfc/UPSTREAM.
# Re-running with the same tag is a no-op (idempotent).
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
dst="$repo_root/internal/hwdb/nbfc/configs"
upstream_file="$repo_root/internal/hwdb/nbfc/UPSTREAM"

tag="${1:-}"
if [[ -z "$tag" ]] && [[ -f "$upstream_file" ]]; then
    tag="$(awk -F': *' '/^tag:/ {print $2; exit}' "$upstream_file" | tr -d ' ')"
fi
if [[ -z "$tag" ]]; then
    echo "error: no tag given and no UPSTREAM file to read default from" >&2
    exit 2
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "Cloning nbfc-linux at tag $tag ..."
git -c advice.detachedHead=false clone --quiet --depth=1 --branch "$tag" \
    https://github.com/nbfc-linux/nbfc-linux.git "$tmp/nbfc-linux"

src="$tmp/nbfc-linux/share/nbfc/configs"
if ! ls "$src"/*.json >/dev/null 2>&1; then
    echo "error: upstream layout changed — no configs found under $src" >&2
    exit 3
fi

mkdir -p "$dst"
# Clear existing configs only after the new ones are in hand.
find "$dst" -maxdepth 1 -name '*.json' -delete
cp "$src"/*.json "$dst/"
cp "$tmp/nbfc-linux/LICENSE" "$dst/../LICENSE.upstream"

count="$(ls -1 "$dst"/*.json | wc -l | tr -d ' ')"
sha="$(git -C "$tmp/nbfc-linux" rev-parse HEAD)"

cat > "$upstream_file" <<EOF
upstream: github.com/nbfc-linux/nbfc-linux
tag:      $tag
commit:   $sha
synced:   $(date -u +%Y-%m-%dT%H:%M:%SZ)
config_count: $count
license:  GPL-3.0
EOF

echo "Vendored $count configs from nbfc-linux@$tag ($sha)"
echo "Wrote $upstream_file"
