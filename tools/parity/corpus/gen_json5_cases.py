#!/usr/bin/env python3
"""Generate tools/parity/corpus/json5_cases.json — a JSON array of JSONC/JSON5
input-document STRINGS for the internal/json5 parity test.

Covers the hard-requirement features (comments, trailing commas) plus the full
JSON5 dialect pyjson5 accepts, so the Go parser is driven to observed
equivalence. Regenerate only when the corpus intentionally changes.
"""

from __future__ import annotations

import json
from pathlib import Path

CASES = [
    # Standard JSON.
    "{}",
    "[]",
    '{"a": 1, "b": 2}',
    "[1, 2, 3]",
    '{"nested": {"x": [1, {"y": true}]}}',
    '"plain string"',
    "42",
    "-7",
    "3.14",
    "true",
    "false",
    "null",
    # Comments (hard requirement).
    '// line comment\n{"a": 1}',
    '{"a": 1} // trailing line comment',
    '/* block */ {"a": 1}',
    '{"a": 1 /* inline */, "b": 2}',
    '{\n  // per-key comment\n  "a": 1\n}',
    # Trailing commas (hard requirement).
    '{"a": 1,}',
    "[1, 2, 3,]",
    '{"a": 1, "b": 2,}',
    "[[1,], [2,],]",
    # Single quotes.
    "{'single': 'quotes'}",
    "['a', 'b']",
    "{'mix': \"double\"}",
    # Unquoted identifier keys.
    "{unquoted: 1}",
    "{key_with_underscore: 1, $dollar: 2}",
    "{a: 1, b: 2, c: 3}",
    # Hex.
    '{"hex": 0xff}',
    '{"hex2": 0xDEADBEEF}',
    "[0x0, 0x10, 0xFF]",
    # Signs + dotted floats.
    '{"plus": +5}',
    '{"leadingdot": .5}',
    '{"trailingdot": 5.}',
    '{"neg": -3.14, "exp": 1.5e3}',
    # Non-finite.
    '{"inf": Infinity, "ninf": -Infinity, "nan": NaN}',
    # Unicode + escapes in strings.
    '{"unicode": "caf\\u00e9 \\u2603"}',
    '{"escapes": "tab\\tnl\\nquote\\""}',
    '{"astral": "\\ud83d\\ude00"}',
    r'{"hexesc": "\x41\x42"}',
    # Real repo configs are added at test time (read from disk).
    # A few malformed docs pyjson5 REJECTS — both sides must agree on reject.
    '{"a": 1',  # unterminated object
    '{"a": }',  # missing value
    "[1 2 3]",  # missing commas
    "{a b}",  # missing colon
    "nul",  # bad literal
    "",  # empty
    "{} trailing",  # trailing data
]


def main() -> int:
    out = Path(__file__).parent / "json5_cases.json"
    out.write_text(
        json.dumps(CASES, ensure_ascii=True, indent=2) + "\n", encoding="utf-8"
    )
    json.loads(out.read_text(encoding="utf-8"))  # sanity
    print(f"wrote {out} ({len(CASES)} cases)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
