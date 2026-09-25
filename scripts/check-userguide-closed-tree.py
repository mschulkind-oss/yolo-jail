#!/usr/bin/env python3
"""Refuse relative Markdown links that leave the published userguide tree.

Vantage checks whether a link exists in the checkout. This additional check
ensures it will also exist in the static export, which contains only userguide/.
"""
import pathlib
import re
import sys
from urllib.parse import unquote, urlsplit

LINK = re.compile(r"\]\(\s*(<[^>]+>|[^\s)]+)")
REFERENCE = re.compile(r"^\s*\[[^]]+\]:\s*(<[^>]+>|\S+)", re.M)
FENCE = re.compile(r"^\s*(`{3,}|~{3,})")


def targets(text):
    lines = []
    fence = None
    for line in text.splitlines():
        marker = FENCE.match(line)
        if marker:
            if fence is None:
                fence = marker.group(1)
            elif marker.group(1)[0] == fence[0] and len(marker.group(1)) >= len(fence):
                fence = None
            lines.append("")
        else:
            lines.append("" if fence else line)
    visible = "\n".join(lines)
    for match in LINK.finditer(visible):
        yield match.group(1).strip("<>")
    for match in REFERENCE.finditer(visible):
        yield match.group(1).strip("<>")


def check(root):
    root = root.resolve()
    problems = []
    for file in sorted(root.rglob("*.md")):
        for target in targets(file.read_text()):
            parts = urlsplit(target)
            if parts.scheme or parts.netloc or not parts.path:
                continue
            dest = (file.parent / unquote(parts.path)).resolve()
            if not dest.is_relative_to(root):
                problems.append(f"{file}: relative link {target!r} leaves {root}")
    return problems


if __name__ == "__main__":
    directory = pathlib.Path(sys.argv[1] if len(sys.argv) > 1 else "userguide")
    if not directory.is_dir():
        sys.exit(f"missing guide directory: {directory}")
    errors = check(directory)
    for error in errors:
        print(error, file=sys.stderr)
    sys.exit(1 if errors else 0)
