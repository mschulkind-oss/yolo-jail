#!/usr/bin/env python3
"""Regression checks for the published-guide boundary."""
import importlib.util
from pathlib import Path
import tempfile
import unittest

SPEC = importlib.util.spec_from_file_location("closed_tree", Path(__file__).with_name("check-userguide-closed-tree.py"))
module = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(module)


class ClosedTreeTest(unittest.TestCase):
    def test_rejects_an_existing_outside_link(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp) / "userguide"
            root.mkdir()
            (Path(temp) / "outside.md").write_text("# Outside\n")
            (root / "README.md").write_text("[outside](../outside.md)\n")
            self.assertEqual(len(module.check(root)), 1)

    def test_accepts_internal_and_external_links_and_ignores_code(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp) / "userguide"
            root.mkdir()
            (root / "inside.md").write_text("# Inside\n")
            (root / "README.md").write_text(
                "[inside](inside.md#inside) [external](https://github.com/x/y)\n"
                "```md\n[example](../outside.md)\n```\n"
            )
            self.assertEqual(module.check(root), [])

    def test_rejects_reference_style_link(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp) / "userguide"
            root.mkdir()
            (root / "README.md").write_text("[more][elsewhere]\n\n[elsewhere]: ../outside.md\n")
            self.assertEqual(len(module.check(root)), 1)


if __name__ == "__main__":
    unittest.main()
