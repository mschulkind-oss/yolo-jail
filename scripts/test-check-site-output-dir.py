#!/usr/bin/env python3
"""Regression checks for the build/deploy output-directory contract."""
import importlib.util
import json
from pathlib import Path
import shutil
import subprocess
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


class BaseHrefCallSiteTest(unittest.TestCase):
    # The base is what makes a page opened by any path but the root render (2026-10-05), so the
    # build script must keep running the step: a test of scripts/site-base-href.py alone stays green
    # when the line that calls it is deleted.
    BUILD = 'vantage build userguide/ -o dist/docs -n "Guide"\n'
    PIN = "python3 scripts/site-base-href.py dist/docs\n"

    def verdict(self, script_text):
        with tempfile.TemporaryDirectory() as temp:
            script, wrangler = Path(temp) / "build.sh", Path(temp) / "wrangler.toml"
            script.write_text(script_text)
            wrangler.write_text('[assets]\ndirectory = "dist/docs"\n')
            return module.check_base_href(script, wrangler)

    def test_the_step_after_the_build_passes(self):
        self.assertIsNone(self.verdict(self.BUILD + self.PIN))
        self.assertIsNone(self.verdict("if x; then\n  " + self.BUILD + "else\n  " + self.BUILD + "fi\n" + self.PIN))

    def test_a_missing_misplaced_or_misdirected_step_fails(self):
        self.assertIsNotNone(self.verdict(self.BUILD))
        self.assertIsNotNone(self.verdict(self.PIN + self.BUILD))
        self.assertIsNotNone(self.verdict(self.BUILD + "# " + self.PIN))
        self.assertIsNotNone(self.verdict(self.BUILD + self.PIN.replace("dist/docs", "dist/stale")))

    def test_the_shipped_build_script_runs_the_step(self):
        root = Path(__file__).resolve().parent.parent
        self.assertIsNone(module.check_base_href(root / "scripts" / "build-site.sh", root / "docs-wrangler.toml"))


# Runs the shipped Worker under node against a stand-in for its ASSETS binding that answers as
# Cloudflare's does with not_found_handling = "single-page-application": the file when one matches,
# and otherwise index.html with a 200 (measured on the live site for /assets/nonexistent.js and
# /guides/assets/<real name>.js, 2026-10-05).
WORKER_HARNESS = r"""
const fs = await import("node:fs");
const [workerPath, requestsJson] = process.argv.slice(1);
const source = fs.readFileSync(workerPath, "utf8");
const worker = (await import("data:text/javascript," + encodeURIComponent(source))).default;
const files = { "/assets/index-abc.js": "text/javascript", "/api/repos.json": "application/json" };
const env = { ASSETS: { fetch: async (request) => {
  const type = files[new URL(request.url).pathname];
  return type ? new Response("file", { headers: { "content-type": type } })
              : new Response("<!doctype html>", { headers: { "content-type": "text/html; charset=utf-8" } });
} } };
const answers = [];
for (const path of JSON.parse(requestsJson)) {
  const response = await worker.fetch(new Request("https://docs.example" + path), env);
  answers.push([path, response.status, response.headers.get("content-type")]);
}
console.log(JSON.stringify(answers));
"""


@unittest.skipIf(shutil.which("node") is None, "node is not on PATH, so the shipped Worker cannot run")
class WorkerFallbackTest(unittest.TestCase):
    def answers(self, *paths):
        worker = Path(__file__).resolve().parent.parent / "docs-worker.js"
        run = subprocess.run(["node", "--input-type=module", "-e", WORKER_HARNESS, str(worker), json.dumps(paths)],
                             capture_output=True, text=True, check=True)
        return {path: (status, kind) for path, status, kind in json.loads(run.stdout)}

    def test_a_missing_file_under_the_exports_trees_is_a_404_at_any_depth(self):
        answers = self.answers("/guides/assets/index-abc.js", "/assets/missing.js", "/guides/api/repos.json", "/api/missing.json")
        for path, (status, _) in answers.items():
            self.assertEqual(status, 404, path)

    def test_files_and_page_paths_are_served_as_before(self):
        answers = self.answers("/assets/index-abc.js", "/api/repos.json", "/guides/macos", "/README.md", "/")
        self.assertEqual(answers["/assets/index-abc.js"], (200, "text/javascript"))
        self.assertEqual(answers["/api/repos.json"], (200, "application/json"))
        for page in ("/guides/macos", "/README.md", "/"):
            self.assertEqual(answers[page], (200, "text/html; charset=utf-8"), page)


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
