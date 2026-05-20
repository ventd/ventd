#!/usr/bin/env python3
"""Union-merge CHANGELOG.md ### Added bullets.

Args: <ours.md> <theirs.md> <out.md>

Reads ours + theirs, takes their [Unreleased] / ### Added bullet
lines, computes ours + (theirs - ours) preserving order, then writes
out a CHANGELOG.md that is structurally identical to ours but with
the merged Added section replacing ours' Added section.

Outside the [Unreleased] / ### Added block, we always prefer ours.
"""

from __future__ import annotations

import sys
from pathlib import Path


def extract_added_bullets(text: str) -> list[str]:
    """Return the bullet lines (one per element, no trailing newline)
    under the FIRST occurrence of `## [Unreleased]` / `### Added`.
    """
    bullets: list[str] = []
    in_unreleased = False
    in_added = False
    for raw in text.splitlines():
        line = raw.rstrip("\n")
        if line.startswith("## [Unreleased]"):
            in_unreleased = True
            continue
        if in_unreleased and line.startswith("### Added"):
            in_added = True
            continue
        if in_added and (line.startswith("### ") or line.startswith("## ")):
            break
        if in_added and line.startswith("- "):
            bullets.append(line)
    return bullets


def merge_added(ours: list[str], theirs: list[str]) -> list[str]:
    """Order-preserving union; ours first, then theirs-not-in-ours."""
    seen = set(ours)
    merged = list(ours)
    for t in theirs:
        if t not in seen:
            merged.append(t)
            seen.add(t)
    return merged


def rebuild(ours_text: str, merged_bullets: list[str]) -> str:
    """Replace ours' ### Added bullet block with merged_bullets.

    Keep all non-Added content from ours; preserve the blank line
    between `### Added` and the bullets, and the blank line after
    the bullet block.
    """
    lines = ours_text.splitlines(keepends=True)
    out: list[str] = []
    i = 0
    while i < len(lines):
        line = lines[i]
        out.append(line)
        i += 1
        if line.startswith("## [Unreleased]"):
            # Scan forward for ### Added inside this section.
            while i < len(lines):
                nxt = lines[i]
                out.append(nxt)
                i += 1
                if nxt.startswith("### Added"):
                    # Insert blank line if not already present.
                    if i < len(lines) and lines[i].strip() == "":
                        out.append(lines[i])
                        i += 1
                    else:
                        out.append("\n")
                    # Emit merged bullets.
                    for b in merged_bullets:
                        out.append(b + "\n")
                    # Skip ours' existing bullet block + the blank line
                    # right after it, up to the next "### " / "## " or EOF.
                    while i < len(lines):
                        l = lines[i]
                        if l.startswith("### ") or l.startswith("## "):
                            break
                        i += 1
                    # Ensure a blank line before the next section.
                    if not out[-1].endswith("\n\n"):
                        out.append("\n")
                    break
                if nxt.startswith("## "):
                    # Hit next top-level section before finding ### Added.
                    break
            # Done with Unreleased handling.
    return "".join(out)


def main(argv: list[str]) -> int:
    if len(argv) != 4:
        print("usage: _resolve-changelog-conflict.py <ours> <theirs> <out>", file=sys.stderr)
        return 2
    ours_path, theirs_path, out_path = (Path(p) for p in argv[1:])
    ours_text = ours_path.read_text(encoding="utf-8")
    theirs_text = theirs_path.read_text(encoding="utf-8")

    ours_bullets = extract_added_bullets(ours_text)
    theirs_bullets = extract_added_bullets(theirs_text)
    merged = merge_added(ours_bullets, theirs_bullets)

    rebuilt = rebuild(ours_text, merged)
    out_path.write_text(rebuilt, encoding="utf-8")
    print(
        f"ours={len(ours_bullets)} bullets; theirs={len(theirs_bullets)} bullets; "
        f"merged={len(merged)} bullets",
        file=sys.stderr,
    )
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
