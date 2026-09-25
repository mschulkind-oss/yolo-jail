#!/usr/bin/env python3
"""Regression checks for the build/deploy output-directory contract."""
import importlib.util
from pathlib import Path
import tempfile
import unittest

SPEC = importlib.util.spec_from_file_location("site_output", Path(__file__).with_name("check-site-output-dir.py"))
module = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(module)


class OutputDirectoryTest(unittest.TestCase):
    def test_agreement_and_drift(self):
        with tempfile.TemporaryDirectory() as temp:
            script, wrangler = Path(temp) / "build.sh", Path(temp) / "wrangler.toml"
            script.write_text('vantage build userguide/ -o dist/docs -n "Guide"\n')
            wrangler.write_text('[assets]\ndirectory = "dist/docs"\n')
            self.assertIsNone(module.check(script, wrangler))
            wrangler.write_text('[assets]\ndirectory = "dist/stale"\n')
            self.assertIsNotNone(module.check(script, wrangler))

    def test_every_install_path_must_agree(self):
        with tempfile.TemporaryDirectory() as temp:
            script, wrangler = Path(temp) / "build.sh", Path(temp) / "wrangler.toml"
            script.write_text('vantage build userguide/ -o dist/docs\nvantage build userguide/ -o dist/stale\n')
            wrangler.write_text('[assets]\ndirectory = "dist/docs"\n')
            self.assertIsNotNone(module.check(script, wrangler))


if __name__ == "__main__":
    unittest.main()
