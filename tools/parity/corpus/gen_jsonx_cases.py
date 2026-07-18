#!/usr/bin/env python3
"""Generate tools/parity/corpus/jsonx_cases.json.

The corpus intentionally includes strings with control characters and astral
code points, which can't be typed as literal bytes in a JSON file (json
forbids raw control chars). This generator emits them via escape sequences so
the committed corpus is valid JSON with proper \\uXXXX escapes.

Run: `python tools/parity/corpus/gen_jsonx_cases.py` (regenerate only when the
corpus intentionally changes — it's committed and diffed).
"""

from __future__ import annotations

import json
from pathlib import Path

CASES = [
    {},
    [],
    {"b": 1, "a": 2, "c": 3},
    {"z": {"y": {"x": 1}}},
    [1, 2, 3],
    [1, "two", 3.5, True, False, None],
    {"nested": {"list": [{"k": "v"}, {"k2": "v2"}]}},
    "plain string",
    "unicode: café münchen ☃ 日本語",
    "html-ish: <tag> & \"quote\" 'apos'",
    "escapes: \n\t\r\b\f\\ and slash /",
    "control: \u0000\u0001\u001f",
    "astral: \U0001f600 \U0001f389 \U0001f600",
    {"packages": ["strace", "gtk4.dev"]},
    {
        "YOLO_JAIL_DAEMONS": [
            {
                "name": "claude-oauth-broker",
                "cmd": ["python3", "-m", "src.oauth_broker_jail"],
                "restart": "on-failure",
            }
        ]
    },
    {"int": 42, "negint": -7, "zero": 0, "big": 9007199254740992},
    {"float": 3.14, "negfloat": -0.5, "exp": 1e10, "smallexp": 1.5e-8, "intfloat": 2.0},
    # Float repr boundaries (CPython switches fixed<->exp at decpt<=-4 || >16).
    [
        0.0,
        -0.0,
        1.0,
        100.0,
        1e16,
        1e17,
        1e-4,
        1e-5,
        1234567890123456.0,
        12345678901234567.0,
        0.1,
        0.0001,
        0.00001,
        123.456,
        -0.000123,
        1e100,
        1e-100,
        9.999999999999999e22,
    ],
    {"empty_obj": {}, "empty_arr": [], "empty_str": ""},
    {"mixed": [{"a": 1}, [2, 3], "s", None, True]},
    {"key with spaces": 1, "key/with/slashes": 2, "unicode-key-café": 3},
    {"deep": [[[[[1]]]]]},
    {"bools": [True, False], "nulls": [None, None]},
    "line1\nline2\nline3",
    {"a": 1, "z": 2, "m": 3, "b": 4},
    [[], [[]], [[], []]],
    {"sortcheck_Z": 1, "sortcheck_a": 2, "sortcheck_A": 3, "sortcheck_0": 4},
]


def main() -> int:
    out = Path(__file__).parent / "jsonx_cases.json"
    # ensure_ascii=True so control chars and non-ASCII are \uXXXX-escaped and
    # the file is pure ASCII (portable, no raw control bytes).
    text = json.dumps(CASES, ensure_ascii=True, indent=2) + "\n"
    out.write_text(text, encoding="utf-8")
    # Sanity: it must round-trip.
    json.loads(out.read_text(encoding="utf-8"))
    print(f"wrote {out} ({len(CASES)} cases)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
