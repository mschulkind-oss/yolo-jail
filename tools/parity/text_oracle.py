#!/usr/bin/env python3
"""Text-primitive oracle for internal/shquote + internal/pytext parity.

Reads a JSON document from stdin of the form:

    {"quote": ["arg1", "arg2", ...], "join": [["a","b"], ...], "repr": ["s", ...]}

and writes the byte outputs Python produces for each, so the Go tests can
byte-compare:

    {"quote": {"<arg>": shlex.quote(arg)},
     "join":  ["shlex.join(list)", ...],
     "repr":  {"<s>": repr(s)}}

shlex.quote/join and repr are the exact source-of-truth functions the Go
ports mirror. Cross-language testing them is cheap and catches any divergence
in the escape/quote-selection rules.
"""

from __future__ import annotations

import json
import shlex
import sys


def main() -> int:
    spec = json.loads(sys.stdin.read())
    out: dict = {}
    if "quote" in spec:
        out["quote"] = {arg: shlex.quote(arg) for arg in spec["quote"]}
    if "join" in spec:
        out["join"] = [shlex.join(lst) for lst in spec["join"]]
    if "repr" in spec:
        out["repr"] = {s: repr(s) for s in spec["repr"]}
    sys.stdout.write(json.dumps(out, ensure_ascii=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
