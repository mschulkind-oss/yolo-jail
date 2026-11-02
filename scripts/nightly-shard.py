#!/usr/bin/env python3
"""Partition the integration suite across the macOS nightly's shards by measured duration.

Two subcommands:

  select --durations FILE --total N --index I < all-tests
      Read every test name the suite lists (`go test -list`, one per line) on stdin and print
      the names shard I of N runs. Every shard computes the SAME whole partition and prints
      its own bin, so the union is the listing and no test is in two shards, whatever the
      table says: the table only decides which bin a test lands in, never whether it runs.

  durations LOG...
      Read the top-level `--- PASS|FAIL|SKIP: TestX (Ns)` lines of downloaded shard logs and
      print the table `select` reads: each test's MEDIAN duration across the logs, in whole
      seconds. Regenerating it is how the table is kept current.

The partition is longest-processing-time first: tests in descending measured duration (name
breaks a tie), each to the bin with the least load so far (lowest index breaks a tie). A test
the table does not name weighs the table's mean, so a new test counts as an average one until
the table is regenerated. A stale table therefore costs balance and nothing else.

Why it exists: round-robin over the sorted names put 8 to 56 minutes of tests on a shard in the
same run (run 41465971761), and shards 6 and 7 ran into their 60-minute step cap with no test
failing. See the comment above `strategy` in .github/workflows/nightly-macos.yml.
"""

import argparse
import re
import statistics
import sys

RESULT = re.compile(r"^(?:\S+Z )?--- (?:PASS|FAIL|SKIP): (Test\S+) \(([0-9.]+)s\)\s*$")


def read_table(path):
    table = {}
    with open(path, encoding="utf-8") as f:
        for n, line in enumerate(f, 1):
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            fields = line.split()
            if len(fields) != 2 or not fields[1].isdigit():
                raise SystemExit(
                    f"{path}:{n}: expected `<TestName> <seconds>`, got {line!r}. "
                    f"Regenerate the table with `{sys.argv[0]} durations <shard logs>`."
                )
            table[fields[0]] = int(fields[1])
    return table


def partition(tests, table, total):
    """Return `total` lists of test names, balanced by the table's durations."""
    default = statistics.mean(table.values()) if table else 1
    weight = {t: table.get(t, default) for t in tests}
    bins = [[0, i, []] for i in range(total)]
    for t in sorted(set(tests), key=lambda t: (-weight[t], t)):
        b = min(bins, key=lambda b: (b[0], b[1]))
        b[0] += weight[t]
        b[2].append(t)
    return [sorted(b[2]) for b in bins]


def durations(paths):
    seen = {}
    for p in paths:
        with open(p, encoding="utf-8", errors="replace") as f:
            for line in f:
                m = RESULT.match(line.rstrip("\n"))
                if m:
                    seen.setdefault(m.group(1), []).append(float(m.group(2)))
    return {t: int(round(statistics.median(v))) for t, v in seen.items()}


def main(argv):
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    sub = ap.add_subparsers(dest="cmd", required=True)
    s = sub.add_parser("select")
    s.add_argument("--durations", required=True)
    s.add_argument("--total", type=int, required=True)
    s.add_argument("--index", type=int, required=True, help="1-based, as matrix.shard is")
    d = sub.add_parser("durations")
    d.add_argument("logs", nargs="+")
    a = ap.parse_args(argv)

    if a.cmd == "select":
        if a.total < 1 or not 1 <= a.index <= a.total:
            raise SystemExit(f"--index {a.index} is not a shard of --total {a.total}")
        tests = [line.strip() for line in sys.stdin if line.strip()]
        bins = partition(tests, read_table(a.durations), a.total)
        for t in bins[a.index - 1]:
            print(t)
        return 0

    table = durations(a.logs)
    if not table:
        raise SystemExit("no `--- PASS|FAIL|SKIP: TestX (Ns)` line in any of the logs given")
    print("# Median duration in seconds of each integration test on the macOS nightly, read from")
    print("# its shard logs by `python3 scripts/nightly-shard.py durations <logs>`.")
    for t in sorted(table):
        print(f"{t} {table[t]}")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
