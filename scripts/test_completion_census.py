#!/usr/bin/env python3
"""Regressions for completion-census.py's test-log parsing: what counts as a repository read.

The census must not depend on where the checkout lives. Production code probes the jail's own
workspace path, the literal `/workspace`, so the same test logs `stat /workspace` from every
checkout; only in the checkout that IS `/workspace` does that path name the repository root.
"""
import importlib.util
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest import mock

HERE = Path(__file__).resolve().parent
SPEC = importlib.util.spec_from_file_location("completion_census", HERE / "completion-census.py")
census = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(census)


class Everything:
    """A visibility stub: every path is tracked or nonignored."""

    def contains(self, path, directory=False):
        return True


class ParseLogTest(unittest.TestCase):
    def setUp(self):
        self._root = census.ROOT
        self._workspace = census.JAIL_WORKSPACE
        self.addCleanup(setattr, census, "ROOT", self._root)
        self.addCleanup(setattr, census, "JAIL_WORKSPACE", self._workspace)
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.tmp = Path(tmp.name).resolve()
        for d in ("pkg", "other"):
            (self.tmp / d).mkdir()
        (self.tmp / "other" / "data.txt").write_text("x")

    def parse(self, root, lines):
        census.ROOT = Path(root)
        return census.parse_log("# test log\n" + "\n".join(lines) + "\n",
                                str(Path(root) / "pkg"), {"pkg", "other"}, Everything())

    def test_the_jail_workspace_probe_is_not_a_root_listing_when_the_checkout_is_workspace(self):
        probe = ["getenv YOLO_WORKSPACE", "stat /workspace"]
        self.assertEqual(self.parse("/workspace", probe), (set(), set(), set()))

    def test_the_probe_reads_the_same_from_every_checkout(self):
        probe = ["getenv YOLO_WORKSPACE", "stat /workspace", "stat " + str(self.tmp)]
        here = self.parse(self.tmp, probe)
        at_workspace = self.parse("/workspace", ["getenv YOLO_WORKSPACE", "stat /workspace"])
        self.assertEqual(here, at_workspace)
        self.assertEqual(here, (set(), set(), set()))

    def test_a_stat_of_the_root_by_a_relative_spelling_is_not_a_listing(self):
        self.assertEqual(self.parse(self.tmp, ["stat ..", "stat ../."]), (set(), set(), set()))

    def test_opening_the_root_is_still_a_listing_by_every_spelling(self):
        for spelling in (str(self.tmp), "..", "../pkg/.."):
            with self.subTest(spelling=spelling):
                self.assertEqual(self.parse(self.tmp, ["open " + spelling]), (set(), {"."}, set()))

    def test_the_jail_workspace_mise_toml_is_no_reader_from_another_checkout(self):
        # Production's retire surgery opens the literal /workspace/mise.toml. From any checkout
        # but /workspace that is not the repository's file, so no reader is recorded.
        got = self.parse(self.tmp, ["stat /workspace", "open /workspace/mise.toml"])
        self.assertEqual(got, (set(), set(), set()))

    def test_an_open_under_the_jail_workspace_outside_the_checkout_is_named(self):
        census.ROOT = self.tmp
        log = "\n".join(["# test log", "stat /workspace", "stat /workspace/.claude",
                         "open /workspace/mise.toml", "stat /workspace/flake.nix",
                         "open " + str(self.tmp / "other" / "data.txt"), "open ../other/data.txt"])
        self.assertEqual(census.jail_workspace_opens(log, str(self.tmp / "pkg")),
                         {"/workspace/mise.toml"})

    def test_an_open_of_the_checkout_that_is_the_jail_workspace_is_a_read_not_a_leak(self):
        census.ROOT = Path("/workspace")
        self.assertEqual(census.jail_workspace_opens("open /workspace/mise.toml\n", "/workspace/pkg"),
                         set())

    def test_a_test_that_opens_the_jail_workspace_stops_the_census_with_its_fix(self):
        census.refuse_jail_workspace_opens({})  # nothing leaked: no refusal
        with self.assertRaises(census.CensusError) as caught:
            census.refuse_jail_workspace_opens(
                {("internal/x", "TestReadsTheLiveCheckout"): {"/workspace/mise.toml"},
                 ("internal/y", "TestAlsoReadsIt"): {"/workspace/mise.toml"}})
        message = str(caught.exception)
        for part in ("internal/x TestReadsTheLiveCheckout", "internal/y TestAlsoReadsIt",
                     "/workspace/mise.toml", "Env.Workspace",
                     "rerun scripts/completion-census.py"):
            self.assertIn(part, message)

    def test_other_directories_and_files_are_still_recorded(self):
        got = self.parse(self.tmp, ["stat " + str(self.tmp / "other"),
                                    "open ../other/data.txt", "stat ../missing"])
        self.assertEqual(got, ({"other/data.txt"}, {"other"}, {"missing"}))

    def _fixture_namespace(self):
        case = self.tmp / f"case-{len(list(self.tmp.iterdir()))}"
        # Model a jail checkout under a controlled workspace separate from TMPDIR. These
        # classifier tests must not depend on the runner's temp path being under /workspace.
        workspace = case / "workspace"
        self.assertNotEqual(os.path.commonpath((str(self.tmp), str(workspace))), str(workspace))
        durable = workspace / ".yolo" / "durable"
        source = durable / "worktrees" / "source"
        source.mkdir(parents=True)
        fixture = durable / "cc-fixture"
        fixture.mkdir(mode=0o700)
        os.chmod(fixture, 0o700)
        marker = fixture / ".completion-census-fixture"
        marker.write_text("completion-census dedicated fixture root v1\n")
        os.chmod(marker, 0o600)
        env = {"TMPDIR": str(fixture), "YOLO_CENSUS_FIXTURE_ROOT": str(fixture)}
        census.ROOT = source.resolve()
        census.JAIL_WORKSPACE = str(workspace.resolve())
        return source, fixture, env

    def test_dedicated_fixture_namespace_is_allowed_but_source_is_not(self):
        source, fixture, env = self._fixture_namespace()
        allowed = census.validate_fixture_root(source, env)
        self.assertEqual(allowed, fixture.resolve())
        self.assertFalse((fixture / census.FIXTURE_ROOT_MARKER).exists())
        with self.assertRaises(census.CensusError):
            census.validate_fixture_root(source, env)
        log = "open " + str(fixture / "TestCase123" / "001" / "fixture.txt")
        self.assertEqual(census.jail_workspace_opens(log, str(source / "pkg"), allowed), set())
        removed = fixture / "RemovedFixture" / "missing.txt"
        self.assertEqual(census.jail_workspace_opens("open " + str(removed), str(source / "pkg"), allowed), set())
        source_file = source / "internal" / "source.go"
        source_file.parent.mkdir()
        source_file.write_text("source")
        log = "open " + str(source_file)
        self.assertEqual(census.jail_workspace_opens(log, str(source / "pkg"), allowed), set())

    def test_fixture_prefix_confusion_and_symlink_escape_remain_leaks(self):
        source, fixture, env = self._fixture_namespace()
        allowed = census.validate_fixture_root(source, env)
        source_file = source / "internal" / "source.go"
        source_file.parent.mkdir()
        source_file.write_text("source")
        alias = fixture / "source-alias.go"
        alias.symlink_to(source_file)
        sibling = fixture.with_name(fixture.name + "-other") / "secret"
        sibling_worktree = source.parent / "other" / "internal" / "source.go"
        log = "open " + str(alias) + "\nopen " + str(sibling) + "\nopen " + str(sibling_worktree)
        leaks = census.jail_workspace_opens(log, str(source / "pkg"), allowed)
        self.assertEqual(leaks, {str(alias.resolve()), str(sibling), str(sibling_worktree)})

    def test_dedicated_fixture_configuration_rejects_broad_and_unsafe_roots(self):
        source, fixture, env = self._fixture_namespace()
        for path in (Path("/workspace"), source.parent.parent, source.parent, source, self.tmp):
            with self.subTest(path=path):
                bad_env = {"TMPDIR": str(path), "YOLO_CENSUS_FIXTURE_ROOT": str(path)}
                with self.assertRaises(census.CensusError):
                    census.validate_fixture_root(source, bad_env)
        bad_env = dict(env, YOLO_CENSUS_FIXTURE_ROOT=str(fixture) + "-other")
        with self.assertRaises(census.CensusError):
            census.validate_fixture_root(source, bad_env)

    def test_dedicated_fixture_rejects_symlinked_root_and_existing_contents(self):
        source, fixture, env = self._fixture_namespace()
        marker = fixture / ".completion-census-fixture"
        marker.unlink()
        (fixture / "source.go").write_text("not an empty fixture namespace")
        with self.assertRaises(census.CensusError):
            census.validate_fixture_root(source, env)

        source, fixture, env = self._fixture_namespace()
        link = fixture.parent / "cc-link"
        link.symlink_to(fixture, target_is_directory=True)
        bad_env = {"TMPDIR": str(link), "YOLO_CENSUS_FIXTURE_ROOT": str(link)}
        with self.assertRaises(census.CensusError):
            census.validate_fixture_root(source, bad_env)

    def test_fixture_target_resolution_errors_are_not_exempt(self):
        source, fixture, env = self._fixture_namespace()
        allowed = census.validate_fixture_root(source, env)
        loop = fixture / "loop"
        loop.symlink_to(loop)
        leaks = census.jail_workspace_opens("open " + str(loop), str(source / "pkg"), allowed)
        self.assertEqual(leaks, {str(loop)})

        denied = fixture / "denied"
        denied.write_text("fixture file")
        original_resolve = Path.resolve

        def deny_fixture_target(path, *args, **kwargs):
            if path == denied:
                raise PermissionError("injected unresolvable fixture path")
            return original_resolve(path, *args, **kwargs)

        with mock.patch.object(Path, "resolve", deny_fixture_target), \
                mock.patch.object(census.os, "open", side_effect=PermissionError("injected root denial")):
            leaks = census.jail_workspace_opens("open " + str(denied), str(source / "pkg"), allowed)
        self.assertEqual(leaks, {str(denied)})

    def test_unresolvable_long_fixture_path_is_walked_from_its_private_anchor(self):
        source, fixture, env = self._fixture_namespace()
        allowed = census.validate_fixture_root(source, env)
        source_file = source / "internal" / "source.go"
        source_file.parent.mkdir()
        source_file.write_text("outside")

        components = ["b" * 200 for _ in range(22)]
        flags = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW
        descriptors = [os.open(fixture, flags)]
        alias_created = loop_created = False
        try:
            for component in components:
                os.mkdir(component, 0o755, dir_fd=descriptors[-1])
                descriptors.append(os.open(component, flags, dir_fd=descriptors[-1]))
            deep_missing = fixture.joinpath(*components, "leaf.md")
            deep_alias = fixture.joinpath(*components, "outside.md")
            os.symlink(str(source_file), "outside.md", dir_fd=descriptors[-1])
            alias_created = True
            loop = fixture / "loop"
            os.symlink(str(loop), "loop", dir_fd=descriptors[0])
            loop_created = True
            deep_loop = loop.joinpath(*components, "leaf.md")
            self.assertGreater(len(os.fsencode(deep_missing)), 4096)
            self.assertEqual(census.jail_workspace_opens(
                "open " + str(deep_missing), str(source / "pkg"), allowed), set())
            self.assertEqual(census.jail_workspace_opens(
                "open " + str(deep_alias), str(source / "pkg"), allowed), {str(deep_alias)})
            self.assertEqual(census.jail_workspace_opens(
                "open " + str(deep_loop), str(source / "pkg"), allowed), {str(deep_loop)})
        finally:
            if alias_created:
                os.unlink("outside.md", dir_fd=descriptors[-1])
            if loop_created:
                os.unlink("loop", dir_fd=descriptors[0])
            for index in range(len(descriptors) - 1, 0, -1):
                os.close(descriptors[index])
                os.rmdir(components[index - 1], dir_fd=descriptors[index - 1])
            os.close(descriptors[0])

    def test_fixture_symlink_parent_components_are_not_lexically_collapsed(self):
        source, fixture, env = self._fixture_namespace()
        allowed = census.validate_fixture_root(source, env)
        source_dir = source / "internal"
        source_dir.mkdir()
        source_file = source / "source.go"
        source_file.write_text("checkout bytes")
        link = fixture / "link"
        link.symlink_to(source_dir, target_is_directory=True)

        through_parent = fixture / "link" / ".." / "source.go"
        self.assertEqual(census.jail_workspace_opens(
            "open " + str(through_parent), str(source / "pkg"), allowed), {str(source_file)})

        components = ["c" * 200 for _ in range(22)]
        flags = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW
        descriptors = [os.open(source, flags)]
        try:
            for component in components:
                os.mkdir(component, 0o755, dir_fd=descriptors[-1])
                descriptors.append(os.open(component, flags, dir_fd=descriptors[-1]))
            deep_parent = fixture.joinpath("link", "..", *components, "leaf.md")
            self.assertGreater(len(os.fsencode(deep_parent)), 4096)
            self.assertEqual(census.jail_workspace_opens(
                "open " + str(deep_parent), str(source / "pkg"), allowed), {str(deep_parent)})
        finally:
            for index in range(len(descriptors) - 1, 0, -1):
                os.close(descriptors[index])
                os.rmdir(components[index - 1], dir_fd=descriptors[index - 1])
            os.close(descriptors[0])

    def test_relative_parent_path_to_fixture_is_resolved_from_package_cwd(self):
        source, fixture, env = self._fixture_namespace()
        allowed = census.validate_fixture_root(source, env)
        pkgdir = source / "pkg"
        pkgdir.mkdir()
        fixture_file = fixture / "reader path with spaces and 'quotes" / "yolo-jail.jsonc"
        fixture_file.parent.mkdir()
        fixture_file.write_text("fixture")
        relative = os.path.relpath(fixture_file, pkgdir)
        fixture_file.unlink()
        fixture_file.parent.rmdir()

        self.assertIn("..", Path(relative).parts)
        self.assertEqual(census.jail_workspace_opens(
            "open " + relative, str(pkgdir), allowed), set())

    def test_configured_fixture_root_does_not_hide_primary_workspace_reads(self):
        source, fixture, env = self._fixture_namespace()
        allowed = census.validate_fixture_root(source, env)
        census.JAIL_WORKSPACE = "/workspace"
        path = "/workspace/internal/source.go"
        leaks = census.jail_workspace_opens("open " + path, str(source / "pkg"), allowed)
        self.assertEqual(leaks, {path})

    def test_unconfigured_fixture_root_does_not_exempt_external_workspace_reads(self):
        source, fixture, env = self._fixture_namespace()
        path = str(Path(census.JAIL_WORKSPACE) / "internal" / "source.go")
        leaks = census.jail_workspace_opens("open " + path, str(source / "pkg"))
        self.assertEqual(leaks, {path})


# A stand-in test binary: it writes its test log, and, like a test whose child lost the parent's
# TMPDIR, makes a testtmp.Isolate directory of its session in /tmp and exits without removing it.
LEAKY_TEST_BINARY = """#!{python}
import os, random, sys
def b36(n, w):
    s = ''
    while n: n, r = divmod(n, 36); s = '0123456789abcdefghijklmnopqrstuvwxyz'[r] + s
    return s.rjust(w, '0')[-w:]
log = [a for a in sys.argv if a.startswith('-test.testlogfile=')][0].split('=', 1)[1]
open(log, 'w').write('')
if os.environ.get('LEAK'):
    os.mkdir('/tmp/tt' + b36(os.getsid(0), 5) + b36(random.randrange(36**4), 4))
"""


class SessionLeftoverTest(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.tmp = Path(tmp.name)
        self.binary = self.tmp / "pkg.test"
        self.binary.write_text(LEAKY_TEST_BINARY.format(python=sys.executable))
        self.binary.chmod(0o755)
        self.gate = census.load_leak_gate()

    def run_one(self, leak):
        with mock.patch.dict(os.environ, {"LEAK": "1" if leak else ""}):
            return census.run_test(self.binary, self.tmp, "TestX", self.tmp / "x.log", self.gate)

    def test_a_tests_leftover_in_tmp_is_named_and_removed(self):
        _, left = self.run_one(leak=True)
        self.assertEqual(len(left), 1, left)
        self.addCleanup(lambda: os.path.isdir(left[0]) and os.rmdir(left[0]))
        self.assertTrue(os.path.basename(left[0]).startswith("tt"), left)
        self.assertFalse(os.path.exists(left[0]), "the census must remove what the test left")
        with self.assertRaises(census.CensusError) as cm:
            census.refuse_session_leftovers({("pkg", "TestX"): left}, self.gate)
        self.assertIn("pkg TestX", str(cm.exception))
        self.assertIn("testtmp.ChildEnv", str(cm.exception))

    def test_a_clean_test_leaves_nothing_to_refuse(self):
        _, left = self.run_one(leak=False)
        self.assertEqual(left, [])
        census.refuse_session_leftovers({}, self.gate)


class SharedTestCacheTest(unittest.TestCase):
    """The census removes the embedded-pack cache base its test binaries share in TMPDIR
    (internal/packload, TestSharedCachePrefix) once they have all run, or the leak gate it runs
    under fails every census on it."""

    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.tmp = Path(tmp.name)
        self.base = self.tmp / f"yolo-test-embedded-packs-{os.geteuid()}"
        tree = self.base / "0123abcd" / "claude"
        tree.mkdir(parents=True)
        (tree / "pack.toml").write_text("")
        (self.base / "0123abcd" / ".lease").write_text("")
        os.chmod(tree, 0o555)  # the cache tree is immutable
        os.chmod(self.base / "0123abcd", 0o555)
        self.addCleanup(lambda: os.path.isdir(self.base) and census.load_leak_gate().remove_tree(self.base))

    def test_a_census_run_removes_the_shared_cache_base_its_tests_left(self):
        other = self.tmp / "yolo-test-embedded-packs-99999"  # another euid's name: not ours
        other.mkdir()
        out = self.tmp / "readers.json"
        with mock.patch.object(census, "go_packages", return_value=([], set())), \
                mock.patch.object(census, "tracked_dirs", return_value={}), \
                mock.patch.object(census, "GitVisibility", return_value=Everything()), \
                mock.patch.object(census, "validate_fixture_root", return_value=None), \
                mock.patch.object(census, "OUT", out), \
                mock.patch.object(census, "ROOT", self.tmp), \
                mock.patch.object(tempfile, "tempdir", str(self.tmp)):
            census.main()
        self.assertTrue(out.exists(), "the stubbed census did not run to its output")
        self.assertFalse(self.base.exists(), "the census left its tests' shared cache base in TMPDIR")
        self.assertTrue(other.exists(), "the census removed a base that is not this euid's")

    def test_a_failed_census_removes_it_too(self):
        with mock.patch.object(census, "validate_fixture_root", side_effect=census.CensusError("x")), \
                mock.patch.object(tempfile, "tempdir", str(self.tmp)):
            with self.assertRaises(census.CensusError):
                census.main()
        self.assertFalse(self.base.exists(), "a failed census left its tests' shared cache base in TMPDIR")


class GatedArgvTest(unittest.TestCase):
    def test_an_ordinary_run_reruns_itself_under_the_leak_gate(self):
        argv = census.gated_argv({}, ["completion-census.py"])
        self.assertTrue(argv[1].endswith("tmp-leak-gate.py"), argv)
        self.assertTrue(argv[3].endswith("completion-census.py"), argv)

    def test_a_gated_or_fixture_run_does_not(self):
        self.assertIsNone(census.gated_argv({"TMP_LEAK_GATE_DIR": "/tmp/yt.x"}, ["c"]))
        self.assertIsNone(census.gated_argv({census.FIXTURE_ROOT_ENV: "/f"}, ["c"]))


if __name__ == "__main__":
    unittest.main()
