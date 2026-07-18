#!/usr/bin/env python3
"""JSON5/JSONC decode oracle for internal/json5 parity (go-port Stage 2 Spike A).

Reads a JSON array of INPUT STRINGS from stdin (each a JSONC/JSON5 document),
parses each with pyjson5 (the source of truth), and emits for each either:

    {"ok": true,  "canonical": <json.dumps(value, sort_keys, ensure_ascii)>}
    {"ok": false}                      # pyjson5 rejected it

so the Go test can parse the SAME input with json5.Decode, re-encode via
jsonx.DumpsSnapshot, and byte-compare the canonical forms (and agree on
accept/reject). Comparing canonical re-encodings (not the raw parse) is what
makes the two languages' value models line up.

Emitted as one canonical JSON document.
"""

from __future__ import annotations

import json
import sys


def canonical(value) -> str:
    # Match jsonx.DumpsSnapshot: indent=2, sort_keys, ensure_ascii. Non-finite
    # floats (inf/nan) serialize as Infinity/-Infinity/NaN, which jsonx also
    # emits — so they compare equal.
    return json.dumps(value, indent=2, sort_keys=True, ensure_ascii=True)


def main() -> int:
    import pyjson5

    inputs = json.loads(sys.stdin.read())
    out = []
    for doc in inputs:
        try:
            value = pyjson5.loads(doc)
            out.append({"ok": True, "canonical": canonical(value)})
        except Exception:
            out.append({"ok": False})
    sys.stdout.write(json.dumps(out, ensure_ascii=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
