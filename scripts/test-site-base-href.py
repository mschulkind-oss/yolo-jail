#!/usr/bin/env python3
"""Regression checks for pinning the site's HTML entry points to the site root."""
import importlib.util
from pathlib import Path
import re
import tempfile
import unittest
from urllib.parse import urljoin

SPEC = importlib.util.spec_from_file_location("site_base_href", Path(__file__).with_name("site-base-href.py"))
module = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(module)

# The shape of the entry point `vantage build` writes: relative asset URLs in a plain <head>.
EXPORT = """<!doctype html>
<html lang="en">
  <head>
    <script>window.__VANTAGE_STATIC__=true;</script>
    <script type="module" crossorigin src="./assets/index-abc.js"></script>
    <link rel="stylesheet" crossorigin href="./assets/index-abc.css">
  </head>
  <body>
    <div id="root"></div>
  </body>
</html>
"""


def resolve(html: str, page: str, reference: str) -> str:
    """Resolve reference as a browser does from a document at page: against its base, if it has one."""
    base = re.search(r'<base href="([^"]*)"', html)
    return urljoin(urljoin(page, base.group(1)) if base else page, reference)


class PinTest(unittest.TestCase):
    def test_a_relative_asset_resolves_to_the_root_from_any_page(self):
        page = "https://docs.example/guides/macos"
        # The defect, measured on the live site 2026-10-05: from a page below the root, the export's
        # own URL names a directory that does not exist, which the Worker answered with the HTML page.
        self.assertEqual(resolve(EXPORT, page, "./assets/index-abc.js"), "https://docs.example/guides/assets/index-abc.js")
        pinned = module.pin(EXPORT)
        self.assertEqual(resolve(pinned, page, "./assets/index-abc.js"), "https://docs.example/assets/index-abc.js")
        self.assertEqual(resolve(pinned, page, "./api/repos.json"), "https://docs.example/api/repos.json")
        self.assertEqual(resolve(pinned, "https://docs.example/", "./assets/index-abc.js"), "https://docs.example/assets/index-abc.js")

    def test_the_base_comes_first_in_the_head(self):
        pinned = module.pin(EXPORT)
        head = pinned[pinned.index("<head>") + len("<head>"):]
        self.assertTrue(head.lstrip().startswith(module.BASE))
        self.assertEqual(pinned.replace(f"\n    {module.BASE}", "", 1), EXPORT)

    def test_pinning_twice_changes_nothing(self):
        pinned = module.pin(EXPORT)
        self.assertEqual(module.pin(pinned), pinned)

    def test_a_head_with_attributes_is_found(self):
        self.assertIn(module.BASE, module.pin(EXPORT.replace("<head>", '<head data-x="1">')))

    def test_a_different_base_is_refused(self):
        with self.assertRaisesRegex(ValueError, "different base"):
            module.pin(EXPORT.replace("<head>", '<head>\n    <base href="/docs/">'))

    def test_no_head_is_refused(self):
        with self.assertRaisesRegex(ValueError, "no <head>"):
            module.pin("<!doctype html><div id=\"root\"></div>")


class DirectoryTest(unittest.TestCase):
    def test_every_entry_point_is_pinned(self):
        with tempfile.TemporaryDirectory() as temp:
            export = Path(temp)
            (export / "index.html").write_text(EXPORT)
            (export / "404.html").write_text(EXPORT)
            (export / "assets").mkdir()
            (export / "assets" / "index-abc.js").write_text("export {};\n")
            self.assertEqual([page.name for page in module.pin_directory(export)], ["404.html", "index.html"])
            for name in ("index.html", "404.html"):
                self.assertIn(module.BASE, (export / name).read_text())
            self.assertEqual((export / "assets" / "index-abc.js").read_text(), "export {};\n")

    def test_a_directory_without_an_index_is_refused(self):
        with tempfile.TemporaryDirectory() as temp:
            with self.assertRaisesRegex(ValueError, "no index.html"):
                module.pin_directory(Path(temp))

    def test_a_refusal_names_the_file(self):
        with tempfile.TemporaryDirectory() as temp:
            export = Path(temp)
            (export / "index.html").write_text("<div></div>")
            with self.assertRaisesRegex(ValueError, "index.html has no <head>"):
                module.pin_directory(export)


if __name__ == "__main__":
    unittest.main()
