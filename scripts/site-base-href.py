#!/usr/bin/env python3
"""Pin the site's HTML entry points to the site root with <base href="/">.

Vantage's static export names its scripts and styles (./assets/...) and its pre-rendered page data
(./api/...) by RELATIVE path, which resolve correctly only from the document root. The Worker
answers every other path (/guides/macos) with the same index.html, so a browser there asked for
/guides/assets/... and /guides/api/..., got that HTML back with a 200, refused it as a script on
its MIME type, and showed a blank page (measured on docs.yolo-jail.mschulkind.dev, 2026-10-05). A
base element makes every relative URL in the document resolve against the root, wherever the page
was served from. The site is served at its domain's root, so "/" is that root.

Usage: site-base-href.py <export directory>. Exits non-zero, naming the file, when an entry point
has no <head> or already names a different base, so a changed export fails the build instead of
publishing a blank site.
"""
from pathlib import Path
import re
import sys

BASE = '<base href="/" />'
_HEAD = re.compile(r"<head(?:\s[^>]*)?>", re.IGNORECASE)
_ANY_BASE = re.compile(r"<base\b[^>]*>", re.IGNORECASE)
_ROOT_BASE = re.compile(r"""<base\s+href\s*=\s*["']/["']\s*/?>""", re.IGNORECASE)


def pin(html: str) -> str:
    """Return html with BASE as the first element of its <head>; unchanged if already pinned."""
    existing = _ANY_BASE.search(html)
    if existing:
        if _ROOT_BASE.fullmatch(existing.group(0)):
            return html
        raise ValueError(f"already names a different base, {existing.group(0)}")
    head = _HEAD.search(html)
    if not head:
        raise ValueError("has no <head> to put the base in")
    return f"{html[:head.end()]}\n    {BASE}{html[head.end():]}"


def pin_directory(export: Path) -> list[Path]:
    """Pin every HTML file at the top of the export, which is where Vantage writes its entry points."""
    pages = sorted(export.glob("*.html"))
    if export / "index.html" not in pages:
        raise ValueError(f"{export} has no index.html; is it a Vantage export?")
    for page in pages:
        try:
            page.write_text(pin(page.read_text()))
        except ValueError as problem:
            raise ValueError(f"{page} {problem}") from None
    return pages


if __name__ == "__main__":
    if len(sys.argv) != 2:
        sys.exit("usage: site-base-href.py <export directory>")
    try:
        pin_directory(Path(sys.argv[1]))
    except ValueError as problem:
        sys.exit(str(problem))
