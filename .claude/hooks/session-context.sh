#!/usr/bin/env bash
cd "${CLAUDE_PROJECT_DIR:-$(pwd)}" || exit 0
echo "=== ventd session context ==="
echo "Branch: $(git rev-parse --abbrev-ref HEAD 2>/dev/null)"
echo "Recent commits:"
git log --oneline -5 2>/dev/null
echo "Dirty files:"
git status --short 2>/dev/null | head -10
echo ""
echo "=== Invariants (don't violate) ==="
echo "- .claude/rules/*.md are invariant bindings, 1:1 with subtests"
echo "- Conventional Commits required; linear history only"
echo "- Haiku for mechanical work, Sonnet for impl, NEVER Opus in CC"
echo "- CGO_ENABLED=0; purego dlopen for NVML"
echo "- Budget: \$300/mo — no multi-agent orchestration"
