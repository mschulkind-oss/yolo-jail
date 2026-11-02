#!/usr/bin/env python3
"""Snapshot, packaging, and standalone wrapper regressions."""
import importlib.util
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import sys
import tempfile
import unittest
from types import SimpleNamespace

HERE = Path(__file__).resolve().parent
SPEC = importlib.util.spec_from_file_location("matched_bundle", HERE / "matched-bundle.py")
mb = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(mb)


def clean_git_env():
    return {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}


def repo_fixture(root: Path, *, real_scripts=False):
    repo = root / "checkout with spaces"
    repo.mkdir(parents=True)
    (repo / ".gitignore").write_text("ignored.txt\ninternal/generated/\n")
    for rel, text in {"go.mod": "module test.invalid\n", "flake.nix": "{}\n",
                      "flake.lock": "{}\n", "Justfile": "set positional-arguments\ndev *args:\n    ./scripts/dev-wrapper.sh \"$@\"\n",
                      "cmd/yolo/main.go": "package main\n",
                      "internal/generated/tracked.go": "package generated\n"}.items():
        path = repo / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text)
    scripts = repo / "scripts"
    scripts.mkdir()
    for name in ("build-go.sh", "stage-source-bundle.sh", "matched-bundle.py", "dev-wrapper.sh"):
        if real_scripts:
            shutil.copy2(HERE / name, scripts / name)
        elif name == "stage-source-bundle.sh":
            (scripts / name).write_text("SHIPPED_BINARIES=(yolo yolo-helper)\nGUEST_BINARIES=(guest-helper)\n")
        else:
            (scripts / name).write_text(f"fixture {name}\n")
    env = clean_git_env()
    subprocess.run(["git", "init", "-q"], cwd=repo, env=env, check=True)
    if real_scripts:
        # The Justfile's install recipe goes through the slot helper, which is not a build input.
        shutil.copy2(HERE / "launcher-slot.py", scripts / "launcher-slot.py")
    subprocess.run(["git", "add", "go.mod", "flake.nix", "flake.lock", "Justfile", "scripts", "cmd"],
                   cwd=repo, env=env, check=True)
    subprocess.run(["git", "add", "-f", "internal/generated/tracked.go"], cwd=repo, env=env, check=True)
    subprocess.run(["git", "-c", "user.name=test", "-c", "user.email=test@example.invalid",
                    "commit", "-qm", "base"], cwd=repo, env=env, check=True)
    return repo


def _fake_helper_dirs(bundle: Path, snapshot: Path):
    shipped, guest = mb._binary_lists(snapshot / "scripts/stage-source-bundle.sh")
    host_os, host_arch = mb._platform(snapshot)
    targets = {f"linux-{host_arch}": shipped}
    if host_os == "darwin":
        targets[f"darwin-{host_arch}"] = guest
    for dirname, names in targets.items():
        directory = bundle / "bin" / dirname
        directory.mkdir(parents=True)
        for name in names:
            binary = directory / name
            binary.write_bytes(b"helper")
            binary.chmod(0o755)


def fake_assembler(snapshot, output, *, identity, build_kind, version="", commit="", **_):
    output.mkdir()
    (output / "bin").mkdir()
    launcher = output / "bin/yolo"
    launcher.write_text("#!/bin/sh\nprintf '%s\\n' 'snapshot identity: " + identity + "' 'build kind: " + build_kind
                        + "' 'build stamp: " + (version or "unstamped") + "' 'commit: " + (commit or "unknown") + "'\n")
    launcher.chmod(0o755)
    bundle = output / "share/yolo-jail"
    bundle.mkdir(parents=True)
    _fake_helper_dirs(bundle, snapshot)
    (bundle / "flake.nix").write_text("flake")
    (bundle / "flake.lock").write_text("lock")
    (bundle / "yolo-build-stamp.json").write_text(json.dumps({"version": version, "commit": commit,
                                                              "snapshotIdentity": identity, "buildKind": build_kind}))


class SnapshotTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name).resolve()
        self.repo = repo_fixture(self.root)

    def tearDown(self):
        self.tmp.cleanup()

    def test_manifest_captures_dirty_and_new_source_but_excludes_ignored_tracked_and_untracked(self):
        (self.repo / "cmd/yolo/main.go").write_text("package main // dirty\n")
        (self.repo / "internal/new.go").write_text("package internal\n")
        (self.repo / "ignored.txt").write_text("secret\n")
        (self.repo / "internal/generated/ignored.txt").write_text("secret\n")
        with tempfile.TemporaryDirectory() as td:
            snapshot = Path(td) / "snapshot"
            identity = mb.freeze_snapshot(self.repo, snapshot)
            self.assertEqual((snapshot / "cmd/yolo/main.go").read_text(), "package main // dirty\n")
            self.assertTrue((snapshot / "internal/new.go").is_file())
            self.assertFalse((snapshot / "ignored.txt").exists())
            self.assertFalse((snapshot / "internal/generated/ignored.txt").exists())
            self.assertFalse((snapshot / "internal/generated/tracked.go").exists())
            self.assertFalse((snapshot / ".git").exists())
            self.assertEqual(identity, mb.snapshot_identity(snapshot))

    def test_platform_uses_native_host_despite_target_overrides(self):
        from unittest import mock
        calls = []
        native = tuple(subprocess.check_output(["go", "env", key], text=True).strip()
                       for key in ("GOHOSTOS", "GOHOSTARCH"))

        def check_output(argv, **_):
            calls.append(argv)
            return native[0 if argv[-1] == "GOHOSTOS" else 1] + "\n"

        with mock.patch.dict(os.environ, {"GOOS": "darwin", "GOARCH": "arm64"}), \
             mock.patch.object(mb.subprocess, "check_output", side_effect=check_output):
            self.assertEqual(mb._platform(self.repo), native)
        self.assertEqual(calls, [["go", "env", "GOHOSTOS"], ["go", "env", "GOHOSTARCH"]])

    def test_subprocess_environment_scrubs_repo_root_without_changing_parent(self):
        from unittest import mock
        calls=[]
        def check_output(argv, **kw):
            calls.append((argv, kw.get("env")))
            if argv[0] == "go":
                return "linux\n" if argv[-1] == "GOHOSTOS" else "amd64\n"
            return "" if kw.get("text") else b""
        def run(argv, **kw):
            calls.append((argv, kw.get("env")))
            return SimpleNamespace(stdout=b"")
        with mock.patch.dict(os.environ, {"YOLO_REPO_ROOT":"/not-a-child-path"}):
            with mock.patch.object(mb.subprocess, "check_output", side_effect=check_output), \
                 mock.patch.object(mb.subprocess, "run", side_effect=run):
                self.assertEqual(mb._platform(self.repo), ("linux", "amd64"))
                mb._source_stamps(self.repo)
                mb._git_paths(self.repo)
            self.assertEqual(os.environ["YOLO_REPO_ROOT"], "/not-a-child-path")
        self.assertGreater(len(calls), 0)
        for argv, env in calls:
            self.assertIsNotNone(env, f"{argv} inherited the parent environment directly")
            self.assertNotIn("YOLO_REPO_ROOT", env, f"{argv} received the obsolete selector")

    def test_git_state_env_cannot_redirect_the_manifest(self):
        other = self.root / "other"
        other.mkdir()
        subprocess.run(["git", "init", "-q"], cwd=other, check=True, env=clean_git_env())
        keys = ("GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE")
        old = {k: os.environ.get(k) for k in keys}
        os.environ.update({"GIT_DIR": str(other / ".git"), "GIT_WORK_TREE": str(other),
                           "GIT_INDEX_FILE": str(other / "index")})
        try:
            paths = mb.collect_inputs(self.repo)
        finally:
            for k, v in old.items():
                if v is None:
                    os.environ.pop(k, None)
                else:
                    os.environ[k] = v
        self.assertIn("cmd/yolo/main.go", paths)

    def test_external_and_ignored_symlinks_fail_before_following_targets(self):
        source = self.repo / "cmd/yolo/main.go"
        outside = self.root / "outside.go"
        outside.write_text("package main\n")
        source.unlink()
        source.symlink_to(outside)
        with self.assertRaisesRegex(ValueError, "symlink"):
            mb.collect_inputs(self.repo)
        source.unlink()
        source.symlink_to("../../internal/generated/tracked.go")
        with self.assertRaisesRegex(ValueError, "not in snapshot"):
            mb.collect_inputs(self.repo)

    def test_copied_build_scripts_run_from_frozen_tree_after_live_edit(self):
        self.repo = repo_fixture(self.root / "second")
        with tempfile.TemporaryDirectory() as td:
            snapshot = Path(td).resolve() / "snapshot"
            identity = mb.freeze_snapshot(self.repo, snapshot)
            (self.repo / "scripts/build-go.sh").write_text("changed live script\n")
            goos = subprocess.check_output(["go", "env", "GOHOSTOS"], text=True).strip()
            goarch = subprocess.check_output(["go", "env", "GOHOSTARCH"], text=True).strip()
            launcher = "#!/bin/sh\nprintf '%s\\n' 'snapshot identity: " + identity + "' 'build kind: development'\n"
            seen = []
            def run(argv, *, cwd, env, check, stdout):
                seen.append(Path(argv[0]))
                # Progress goes to stderr: the caller's stdout carries the launcher path, and a
                # deploy discards it.
                self.assertEqual(stdout, 2)
                self.assertNotIn("YOLO_REPO_ROOT", env)
                self.assertEqual(cwd, snapshot)
                self.assertTrue(Path(argv[0]).is_relative_to(snapshot))
                if str(argv[0]).endswith("build-go.sh"):
                    self.assertEqual(Path(argv[0]).read_text(), "fixture build-go.sh\n")
                    binary = snapshot / "dist-go" / f"{goos}-{goarch}" / "yolo"
                    binary.parent.mkdir(parents=True)
                    binary.write_text(launcher)
                    binary.chmod(0o755)
                else:
                    self.assertEqual(Path(argv[0]).read_text(), (snapshot / "scripts/stage-source-bundle.sh").read_text())
                    dest = Path(argv[1]); dest.mkdir(parents=True)
                    (dest / "flake.nix").write_text("flake")
                    (dest / "flake.lock").write_text("lock")
                    _fake_helper_dirs(dest, snapshot)
                    (dest / "bin" / f"linux-{goarch}" / "yolo").write_text(launcher)
                    (dest / "yolo-build-stamp.json").write_text(json.dumps({"snapshotIdentity": identity, "buildKind": "development"}))
                return SimpleNamespace(returncode=0)
            output = Path(td) / "bundle"
            mb.assemble(snapshot, output, identity=identity, build_kind="development",
                        version="v", commit="c", runner=run)
            # Linux: the stage alone, whose linux yolo IS the launcher. macOS: the stage, then
            # the one darwin build of yolo, which the bundle's guest set does not include.
            self.assertEqual(len(seen), 1 if goos == "linux" else 2)

    def test_assemble_builds_yolo_once_and_the_launcher_is_the_bundles_copy(self):
        """The real build-go.sh and stage-source-bundle.sh, with a fake go that records every
        build: an assemble compiles cmd/yolo exactly once for the host platform, and on Linux
        bin/yolo is byte-identical to the bundle's bin/linux-<arch>/yolo. The fake go reports
        the host, so every case runs on every machine: the real host, and forced Linux and
        macOS hosts (a Mac host's stage also builds the jail's linux yolo, a different
        program by nature, which must not count as a second build of the launcher)."""
        real_os = subprocess.check_output(["go", "env", "GOHOSTOS"], text=True).strip()
        real_arch = subprocess.check_output(["go", "env", "GOHOSTARCH"], text=True).strip()
        for host_os, host_arch in [(real_os, real_arch), ("linux", "amd64"), ("darwin", "arm64")]:
            with self.subTest(host=f"{host_os}/{host_arch}"):
                self._assert_one_yolo_build(host_os, host_arch)

    def _assert_one_yolo_build(self, host_os: str, host_arch: str):
        root = self.root / f"one-build-{host_os}-{host_arch}"
        if root.exists():
            shutil.rmtree(root)
        repo = repo_fixture(root, real_scripts=True)
        shipped, guest = mb._binary_lists(repo / "scripts/stage-source-bundle.sh")
        for name in {*shipped, *guest, "goprobe"}:
            (repo / "cmd" / name).mkdir(parents=True, exist_ok=True)
            (repo / "cmd" / name / "main.go").write_text("package main\n")
        env = clean_git_env()
        subprocess.run(["git", "add", "cmd"], cwd=repo, env=env, check=True)
        subprocess.run(["git", "-c", "user.name=test", "-c", "user.email=test@example.invalid",
                        "commit", "-qm", "cmds"], cwd=repo, env=env, check=True)
        fakebin = root / "fakebin"; fakebin.mkdir()
        log = root / "builds.log"
        fake = fakebin / "go"
        fake.write_text(f"""#!{sys.executable}
import os, pathlib, re, sys
args = sys.argv[1:]
if args[:1] == ['env']:
    values = {{'GOHOSTOS': {host_os!r}, 'GOHOSTARCH': {host_arch!r}, 'GOOS': os.environ.get('GOOS', {host_os!r}),
              'GOARCH': os.environ.get('GOARCH', {host_arch!r})}}
    print('\\n'.join(values.get(k, '') for k in args[1:])); raise SystemExit(0)
if args[:1] != ['build']:
    raise SystemExit('fake go: unexpected ' + ' '.join(args))
pkg = args[-1]; out = pathlib.Path(args[args.index('-o') + 1])
ldflags = args[args.index('-ldflags') + 1]
stamp = dict(re.findall(r'internal/version\\.(\\w+)=(\\S*)', ldflags))
with open(os.environ['YOLO_TEST_BUILDS'], 'a') as f:
    f.write(f"{{os.environ['GOOS']}}/{{os.environ['GOARCH']}} {{pkg}}\\n")
out.parent.mkdir(parents=True, exist_ok=True)
out.write_text("#!/bin/sh\\nprintf '%s\\\\n' 'snapshot identity: " + stamp.get('SnapshotIdentity', '') + "' 'build kind: "
               + stamp.get('BuildKind', '') + "' 'built for: " + os.environ['GOOS'] + "/" + os.environ['GOARCH'] + "'\\n")
out.chmod(0o755)
""")
        fake.chmod(0o755)
        from unittest import mock
        with tempfile.TemporaryDirectory() as td, \
             mock.patch.dict(os.environ, {"PATH": str(fakebin) + os.pathsep + os.environ["PATH"],
                                          "YOLO_TEST_BUILDS": str(log)}):
            snapshot = Path(td).resolve() / "snapshot"
            identity = mb.freeze_snapshot(repo, snapshot)
            output = Path(td) / "bundle"
            mb.assemble(snapshot, output, identity=identity, build_kind="installed",
                        version="v1", commit="c1")
            builds = log.read_text().splitlines()
            yolo_builds = [b for b in builds if b.endswith(" ./cmd/yolo")]
            host_build = f"{host_os}/{host_arch} ./cmd/yolo"
            self.assertEqual(yolo_builds.count(host_build), 1,
                             f"cmd/yolo must be compiled exactly once for the host platform: {builds}")
            self.assertEqual(len(yolo_builds), len(set(yolo_builds)),
                             f"a platform's yolo was compiled twice: {builds}")
            self.assertTrue(all(b == host_build or b.startswith("linux/") for b in yolo_builds),
                            f"cmd/yolo was built for a platform that is neither the host nor the jail: {builds}")
            launcher = (output / "bin/yolo").read_bytes()
            share = output / "share/yolo-jail/bin"
            if host_os == "linux":
                self.assertEqual(launcher, (share / f"linux-{host_arch}/yolo").read_bytes(),
                                 "the installed launcher is not the bundle's own linux yolo")
            else:
                self.assertFalse((share / f"darwin-{host_arch}/yolo").exists(),
                                 "the darwin guest set gained a yolo; the launcher must then be that file")
                self.assertIn(f"built for: darwin/{host_arch}", launcher.decode())

    def test_build_go_stamps_identity_and_no_checkout_path(self):
        root = self.root / "build"
        (root / "cmd/yolo").mkdir(parents=True); (root / "scripts").mkdir()
        shutil.copy2(HERE / "build-go.sh", root / "scripts/build-go.sh")
        fakebin = root / "fakebin"; fakebin.mkdir()
        log = root / "args"
        fake = fakebin / "go"
        fake.write_text("""#!/usr/bin/env python3
import os, pathlib, sys
args=sys.argv[1:]
if args[0]=='build':
 pathlib.Path(os.environ['LOG']).write_text(' '.join(args))
 out=pathlib.Path(args[args.index('-o')+1]); out.parent.mkdir(parents=True, exist_ok=True); out.write_bytes(b'x')
"""); fake.chmod(0o755)
        env = os.environ.copy(); env.update({"PATH": str(fakebin)+os.pathsep+env["PATH"], "LOG": str(log),
            "GOOS":"linux", "GOARCH":"amd64", "SOURCE_DIR":"/tmp/space path", "SOURCE_BRANCH":"topic",
            "SNAPSHOT_IDENTITY":"a"*64, "BUILD_KIND":"installed"})
        subprocess.run([str(root/"scripts/build-go.sh")], cwd=root, env=env,
                       text=True, capture_output=True, check=True)
        args = log.read_text()
        self.assertIn("SnapshotIdentity="+"a"*64, args); self.assertIn("BuildKind=installed", args)
        # An install is a snapshot: a stale SOURCE_DIR/SOURCE_BRANCH in the environment
        # stamps nothing about the checkout (OQ-IP1).
        self.assertNotIn("SourceDir", args); self.assertNotIn("SourceBranch", args)


class PackageTests(unittest.TestCase):
    def test_build_cache_reuses_complete_identity_and_rebuilds_dirty_or_incomplete(self):
        with tempfile.TemporaryDirectory() as td:
            root=Path(td); repo=repo_fixture(root); home=root/"home"; calls=[]
            def builder(snapshot, output, *, identity, build_kind, **kw):
                calls.append(identity); fake_assembler(snapshot, output, identity=identity, build_kind=build_kind, **kw)
            first=mb.build_dev_bundle(repo, home, builder=builder)
            self.assertEqual(first, mb.build_dev_bundle(repo, home, builder=builder))
            self.assertEqual(len(calls), 1)
            (repo/"cmd/yolo/main.go").write_text("package main // dirty\n")
            dirty=mb.build_dev_bundle(repo, home, builder=builder)
            self.assertNotEqual(first, dirty); self.assertNotEqual(calls[0], calls[1])
            cache=dirty.parent.parent
            _,host_arch=mb._platform(repo)
            for directory in (cache,cache/"share",cache/"share/yolo-jail",cache/"share/yolo-jail/bin",
                              cache/f"share/yolo-jail/bin/linux-{host_arch}"):
                directory.chmod(0o755)
            helper=cache/f"share/yolo-jail/bin/linux-{host_arch}/yolo-helper"
            helper.chmod(0o644); helper.unlink()
            rebuilt=mb.build_dev_bundle(repo, home, builder=builder)
            self.assertNotEqual(dirty, rebuilt); self.assertEqual(len(calls), 3)

    def test_a_publish_runs_the_new_launchers_reaper_and_a_reuse_touches_instead(self):
        # One rule, in Go: the Python side only hands the freshly published launcher the
        # directory, and marks a reused bundle as recently used so the reaper keeps it.
        with tempfile.TemporaryDirectory() as td:
            root=Path(td); repo=repo_fixture(root); home=root/"home"; reaped=[]
            def reaper(launcher, directory): reaped.append((launcher, directory))
            first=mb.build_dev_bundle(repo, home, builder=fake_assembler, reaper=reaper)
            self.assertEqual(reaped, [(first, (home/".local/share/yolo-jail/dev-bundles").resolve())])
            bundle=first.parent.parent
            os.utime(bundle, (1_000_000, 1_000_000))
            self.assertEqual(first, mb.build_dev_bundle(repo, home, builder=fake_assembler, reaper=reaper))
            self.assertEqual(len(reaped), 1, "a reuse ran the reaper")
            self.assertGreater(bundle.stat().st_mtime, 1_000_000, "a reuse did not mark the bundle used")

    def test_a_candidate_that_cannot_be_touched_is_not_reused(self):
        # The reaper renames a bundle aside before deleting it; a touch that fails may be one
        # racing that removal, so the candidate is skipped and a fresh bundle is built.
        with tempfile.TemporaryDirectory() as td:
            root=Path(td); repo=repo_fixture(root); home=root/"home"; calls=[]
            def builder(snapshot, output, **kw):
                calls.append(kw["identity"]); fake_assembler(snapshot, output, **kw)
            first=mb.build_dev_bundle(repo, home, builder=builder, reaper=lambda *a: None)
            original=mb._touch_reused_bundle
            mb._touch_reused_bundle=lambda bundle: False
            try:
                second=mb.build_dev_bundle(repo, home, builder=builder, reaper=lambda *a: None)
            finally:
                mb._touch_reused_bundle=original
            self.assertNotEqual(first, second); self.assertEqual(len(calls), 2)
            self.assertFalse(mb._touch_reused_bundle(root/"missing-bundle"))

    def test_a_published_bundle_is_stamped_as_of_its_publish_not_its_build_start(self):
        with tempfile.TemporaryDirectory() as td:
            root=Path(td); repo=repo_fixture(root); home=root/"home"
            def builder(snapshot, output, **kw):
                fake_assembler(snapshot, output, **kw); os.utime(output, (1_000_000, 1_000_000))
            launcher=mb.build_dev_bundle(repo, home, builder=builder, reaper=lambda *a: None)
            self.assertGreater(launcher.parent.parent.stat().st_mtime, 1_000_000,
                               "copytree's copied build-start mtime reached the published bundle")

    def test_reaper_runs_the_launchers_internal_verb_and_never_fails_the_build(self):
        calls=[]
        def runner(argv, **kw): calls.append((argv, kw)); raise subprocess.TimeoutExpired(argv, 1)
        mb._reap_dev_bundles(Path("/b/bin/yolo"), Path("/state/dev-bundles"), runner=runner)
        self.assertEqual(calls[0][0], ["/b/bin/yolo", "internal", "dev-bundle-reap", "--dir", "/state/dev-bundles"])
        self.assertIs(calls[0][1]["check"], False)
        mb._reap_dev_bundles(Path("/nonexistent/bin/yolo"), Path("/state/dev-bundles"))

    def test_a_build_says_so_on_stderr_and_a_reuse_says_nothing(self):
        from contextlib import redirect_stderr, redirect_stdout
        from io import StringIO
        with tempfile.TemporaryDirectory() as td:
            root=Path(td); repo=repo_fixture(root); home=root/"home"
            for expect_build in (True, False):
                with self.subTest(build=expect_build):
                    stdout=StringIO(); stderr=StringIO()
                    with redirect_stdout(stdout), redirect_stderr(stderr):
                        mb.build_dev_bundle(repo, home, builder=fake_assembler)
                    self.assertEqual(stdout.getvalue(), "", "build progress reached stdout")
                    said=stderr.getvalue()
                    self.assertEqual("Building a development bundle" in said, expect_build, said)
                    self.assertEqual("Built the development bundle in" in said, expect_build, said)

    def test_build_cache_never_reuses_a_bundle_stamped_for_another_commit_or_dirtiness(self):
        # The same frozen content can be built from a dirty tree at the parent commit and then
        # from the clean commit that records it; the cached bundle must not carry the first stamp.
        with tempfile.TemporaryDirectory() as td:
            root=Path(td); repo=repo_fixture(root); home=root/"home"; calls=[]; env=clean_git_env()
            def builder(snapshot, output, **kw):
                calls.append((kw["version"], kw["commit"])); fake_assembler(snapshot, output, **kw)
            def stamp(launcher):
                return json.loads((launcher.parent.parent/"share/yolo-jail/yolo-build-stamp.json").read_text())
            def head():
                return subprocess.check_output(["git","rev-parse","--short=12","HEAD"],cwd=repo,env=env,text=True).strip()
            (repo/"cmd/yolo/main.go").write_text("package main // change\n")
            parent=head()
            dirty=mb.build_dev_bundle(repo, home, builder=builder)
            self.assertEqual(stamp(dirty)["commit"], parent)
            self.assertTrue(stamp(dirty)["version"].endswith("-dirty"))
            subprocess.run(["git","-c","user.name=test","-c","user.email=test@example.invalid",
                            "commit","-qam","record change"],cwd=repo,env=env,check=True)
            child=head()
            clean=mb.build_dev_bundle(repo, home, builder=builder)
            self.assertNotEqual(dirty, clean)
            self.assertEqual(len(calls), 2)
            self.assertEqual(stamp(clean)["commit"], child)
            self.assertFalse(stamp(clean)["version"].endswith("-dirty"))
            fields=subprocess.run([str(clean)],capture_output=True,text=True,check=True).stdout.splitlines()
            self.assertIn("commit: "+child, fields)
            # An identical clean tree at the same commit still reuses its bundle.
            self.assertEqual(clean, mb.build_dev_bundle(repo, home, builder=builder))
            self.assertEqual(len(calls), 2)

    def test_cached_launcher_stamped_unlike_its_bundle_metadata_is_a_miss(self):
        with tempfile.TemporaryDirectory() as td:
            root=Path(td); repo=repo_fixture(root); home=root/"home"; calls=[]
            def builder(snapshot, output, **kw):
                calls.append(kw["identity"]); fake_assembler(snapshot, output, **kw)
            first=mb.build_dev_bundle(repo, home, builder=builder)
            first.parent.chmod(0o755); first.chmod(0o755)
            first.write_text(first.read_text().replace("'commit: ", "'commit: 0000"))
            second=mb.build_dev_bundle(repo, home, builder=builder)
            self.assertNotEqual(first, second); self.assertEqual(len(calls), 2)

    def test_failed_and_concurrent_publication_is_atomic_and_unique(self):
        from concurrent.futures import ThreadPoolExecutor
        with tempfile.TemporaryDirectory() as td:
            home=Path(td)/"home"
            with self.assertRaises(RuntimeError):
                mb.validate_and_publish_dev(home, {"bin/yolo": (b"bad",0o755)}, "x")
            root=home/".local/share/yolo-jail/dev-bundles"
            self.assertFalse(root.exists() and list(root.iterdir()))
            payload={"bin/yolo": (b"#!/bin/sh\nprintf '%s\\n' 'snapshot identity: x' 'build kind: development'\n",0o755),
                     "share/yolo-jail/flake.nix":(b"f",0o444), "share/yolo-jail/flake.lock":(b"l",0o444),
                     "share/yolo-jail/yolo-build-stamp.json":(b'{"snapshotIdentity":"x","buildKind":"development"}',0o444)}
            with ThreadPoolExecutor(max_workers=4) as pool:
                paths=list(pool.map(lambda _: mb.publish_dev_bundle(home,payload,identity="x"),range(4)))
            self.assertEqual(len({str(p) for p in paths}),4)
            self.assertTrue(all(not p.stat().st_mode & 0o222 for p in paths))

    def test_invalid_cached_launcher_is_a_miss_and_is_never_executed(self):
        with tempfile.TemporaryDirectory() as td:
            root=Path(td); repo=repo_fixture(root); home=root/"home"; builds=[]; executed=[]
            first=mb.build_dev_bundle(repo, home, builder=lambda snapshot, output, **kw:
                fake_assembler(snapshot, output, **kw))
            failed_generation=first.parent.parent
            first.chmod(0o755)
            first.write_text("#!/bin/sh\nexit 7\n")
            first.chmod(0o755)

            def builder(snapshot, output, *, identity, build_kind, **kw):
                builds.append(identity)
                fake_assembler(snapshot, output, identity=identity, build_kind=build_kind, **kw)

            mb.command(repo, "dev", home=home, builder=builder,
                       execer=lambda path, argv, env: executed.append((path, argv, env)))
            self.assertEqual(len(builds), 1)
            self.assertEqual(len(executed), 1)
            self.assertNotEqual(Path(executed[0][0]), first)
            self.assertTrue(Path(executed[0][0]).read_text().splitlines()[1].startswith(
                "printf '%s\\n' 'snapshot identity: " + builds[0] + "' 'build kind: development'"))
            self.assertTrue(failed_generation.is_dir())
            self.assertEqual(first.read_text(), "#!/bin/sh\nexit 7\n")

    def test_dev_command_notices_without_value_and_preserves_parent_selector(self):
        from contextlib import redirect_stderr
        from io import StringIO
        from unittest import mock
        with tempfile.TemporaryDirectory() as td:
            repo=repo_fixture(Path(td)); home=Path(td)/"home"; launcher=Path(td)/"matched/yolo"
            launcher.parent.mkdir(); launcher.write_text("launcher")
            executed=[]; stderr=StringIO(); secret="/private/source/checkout"
            with mock.patch.dict(os.environ,{"YOLO_REPO_ROOT":secret}), \
                 mock.patch.object(mb,"build_dev_bundle",return_value=launcher), \
                 redirect_stderr(stderr):
                mb.command(repo,"dev",home=home,execer=lambda path,argv,env: executed.append((path,argv,env)))
                self.assertEqual(os.environ["YOLO_REPO_ROOT"],secret)
            self.assertEqual(executed[0][0],str(launcher))
            self.assertNotIn("YOLO_REPO_ROOT",executed[0][2])
            self.assertIn("Ignoring obsolete YOLO_REPO_ROOT",stderr.getvalue())
            self.assertNotIn(secret,stderr.getvalue())

    def test_a_launcher_name_ending_in_test_is_refused_before_anything_runs(self):
        from contextlib import redirect_stderr
        from io import StringIO
        from unittest import mock
        stderr=StringIO()
        with mock.patch.object(mb,"command",side_effect=AssertionError("command ran")), \
             mock.patch.object(sys,"argv",["matched-bundle.py","install","--name","yolo.test"]), \
             redirect_stderr(stderr):
            self.assertEqual(mb.main(),1)
        self.assertIn("Go test binary",stderr.getvalue())

    def test_dev_command_reports_on_stderr_and_runs_under_the_invoked_name(self):
        from contextlib import redirect_stderr, redirect_stdout
        from io import StringIO
        from unittest import mock
        with tempfile.TemporaryDirectory() as td:
            repo=repo_fixture(Path(td)); home=Path(td)/"home"; launcher=Path(td)/"matched/yolo"
            launcher.parent.mkdir(); launcher.write_text("launcher")
            cases=(("/home/u/go/bin/yolo-dev","/home/u/go/bin/yolo-dev"),
                   (str(repo/"scripts/dev-wrapper.sh"),str(launcher)), (None,str(launcher)))
            for argv0,shown in cases:
                with self.subTest(argv0=argv0):
                    executed=[]; stdout=StringIO(); stderr=StringIO()
                    with mock.patch.object(mb,"build_dev_bundle",return_value=launcher), \
                         redirect_stdout(stdout), redirect_stderr(stderr):
                        mb.command(repo,"dev",home=home,args=["host-run","--","x"],argv0=argv0,
                                   execer=lambda path,argv,env: executed.append((path,argv,env)))
                    self.assertEqual(executed[0][0],str(launcher))
                    self.assertEqual(executed[0][1],[shown,"host-run","--","x"])
                    self.assertEqual(stdout.getvalue(),"","the dev wrapper wrote to the launcher's stdout")
                    self.assertIn(f"Matched development bundle: {launcher}",stderr.getvalue())

    # OQ-MDB1: a failed development build runs the newest earlier development build, says so,
    # and with none fails; build-dev, which produces a bundle, always fails.
    @staticmethod
    def _failing_builder(snapshot, output, **kw):
        raise subprocess.CalledProcessError(2, ["stage-source-bundle.sh"])

    def _earlier_bundle(self, repo, home, text, mtime):
        (repo/"cmd/yolo/main.go").write_text(text)
        launcher=mb.build_dev_bundle(repo, home, builder=fake_assembler, reaper=lambda *a: None)
        os.utime(launcher.parent.parent, (mtime, mtime))
        return launcher

    def _run_dev(self, repo, home, mode="dev"):
        from contextlib import redirect_stderr
        from io import StringIO
        executed=[]; stderr=StringIO(); result=None; error=None
        with redirect_stderr(stderr):
            try:
                result=mb.command(repo, mode, home=home, builder=self._failing_builder, argv0="/u/bin/yolo-dev",
                                  args=["--", "bash"], execer=lambda path, argv, env: executed.append((path, argv)))
            except RuntimeError as exc:
                error=exc
        return executed, stderr.getvalue(), result, error

    def test_a_failed_build_runs_the_newest_earlier_bundle_and_names_it(self):
        with tempfile.TemporaryDirectory() as td:
            root=Path(td); repo=repo_fixture(root); home=root/"home"
            older=self._earlier_bundle(repo, home, "package main // a\n", 1_000_000)
            newer=self._earlier_bundle(repo, home, "package main // b\n", 2_000_000)
            stamp=json.loads((newer.parent.parent/"share/yolo-jail/yolo-build-stamp.json").read_text())
            (repo/"cmd/yolo/main.go").write_text("package main // broken\n")
            executed, said, _, error=self._run_dev(repo, home)
            self.assertIsNone(error)
            self.assertEqual(executed, [(str(newer), ["/u/bin/yolo-dev", "--", "bash"])])
            self.assertIn("development bundle failed", said)
            self.assertIn("stage-source-bundle.sh", said, "the build failure was not reported")
            self.assertIn("edits are NOT running", said)
            self.assertIn(f"commit {stamp['commit']}", said)
            self.assertIn(f"build stamp {stamp['version']}", said)
            self.assertIn(str(newer), said)
            self.assertNotIn("Matched development bundle", said)
            self.assertGreater(newer.parent.parent.stat().st_mtime, 2_000_000, "the fallback was not touched")
            self.assertEqual(older.parent.parent.stat().st_mtime, 1_000_000)

    def test_a_failed_build_skips_a_broken_newer_bundle_and_leaves_its_age(self):
        with tempfile.TemporaryDirectory() as td:
            root=Path(td); repo=repo_fixture(root); home=root/"home"
            good=self._earlier_bundle(repo, home, "package main // a\n", 1_000_000)
            broken=self._earlier_bundle(repo, home, "package main // b\n", 2_000_000)
            broken.parent.chmod(0o755); broken.chmod(0o755); broken.write_text("#!/bin/sh\nexit 7\n")
            os.utime(broken.parent.parent, (2_000_000, 2_000_000))
            (repo/"cmd/yolo/main.go").write_text("package main // broken\n")
            executed, _, _, error=self._run_dev(repo, home)
            self.assertIsNone(error)
            self.assertEqual(executed[0][0], str(good))
            self.assertEqual(broken.parent.parent.stat().st_mtime, 2_000_000)

    def test_a_failed_build_with_no_earlier_bundle_fails_naming_the_fix(self):
        with tempfile.TemporaryDirectory() as td:
            root=Path(td); repo=repo_fixture(root); home=root/"home"
            executed, _, _, error=self._run_dev(repo, home)
            self.assertEqual(executed, [])
            self.assertIsNotNone(error)
            self.assertIn("stage-source-bundle.sh", str(error))
            self.assertIn("Fix the build error above and rerun", str(error))
            self.assertIn("no earlier development bundle", str(error))

    def test_a_failed_build_never_runs_a_bundle_for_another_architecture(self):
        with tempfile.TemporaryDirectory() as td:
            root=Path(td); repo=repo_fixture(root); home=root/"home"
            launcher=self._earlier_bundle(repo, home, "package main // a\n", 1_000_000)
            _, host_arch=mb._platform(repo)
            helpers=launcher.parent.parent/"share/yolo-jail/bin"
            helpers.chmod(0o755)
            (helpers/f"linux-{host_arch}").rename(helpers/"linux-otherarch")
            (repo/"cmd/yolo/main.go").write_text("package main // broken\n")
            executed, _, _, error=self._run_dev(repo, home)
            self.assertEqual(executed, [])
            self.assertIsNotNone(error)
            self.assertIn("no earlier development bundle", str(error))

    def test_build_dev_fails_on_a_failed_build_even_with_an_earlier_bundle(self):
        with tempfile.TemporaryDirectory() as td:
            root=Path(td); repo=repo_fixture(root); home=root/"home"
            self._earlier_bundle(repo, home, "package main // a\n", 1_000_000)
            (repo/"cmd/yolo/main.go").write_text("package main // broken\n")
            executed, said, result, error=self._run_dev(repo, home, mode="build-dev")
            self.assertEqual(executed, []); self.assertIsNone(result)
            self.assertIsInstance(error, mb.DevBuildFailed)
            self.assertNotIn("NOT running", said)

    def test_install_command_runs_shared_activation_workflow(self):
        result, gobin = self._just_install("yolo")
        self.assertIn("Launcher slot "+str(gobin/"yolo")+": empty.",result.stdout)

    def test_install_under_a_name_switches_that_slot_back_from_the_dev_launcher(self):
        """`just install yolo-dev` over a `just deploy-dev yolo-dev` link replaces the link, says so,
        and leaves the default slot alone."""
        def prepare(repo, gobin):
            gobin.mkdir()
            os.symlink(repo/"scripts/dev-wrapper.sh", gobin/"yolo-dev")
        result, gobin = self._just_install("yolo-dev", prepare)
        self.assertFalse((gobin/"yolo-dev").is_symlink(),"the dev link is still in the slot")
        self.assertFalse((gobin/"yolo").exists(),"a named install wrote the default slot")
        self.assertIn("replacing the dev launcher for the checkout at",result.stdout)
        self.assertIn("Switched yolo-dev from the dev launcher to a deployed one; `just deploy-dev yolo-dev` switches it back.",result.stdout)

    def _just_install(self, name, prepare=None):
        with tempfile.TemporaryDirectory() as td:
            root=Path(td); repo=repo_fixture(root, real_scripts=True); home=root/"home"
            home.mkdir(); gobin=root/"gobin"; generation=root/"generation"
            shutil.copy2(HERE.parent/"Justfile", repo/"Justfile")
            if prepare is not None:
                prepare(repo, gobin)
            fakebin=root/"fakebin"; fakebin.mkdir()
            driver=root/"install-driver.py"
            driver.write_text(r'''import importlib.util, json, os, subprocess, sys
from pathlib import Path
from types import SimpleNamespace

script = Path(sys.argv[1]).resolve()
spec = importlib.util.spec_from_file_location("install_under_test", script)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
if script.name != "matched-bundle.py":
    # The slot helper runs as itself and reaches matched-bundle.py through this shim again.
    sys.argv = [str(script), *sys.argv[2:]]
    raise SystemExit(module.main())
real_run = subprocess.run
events = []

def runner(argv, **kwargs):
    if argv and (argv[0] in ("git", "go", "readlink") or "--version" in argv):
        return real_run(argv, **kwargs)
    if "migrate-host" in argv:
        events.append(("migrate-host", argv))
        return SimpleNamespace(stdout="")
    if "--stage" in argv:
        generation = Path(os.environ["YOLO_TEST_GENERATION"])
        generation.mkdir()
        events.append(("stage", str(generation)))
        return SimpleNamespace(stdout=str(generation))
    if "--activate" in argv:
        events.append(("activate", argv[-1]))
        return SimpleNamespace(stdout="")
    raise AssertionError(f"unexpected install subprocess: {argv}")

def assemble(snapshot, output, *, identity, build_kind, **kwargs):
    output.mkdir()
    (output / "bin").mkdir()
    launcher = output / "bin/yolo"
    launcher.write_text("#!/bin/sh\nprintf '%s\\n' 'snapshot identity: " + identity + "' 'build kind: " + build_kind + "'\n")
    launcher.chmod(0o755)
    bundle = output / "share/yolo-jail"
    bundle.mkdir(parents=True)
    for name in ("flake.nix", "flake.lock"):
        (bundle / name).write_text(name)
    (bundle / "yolo-build-stamp.json").write_text(json.dumps({"snapshotIdentity": identity, "buildKind": build_kind}))
    shipped, guest = module._binary_lists(snapshot / "scripts/stage-source-bundle.sh")
    host_os, host_arch = module._platform(snapshot)
    targets = {f"linux-{host_arch}": shipped}
    if host_os == "darwin":
        targets[f"darwin-{host_arch}"] = guest
    for dirname, names in targets.items():
        directory = bundle / "bin" / dirname
        directory.mkdir(parents=True)
        for name in names:
            binary = directory / name
            binary.write_bytes(b"helper")
            binary.chmod(0o755)

def command(*args, **kwargs):
    kwargs["runner"] = runner
    return original_command(*args, **kwargs)

original_command = module.command
module._source_stamps = lambda repo: ("v", "c")
module.assemble = assemble
module.command = command
module.subprocess.run = runner
sys.argv = [str(script), *sys.argv[2:]]
code = module.main()
Path(os.environ["YOLO_TEST_EVENTS"]).write_text(json.dumps(events))
raise SystemExit(code)
''')
            shim=fakebin/"python3"
            shim.write_text("#!/bin/sh\nexec "+sys.executable+" "+str(driver)+" \"$@\"\n")
            shim.chmod(0o755)
            env=os.environ.copy()
            env.update({"PATH":str(fakebin)+os.pathsep+env.get("PATH",""), "HOME":str(home),
                        "GOBIN":str(gobin), "YOLO_VERSION":"", "YOLO_TEST_GENERATION":str(generation),
                        "YOLO_TEST_EVENTS":str(root/"events.json")})
            result=subprocess.run(["just", "install", name], cwd=repo, env=env, text=True, capture_output=True)
            self.assertEqual(result.returncode,0,result.stdout+result.stderr)
            self.assertTrue((gobin/name).is_file(),"the production Justfile install flow did not install its launcher")
            events=json.loads((root/"events.json").read_text())
            self.assertEqual([event[0] for event in events],["migrate-host","stage","activate"])
            # migrate-host inspects the GOBIN this install writes, for this launcher name only.
            self.assertIn(f"--gobin={gobin}",events[0][1])
            self.assertIn(f"--name={name}",events[0][1])
            self.assertEqual(events[2][1],events[1][1])
            self.assertEqual(events[1][1],str(generation))
            self.assertTrue((generation/"flake.nix").is_file(),"install did not stage the matched flake")
            self.assertTrue((generation/"yolo-build-stamp.json").is_file(),"install did not stage matched identity metadata")
            self.assertEqual(result.stdout.count(f"Installed {gobin/name}"),1)
            return result, gobin

    def test_install_generation_activates_only_identity_match(self):
        with tempfile.TemporaryDirectory() as td:
            root=Path(td); identity="b"*64; launcher=root/"yolo"
            launcher.write_text("#!/bin/sh\nprintf '%s\\n' 'snapshot identity: "+identity+"' 'build kind: installed'\n"); launcher.chmod(0o755)
            bundle=root/"bundle"; bundle.mkdir()
            for name,value in (("flake.nix","f"),("flake.lock","l")): (bundle/name).write_text(value)
            (bundle/"yolo-build-stamp.json").write_text(json.dumps({"snapshotIdentity":identity,"buildKind":"installed"}))
            generation=root/"generation"; calls=[]
            def run(argv,**kw):
                calls.append(argv)
                if "--stage" in argv: generation.mkdir(); return SimpleNamespace(stdout=str(generation))
                return SimpleNamespace(stdout="")
            mb.publish_install_generation(launcher,bundle,identity,{},runner=run)
            self.assertEqual(len(calls),2)
            bad=root/"bad"; shutil.copytree(bundle,bad); (bad/"yolo-build-stamp.json").write_text('{"snapshotIdentity":"wrong","buildKind":"installed"}')
            generation2=root/"generation2"
            def mismatch(argv,**kw):
                calls.append(argv)
                if "--stage" in argv: generation2.mkdir(); return SimpleNamespace(stdout=str(generation2))
                return SimpleNamespace(stdout="")
            with self.assertRaisesRegex(RuntimeError,"identity"): mb.publish_install_generation(launcher,bad,identity,{},runner=mismatch)
            self.assertEqual(len(calls),3)


class WrapperTests(unittest.TestCase):
    def test_just_dev_delegates_to_checkout_wrapper(self):
        just= (HERE.parent/"Justfile").read_text()
        self.assertIn("dev *args:",just); self.assertIn('./scripts/dev-wrapper.sh "$@"',just)
        self.assertIn("set positional-arguments",just)


class StandaloneWrapperTests(unittest.TestCase):
    def setUp(self):
        self.tmp=tempfile.TemporaryDirectory(); self.root=Path(self.tmp.name).resolve()
        self.repo=repo_fixture(self.root,real_scripts=True)
        with tempfile.TemporaryDirectory() as td: self.identity=mb.freeze_snapshot(self.repo,Path(td).resolve()/"snapshot")
        version,commit=mb._source_stamps(self.repo)
        self.home=self.root/"home"; self.log=self.root/"launcher.json"; self.stale_marker=self.root/"stale"
        installed=self.root/"installed-generation"; installed.mkdir()
        self.active=self.home/".local/share/yolo-jail/flake-bundle"
        self.active.parent.mkdir(parents=True); self.active.symlink_to(installed)
        self.active_target=os.readlink(self.active)
        source=f'''#!/usr/bin/env python3
import json, os, signal, sys
identity={self.identity!r}
if sys.argv[1:]==["--version","--full"]:
 print("snapshot identity: "+identity); print("build kind: development")
 print("build stamp: "+{version!r}); print("commit: "+{commit!r}); raise SystemExit(0)
with open(os.environ["YOLO_TEST_LOG"],"w") as f: json.dump({{"argv0":sys.argv[0],"args":sys.argv[1:],"cwd":os.getcwd(),"hasRepoRoot":"YOLO_REPO_ROOT" in os.environ,"path":os.environ.get("PATH")}},f)
if os.environ.get("YOLO_TEST_SIGNAL"): os.kill(os.getpid(),getattr(signal,"SIG"+os.environ["YOLO_TEST_SIGNAL"]))
raise SystemExit(23)
'''
        payload={"bin/yolo":(source.encode(),0o755),"share/yolo-jail/flake.nix":(b"f",0o444),
            "share/yolo-jail/flake.lock":(b"l",0o444),"share/yolo-jail/yolo-build-stamp.json":
            (json.dumps({"version":version,"commit":commit,"snapshotIdentity":self.identity,
                         "buildKind":"development"}).encode(),0o444)}
        shipped,guest=mb._binary_lists(self.repo/"scripts/stage-source-bundle.sh")
        host_os,host_arch=mb._platform(self.repo)
        targets={f"linux-{host_arch}":shipped}
        if host_os=="darwin": targets[f"darwin-{host_arch}"]=guest
        for dirname,names in targets.items():
            for name in names: payload[f"share/yolo-jail/bin/{dirname}/{name}"]=(b"helper",0o755)
        mb.publish_dev_bundle(self.home,payload,identity=self.identity)
        self.links=self.root/"path bin"; self.links.mkdir()
        script=self.repo/"scripts/dev-wrapper.sh"
        (self.links/"relative").symlink_to(os.path.relpath(script,self.links))
        (self.links/"yolo-dev").symlink_to("relative")
        (self.links/"yolo").symlink_to(str(self.links/"yolo-dev"))
        self.stale=self.root/"stale bin"; self.stale.mkdir()
        old=self.stale/"yolo"; old.write_text(f"#!/usr/bin/env python3\nfrom pathlib import Path\nPath({str(self.stale_marker)!r}).touch()\n"); old.chmod(0o755)
        self.cwd=self.root/"unrelated cwd"; self.cwd.mkdir()
        self.other=self.root/"other checkout"; self.other.mkdir()

    def tearDown(self): self.tmp.cleanup()

    def env(self):
        env=os.environ.copy(); env.update({"HOME":str(self.home),"YOLO_REPO_ROOT":str(self.other),
            "YOLO_TEST_LOG":str(self.log),"PATH":str(self.stale)+os.pathsep+str(self.links)+os.pathsep+"/usr/bin:/bin"+os.pathsep+os.environ.get("PATH","")})
        env.pop("YOLO_TEST_SIGNAL",None); return env

    def test_yolo_and_yolo_dev_symlink_names_use_selected_checkout_from_unrelated_cwd(self):
        self.assertEqual(shutil.which("yolo",path=self.env()["PATH"]),str(self.stale/"yolo"))
        for name,args in (("yolo-dev",["--","bash","-c","two words"]),("yolo",["--","bash","-c","arg with spaces"])):
            env=self.env()
            command=["yolo-dev",*args] if name=="yolo-dev" else [str(self.links/"yolo"),*args]
            result=subprocess.run(command,cwd=self.cwd,env=env,text=True,capture_output=True)
            self.assertEqual(result.returncode,23,result.stderr)
            record=json.loads(self.log.read_text()); self.assertEqual(record["args"],args)
            self.assertNotIn("Matched development bundle",result.stdout)
            self.assertIn("Matched development bundle",result.stderr)
            self.assertEqual(record["cwd"],str(self.cwd)); self.assertFalse(record["hasRepoRoot"])
            self.assertEqual(record["path"],env["PATH"]); self.assertEqual(os.readlink(self.active),self.active_target)
            self.assertIn("remove that export",result.stderr); self.assertNotIn(str(self.other),result.stderr)
            self.assertFalse(self.stale_marker.exists())
        self.assertEqual(len(list((self.home/".local/share/yolo-jail/dev-bundles").glob("*"))),1)

    def test_wrapper_passes_the_name_it_was_invoked_as(self):
        """dev-wrapper.sh hands matched-bundle.py --argv0 <the invoked path>, which the launcher
        then runs under (a shebang stub cannot observe its own argv[0], so the handoff is pinned)."""
        checkout=self.root/"argv0 checkout"; (checkout/"scripts").mkdir(parents=True)
        shutil.copy2(HERE/"dev-wrapper.sh",checkout/"scripts/dev-wrapper.sh")
        record=self.root/"argv.json"
        (checkout/"scripts/matched-bundle.py").write_text(
            "import json, sys\n"
            f"def main():\n    json.dump(sys.argv, open({str(record)!r}, 'w'))\n    return 0\n")
        link=self.links/"yolo-named"; link.symlink_to(checkout/"scripts/dev-wrapper.sh")
        result=subprocess.run([str(link),"host-run","--","true"],cwd=self.cwd,env=self.env(),text=True,capture_output=True)
        self.assertEqual(result.returncode,0,result.stderr)
        argv=json.loads(record.read_text())
        self.assertEqual(argv[argv.index("--argv0")+1],str(link))
        self.assertEqual(argv[argv.index("--")+1:],["host-run","--","true"])

    def test_wrapper_without_obsolete_selector_is_silent_about_it(self):
        env=self.env(); env.pop("YOLO_REPO_ROOT")
        result=subprocess.run([str(self.links/"yolo-dev"),"--","quiet"],cwd=self.cwd,env=env,text=True,capture_output=True)
        self.assertEqual(result.returncode,23,result.stderr)
        self.assertNotIn("YOLO_REPO_ROOT",result.stderr)
        self.assertEqual(json.loads(self.log.read_text())["path"],env["PATH"])

    def test_broken_and_cyclic_aliases_fail_without_build_or_launch(self):
        import runpy
        resolver=runpy.run_path(str(HERE/"dev-wrapper.sh"))
        broken=self.links/"broken"; broken.symlink_to("missing-target")
        cycle_a=self.links/"cycle-a"; cycle_b=self.links/"cycle-b"
        cycle_a.symlink_to(cycle_b); cycle_b.symlink_to(cycle_a)
        for path in (broken,cycle_a):
            with self.subTest(path=path.name):
                with self.assertRaisesRegex(RuntimeError,"relink"):
                    resolver["resolve_checkout"](path)
                try:
                    result=subprocess.run([str(path),"--","not-run"],cwd=self.cwd,env=self.env(),capture_output=True)
                except OSError:
                    result=None  # The kernel can reject a broken/cyclic executable before Python starts.
                self.assertTrue(result is None or result.returncode != 23)
                self.assertFalse(self.log.exists())
        generations=list((self.home/".local/share/yolo-jail/dev-bundles").glob("*"))
        self.assertEqual(len(generations),1)

        env=self.env(); env["YOLO_TEST_SIGNAL"]="TERM"
        signal_run=subprocess.run([str(self.links/"yolo"),"--","signal"],cwd=self.cwd,env=env,text=True,capture_output=True)
        self.assertEqual(signal_run.returncode,-signal.SIGTERM)
        base_log=self.log.read_text()
        (self.repo/"cmd/yolo/main.go").write_text("package main // changed\n")
        dirty=subprocess.run([str(self.links/"yolo-dev"),"--","dirty"],cwd=self.cwd,env=self.env(),text=True,capture_output=True)
        # OQ-MDB1: the dirty build fails, so the wrapper runs the last development build and says so.
        self.assertEqual(dirty.returncode,23,dirty.stderr)
        self.assertEqual(json.loads(self.log.read_text())["args"],["--","dirty"])
        self.assertIn("edits are NOT running",dirty.stderr)
        self.log.write_text(base_log)
        (self.repo/"cmd/yolo/main.go").write_text("package main\n")
        cache=next((self.home/".local/share/yolo-jail/dev-bundles").glob(f"{self.identity[:16]}-*"))
        cache.chmod(0o755); (cache/"share").chmod(0o755); (cache/"share/yolo-jail").chmod(0o755)
        (cache/"share/yolo-jail/flake.nix").unlink()
        incomplete=subprocess.run([str(self.links/"yolo-dev"),"--","incomplete"],cwd=self.cwd,env=self.env(),text=True,capture_output=True)
        self.assertNotEqual(incomplete.returncode,23); self.assertEqual(self.log.read_text(),base_log)
        self.assertFalse(self.stale_marker.exists())


class FreshExecutableTests(unittest.TestCase):
    """A launcher another thread's fork briefly holds open for writing is busy, not broken."""

    def test_a_momentarily_busy_executable_is_retried(self):
        import errno
        calls = []
        real = mb.subprocess.run

        def busy_then_ok(argv, **kw):
            calls.append(argv)
            if len(calls) < 3:
                raise OSError(errno.ETXTBSY, "Text file busy", argv[0])
            return subprocess.CompletedProcess(argv, 0, "ok\n", "")

        mb.subprocess.run = busy_then_ok
        try:
            result = mb._run_fresh_executable(["/x/yolo", "--version"], {})
        finally:
            mb.subprocess.run = real
        self.assertEqual(result.stdout, "ok\n")
        self.assertEqual(len(calls), 3)

    def test_any_other_error_is_not_retried(self):
        import errno
        calls = []
        real = mb.subprocess.run

        def missing(argv, **kw):
            calls.append(argv)
            raise OSError(errno.ENOENT, "No such file", argv[0])

        mb.subprocess.run = missing
        try:
            with self.assertRaises(OSError):
                mb._run_fresh_executable(["/x/yolo"], {})
        finally:
            mb.subprocess.run = real
        self.assertEqual(len(calls), 1)


if __name__ == "__main__":
    unittest.main()
