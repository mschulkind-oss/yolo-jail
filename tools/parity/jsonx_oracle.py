#!/usr/bin/env python3
"""JSON encoding oracle for internal/jsonx parity (go-port plan §5, Stage 2).

Reads a JSON array of test cases from stdin (each an arbitrary JSON value) and
writes, for each, the byte output of BOTH json.dumps forms the Go port must
match:

    {"snapshot": json.dumps(v, indent=2, sort_keys=True, ensure_ascii=True),
     "compact":  json.dumps(v)}

The Go test (internal/jsonx/parity_test.go) feeds the same corpus through
DumpsSnapshot/DumpsCompact and byte-compares. Because both sides parse the
corpus from the SAME JSON bytes, key order is defined by the source text and
must survive the round trip identically.

Emitted as one canonical JSON document so the Go test can decode it with its
own order-preserving decoder.
"""

from __future__ import annotations

import json
import sys


def main() -> int:
    raw = sys.stdin.read()
    # Preserve key order from the source text: object_pairs_hook keeps a dict
    # in insertion order (Python dicts are ordered), matching what the Go
    # OrderedMap decoder does.
    cases = json.loads(raw)
    out = []
    for v in cases:
        out.append(
            {
                "snapshot": json.dumps(
                    v, indent=2, sort_keys=True, ensure_ascii=True
                ),
                "compact": json.dumps(v),
            }
        )
    sys.stdout.write(json.dumps(out, ensure_ascii=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
