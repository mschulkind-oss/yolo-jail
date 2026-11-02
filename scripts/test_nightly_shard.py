#!/usr/bin/env python3
"""Regression tests for scripts/nightly-shard.py and the nightly step that calls it."""

import importlib.util
import os
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)
SCRIPT = os.path.join(HERE, "nightly-shard.py")
TABLE = os.path.join(ROOT, ".github", "nightly-macos-durations.txt")
WORKFLOW = os.path.join(ROOT, ".github", "workflows", "nightly-macos.yml")

spec = importlib.util.spec_from_file_location("nightly_shard", SCRIPT)
shard = importlib.util.module_from_spec(spec)
spec.loader.exec_module(shard)


def run_select(tests, table_text, total, index):
    with tempfile.TemporaryDirectory() as d:
        table = os.path.join(d, "table.txt")
        with open(table, "w") as f:
            f.write(table_text)
        return subprocess.run(
            [sys.executable, SCRIPT, "select", "--durations", table,
             "--total", str(total), "--index", str(index)],
            input="\n".join(tests) + "\n", capture_output=True, text=True)


class Partition(unittest.TestCase):
    def test_every_listed_test_runs_exactly_once(self):
        tests = [f"Test{i:03d}" for i in range(50)] + ["TestNew", "TestNewer"]
        table = {f"Test{i:03d}": (i * 37) % 600 for i in range(50)}
        table["TestRetired"] = 9000  # in the table, gone from the listing
        bins = shard.partition(tests, table, 7)
        flat = [t for b in bins for t in b]
        self.assertEqual(sorted(flat), sorted(tests))
        self.assertTrue(all(bins), "a shard with nothing to run fails the nightly")

    def test_a_slow_test_gets_a_shard_of_its_own(self):
        tests = ["TestSlow"] + [f"TestQuick{i}" for i in range(30)]
        table = {"TestSlow": 1800, **{f"TestQuick{i}": 60 for i in range(30)}}
        bins = shard.partition(tests, table, 3)
        slow = next(b for b in bins if "TestSlow" in b)
        self.assertEqual(slow, ["TestSlow"])
        loads = sorted(sum(table[t] for t in b) for b in bins)
        self.assertEqual(loads, [900, 900, 1800])

    def test_beats_round_robin_on_the_measured_shape(self):
        # Round-robin over sorted names is what the step did before: here it puts both slow
        # tests on one shard.
        tests = ["TestA", "TestB", "TestC", "TestD"]
        table = {"TestA": 1000, "TestB": 10, "TestC": 1000, "TestD": 10}
        bins = shard.partition(tests, table, 2)
        self.assertEqual(max(sum(table[t] for t in b) for b in bins), 1010)

    def test_the_partition_does_not_depend_on_the_listing_order(self):
        tests = [f"Test{i}" for i in range(40)]
        table = {t: (n * 13) % 97 for n, t in enumerate(tests)}
        a = shard.partition(tests, table, 5)
        b = shard.partition(list(reversed(tests)), table, 5)
        self.assertEqual(a, b)

    def test_an_unmeasured_test_weighs_the_tables_mean(self):
        table = {"TestBig": 300, "TestSmall": 100}
        # TestNew weighs 200, so it joins TestSmall rather than TestBig.
        bins = shard.partition(["TestBig", "TestSmall", "TestNew"], table, 2)
        self.assertIn(["TestNew", "TestSmall"], bins)


class Durations(unittest.TestCase):
    def test_reads_top_level_results_and_takes_the_median(self):
        with tempfile.TemporaryDirectory() as d:
            logs = []
            for n, secs in enumerate(["10.0", "30.0", "200.0"]):
                p = os.path.join(d, f"shard{n}.log")
                with open(p, "w") as f:
                    f.write(f"2026-11-02T16:38:55.8368860Z --- PASS: TestA ({secs}s)\n")
                    f.write("2026-11-02T16:38:55.8368860Z     --- PASS: TestA/sub (999.0s)\n")
                    f.write("--- SKIP: TestB (0.01s)\n")
                    f.write("=== RUN   TestC\n")
                logs.append(p)
            self.assertEqual(shard.durations(logs), {"TestA": 30, "TestB": 0})


class Select(unittest.TestCase):
    def test_the_shards_union_is_the_listing(self):
        tests = [f"Test{i}" for i in range(23)]
        table = "".join(f"Test{i} {i * 7}\n" for i in range(23))
        got = []
        for i in range(1, 5):
            r = run_select(tests, "# comment\n" + table, 4, i)
            self.assertEqual(r.returncode, 0, r.stderr)
            got += r.stdout.split()
        self.assertEqual(sorted(got), sorted(tests))

    def test_an_index_outside_the_matrix_is_refused(self):
        r = run_select(["TestA"], "TestA 1\n", 4, 5)
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("is not a shard of", r.stderr)

    def test_a_malformed_table_names_the_regeneration_command(self):
        r = run_select(["TestA"], "TestA one\n", 1, 1)
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("nightly-shard.py durations", r.stderr)


class CommittedFiles(unittest.TestCase):
    def test_the_committed_table_parses(self):
        self.assertGreater(len(shard.read_table(TABLE)), 0)

    def test_the_nightly_partitions_through_this_script(self):
        # The call site: delete it, or go back to round-robin, and this fails.
        with open(WORKFLOW) as f:
            wf = f.read()
        self.assertIn(
            "python3 scripts/nightly-shard.py select --durations .github/nightly-macos-durations.txt "
            '--total "$total" --index "$idx"', wf)
        self.assertNotIn("NR % t", wf)


if __name__ == "__main__":
    unittest.main()
