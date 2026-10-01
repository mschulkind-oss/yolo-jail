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


class WorkerBindingTest(unittest.TestCase):
    # A Worker that reads env.ASSETS with no [assets] binding throws on every request that reaches
    # it: 1101 for each non-navigation request to a page that is not a file (curl, crawlers, link
    # previews), measured on docs.yolo-jail.mschulkind.dev on 2026-10-01.
    def test_the_worker_reads_only_a_declared_binding(self):
        with tempfile.TemporaryDirectory() as temp:
            worker, wrangler = Path(temp) / "worker.js", Path(temp) / "wrangler.toml"
            worker.write_text("export default { async fetch(request, env) { return env.ASSETS.fetch(request); } };\n")
            wrangler.write_text('[assets]\ndirectory = "dist/docs"\n')
            self.assertIsNotNone(module.check_bindings(worker, wrangler))
            wrangler.write_text('[assets]\nbinding = "ASSETS"\ndirectory = "dist/docs"\n')
            self.assertIsNone(module.check_bindings(worker, wrangler))
            wrangler.write_text('[assets]\nbinding = "STATIC"\ndirectory = "dist/docs"\n')
            self.assertIsNotNone(module.check_bindings(worker, wrangler))

    def test_the_shipped_worker_and_config_agree(self):
        root = Path(__file__).resolve().parent.parent
        self.assertIsNone(module.check_bindings(root / "docs-worker.js", root / "docs-wrangler.toml"))


if __name__ == "__main__":
    unittest.main()
