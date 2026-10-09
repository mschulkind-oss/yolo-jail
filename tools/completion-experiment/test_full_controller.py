#!/usr/bin/env python3
"""Focused controller tests with one owned temporary emitter compile per fixture setup."""
import sys
sys.dont_write_bytecode = True

import ast
import base64
import hashlib
import importlib.util
import inspect
import io
import json
import os
import platform
import signal
import stat
import struct
import subprocess
import tempfile
import threading
import types
import unittest
from unittest import mock
from pathlib import Path

HERE = Path(__file__).resolve().parent
E_ROOT = HERE
REPO_ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(HERE))
_controller_source = os.environ.get('YOLO_CONTROLLER_SOURCE')
if _controller_source:
    _spec = importlib.util.spec_from_file_location('full_controller', _controller_source)
    fc = importlib.util.module_from_spec(_spec)
    sys.modules['full_controller'] = fc
    _spec.loader.exec_module(fc)
    fc.__file__ = str(HERE / 'full_controller.py')
else:
    import full_controller as fc


def _run_owned_compile(command, cwd, timeout_seconds=120, popen_factory=None):
    """Run one inherited-environment compile and reap its owned process tree on interruption."""
    factory = popen_factory or subprocess.Popen
    process = factory(command, cwd=cwd, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                      start_new_session=True)
    try:
        stdout, stderr = process.communicate(timeout=timeout_seconds)
    except BaseException:
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        except OSError:
            try:
                process.kill()
            except ProcessLookupError:
                pass
        process.communicate()
        raise
    return subprocess.CompletedProcess(command, process.returncode, stdout, stderr)


def _host_pair():
    system = platform.system().lower()
    os_name = {'darwin': 'darwin', 'windows': 'windows', 'linux': 'linux'}.get(system, system)
    machine = platform.machine().lower()
    arch = {'x86_64': 'amd64', 'amd64': 'amd64', 'aarch64': 'arm64', 'arm64': 'arm64',
            'i386': '386', 'i686': '386'}.get(machine, machine)
    return os_name, arch


class ControllerFixture(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if platform.system() != 'Linux':
            raise unittest.SkipTest('owned native Monitor fixture requires Linux')
        cls.unit_temp = tempfile.TemporaryDirectory(prefix='completion-experiment-emitter-')
        cls.addClassCleanup(cls.unit_temp.cleanup)
        cls.unit_root = Path(cls.unit_temp.name)
        cls.binary = cls.unit_root / 'emitter.test'
        logical = REPO_ROOT / 'tools' / 'pack-binaries'
        cls.compilation_source_paths = (
            HERE / 'main.go', HERE / 'toolchain.go', HERE / 'pin.go', HERE / 'toolchain_go_emitter_test.go',
            HERE / 'recipe.go', logical / 'main.go', logical / 'toolchain.go', logical / 'pin.go', logical / 'recipe.go',
            REPO_ROOT / '.goreleaser.yaml', REPO_ROOT / 'Justfile', REPO_ROOT / 'go.mod', REPO_ROOT / 'go.sum')
        source_paths = cls.compilation_source_paths
        cls.compilation_sources = {
            str(path): hashlib.sha256(path.read_bytes()).hexdigest() for path in source_paths}
        command = ['go', 'test', '-c', '-short', '-mod=vendor', '-o', str(cls.binary),
                   './tools/completion-experiment']
        result = _run_owned_compile(command, cwd=REPO_ROOT, timeout_seconds=120)
        cls.compile_result = dict(command=command, cwd=str(REPO_ROOT), returncode=result.returncode,
                                  stdout=result.stdout, stderr=result.stderr)
        if result.returncode != 0:
            raise RuntimeError('one-shot experimental helper compile failed: '+
                               result.stderr.decode('utf-8', errors='replace'))
        if (not cls.binary.is_file() or not stat.S_ISREG(cls.binary.stat().st_mode) or
                not os.access(cls.binary, os.X_OK)):
            raise RuntimeError('one-shot experimental helper output is not a regular executable')
        cls.artifact_binding = (hashlib.sha256(cls.binary.read_bytes()).hexdigest(),
                                cls.binary.stat().st_size, stat.S_IMODE(cls.binary.stat().st_mode))
        if cls.compilation_sources != {
                str(path): hashlib.sha256(path.read_bytes()).hexdigest() for path in source_paths}:
            raise RuntimeError('experimental helper source binding changed during compilation')

    def setUp(self):
        current_sources = {
            str(path): hashlib.sha256(path.read_bytes()).hexdigest()
            for path in self.compilation_source_paths}
        self.assertEqual(current_sources, self.compilation_sources,
                         'experimental helper source inputs changed after its one-shot compile')
        self.assertTrue(self.binary.is_file() and stat.S_ISREG(self.binary.stat().st_mode))
        self.assertTrue(os.access(self.binary, os.X_OK))
        self.assertEqual((hashlib.sha256(self.binary.read_bytes()).hexdigest(),
                          self.binary.stat().st_size, stat.S_IMODE(self.binary.stat().st_mode)),
                         self.artifact_binding, 'temporary compiled helper changed after setup')
        self.temp = tempfile.TemporaryDirectory(prefix='python-controller-fixture-')
        self.root = Path(self.temp.name)
        self.bin_dir = self.root / 'commands'
        self.bin_dir.mkdir()
        self.tc = self.root / 'toolchain'
        (self.tc / 'bin').mkdir(parents=True)
        (self.tc / 'pkg/tool').mkdir(parents=True)
        (self.tc / 'src').mkdir(parents=True)
        (self.tc / 'VERSION').write_text('go1.26.7\n')
        (self.tc / 'pkg/tool/compile').write_text('owned synthetic compile stand-in\n')
        (self.tc / 'pkg/tool/compile').chmod(0o755)
        (self.tc / 'src/input.go').write_text('package fixture\n')
        self.log = self.root / 'commands.log'
        self.marker = self.root / 'build.marker'
        self.fake_go = self.bin_dir / 'go'
        self.fake_go.write_text(self._host_go_script())
        self.fake_go.chmod(0o755)
        self.fake_tc_go = self.tc / 'bin/go'
        self.fake_tc_go.write_text(self._toolchain_go_script())
        self.fake_tc_go.chmod(0o755)
        self.home = os.environ.get('HOME', str(self.root / 'home'))
        self.host_os, self.host_arch = _host_pair()
        self.original_env = dict(os.environ)
        self.parent_env = dict(self.original_env)
        self.parent_env['PATH'] = str(self.bin_dir) + os.pathsep + self.original_env.get('PATH', '/usr/bin:/bin')
        self.child_env = dict(self.parent_env)
        self.child_env.update({
            'PWD': str(REPO_ROOT),
            'EMITTER_LOG': str(self.log),
            'EMITTER_TC': str(self.tc),
            'EMITTER_MARKER': str(self.marker),
            'EMITTER_PROXY_MODE': 'custom',
            'EMITTER_DOWNLOAD_MODE': 'ok',
            'EMITTER_GOOS': self.host_os,
            'EMITTER_GOARCH': self.host_arch,
            'YOLO_GO_EMITTER_COMPILED_CHILD': '1',
        })
        metadata_root = self.root / 'metadata-goroot'
        (metadata_root / 'bin').mkdir(parents=True)
        (metadata_root / 'pkg/tool').mkdir(parents=True)
        (metadata_root / 'src').mkdir()
        (metadata_root / 'VERSION').write_text('go1.26.7\n')
        (metadata_root / 'go.env').write_text('CGO_CFLAGS=-O2 -g\n')
        (metadata_root / 'bin/go').write_text('not executed; injected metadata fixture\n')
        (metadata_root / 'bin/go').chmod(0o755)
        self.metadata_root = metadata_root
        self.child_goenv = self.root / 'child-go.env'
        self.child_goenv.write_text('CGO_CFLAGS=-O2 -g\\nCGO_CPPFLAGS=\\nCGO_CXXFLAGS=-O2 -g\\nCGO_LDFLAGS=-O2 -g\\n')
        self.child_env['GOENV'] = str(self.child_goenv)
        self.metadata_calls = []
        self.full_capture = fc.FullCapture(self.parent_env, REPO_ROOT, runner=self._metadata_runner)
        self.context = fc.ContextCapture(self.full_capture, self.binary, REPO_ROOT, self.child_env,
                                         [str(self.binary), '-test.short=true',
                                          '-test.run=^TestGoEmitterCompiledPrefixHelperChild$'],
                                         metadata_runner=self._metadata_runner)
        self.events = []
        self.monitor_seen = []

    def tearDown(self):
        self.temp.cleanup()

    def _host_go_script(self):
        checksum = 'h1:' + ('A' * 43) + '='
        return '''#!/bin/sh
printf '%s\\n' "$*" >> "$EMITTER_LOG"
case "$1" in
  env) printf '{"GOPROXY":"https://proxy.synthetic.invalid","GOSUMDB":"sum.golang.org"}\\n' ;;
  mod) printf '{"Dir":"%s","Sum":"@@SUM@@"}\\n' "$EMITTER_TC" ;;
  *) exit 29 ;;
esac
'''.replace('@@SUM@@', checksum)

    def _toolchain_go_script(self):
        return '''#!/bin/sh
printf '%s\\n' "$*" >> "$EMITTER_LOG"
case "$1" in
  version) printf 'go version go1.26.7 %s/%s\\n' "$EMITTER_GOOS" "$EMITTER_GOARCH" ;;
  build)
    printf 'build-started\\n' >> "$EMITTER_MARKER"
    printf 'build-target %s/%s %s\\n' "$GOOS" "$GOARCH" "$*" >> "$EMITTER_MARKER"
    output= previous=
    for arg in "$@"; do
      if [ "$previous" = -o ]; then output=$arg; fi
      previous=$arg
    done
    if [ -n "$output" ]; then
      mkdir -p "${output%/*}"
      printf 'synthetic build bytes\\n' > "$output"
      if [ $? -ne 0 ]; then exit 23; fi
      printf 'build-produced\\n' >> "$EMITTER_LOG"
    fi
    ;;
  *) exit 22 ;;
esac
'''

    def _metadata_runner(self, argv, *, cwd, env):
        self.metadata_calls.append((tuple(argv), cwd, dict(env)))
        selected_go = fc.shutil.which('go', path=env.get('PATH', ''))
        self.assertIsNotNone(selected_go)
        self.assertEqual(argv[0], str(Path(selected_go).resolve(strict=True)))
        self.assertEqual(argv[1:3], ['env', '-json'])
        self.assertEqual(argv[3:], list(fc.FIELDS))
        values = {key: '' for key in fc.FIELDS}
        values.update(
            GOENV=env.get('GOENV', 'off'), GOROOT=str(self.metadata_root),
            GOTOOLDIR=str(self.metadata_root / 'pkg/tool'), GOVERSION='go1.26.7',
            GOTOOLCHAIN=env.get('GOTOOLCHAIN', 'auto'), GOOS=env.get('GOOS', self.host_os),
            GOARCH=env.get('GOARCH', self.host_arch), GOHOSTOS=self.host_os, GOHOSTARCH=self.host_arch,
            GOMOD=str(REPO_ROOT / 'go.mod'), GOWORK=env.get('GOWORK', 'off'),
            GOCACHE=env.get('GOCACHE') or str(Path(self.home) / '.cache/go-build'),
            GOMODCACHE=env.get('GOMODCACHE') or str(Path(self.home) / 'go/pkg/mod'),
            GOPATH=env.get('GOPATH', str(Path(self.home) / 'go')),
            GOFLAGS=env.get('GOFLAGS', ''), CGO_ENABLED=env.get('CGO_ENABLED', '1'), CC='', CXX='',
            GOPROXY=env.get('GOPROXY', 'https://proxy.golang.org,direct'),
            GOSUMDB=env.get('GOSUMDB', 'sum.golang.org'),
        )
        if 'GOOS' in env and env['GOOS']:
            values['GOOS'] = env['GOOS']
        return values

    def _observer(self, event, monitor, footprint):
        self.events.append(event)
        self.monitor_seen.append((event, monitor, set(footprint.members)))

    def _completed_fixture_context(self, mode):
        telemetry = self.root / ('fixture-' + mode + '.json')
        child_env = dict(self.child_env)
        child_env.update({
            'YOLO_PYTHON_CONTROLLER_EMITTER_CHILD': '1',
            'YOLO_PYTHON_CONTROLLER_EMITTER_MODE': mode,
            'YOLO_PYTHON_CONTROLLER_EMITTER_TELEMETRY': str(telemetry),
        })
        argv = [str(self.binary), '-test.short=true',
                '-test.run=^TestPythonControllerEmitterFixtureChild$']
        context = fc.ContextCapture(
            fc.FullCapture(self.parent_env, REPO_ROOT, runner=self._metadata_runner),
            self.binary, REPO_ROOT, child_env, argv, metadata_runner=self._metadata_runner)
        return context, child_env, telemetry

    def _record_completed_fixture(self, mode, result, telemetry_value):
        snapshot = result.snapshot
        commands = self._read_log().splitlines()
        command_counts = {}
        for command in commands:
            verb = command.split(' ', 1)[0] if command else ''
            command_counts[verb] = command_counts.get(verb, 0) + 1
        target_lines = [line for line in self.marker.read_text().splitlines()
                        if line.startswith('build-target ')] if self.marker.exists() else []
        record = dict(
            test=self.id(), mode=mode, transitions=list(result.transitions),
            resultState=result.state, errorCode=result.error_code,
            doneStatus=result.done_status, wrapperStatus=result.wrapper_status,
            releaseCount=result.transitions.count('RELEASE'),
            helperProcessLaunches=result.report['helperProcessLaunches'],
            metadataProviderCalls=result.report['metadataProviderCalls'],
            fakeGoCommandCounts=command_counts, buildTargetCount=len(target_lines),
            syntheticWanted=telemetry_value['builds'],
            snapshotOwned=snapshot is not None and snapshot.owner.owned_snapshot is snapshot,
            snapshotAcquiredNone=snapshot is not None and snapshot.acquired is None,
            snapshotFootprint=(dict(digest=snapshot.footprint.digest,
                                    members=len(snapshot.footprint.members),
                                    files=snapshot.footprint.files,
                                    directories=snapshot.footprint.directories,
                                    bytes=snapshot.footprint.bytes)
                               if snapshot is not None else None),
            fixtureBinding=(snapshot.report['fixtureBinding'] if snapshot is not None else None),
            profileState=result.report['profileState'],
            qualityOutcomes=result.report['qualityOutcomes'],
            baselinePublished=result.report['baselinePublished'])
        evidence = self.root / 'runtime-results'
        evidence.mkdir(parents=True, exist_ok=True)
        with (evidence / (mode + '.jsonl')).open('a', encoding='utf-8') as stream:
            stream.write(json.dumps(record, sort_keys=True, separators=(',', ':')) + '\n')

    def test_compiled_matching_fixture_returns_owned_completed_snapshot(self):
        context, child_env, telemetry = self._completed_fixture_context('matching')
        recapture_calls = []
        before_release_recaptures = []
        original_recapture = context.recapture

        def tracked_recapture(reconciled, acquired=None):
            recapture_calls.append(acquired)
            return original_recapture(reconciled, acquired)

        def observed(event, monitor, footprint):
            self._observer(event, monitor, footprint)
            if event == 'before-release':
                before_release_recaptures.append(len(recapture_calls))

        context.recapture = tracked_recapture
        result = fc.run_full(context, self.binary, child_env, REPO_ROOT,
                             timeout_seconds=15, observer=observed)
        self.assertEqual(result.state, 'completed-fixture-only')
        self.assertIsNone(result.error_code)
        self.assertEqual(result.done_status, 0)
        self.assertEqual(result.wrapper_status, 0)
        self.assertEqual(result.transitions,
                         ('INIT', 'HELLO', 'START', 'RETURN:provider_return', 'RELEASE', 'DONE',
                          'FINISH', 'WAIT', 'FINAL-OBSERVATION'))
        self.assertEqual(self.events.count('before-release'), 1)
        self.assertEqual(self.events.count('wrapper-reaped'), 1)
        self.assertEqual(before_release_recaptures, [2],
                         'parent/child full readers must reread protected scope before RELEASE')
        self.assertEqual(len(recapture_calls), 4,
                         'actual matching FULL must rediscover before and after the terminal exchange')
        learned = next(item for item in self.monitor_seen if item[0] == 'learned-registered')
        self.assertIn(str(self.tc / 'bin/go'), learned[2])
        self.assertIn(str(self.tc / 'VERSION'), learned[2])
        self.assertIn(str(self.tc / 'bin/go'), learned[1].paths)
        self.assertIn(str(self.tc / 'VERSION'), learned[1].paths)
        self.assertIn(str(self.child_goenv), learned[2])
        self.assertIn(str(self.child_goenv), learned[1].paths)
        telemetry_value = json.loads(telemetry.read_text())
        self.assertEqual(telemetry_value['mode'], 'matching')
        expected = [(item['binary'], item['platform'], item['sha256'])
                    for item in telemetry_value['builds']]
        self.assertTrue(expected, 'actual helper did not record its release-derived wanted set')
        marker_lines = self.marker.read_text().splitlines()
        targets = [line for line in marker_lines if line.startswith('build-target ')]
        self.assertEqual(len(targets), len(expected), 'actual fake builder count differs from wanted builds')
        for target, (binary, platform_name, _digest) in zip(targets, expected):
            self.assertIn(platform_name, target)
            self.assertIn('./cmd/' + binary, target)
        self.assertIn('build-produced', self._read_log())
        self.assertIsNotNone(result.snapshot,
                             'successful actual compiled matching transcript must create its owned snapshot')
        snapshot = result.snapshot
        self.assertIs(snapshot.owner.owned_snapshot, snapshot)
        self.assertIs(snapshot.owner.context, context)
        self.assertIsNotNone(snapshot.acquired)
        self.assertIs(snapshot.context.request, snapshot.acquired.request)
        self.assertEqual(snapshot.context.child.fixture_mode, 'matching')
        self.assertEqual(snapshot.context.child.launch_argv, tuple(context.launch_argv))
        self.assertEqual(snapshot.report, result.report)
        self.assertIn(str(self.tc / 'bin/go'), snapshot.footprint.members)
        self.assertIn(str(self.tc / 'VERSION'), snapshot.footprint.members)
        self.assertEqual(snapshot.report['fixtureBinding']['sourceDigest'], snapshot.context.sources.digest)
        self.assertEqual(snapshot.report['profileState'], 'Unknown')
        self.assertEqual(snapshot.report['qualityOutcomes'], [])
        self.assertFalse(snapshot.report['baselinePublished'])
        self.assertEqual(snapshot.report['acquisitionState'],
                         'synthetic fixture accepted by A; no provenance or integrity proof')
        self.assertEqual(snapshot.report['helperProcessLaunches'], 1)
        self.assertNotIn('actualGoFamilyArgv', snapshot.report)
        self.assertIn('separate from any zero-launch claim', snapshot.report['goFamilyExecutionScope'])
        self.assertNotIn(str(self.root), repr(snapshot.report))
        self._record_completed_fixture('matching', result, telemetry_value)

    def test_compiled_no_official_fixture_returns_owned_context_snapshot(self):
        context, child_env, telemetry = self._completed_fixture_context('no-official')
        result = fc.run_full(context, self.binary, child_env, REPO_ROOT,
                             timeout_seconds=15, observer=self._observer)
        self.assertEqual(result.state, 'completed-fixture-only')
        self.assertIsNone(result.error_code)
        self.assertEqual(result.done_status, 0)
        self.assertEqual(result.wrapper_status, 0)
        self.assertEqual(result.transitions,
                         ('INIT', 'HELLO', 'START', 'RETURN:no_acquisition', 'DONE',
                          'FINISH', 'WAIT', 'FINAL-OBSERVATION'))
        self.assertEqual(self.events.count('before-release'), 0)
        self.assertEqual(self.events.count('wrapper-reaped'), 1)
        self.assertFalse(self.marker.exists(), 'no-official fixture must not reach the fake builder')
        self.assertEqual(self._read_log(), '', 'no-official fixture must not invoke fake Go commands')
        telemetry_value = json.loads(telemetry.read_text())
        self.assertEqual(telemetry_value, {'mode': 'no-official', 'builds': []})
        snapshot = result.snapshot
        self.assertIsNotNone(snapshot, 'successful actual no-official transcript must own its snapshot')
        self.assertIs(snapshot.owner.owned_snapshot, snapshot)
        self.assertIs(snapshot.owner.context, context)
        self.assertIsNone(snapshot.acquired)
        self.assertEqual(snapshot.context.child.fixture_mode, 'no-official')
        self.assertEqual(snapshot.context.child.launch_argv, tuple(context.launch_argv))
        self.assertEqual(snapshot.report, result.report)
        self.assertEqual(snapshot.report['fixtureBinding']['sourceDigest'], snapshot.context.sources.digest)
        self.assertTrue(snapshot.report['contextOnly'])
        self.assertFalse(snapshot.report['releaseSent'])
        self.assertEqual(snapshot.report['acquisitionState'], 'none')
        self.assertEqual(snapshot.report['helperProcessLaunches'], 1)
        self.assertNotIn('actualGoFamilyArgv', snapshot.report)
        self.assertIn('separate from any zero-launch claim', snapshot.report['goFamilyExecutionScope'])
        self.assertEqual(snapshot.report['profileState'], 'Unknown')
        self.assertEqual(snapshot.report['qualityOutcomes'], [])
        self.assertFalse(snapshot.report['baselinePublished'])
        self.assertNotIn(str(self.root), repr(snapshot.report))
        self._record_completed_fixture('no-official', result, telemetry_value)

    def test_compiled_no_official_hostile_terminals_do_not_return_snapshots(self):
        receive = fc.FramedSession.receive
        wait_owned = fc.ControllerAdapter._wait_owned

        for fault in ('error_done_zero', 'null_status', 'boolean_status', 'missing_done', 'wrapper_mismatch'):
            with self.subTest(fault=fault):
                context, child_env, _telemetry = self._completed_fixture_context('no-official')
                pending = []

                def hostile_receive(session, allowed, body_limit=fc.MAX_FRAME):
                    if pending:
                        return pending.pop()
                    frame = receive(session, allowed, body_limit)
                    if frame.kind != 'DONE':
                        return frame
                    if fault == 'error_done_zero':
                        pending.append(frame)
                        return fc.Frame('ERROR', {'stage': 'check', 'status': 'failed',
                                                  'code': 'check_failed'}, frame.sequence,
                                        frame.nonce, b'{"fixture":"error"}')
                    if fault == 'null_status':
                        payload = dict(frame.payload, exit_status=None)
                        return fc.Frame(frame.kind, payload, frame.sequence, frame.nonce, frame.raw)
                    if fault == 'boolean_status':
                        payload = dict(frame.payload, exit_status=True)
                        return fc.Frame(frame.kind, payload, frame.sequence, frame.nonce, frame.raw)
                    if fault == 'missing_done':
                        raise fc.ProtocolError('fixture suppressed terminal DONE')
                    return frame

                def mismatched_wait(controller, session, timeout_seconds):
                    return wait_owned(controller, session, timeout_seconds) + 1

                fc.FramedSession.receive = hostile_receive
                if fault == 'wrapper_mismatch':
                    fc.ControllerAdapter._wait_owned = mismatched_wait
                try:
                    with self.assertRaises(fc.ProtocolError):
                        fc.run_full(context, self.binary, child_env, REPO_ROOT,
                                    timeout_seconds=15, observer=self._observer)
                finally:
                    fc.FramedSession.receive = receive
                    fc.ControllerAdapter._wait_owned = wait_owned
                self.assertFalse(self.marker.exists())
                self.assertEqual(self._read_log(), '')

    def test_compiled_no_official_late_mutation_drain_and_disposal_never_publish(self):
        original_goenv = self.child_goenv.read_bytes()
        try:
            context, child_env, _telemetry = self._completed_fixture_context('no-official')
            mutated = []

            def late_mutation_observer(event, monitor, footprint):
                self._observer(event, monitor, footprint)
                if event == 'wrapper-reaped' and not mutated:
                    self.child_goenv.write_bytes(original_goenv + b'# late fixture mutation\\n')
                    mutated.append(True)

            with self.assertRaises(fc.ControllerMutation):
                fc.run_full(context, self.binary, child_env, REPO_ROOT, timeout_seconds=15,
                            observer=late_mutation_observer)
            self.assertTrue(mutated)
            self.child_goenv.write_bytes(original_goenv)

            for fault in ('final_drain', 'dispose'):
                with self.subTest(fault=fault):
                    context, child_env, _telemetry = self._completed_fixture_context('no-official')
                    after_reap = threading.Event()

                    def monitor_factory():
                        monitor = fc.closure.Monitor()

                        class FaultMonitor:
                            def __getattr__(self, name):
                                return getattr(monitor, name)

                            def drain(self):
                                if fault == 'final_drain' and after_reap.is_set():
                                    raise OSError('owned fixture final drain failed')
                                return monitor.drain()

                            def close(self):
                                monitor.close()
                                if fault == 'dispose':
                                    raise OSError('owned fixture observer disposal failed')

                        return FaultMonitor()

                    def observed(event, monitor, footprint):
                        self._observer(event, monitor, footprint)
                        if event == 'wrapper-reaped':
                            after_reap.set()

                    with self.assertRaises(fc.ControllerUnavailable):
                        fc.run_full(context, self.binary, child_env, REPO_ROOT,
                                    timeout_seconds=15, observer=observed,
                                    monitor_factory=monitor_factory)
                    self.assertFalse(self.marker.exists())
                    self.assertEqual(self._read_log(), '')
        finally:
            self.child_goenv.write_bytes(original_goenv)

    def test_actual_compiled_child_return_barrier_failure_and_final_observation(self):
        current_sources = {str(path): hashlib.sha256(path.read_bytes()).hexdigest()
                           for path in self.compilation_source_paths}
        self.assertEqual(current_sources, self.compilation_sources,
                         'compiled helper E package-source bindings must remain unchanged')
        verification_failures = []
        verify_calls = []
        recapture_calls = []
        original_recapture = self.context.recapture

        def tracked_recapture(context, acquired=None):
            recapture_calls.append((context, acquired))
            return original_recapture(context, acquired)

        self.context.recapture = tracked_recapture

        def _verify_leaf_body():
            verify_calls.append(True)
            learned = next((entry for entry in reversed(self.monitor_seen) if entry[0] == 'learned-registered'), None)
            if learned is None:
                verification_failures.append('actual Monitor did not register the returned distribution')
                raise AssertionError(verification_failures[-1])
            if len(recapture_calls) != 2:
                verification_failures.append('learned scope full-reader rediscovery did not occur')
                raise AssertionError(verification_failures[-1])
            _, monitor, members = learned
            self.assertIn(str(self.tc / 'bin/go'), members)
            self.assertIn(str(self.tc / 'VERSION'), members)
            for watched in (str(self.tc / 'bin/go'), str(self.tc / 'VERSION')):
                if watched not in monitor.paths:
                    verification_failures.append('actual Monitor did not register returned tree path ' + watched)
                    raise AssertionError(verification_failures[-1])
            self.assertIn(str(self.child_goenv), members)
            self.assertIn(str(self.child_goenv), monitor.paths)

        def verify_leaf():
            try:
                _verify_leaf_body()
            except AssertionError as exc:
                verification_failures.append(str(exc))
                raise

        join_entered = threading.Event()
        release_join = threading.Event()
        run_done = threading.Event()
        outcome = {}

        def gated_join(reader, timeout):
            join_entered.set()
            if not release_join.wait(timeout):
                return False
            return reader.join(timeout)

        def invoke():
            try:
                outcome['result'] = fc.run_full(self.context, self.binary, self.child_env, REPO_ROOT,
                                                verify_leaf=verify_leaf, timeout_seconds=5,
                                                observer=self._observer, stderr_joiner=gated_join)
            except BaseException as exc:
                outcome['error'] = exc
            finally:
                run_done.set()

        invocation = threading.Thread(target=invoke, name='owned-controller-integration')
        invocation.start()
        entered = join_entered.wait(5)
        try:
            if entered and 'wrapper-reaped' in self.events:
                self.assertFalse(run_done.is_set(), 'classification escaped the held final stderr join')
        finally:
            release_join.set()
        invocation.join(5)
        self.assertFalse(invocation.is_alive(), 'controller did not finish after stderr gate release')
        if not entered and outcome.get('result') is None and 'error' not in outcome:
            self.fail('P.run_full did not dispatch the controller to its owned helper launch')
        if 'error' in outcome:
            if verification_failures:
                self.fail('actual helper barrier control failed: ' + '; '.join(verification_failures))
            self.assertTrue(self.marker.exists(),
                            'expected RELEASE to produce the actual fake build/terminal progression')
            self.fail('actual helper launch/protocol/barrier integration failed: ' + repr(outcome['error']))
        self.assertTrue(entered, 'terminal stderr join was not entered')
        self.assertIn('wrapper-reaped', self.events,
                      'terminal path did not reach wrapper reap: ' + repr(outcome) +
                      ' verification=' + repr(verification_failures))
        self.assertIsNotNone(outcome.get('result'), 'P.run_full did not dispatch the controller')
        result = outcome['result']
        self.assertEqual(result.state, 'ordinary-failure')
        self.assertEqual(result.error_code, 'check_failed')
        self.assertEqual(result.done_status, 1)
        self.assertEqual(result.wrapper_status, 1)
        self.assertIsNone(result.snapshot)
        self.assertEqual(verify_calls, [True])
        self.assertTrue(self.marker.is_file())
        self.assertIn('build-started', self.marker.read_text())
        self.assertEqual(self.events.count('before-release'), 1)
        self.assertIn('FINAL-OBSERVATION', result.transitions)
        self.assertEqual(result.report['profileState'], 'Unknown')
        self.assertEqual(result.report['qualityOutcomes'], [])
        self.assertFalse(result.report['baselinePublished'])
        self.assertEqual(result.report['actualGoFamilyArgv'], [])
        self.assertEqual(result.report['helperProcessLaunches'], 1)
        self.assertEqual(len(recapture_calls), 4, 'both learned and final contexts must be rediscovered')
        self.assertEqual(result.report['metadataProviderCalls'], 6)
        self.assertEqual(len(self.metadata_calls), 6)
        self.assertTrue(all(call[0][1:3] == ('env', '-json') for call in self.metadata_calls))
        self.assertFalse(any('pack-binaries check' in ' '.join(call[0]) for call in self.metadata_calls))
        self.assertEqual(self.full_capture.env, self.parent_env)
        self.assertEqual(len(self.full_capture.commands), 3)
        self.assertEqual(len(self.context.child_full.commands), 3)
        self.assertIn(self.child_goenv, self.context.child_full.config_paths(
            self.context.child_metadata['base']))
        command_log = self._read_log()
        self.assertIn('env -json GOPROXY GOSUMDB', command_log)
        self.assertIn('mod download -json', command_log)
        self.assertIn('version', command_log)
        self.assertIn('build -trimpath', command_log)
        self.assertIn('build-produced', command_log)
        self.assertNotEqual(hashlib.sha256(b'synthetic build bytes\\n').hexdigest(), 'a' * 64)
        report_text = repr(result.report)
        self.assertNotIn(str(self.root), report_text)
        self.assertNotIn('h1:', report_text)
        self.assertEqual(result.transitions,
                         ('INIT', 'HELLO', 'START', 'RETURN:provider_return', 'RELEASE', 'ERROR',
                          'DONE', 'FINISH', 'WAIT', 'FINAL-OBSERVATION'))
        source = (E_ROOT / 'main.go').read_bytes()
        self.assertGreater(len(source), 0)

    def _read_log(self):
        return self.log.read_text() if self.log.exists() else ''

    def test_child_metadata_context_mismatch_refuses_before_release(self):
        def mismatched_runner(argv, *, cwd, env):
            values = self._metadata_runner(argv, cwd=cwd, env=env)
            values['GOHOSTOS'] = 'not-the-child-host'
            return values

        full = fc.FullCapture(self.parent_env, REPO_ROOT, runner=self._metadata_runner)
        context = fc.ContextCapture(full, self.binary, REPO_ROOT, self.child_env,
                                    [str(self.binary), '-test.short=true',
                                     '-test.run=^TestGoEmitterCompiledPrefixHelperChild$'],
                                    metadata_runner=mismatched_runner)
        with self.assertRaises(fc.ProtocolError):
            fc.run_full(context, self.binary, self.child_env, REPO_ROOT, timeout_seconds=5,
                        observer=self._observer)
        self.assertFalse(self.marker.exists(), 'context mismatch must not cross RELEASE')
        self.assertNotIn('before-release', self.events)
        self.assertEqual(len(self.metadata_calls), 6)

    def test_child_downloader_mismatch_refuses_before_release(self):
        alternate = self.root / 'alternate-bin'
        alternate.mkdir()
        (alternate / 'go').symlink_to(self.fake_go)
        child_env = dict(self.child_env)
        child_env['PATH'] = str(alternate) + os.pathsep + child_env['PATH']
        context = fc.ContextCapture(
            fc.FullCapture(self.parent_env, REPO_ROOT, runner=self._metadata_runner),
            self.binary, REPO_ROOT, child_env,
            [str(self.binary), '-test.short=true',
             '-test.run=^TestGoEmitterCompiledPrefixHelperChild$'],
            metadata_runner=self._metadata_runner)
        with self.assertRaises(fc.ProtocolError):
            fc.run_full(context, self.binary, child_env, REPO_ROOT,
                        timeout_seconds=5, observer=self._observer)
        self.assertFalse(self.marker.exists())
        self.assertNotIn('before-release', self.events)

    def test_child_pin_mismatch_refuses_before_release(self):
        class WrongPinCapture(fc.FullCapture):
            def __init__(self, env, cwd, runner):
                super().__init__(env, cwd, runner=runner)
                self.pin = 'go0.0.0-test-mismatch'

        context = fc.ContextCapture(
            fc.FullCapture(self.parent_env, REPO_ROOT, runner=self._metadata_runner),
            self.binary, REPO_ROOT, self.child_env,
            [str(self.binary), '-test.short=true',
             '-test.run=^TestGoEmitterCompiledPrefixHelperChild$'],
            metadata_runner=self._metadata_runner, child_capture_factory=WrongPinCapture)
        with self.assertRaises(fc.ProtocolError):
            fc.run_full(context, self.binary, self.child_env, REPO_ROOT,
                        timeout_seconds=5, observer=self._observer)
        self.assertFalse(self.marker.exists())
        self.assertNotIn('before-release', self.events)

    def _precompute_fixture_scope_budget(self):
        defaults = fc.Limits()
        parent_capture = fc.FullCapture(self.parent_env, REPO_ROOT, runner=self._metadata_runner)
        parent_metadata = {name: parent_capture.metadata('full', target) for name, target in
                           (('base', None), ('lint-linux', 'linux'), ('lint-darwin', 'darwin'))}
        parent_scope = parent_capture.discover(parent_metadata, self.parent_env, None)
        child_capture = fc.FullCapture(self.child_env, REPO_ROOT, runner=self._metadata_runner)
        child_metadata = {name: child_capture.metadata('full', target) for name, target in
                          (('base', None), ('lint-linux', 'linux'), ('lint-darwin', 'darwin'))}
        child_scope = child_capture.discover(child_metadata, self.child_env, None)
        context = fc.ContextCapture(parent_capture, self.binary, REPO_ROOT, self.child_env,
                                    [str(self.binary), '-test.short=true',
                                     '-test.run=^TestGoEmitterCompiledPrefixHelperChild$'],
                                    metadata_runner=self._metadata_runner, limits=defaults)
        go = fc.shutil.which('go', path=self.child_env.get('PATH', ''))
        self.assertIsNotNone(go)
        source_scope = fc.closure.identity(
            context._source_paths + (Path(self.binary).resolve(strict=True), Path(go).absolute()),
            trees=context._source_trees, limits=defaults)
        base = fc.merge_scopes((parent_scope, child_scope, source_scope), defaults)
        return len(base.members), base.bytes

    def test_combined_reader_byte_entry_and_watch_limits_refuse_before_release(self):
        for budget in ('bytes', 'entries', 'watches'):
            with self.subTest(budget=budget):
                self.marker.unlink(missing_ok=True)
                self.events.clear()
                self.monitor_seen.clear()
                if budget == 'bytes':
                    base_entries, base_bytes = self._precompute_fixture_scope_budget()
                    limits = fc.Limits(entries=65536, bytes=base_bytes + 1)
                elif budget == 'entries':
                    base_entries, _base_bytes = self._precompute_fixture_scope_budget()
                    limits = fc.Limits(entries=base_entries + 1)
                else:
                    limits = fc.Limits(watches=1)
                context = fc.ContextCapture(
                    fc.FullCapture(self.parent_env, REPO_ROOT, runner=self._metadata_runner),
                    self.binary, REPO_ROOT, self.child_env,
                    [str(self.binary), '-test.short=true',
                     '-test.run=^TestGoEmitterCompiledPrefixHelperChild$'],
                    metadata_runner=self._metadata_runner, limits=limits)
                self.metadata_calls.clear()
                scope_calls = []

                def scope_reader_hook(phase, base, acquired, error):
                    scope_calls.append((phase, base, acquired, error))

                failure = None
                try:
                    fc.run_full(context, self.binary, self.child_env, REPO_ROOT,
                                timeout_seconds=5, observer=self._observer,
                                scope_reader_hook=scope_reader_hook)
                except BaseException as exc:
                    failure = exc
                if budget in ('bytes', 'entries'):
                    self.assertIsInstance(failure, fc.AcquisitionFailure,
                                          'reduced allowance must fail in A distribution scope reader')
                    self.assertEqual(failure.code, 'distribution_scope_unknown')
                    self.assertEqual([call[0] for call in scope_calls], ['enter', 'error'])
                    acquired = scope_calls[0][2]
                    self.assertIsNotNone(acquired, 'A reader must receive the accepted provider result')
                    self.assertTrue(acquired.returned_binary)
                    self.assertEqual(len(self.metadata_calls), 6,
                                     'valid parent/child whitelisted metadata must precede A reader refusal')
                else:
                    self.assertIsInstance(failure, fc.ControllerUnavailable,
                                          'reduced watch allowance must refuse at real Monitor registration')
                    self.assertEqual(scope_calls, [], 'watch refusal occurs before provider result scope reading')
                self.assertFalse(self.marker.exists(), 'bounded reader refusal must precede RELEASE/build')
                self.assertNotIn('before-release', self.events)

    def _assert_metadata_only_descriptor_drift_invalidates(self, side):
        drift = threading.Event()
        descriptors = []
        if side == 'parent':
            reader = self.context._discover_parent
            def discover_parent(metadata, env):
                values = metadata
                if drift.is_set():
                    values = {name: dict(item) for name, item in metadata.items()}
                    values['base']['GOEXPERIMENT'] = 'fixture-only-parent-drift'
                result = reader(values, env)
                descriptors.append((drift.is_set(), result))
                return result
            self.context._discover_parent = discover_parent
        else:
            reader = self.context._discover_child
            def discover_child(child, metadata, env):
                values = metadata
                if drift.is_set():
                    values = {name: dict(item) for name, item in metadata.items()}
                    values['base']['GOEXPERIMENT'] = 'fixture-only-child-drift'
                result = reader(child, values, env)
                descriptors.append((drift.is_set(), result))
                return result
            self.context._discover_child = discover_child

        def observer(event, monitor, footprint):
            self._observer(event, monitor, footprint)
            if event == 'learned-registered':
                drift.set()

        failure = None
        try:
            fc.run_full(self.context, self.binary, self.child_env, REPO_ROOT,
                        timeout_seconds=5, observer=observer)
        except BaseException as exc:
            failure = exc
        before = next(result for changed, result in descriptors if not changed)
        after = next(result for changed, result in descriptors if changed)
        self.assertEqual(before.members, after.members,
                         'metadata-only GOEXPERIMENT drift must preserve exact members')
        self.assertEqual(before.bytes, after.bytes,
                         'metadata-only GOEXPERIMENT drift must preserve byte counts')
        self.assertNotEqual(before.digest, after.digest,
                            'C descriptor must bind changed effective GOEXPERIMENT metadata')
        self.assertIsInstance(failure, fc.ControllerMutation,
                              f'{side} reader descriptor drift must invalidate protected recapture')
        self.assertFalse(self.marker.exists(), 'descriptor drift must refuse before RELEASE/build')
        self.assertNotIn('before-release', self.events)
        self.assertEqual(len(self.metadata_calls), 6, 'recapture must use cached metadata, not refresh it')

    def test_child_goexperiment_metadata_only_drift_invalidates_protected_recapture(self):
        self._assert_metadata_only_descriptor_drift_invalidates('child')

    def test_parent_goexperiment_metadata_only_drift_invalidates_protected_recapture(self):
        self._assert_metadata_only_descriptor_drift_invalidates('parent')

    def test_child_goenv_restored_write_is_monitor_mutation_before_release(self):
        def mutate_and_restore():
            original = self.child_goenv.read_bytes()
            self.child_goenv.write_bytes(original + b'# transient\\n')
            self.child_goenv.write_bytes(original)

        with self.assertRaises(fc.ControllerMutation):
            fc.run_full(self.context, self.binary, self.child_env, REPO_ROOT,
                        verify_leaf=mutate_and_restore, timeout_seconds=5, observer=self._observer)
        self.assertFalse(self.marker.exists(), 'decoded child GOENV mutation must prevent RELEASE')
        self.assertNotIn('RELEASE', self._read_log())

    def test_late_returned_scope_restored_write_is_detected_after_wrapper_reap(self):
        changed = []

        def late_mutation(event, monitor, footprint):
            self._observer(event, monitor, footprint)
            if event == 'wrapper-reaped' and not changed:
                path = self.tc / 'VERSION'
                original = path.read_bytes()
                path.write_bytes(original + b'# transient\\n')
                path.write_bytes(original)
                changed.append(True)

        with self.assertRaises(fc.ControllerMutation):
            fc.run_full(self.context, self.binary, self.child_env, REPO_ROOT,
                        timeout_seconds=5, observer=late_mutation)
        self.assertTrue(changed, 'late returned-scope hook did not run after wrapper reap')
        self.assertTrue(self.marker.is_file())

    def test_decoded_restored_mutation_precedes_verify_and_cleanup_failures(self):
        observed_monitors = []
        pre_release_monitors = []

        def observe(event, monitor, footprint):
            self._observer(event, monitor, footprint)
            observed_monitors.append(monitor)
            if event == 'before-release':
                pre_release_monitors.append(monitor)

        def mutate_then_fail_verification():
            original = self.child_goenv.read_bytes()
            self.child_goenv.write_bytes(original + b'# restored mutation\\n')
            self.child_goenv.write_bytes(original)
            self.assertTrue(pre_release_monitors, 'protected Monitor was not retained before verification')
            with self.assertRaises(fc.Mutated):
                pre_release_monitors[-1].drain()
            self.assertTrue(pre_release_monitors[-1].events, 'actual Monitor did not decode mutation before fault')
            raise RuntimeError('injected verification failure')

        def fail_cleanup_stderr_joiner(reader, timeout):
            reader.join(timeout)
            return False

        failure = None
        try:
            fc.run_full(self.context, self.binary, self.child_env, REPO_ROOT,
                        verify_leaf=mutate_then_fail_verification, timeout_seconds=5,
                        observer=observe, stderr_joiner=fail_cleanup_stderr_joiner)
        except BaseException as exc:
            failure = exc
        self.assertIsInstance(failure, fc.ControllerMutation,
                              'decoded Monitor mutation must outrank verification/cleanup failures')
        self.assertTrue(any(monitor.events for monitor in observed_monitors),
                        'actual Monitor must decode the restored-write event before failure classification')
        self.assertFalse(self.marker.exists())

    def test_hostile_terminal_frames_missing_done_wrapper_mismatch_and_trailing_output(self):
        scope = types.SimpleNamespace(digest='terminal-scope', members={'owned': 'file:terminal'})

        class Monitor:
            def register(self, _scope):
                return None
            def drain(self):
                return None

        class Protected:
            current = scope
            monitor = Monitor()
            observer = None
            registrations = 0
            rereads = 0
            def final_drain(self):
                self.monitor.drain()
            def close(self, prior=None):
                return None

        class Context:
            def recapture(self, _reconciled, _acquired):
                return scope

        class Session:
            deadline = fc.time.monotonic() + 2
            def __init__(self, frame=None, missing=False, trailing=b''):
                self.frame = frame
                self.missing = missing
                self.buffer = bytearray(trailing)
                self.sent = []
            def receive(self, _allowed, _limit):
                if self.missing:
                    raise fc.ProtocolError('missing terminal DONE')
                return self.frame
            def send(self, kind, _payload):
                self.sent.append(kind)
            def close_input(self):
                return None

        def invoke(frame=None, pre_error=('check', 'check_failed'), wrapper=1,
                   missing=False, trailing=b''):
            controller = fc.ControllerAdapter.__new__(fc.ControllerAdapter)
            controller.context = Context()
            controller._process = types.SimpleNamespace(wait=lambda timeout: wrapper,
                                                        stdin=None, stdout=None, stderr=None)
            controller._stderr = fc.BoundedStderr(io.BytesIO(b''))
            controller.stderr_joiner = lambda reader, timeout: reader.join(timeout)
            controller._streams_closed = False
            controller.cancel_event = None
            controller._transitions = []
            controller.phase = 'started'
            controller.process_launches = 0
            controller.metadata_provider_calls = 0
            controller._protected = None
            session = Session(frame, missing=missing, trailing=trailing)
            return controller._finish_context_only(session, Protected(), object(), pre_error)

        hostile = (
            (fc.Frame('DONE', {'exit_status': None}, 1, 'terminal', b'{}'), ('check', 'check_failed'), 1, False, b''),
            (fc.Frame('DONE', {'exit_status': True}, 1, 'terminal', b'{}'), ('check', 'check_failed'), 1, False, b''),
            (fc.Frame('DONE', {'exit_status': 0}, 1, 'terminal', b'{}'), ('check', 'check_failed'), 0, False, b''),
            (fc.Frame('DONE', {'exit_status': 1}, 1, 'terminal', b'{}'), ('check', 'check_failed'), 0, False, b''),
            (fc.Frame('DONE', {'exit_status': 0}, 1, 'terminal', b'{}'), None, 0, False, b'extra'),
        )
        for frame, pre_error, wrapper, missing, trailing in hostile:
            with self.subTest(frame=frame, pre_error=pre_error, wrapper=wrapper, trailing=trailing):
                with self.assertRaises(fc.ProtocolError):
                    invoke(frame, pre_error, wrapper, missing, trailing)
        with self.assertRaises(fc.ProtocolError):
            invoke(missing=True)
        malformed_error = fc.Frame('ERROR', {'stage': 'check', 'status': None,
                                             'code': 'check_failed'}, 1, 'terminal', b'{}')
        with self.assertRaises(fc.ProtocolError):
            invoke(malformed_error, pre_error=None)

    def test_controller_codecs_reject_ambiguous_or_unbounded_inputs(self):
        with self.assertRaises(fc.ProtocolError):
            fc.strict_json(b'{"outer":{"x":1,"x":2}}')
        with self.assertRaises(fc.ProtocolError):
            fc.strict_json(b'{"x":1} {"y":2}')
        with self.assertRaises(fc.ProtocolError):
            fc._int(True)
        with self.assertRaises(fc.ProtocolError):
            fc._decode_base64('not base64', 64)
        with self.assertRaises(fc.ProtocolError):
            fc._decode_base64(base64.b64encode(b'x' * 65).decode(), 64)
        with self.assertRaises(fc.ProtocolError):
            fc._object({'x': 1, 'unexpected': 2}, ('x',))

    def test_selected_environment_uses_go_json_escape_budget_and_empty_present(self):
        selected = {key: {'present': False} for key in fc.ENV_KEYS}
        selected['PWD'] = {'present': True, 'value': ''}
        env = {key: None for key in fc.ENV_KEYS}
        env['PWD'] = ''
        self.assertEqual(fc.decode_selected_environment(selected, env)['PWD'], '')
        special = dict(selected)
        special['HOME'] = {'present': True, 'value': '<>&\u2028\u2029'}
        expected = dict(env); expected['HOME'] = '<>&\u2028\u2029'
        self.assertEqual(fc.decode_selected_environment(special, expected)['HOME'], expected['HOME'])
        self.assertGreater(fc._env_object_size(special), len(fc._canonical_json(special)))
        with self.assertRaises(fc.ProtocolError):
            bad = dict(selected); bad['HOME'] = {'present': True}
            fc.decode_selected_environment(bad, env)

    def test_frame_reader_rejects_oversized_partial_wrong_nonce_and_bool_header(self):
        import struct
        import types

        def framed():
            read_fd, write_fd = os.pipe()
            stream = os.fdopen(read_fd, 'rb', buffering=0)
            process = types.SimpleNamespace(args=['owned-helper'], stdin=None, stdout=stream)
            session = fc.FramedSession(process, fc.time.monotonic() + 1, nonce='controller-nonce-123')
            return session, write_fd, stream

        session, writer, stream = framed()
        try:
            os.write(writer, struct.pack('>I', fc.MAX_FRAME + 1))
            os.close(writer)
            with self.assertRaises(fc.ProtocolError):
                session.receive({'HELLO'})
        finally:
            stream.close()

        session, writer, stream = framed()
        try:
            os.write(writer, b'\\x00\\x01')
            os.close(writer)
            with self.assertRaises(fc.ProtocolError):
                session.receive({'HELLO'})
        finally:
            stream.close()

        for header in (
            {'protocol': 1, 'nonce': 'wrong-session-nonce', 'sequence': 1, 'kind': 'HELLO', 'payload': {}},
            {'protocol': True, 'nonce': 'controller-nonce-123', 'sequence': 1, 'kind': 'HELLO', 'payload': {}},
        ):
            body = fc._canonical_json(header)
            session, writer, stream = framed()
            try:
                os.write(writer, struct.pack('>I', len(body)) + body)
                os.close(writer)
                with self.assertRaises(fc.ProtocolError):
                    session.receive({'HELLO'})
            finally:
                stream.close()

    def _owned_pipe_session(self, cancel_event=None, io_wait_hook=None):
        read_stdin, write_stdin = os.pipe()
        read_stdout, write_stdout = os.pipe()
        process = types.SimpleNamespace(args=['owned-pipe-helper'],
                                        stdin=os.fdopen(write_stdin, 'wb', buffering=0),
                                        stdout=os.fdopen(read_stdout, 'rb', buffering=0))
        session = fc.FramedSession(process, fc.time.monotonic() + 20,
                                   nonce='controller-pipe-nonce', cancel_event=cancel_event,
                                   io_wait_hook=io_wait_hook)
        return session, process, read_stdin, write_stdout

    def _close_owned_pipe_session(self, session, read_stdin, write_stdout):
        session.close_input()
        session.process.stdout.close()
        for fd in (read_stdin, write_stdout):
            try:
                os.close(fd)
            except OSError:
                pass

    def test_framed_read_cancellation_interrupts_an_entered_owned_pipe_wait(self):
        canceled = threading.Event()
        waiting = threading.Event()
        process = fc.subprocess.Popen([sys.executable, '-c', 'import signal; signal.pause()'],
                                      stdin=fc.subprocess.PIPE, stdout=fc.subprocess.PIPE,
                                      stderr=fc.subprocess.PIPE, bufsize=0,
                                      start_new_session=True, close_fds=True)
        session = fc.FramedSession(process, fc.time.monotonic() + 20,
                                   nonce='controller-owned-child-nonce', cancel_event=canceled,
                                   io_wait_hook=lambda direction: waiting.set() if direction == 'read' else None)
        controller = fc.ControllerAdapter.__new__(fc.ControllerAdapter)
        controller._process = process
        controller._stderr = fc.BoundedStderr(process.stderr)
        controller.stderr_joiner = lambda reader, timeout: reader.join(timeout)
        controller._streams_closed = False
        controller._cleanup_attempted = False
        outcome = {}

        def receive():
            try:
                outcome['value'] = session._read_exact(4)
            except BaseException as exc:
                outcome['error'] = exc

        reader = threading.Thread(target=receive, name='owned-pipe-read-cancel')
        reader.start()
        entered = waiting.wait(3)
        canceled.set()
        reader.join(2)
        try:
            self.assertTrue(entered, 'reader did not enter a no-readiness poll')
            self.assertFalse(reader.is_alive(), 'read cancellation waited for the session deadline')
            self.assertIsInstance(outcome.get('error'), fc.ControllerUnavailable)
            self.assertEqual(session.read_sequence, 0)
            self.assertEqual(session.write_sequence, 0, 'no RELEASE/control frame advanced')
            self.assertIsNone(controller._cleanup(session))
            self.assertIsNotNone(process.poll(), 'owned process group was not reaped')
        finally:
            if process.poll() is None:
                controller._cleanup(session)
            controller._close_owned_streams()

    def test_framed_write_backpressure_preserves_sequence_on_cancellation(self):
        canceled = threading.Event()
        waiting = threading.Event()
        session, process, read_stdin, write_stdout = self._owned_pipe_session(
            cancel_event=canceled, io_wait_hook=lambda direction: waiting.set() if direction == 'write' else None)
        fd = process.stdin.fileno()
        while True:
            try:
                os.write(fd, b'x' * 65536)
            except BlockingIOError:
                break
        outcome = {}

        def send():
            try:
                session.send('RELEASE', {})
            except BaseException as exc:
                outcome['error'] = exc

        writer = threading.Thread(target=send, name='owned-pipe-write-cancel')
        writer.start()
        entered = waiting.wait(3)
        canceled.set()
        writer.join(2)
        try:
            self.assertTrue(entered, 'writer did not reach non-writable poll')
            self.assertFalse(writer.is_alive(), 'write cancellation waited for the session deadline')
            self.assertIsInstance(outcome.get('error'), fc.ControllerUnavailable)
            self.assertEqual(session.write_sequence, 0, 'partial/canceled RELEASE must not advance sequence')
        finally:
            self._close_owned_pipe_session(session, read_stdin, write_stdout)

    def test_cancel_after_partial_control_frame_delivery_does_not_advance_sequence(self):
        canceled = threading.Event()
        waiting = threading.Event()
        session, process, read_stdin, write_stdout = self._owned_pipe_session(
            cancel_event=canceled, io_wait_hook=lambda direction: waiting.set() if direction == 'write' else None)
        payload = {'padding': 'p' * (256 * 1024)}
        body = fc._canonical_json({'protocol': fc.PROTOCOL, 'nonce': session.nonce, 'sequence': 1,
                                   'kind': 'RELEASE', 'payload': payload})
        expected_wire = struct.pack('>I', len(body)) + body
        outcome = {}

        def send():
            try:
                session.send('RELEASE', payload, body_limit=fc.MAX_FRAME)
            except BaseException as exc:
                outcome['error'] = exc

        writer = threading.Thread(target=send, name='owned-pipe-cancel-partial-frame')
        writer.start()
        entered = waiting.wait(3)
        prefix = os.read(read_stdin, 1) if entered else b''
        canceled.set()
        writer.join(2)
        try:
            self.assertTrue(entered, 'large nonblocking frame did not reach backpressure after a partial write')
            self.assertFalse(writer.is_alive())
            self.assertTrue(prefix and expected_wire.startswith(prefix), 'partial wire prefix was not delivered')
            self.assertIsInstance(outcome.get('error'), fc.ControllerUnavailable)
            self.assertEqual(session.write_sequence, 0, 'canceled partial RELEASE must not advance sequence')
        finally:
            self._close_owned_pipe_session(session, read_stdin, write_stdout)

    def test_framed_partial_nonblocking_write_retries_offsets(self):
        waiting_initial = threading.Event()
        waiting_after_partial = threading.Event()
        wait_count = [0]
        drained_initial = threading.Event()
        release_reader = threading.Event()

        def wait_hook(direction):
            if direction == 'write':
                wait_count[0] += 1
                if wait_count[0] == 1:
                    waiting_initial.set()
                else:
                    waiting_after_partial.set()

        canceled = threading.Event()
        session, process, read_stdin, write_stdout = self._owned_pipe_session(
            cancel_event=canceled, io_wait_hook=wait_hook)
        stdin_fd = process.stdin.fileno()
        initial = bytearray()
        while True:
            try:
                chunk = b'x' * 65536
                count = os.write(stdin_fd, chunk)
                initial.extend(chunk[:count])
            except BlockingIOError:
                break
        payload = {'padding': 'p' * (256 * 1024)}
        body = fc._canonical_json({'protocol': fc.PROTOCOL, 'nonce': session.nonce, 'sequence': 1,
                                   'kind': 'RELEASE', 'payload': payload})
        expected_wire = struct.pack('>I', len(body)) + body
        written = {}

        def send_frame():
            try:
                session.send('RELEASE', payload, body_limit=fc.MAX_FRAME)
                written['done'] = True
            except BaseException as exc:
                written['error'] = exc

        def drain_frame():
            data = bytearray()
            remaining = len(initial)
            while remaining:
                chunk = os.read(read_stdin, min(8192, remaining))
                if not chunk:
                    written['reader_error'] = 'truncated initial pipe filler'
                    return
                data.extend(chunk)
                remaining -= len(chunk)
            drained_initial.set()
            release_reader.wait(3)
            remaining = len(expected_wire)
            while remaining:
                chunk = os.read(read_stdin, min(8192, remaining))
                if not chunk:
                    written['reader_error'] = 'truncated partial frame'
                    return
                data.extend(chunk)
                remaining -= len(chunk)
            written['wire'] = bytes(data[len(initial):])

        sender = threading.Thread(target=send_frame, name='owned-pipe-partial-write')
        reader = threading.Thread(target=drain_frame, name='owned-pipe-partial-drain')
        sender.start()
        entered = waiting_initial.wait(3)
        try:
            self.assertTrue(entered, 'writer did not first block on a full pipe')
            reader.start()
            self.assertTrue(drained_initial.wait(3), 'reader did not drain the original backpressure bytes')
            self.assertTrue(waiting_after_partial.wait(3), 'writer did not block again after a partial frame write')
            self.assertFalse(written.get('done', False))
            release_reader.set()
            sender.join(3)
            reader.join(3)
            self.assertFalse(sender.is_alive())
            self.assertFalse(reader.is_alive())
            self.assertNotIn('error', written)
            self.assertNotIn('reader_error', written)
            self.assertEqual(written.get('wire'), expected_wire)
            self.assertEqual(session.write_sequence, 1)
        finally:
            canceled.set()
            release_reader.set()
            if not process.stdin.closed:
                process.stdin.close()
            sender.join(2)
            if reader.ident is not None:
                reader.join(2)
            self._close_owned_pipe_session(session, read_stdin, write_stdout)

    def test_framed_partial_io_completes_and_truncated_pipe_fails(self):
        waiting = threading.Event()
        session, process, read_stdin, write_stdout = self._owned_pipe_session(
            io_wait_hook=lambda direction: waiting.set() if direction == 'read' else None)
        os.write(write_stdout, b'\x00\x04')
        outcome = {}

        def read_header():
            try:
                outcome['header'] = session._read_exact(4)
            except BaseException as exc:
                outcome['error'] = exc

        reader = threading.Thread(target=read_header, name='owned-pipe-partial-read')
        reader.start()
        entered = waiting.wait(3)
        os.write(write_stdout, b'bo')
        reader.join(2)
        try:
            self.assertTrue(entered, 'reader did not consume partial delivery and wait')
            self.assertFalse(reader.is_alive())
            self.assertEqual(outcome.get('header'), b'\x00\x04bo')
        finally:
            self._close_owned_pipe_session(session, read_stdin, write_stdout)

        session, process, read_stdin, write_stdout = self._owned_pipe_session()
        os.write(write_stdout, b'\x00\x04')
        os.close(write_stdout)
        with self.assertRaises(fc.ProtocolError):
            session._read_exact(4)
        self._close_owned_pipe_session(session, read_stdin, -1)

        canceled = threading.Event()
        waiting = threading.Event()
        session, process, read_stdin, write_stdout = self._owned_pipe_session(
            cancel_event=canceled, io_wait_hook=lambda direction: waiting.set() if direction == 'read' else None)
        os.write(write_stdout, b'\x00\x04')
        outcome = {}
        reader = threading.Thread(target=lambda: outcome.update(error=self._capture_read_error(session)),
                                  name='owned-pipe-partial-cancel')
        reader.start()
        entered = waiting.wait(3)
        canceled.set()
        reader.join(2)
        try:
            self.assertTrue(entered)
            self.assertFalse(reader.is_alive())
            self.assertIsInstance(outcome.get('error'), fc.ControllerUnavailable)
            self.assertEqual(bytes(session.buffer), b'\x00\x04')
            self.assertEqual(session.read_sequence, 0)
            self.assertEqual(session.write_sequence, 0)
        finally:
            self._close_owned_pipe_session(session, read_stdin, write_stdout)

    def _capture_read_error(self, session):
        try:
            session._read_exact(4)
        except BaseException as exc:
            return exc
        raise AssertionError('incomplete frame unexpectedly completed')

    def test_terminal_stderr_rejects_read_error_overflow_and_incomplete_join(self):
        class BrokenReader:
            def read(self, _size):
                raise OSError('private fixture read fault')

        controller = fc.ControllerAdapter.__new__(fc.ControllerAdapter)
        controller.stderr_joiner = lambda reader, timeout: reader.join(timeout)
        controller._stderr = fc.BoundedStderr(BrokenReader(), limit=3)
        with self.assertRaises(fc.ControllerUnavailable):
            controller._check_stderr()
        self.assertIsNotNone(controller._stderr.error)

        controller._stderr = fc.BoundedStderr(io.BytesIO(b'four'), limit=3)
        with self.assertRaises(fc.ControllerUnavailable):
            controller._check_stderr()
        self.assertTrue(controller._stderr.overflow)
        self.assertEqual(bytes(controller._stderr.data), b'four')

        class IncompleteReader:
            error = None
            overflow = False
            def join(self, _timeout):
                return False

        controller._stderr = IncompleteReader()
        controller.stderr_joiner = lambda reader, timeout: reader.join(timeout)
        with self.assertRaises(fc.ControllerUnavailable):
            controller._check_stderr()

    def test_owned_context_only_terminal_method_and_pipe_is_not_compiled_zero_plan(self):
        read_in, write_in = os.pipe()
        read_out, write_out = os.pipe()
        read_err, write_err = os.pipe()
        process = types.SimpleNamespace(stdin=os.fdopen(write_in, 'wb', buffering=0),
                                        stdout=os.fdopen(read_out, 'rb', buffering=0),
                                        stderr=os.fdopen(read_err, 'rb', buffering=0),
                                        wait=lambda timeout: 0, poll=lambda: 0)
        os.close(read_in)
        os.close(write_out)
        stderr = fc.BoundedStderr(process.stderr)
        scope = types.SimpleNamespace(digest='owned-context-digest', members=frozenset({'owned-context'}))
        terminal_events = []
        join_entered = threading.Event()
        release_join = threading.Event()
        finished = threading.Event()
        outcome = {}

        class Monitor:
            def register(self, _scope):
                return None
            def drain(self):
                return ()

        class Protected:
            current = scope
            monitor = Monitor()
            observer = lambda self, event, monitor, footprint: terminal_events.append(event)
            registrations = 0
            rereads = 0
            def final_drain(self):
                self.monitor.drain()
            def close(self, prior=None):
                return None

        class Context:
            def recapture(self, _reconciled, _acquired):
                return scope

        class Session:
            deadline = fc.time.monotonic() + 5
            def __init__(self):
                self.sent = []
                self.buffer = bytearray()
            def receive(self, allowed, _limit):
                if 'DONE' not in allowed:
                    raise AssertionError('context-only fixture expected terminal DONE')
                return fc.Frame('DONE', {'exit_status': 0}, 1, 'owned-context-only', b'{}')
            def send(self, kind, _payload):
                self.sent.append(kind)
            def close_input(self):
                process.stdin.close()

        def gated_join(reader, timeout):
            join_entered.set()
            if not release_join.wait(timeout):
                return False
            return reader.join(timeout)

        controller = fc.ControllerAdapter.__new__(fc.ControllerAdapter)
        controller.context = Context()
        controller._process = process
        controller._stderr = stderr
        controller.stderr_joiner = gated_join
        controller._streams_closed = False
        controller.cancel_event = None
        controller._transitions = []
        controller.phase = 'started'
        controller.process_launches = 0
        controller.metadata_provider_calls = 0
        session = Session()
        protected = Protected()

        def finish():
            try:
                outcome['result'] = controller._finish_context_only(session, protected, object())
            except BaseException as exc:
                outcome['error'] = exc
            finally:
                finished.set()

        worker = threading.Thread(target=finish, name='owned-context-only-terminal-pipe')
        worker.start()
        entered = join_entered.wait(3)
        try:
            if entered:
                self.assertIn('wrapper-reaped', terminal_events)
                self.assertFalse(finished.is_set())
        finally:
            os.close(write_err)
            release_join.set()
        try:
            worker.join(3)
            self.assertTrue(entered, 'context-only terminal did not reach stderr join')
            self.assertFalse(worker.is_alive())
            if 'error' in outcome:
                raise outcome['error']
            self.assertEqual(outcome['result'].state, 'context-only-complete')
            self.assertEqual(session.sent, ['FINISH'], 'P-owned method test must not send RELEASE')
            self.assertTrue(finished.is_set())
        finally:
            controller._close_owned_streams()

    def test_selector_zero_omissions_and_go_filemode_bits(self):
        path = self.root / 'zero-selector'
        path.write_bytes(b'')
        path.chmod(0)
        os.utime(path, ns=(0, 0))
        binding = fc._selector_binding(str(path))
        self.assertNotIn('mode', binding)
        self.assertNotIn('size', binding)
        self.assertNotIn('mod_time_ns', binding)
        self.assertEqual(fc._check_binding(dict(binding), str(path)), binding)
        explicit_zero = dict(binding, size=0)
        with self.assertRaises(fc.ProtocolError):
            fc._check_binding(explicit_zero, str(path))

        regular = stat.S_IFREG | 0o4751 | stat.S_ISGID | stat.S_ISVTX
        expected = 0o751 | (1 << 23) | (1 << 22) | (1 << 20)
        self.assertEqual(fc._mode_from_stat(type('Stat', (), {'st_mode': regular})()), expected)
        modes = (
            (stat.S_IFDIR | 0o755, 1 << 31),
            (stat.S_IFLNK | 0o777, 1 << 27),
            (stat.S_IFBLK | 0o600, 1 << 26),
            (stat.S_IFIFO | 0o600, 1 << 25),
            (stat.S_IFSOCK | 0o600, 1 << 24),
            (stat.S_IFCHR | 0o600, (1 << 26) | (1 << 21)),
        )
        for raw_mode, go_type in modes:
            with self.subTest(raw_mode=raw_mode):
                self.assertEqual(fc._mode_from_stat(type('Stat', (), {'st_mode': raw_mode})()),
                                 (raw_mode & 0o777) | go_type)
        with self.assertRaises(fc.ControllerUnavailable):
            fc._mode_from_stat(type('Stat', (), {'st_mode': 0o600})())

    def test_current_frame_codec_rejects_uppercase_null_and_boolean_statuses(self):
        self.assertRaises(fc.ProtocolError, fc._int, None, 0)
        self.assertRaises(fc.ProtocolError, fc._int, True, 0)
        controller = fc.ControllerAdapter.__new__(fc.ControllerAdapter)
        with self.assertRaises(fc.ProtocolError):
            controller._handle_error({'stage': 'check', 'status': None, 'code': 'check_failed'})
        with self.assertRaises(fc.ProtocolError):
            controller._handle_error({'stage': 'check', 'code': 'check_failed'})
        self.assertRaises(fc.ProtocolError, fc._int, None, 0)
        for header in (
            {'Protocol': 1, 'Nonce': 'controller-pipe-nonce', 'Sequence': 1,
             'Kind': 'HELLO', 'Payload': {}},
            {'protocol': 1, 'nonce': 'controller-pipe-nonce', 'sequence': 1,
             'kind': 'HELLO', 'payload': {}, 'extra': None},
        ):
            body = fc._canonical_json(header)
            read_fd, write_fd = os.pipe()
            stream = os.fdopen(read_fd, 'rb', buffering=0)
            process = type('Process', (), {'args': ['owned-pipe-helper'], 'stdin': None, 'stdout': stream})()
            session = fc.FramedSession(process, fc.time.monotonic() + 2, nonce='controller-pipe-nonce')
            try:
                os.write(write_fd, struct.pack('>I', len(body)) + body)
                os.close(write_fd)
                with self.assertRaises(fc.ProtocolError):
                    session.receive({'HELLO'})
            finally:
                stream.close()

    def test_source_named_check_graph_rejects_actual_dependency_deletion(self):
        justfile = (REPO_ROOT / 'Justfile').read_text(encoding='utf-8')
        deleted_dependency = self.root / 'Justfile-deleted-dependency'
        lines = justfile.splitlines()
        dependencies = [index for index, line in enumerate(lines)
                        if line == 'check-ci: lint-ci test-fast check-pack-binaries']
        self.assertEqual(len(dependencies), 1)
        lines[dependencies[0]] = 'check-ci: lint-ci test-fast'
        replaced = '\n'.join(lines) + '\n'
        self.assertIn('check-pack-binaries:', replaced)
        self.assertIn('go run ./tools/pack-binaries check', replaced)
        deleted_dependency.write_text(replaced)
        with self.assertRaises(fc.Unknown):
            fc._source_named_just_graph(deleted_dependency)
        lines = justfile.splitlines()
        parallel = [index for index, line in enumerate(lines) if line == '[parallel]']
        self.assertTrue(parallel)
        self.assertEqual(lines[parallel[-1] + 1].split(':', 1)[0], 'check-ci')
        del lines[parallel[-1]]
        missing_parallel = self.root / 'Justfile-no-parallel'
        missing_parallel.write_text('\n'.join(lines) + '\n')
        with self.assertRaises(fc.Unknown):
            fc._source_named_just_graph(missing_parallel)
        duplicate = self.root / 'Justfile-duplicate-check-ci'
        duplicate.write_text(justfile + chr(10) + 'check-ci: lint-ci test-fast check-pack-binaries' + chr(10))
        with self.assertRaises(fc.Unknown):
            fc._source_named_just_graph(duplicate)

    def test_source_deletion_mutants_break_stage_one_callsite_guards(self):
        """Legacy-named static source guards only; no semantic mutant is executed here."""
        source = (HERE / 'full_controller.py').read_text()
        guards = {
            'provider-dispatch': 'supplied, command_binding = reconcile_observation(observation, child, request_seed)',
            'returned-tree-registration': 'protected_session.learn(learned)',
            'release-after-protection': "protected_session.verify_and_release(lambda: session.send('RELEASE', {}))",
            'failed-session-drain': 'protected_session.close(prior)',
        }
        for guard, required in guards.items():
            with self.subTest(guard=guard):
                disposable = source.replace(required, '', 1)
                self.assertNotEqual(disposable, source)
                self.assertNotIn(required, disposable)
        learn_start = source.index('    def learn(self, footprint):')
        learn_end = source.index('    def verify_and_release', learn_start)
        learn = source[learn_start:learn_end]
        self.assertIn('reread = self.discover()', learn)
        mutated_learn = learn.replace('reread = self.discover()', '', 1)
        self.assertNotIn('reread = self.discover()', mutated_learn)

    def test_source_named_check_graph_and_stage_one_nonclaims(self):
        fp = fc._source_named_just_graph()
        self.assertGreater(fp.files, 0)
        source = (HERE / 'full_controller.py').read_text()
        tree = ast.parse(source)
        names = {node.name for node in ast.walk(tree) if isinstance(node, (ast.FunctionDef, ast.ClassDef))}
        self.assertTrue({'FramedSession', 'ContextCapture', 'ProtectedSession', 'ControllerAdapter',
                         'run_full', 'compare_prose'} <= names)
        self.assertIn("'-test.run=^TestGoEmitterCompiledPrefixHelperChild$'", source)
        self.assertIn("protected_session.learn(learned)", source)
        self.assertIn("protected_session.verify_and_release", source)
        self.assertIn("protected_session.final_drain()", source)
        self.assertIn('parent_reader = self._discover_parent(ctx.parent_metadata', source)
        self.assertIn('child_reader = self._discover_child(ctx.child, ctx.child.metadata', source)
        self.assertIn("'parentReaderDigest': parent_reader.digest", source)
        self.assertIn("'childReaderDigest': child_reader.digest", source)
        self.assertIn('child_reader = self._discover_child(ctx.child, ctx.child.metadata', source)
        self.assertIn("current = self.context.recapture(snapshot.context, snapshot.acquired)", source)
        self.assertIn("profileState='Unknown'", source)
        self.assertIn("qualityOutcomes=[]", source)
        self.assertIn('baselinePublished=False', source)

    def test_injected_metadata_runner_is_complete_and_never_a_live_go_probe(self):
        with self.assertRaises(fc.ControllerUnavailable):
            fc.ContextCapture(self.full_capture, self.binary, REPO_ROOT, self.child_env,
                              [str(self.binary), '-test.short=true',
                               '-test.run=^TestGoEmitterCompiledPrefixHelperChild$'],
                              metadata_runner=None)
        parent = dict(self.parent_env)
        full = fc.FullCapture(parent, REPO_ROOT, runner=self._metadata_runner)
        values = full.metadata('full')
        self.assertEqual(set(values), set(fc.FIELDS))
        self.assertEqual(full.commands[-1]['inheritedEnvironmentUnmodifiedExceptRecipeGOOS'], True)
        self.assertEqual(len(self.metadata_calls), 1)
        self.assertEqual(self.metadata_calls[0][0][1], 'env')
        self.assertEqual(self.metadata_calls[0][0][2], '-json')


class ControllerCompileSetupTests(unittest.TestCase):
    def test_timeout_cleanup_is_bounded_and_owned(self):
        setup_source = inspect.getsource(ControllerFixture.setUpClass)
        self.assertNotIn('subprocess.run(', setup_source)
        self.assertIn('cls.addClassCleanup(cls.unit_temp.cleanup)', setup_source)
        self.assertIn('_run_owned_compile(', setup_source)
        self.assertIn('timeout_seconds=120', setup_source)
        self.assertLess(setup_source.index('addClassCleanup'), setup_source.index('path.read_bytes'))

        command = ['go', 'test', '-c', '-short', '-mod=vendor', '-o', '/tmp/owned/emitter.test',
                   './tools/completion-experiment']
        cwd = str(REPO_ROOT)
        for interruption in ('timeout', 'cancel'):
            with self.subTest(interruption=interruption):
                events = []

                class FakeProcess:
                    pid = 41725
                    returncode = -signal.SIGKILL

                    def __init__(self):
                        self.communicate_calls = 0

                    def communicate(self, timeout=None):
                        self.communicate_calls += 1
                        events.append(('communicate', timeout))
                        if self.communicate_calls == 1:
                            if interruption == 'timeout':
                                raise subprocess.TimeoutExpired(command, timeout, output=b'partial stdout',
                                                                stderr=b'partial stderr')
                            raise KeyboardInterrupt()
                        events.append(('reaped', self.communicate_calls))
                        return b'cleanup stdout', b'cleanup stderr'

                    def kill(self):
                        events.append(('kill-process', self.pid))

                child = FakeProcess()
                factory_calls = []

                def popen_factory(argv, **kwargs):
                    factory_calls.append((argv, kwargs))
                    return child

                def kill_group(pid, sig):
                    events.append(('kill-group', pid, sig))

                expected_error = (subprocess.TimeoutExpired if interruption == 'timeout'
                                  else KeyboardInterrupt)
                with mock.patch.object(os, 'killpg', side_effect=kill_group):
                    with self.assertRaises(expected_error):
                        _run_owned_compile(command, cwd, timeout_seconds=120, popen_factory=popen_factory)
                self.assertEqual(len(factory_calls), 1, 'compile setup must not retry')
                argv, options = factory_calls[0]
                self.assertEqual(argv, command)
                self.assertEqual(options['cwd'], cwd)
                self.assertNotIn('env', options, 'compile must inherit the caller Go environment')
                self.assertTrue(options['start_new_session'], 'the compiler process tree must be owned')
                self.assertEqual(events[0], ('communicate', 120))
                self.assertIn(('kill-group', child.pid, signal.SIGKILL), events)
                self.assertEqual(events[-1], ('reaped', 2), 'timeout/cancellation must wait for the child')


if __name__ == '__main__':
    unittest.main(verbosity=2)
