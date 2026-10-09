#!/usr/bin/env python3
"""Private Stage-1 controller for the accepted compiled Go-emitter fixture.

This is a bounded fixture controller, not production wiring, acquisition policy,
quality evidence, or a reusable baseline. The source packets it composes are read-only.
"""
import sys
sys.dont_write_bytecode = True

import base64
import binascii
import hashlib
import json
import os
import re
import selectors
import shutil
import signal
import stat
import struct
import subprocess
import threading
import time
from dataclasses import dataclass, field, replace
from pathlib import Path
from typing import Any

EXPERIMENT_ROOT = Path(__file__).resolve().parent
REPO_ROOT = Path(__file__).resolve().parents[2]
C_ROOT = A_ROOT = E_ROOT = B_ROOT = EXPERIMENT_ROOT

import capture
import closure
from capture import ENV_KEYS, FIELDS, FullCapture, Refused
from closure import Footprint, Mutated, Unknown, Unavailable
from official_adapter import AcquisitionFailure, AcquisitionRequest, OfficialAdapter, ProviderReturn

PROTOCOL = 1
MAX_FRAME = 2 * 1024 * 1024
MAX_CONTROL = 4 * 1024
MAX_HELLO = 256 * 1024
MAX_ENV = 128 * 1024
MAX_SESSION_MS = 30 * 60 * 1000
JUSTFILE_LIMIT = 256 * 1024
MAX_STDERR = 16 * 1024
IO_POLL_SECONDS = 0.05
OUTPUT_LIMITS = {'effective-proxy': 64 * 1024, 'download': 1024 * 1024, 'version': 4 * 1024}
DYNAMIC_PREFIXES = ('NIX_CFLAGS', 'NIX_LDFLAGS', 'NIX_CC_WRAPPER', 'NIX_BINTOOLS_WRAPPER', 'NIX_DYNAMIC', 'NIX_HARDENING')
NONCE_RE = re.compile(r'^[A-Za-z0-9_.-]{16,128}$')
LEGACY_HELPER_ROOT = 'TestGoEmitterCompiledPrefixHelperChild'
COMPLETED_FIXTURE_ROOT = 'TestPythonControllerEmitterFixtureChild'
LEGACY_HELPER_FILTER = '-test.run=^TestGoEmitterCompiledPrefixHelperChild$'
COMPLETED_FIXTURE_FILTER = '-test.run=^TestPythonControllerEmitterFixtureChild$'


def _fixture_selection(executable, argv, environment):
    expected_executable = str(Path(executable).resolve(strict=True))
    arguments = tuple(argv)
    if (len(arguments) != 3 or arguments[0] != expected_executable or
            arguments[1] != '-test.short=true'):
        raise Refused('compiled helper launch must use one exact short anchored fixture root')
    if arguments[2] == LEGACY_HELPER_FILTER:
        if environment.get('YOLO_GO_EMITTER_COMPILED_CHILD') != '1':
            raise Refused('legacy fixture child guard is not selected')
        return LEGACY_HELPER_ROOT, None
    if arguments[2] != COMPLETED_FIXTURE_FILTER:
        raise Refused('compiled helper launch root is unsupported')
    mode = environment.get('YOLO_PYTHON_CONTROLLER_EMITTER_MODE')
    if (environment.get('YOLO_PYTHON_CONTROLLER_EMITTER_CHILD') != '1' or
            mode not in ('matching', 'no-official') or
            not environment.get('YOLO_PYTHON_CONTROLLER_EMITTER_TELEMETRY')):
        raise Refused('completed-fixture child mode or telemetry binding is unsupported')
    return COMPLETED_FIXTURE_ROOT, mode


@dataclass(frozen=True)
class Limits:
    entries: int = 65536
    bytes: int = 768 * 1024 * 1024
    watches: int = 65536
    stabilization_attempts: int = 4
    excess_detection_bytes: int = 1


class ControllerError(Exception):
    """Generic controller refusal; messages never contain observations or paths."""


class ControllerMutation(Mutated):
    pass


class ControllerUnavailable(Unavailable):
    pass


class ProtocolError(ControllerError):
    pass


def _pairs_no_duplicates(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError('duplicate JSON key')
        result[key] = value
    return result


def _reject_constant(_value):
    raise ValueError('non-JSON constant')


def strict_json(data: bytes):
    try:
        text = data.decode('utf-8', errors='strict')
        return json.loads(text, object_pairs_hook=_pairs_no_duplicates, parse_constant=_reject_constant)
    except (UnicodeError, ValueError, TypeError, RecursionError):
        raise ProtocolError('malformed or ambiguous JSON') from None


def _object(value, required, optional=()):
    if not isinstance(value, dict) or set(value) - set(required) - set(optional) or set(required) - set(value):
        raise ProtocolError('unexpected object shape')
    return value


def _str(value):
    if not isinstance(value, str):
        raise ProtocolError('expected string')
    try:
        value.encode('utf-8')
    except UnicodeError:
        raise ProtocolError('invalid text') from None
    return value


def _int(value, minimum=None, maximum=None):
    if type(value) is not int or (minimum is not None and value < minimum) or (maximum is not None and value > maximum):
        raise ProtocolError('invalid integer')
    return value


def _bool(value):
    if type(value) is not bool:
        raise ProtocolError('invalid boolean')
    return value


def _canonical_json(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':'), ensure_ascii=False).encode('utf-8')


def _env_object_size(value):
    # The selected maps contain string keys and small objects; account for the
    # extra bytes encoding/json spends escaping HTML-sensitive runes.
    text = _canonical_json(value).decode('utf-8')
    size = len(text.encode('utf-8'))
    for char in ('<', '>', '&', '\u2028', '\u2029'):
        size += text.count(char) * (6 - len(char.encode('utf-8')))
    return size


def _selected_keys(env):
    keys = set(ENV_KEYS) | {'PWD'}
    keys.update(k for k in env if k.startswith(DYNAMIC_PREFIXES))
    return keys


def decode_selected_environment(raw, actual_env=None):
    if not isinstance(raw, dict):
        raise ProtocolError('invalid selected environment')
    if actual_env is None:
        expected_keys = set(ENV_KEYS) | {'PWD'}
        expected_keys.update(k for k in raw if k.startswith(DYNAMIC_PREFIXES))
    else:
        expected_keys = _selected_keys(actual_env)
    if set(raw) != expected_keys:
        raise ProtocolError('selected environment key set differs')
    out = {}
    for key, entry in raw.items():
        _str(key)
        _object(entry, ('present',), ('value',))
        present = _bool(entry['present'])
        if present:
            value = _str(entry.get('value', ''))
            out[key] = value
        else:
            if 'value' in entry:
                raise ProtocolError('absent environment entry carried a value')
            out[key] = None
    if actual_env is not None:
        expected = {key: actual_env[key] if key in actual_env else None for key in expected_keys}
        if out != expected:
            raise ProtocolError('selected environment does not match direct launch mapping')
    if _env_object_size(raw) > MAX_ENV:
        raise ProtocolError('selected environment exceeds bound')
    return out


def _mode_from_stat(st):
    mode = stat.S_IMODE(st.st_mode) & 0o777
    if stat.S_ISDIR(st.st_mode):
        mode |= 1 << 31
    elif stat.S_ISLNK(st.st_mode):
        mode |= 1 << 27
    elif stat.S_ISBLK(st.st_mode):
        mode |= 1 << 26
    elif stat.S_ISFIFO(st.st_mode):
        mode |= 1 << 25
    elif stat.S_ISSOCK(st.st_mode):
        mode |= 1 << 24
    elif stat.S_ISCHR(st.st_mode):
        mode |= (1 << 26) | (1 << 21)
    elif not stat.S_ISREG(st.st_mode):
        raise ControllerUnavailable('selector file type is unsupported')
    if st.st_mode & stat.S_ISUID:
        mode |= 1 << 23
    if st.st_mode & stat.S_ISGID:
        mode |= 1 << 22
    if st.st_mode & stat.S_ISVTX:
        mode |= 1 << 20
    return mode


def _selector_binding(path):
    if not path:
        return {'path': '', 'status': 'unavailable'}
    result = {'path': path, 'status': 'unavailable'}
    try:
        resolved = str(Path(path).resolve(strict=True))
        st = os.stat(path)
        mode = _mode_from_stat(st)
        result.update(resolved=resolved, status='stat')
        if mode:
            result['mode'] = mode
        if st.st_size:
            result['size'] = st.st_size
        if st.st_mtime_ns:
            result['mod_time_ns'] = st.st_mtime_ns
    except (OSError, RuntimeError):
        return result
    try:
        link_target = os.readlink(path)
    except OSError:
        pass
    else:
        if link_target:
            result['link_target'] = link_target
    return result


def _check_binding(binding, expected_path):
    optional = ('resolved', 'mode', 'size', 'mod_time_ns', 'link_target')
    _object(binding, ('path', 'status'), optional)
    path = _str(binding['path'])
    status = _str(binding['status'])
    if path != expected_path:
        raise ProtocolError('selector path differs from independent selection')
    expected = _selector_binding(expected_path)
    if status != expected['status']:
        raise ProtocolError('selector availability differs')
    for name in optional:
        if name in expected:
            if name not in binding or type(binding[name]) is not type(expected[name]) or binding[name] != expected[name]:
                raise ProtocolError('selector binding differs')
        elif name in binding:
            raise ProtocolError('selector field should be omitted at its Go zero value')
    return expected


def _direct_child_env(parent_env, overrides):
    child = dict(parent_env)
    child.update(overrides)
    child.setdefault('PWD', overrides.get('PWD', child.get('PWD', '')))
    return child


def _environment_payload_size(raw):
    # Go encoding/json escapes HTML-sensitive strings even though Python doesn't by default.
    return _env_object_size(raw)


@dataclass
class Frame:
    kind: str
    payload: dict
    sequence: int
    nonce: str
    raw: bytes = field(repr=False)


class FramedSession:
    """Bounded bidirectional protocol endpoint owning only its launched child."""
    def __init__(self, process, deadline, nonce=None, stderr_state=None, cancel_event=None, io_wait_hook=None):
        self.process = process
        self.deadline = deadline
        self.cancel_event = cancel_event
        self.io_wait_hook = io_wait_hook
        for stream in (process.stdout, process.stdin):
            if stream is not None:
                os.set_blocking(stream.fileno(), False)
        self.nonce = nonce or ('python-controller-' + hashlib.sha256(os.urandom(32)).hexdigest()[:20])
        self.read_sequence = 0
        self.write_sequence = 0
        self.buffer = bytearray()
        self.stderr_state = stderr_state
        self.launch_argv = tuple(process.args) if isinstance(process.args, (list, tuple)) else (str(process.args),)
        self.process_launches = 1
        if not NONCE_RE.fullmatch(self.nonce):
            raise ProtocolError('invalid local session nonce')

    def _remaining(self):
        if self.cancel_event is not None and self.cancel_event.is_set():
            raise ControllerUnavailable('session canceled')
        remaining = self.deadline - time.monotonic()
        if remaining <= 0:
            raise ControllerUnavailable('session deadline expired')
        return remaining

    def _wait_ready(self, selector, direction):
        while True:
            remaining = self._remaining()
            try:
                ready = selector.select(min(IO_POLL_SECONDS, remaining))
            except InterruptedError:
                self._remaining()
                continue
            self._remaining()
            if ready:
                return True
            if self.io_wait_hook is not None:
                self.io_wait_hook(direction)

    def _read_exact(self, size):
        fd = self.process.stdout.fileno()
        selector = selectors.DefaultSelector()
        selector.register(fd, selectors.EVENT_READ)
        try:
            while len(self.buffer) < size:
                self._remaining()
                self._wait_ready(selector, 'read')
                self._remaining()
                try:
                    chunk = os.read(fd, min(65536, size - len(self.buffer)))
                except (BlockingIOError, InterruptedError):
                    self._remaining()
                    continue
                if not chunk:
                    raise ProtocolError('truncated protocol stream')
                self.buffer.extend(chunk)
            result = bytes(self.buffer[:size])
            del self.buffer[:size]
            return result
        finally:
            selector.close()

    def send(self, kind, payload, body_limit=MAX_CONTROL):
        if kind not in ('INIT', 'START', 'RELEASE', 'FINISH'):
            raise ProtocolError('invalid controller frame kind')
        body = {'protocol': PROTOCOL, 'nonce': self.nonce, 'sequence': self.write_sequence + 1,
                'kind': kind, 'payload': payload}
        encoded = _canonical_json(body)
        if len(encoded) > min(body_limit, MAX_FRAME):
            raise ProtocolError('outbound frame exceeds bound')
        data = struct.pack('>I', len(encoded)) + encoded
        fd = self.process.stdin.fileno()
        selector = selectors.DefaultSelector()
        selector.register(fd, selectors.EVENT_WRITE)
        view = memoryview(data)
        try:
            while view:
                self._remaining()
                self._wait_ready(selector, 'write')
                self._remaining()
                try:
                    count = os.write(fd, view)
                except (BlockingIOError, InterruptedError):
                    self._remaining()
                    continue
                except BrokenPipeError:
                    raise ProtocolError('control pipe closed') from None
                if count <= 0:
                    raise ProtocolError('control pipe closed')
                view = view[count:]
        finally:
            selector.close()
        self.write_sequence += 1

    def receive(self, allowed, body_limit=MAX_FRAME):
        header = self._read_exact(4)
        (size,) = struct.unpack('>I', header)
        if size == 0 or size > min(body_limit, MAX_FRAME):
            raise ProtocolError('declared frame length exceeds bound')
        raw = self._read_exact(size)
        value = strict_json(raw)
        if not isinstance(value, dict):
            raise ProtocolError('frame envelope is not an object')
        _object(value, ('protocol', 'nonce', 'sequence', 'kind', 'payload'))
        if _int(value['protocol']) != PROTOCOL:
            raise ProtocolError('unsupported wire protocol')
        nonce = _str(value['nonce'])
        sequence = _int(value['sequence'], 1, (1 << 64) - 1)
        kind = _str(value['kind'])
        if not NONCE_RE.fullmatch(nonce) or nonce != self.nonce:
            raise ProtocolError('session nonce mismatch')
        if sequence != self.read_sequence + 1 or kind not in allowed:
            raise ProtocolError('unexpected frame order')
        if kind == 'DONE' and len(raw) > MAX_CONTROL:
            raise ProtocolError('DONE frame exceeds control bound')
        payload = value['payload']
        if not isinstance(payload, dict):
            raise ProtocolError('frame payload is not an object')
        self.read_sequence = sequence
        return Frame(kind, payload, sequence, nonce, raw)

    def close_input(self):
        try:
            self.process.stdin.close()
        except (OSError, ValueError):
            pass


class BoundedStderr:
    def __init__(self, stream, limit=MAX_STDERR, on_overflow=None):
        self.stream = stream
        self.limit = limit
        self.on_overflow = on_overflow
        self.data = bytearray()
        self.overflow = False
        self.error = None
        self.done = threading.Event()
        self.thread = threading.Thread(target=self._drain, name='python-controller-stderr', daemon=True)
        self.thread.start()

    def _drain(self):
        try:
            while True:
                chunk = self.stream.read(4096)
                if not chunk:
                    break
                room = self.limit + 1 - len(self.data)
                if room > 0:
                    self.data.extend(chunk[:room])
                if len(self.data) > self.limit:
                    self.overflow = True
                    if self.on_overflow is not None:
                        self.on_overflow()
                    # Continue draining the bounded pipe to let the owned process exit.
        except (OSError, ValueError) as exc:
            self.error = exc
        finally:
            self.done.set()

    def join(self, timeout):
        self.thread.join(timeout)
        return not self.thread.is_alive()


@dataclass
class ChildContext:
    hello: dict
    environment: dict
    executable: str
    cwd: str
    downloader: str
    source_paths: tuple[Path, ...]
    source_trees: tuple[Path, ...]
    launch_argv: tuple[str, ...]
    fixture_root: str
    fixture_mode: str | None
    env_identity: str
    direct_environment: dict
    full_capture: FullCapture
    metadata: dict


@dataclass
class ReconciledContext:
    metadata: dict
    parent_metadata: dict
    parent: Footprint
    sources: Footprint
    merged: Footprint
    request: AcquisitionRequest | None
    descriptor_identity: str
    child: ChildContext
    parent_env: dict
    child_env: dict
    command_binding: dict | None = None


class ContextCapture:
    """Independent parent and direct-child captures with a merged read scope."""
    def __init__(self, full_capture, helper_executable, cwd, child_env, launch_argv,
                 metadata_runner, limits=Limits(), child_capture_factory=FullCapture):
        if metadata_runner is None:
            raise ControllerUnavailable('child metadata runner is unavailable')
        self.full = full_capture
        self.child_metadata_runner = metadata_runner
        self.child_capture_factory = child_capture_factory
        self.limits = limits
        self.helper = str(Path(helper_executable).resolve(strict=True))
        self.cwd = str(Path(cwd).resolve(strict=True))
        self.child_env = dict(child_env)
        self.launch_argv = tuple(launch_argv)
        self.fixture_root, self.fixture_mode = _fixture_selection(self.helper, self.launch_argv, self.child_env)
        self.metadata = None
        self.child_metadata = None
        self.child_full = None
        self.parent_before = dict(full_capture.env)
        self._source_paths = self._owned_source_paths()
        self._source_trees = self._owned_source_trees()
        self._parent_calls = 0
        self._child_calls = 0

    def _owned_source_paths(self):
        logical = REPO_ROOT / 'tools' / 'pack-binaries'
        return (E_ROOT / 'main.go', E_ROOT / 'toolchain.go', E_ROOT / 'pin.go',
                E_ROOT / 'toolchain_go_emitter_test.go', E_ROOT / 'recipe.go',
                logical / 'main.go', logical / 'toolchain.go', logical / 'pin.go', logical / 'recipe.go',
                REPO_ROOT / '.goreleaser.yaml', REPO_ROOT / 'Justfile',
                REPO_ROOT / 'go.mod', REPO_ROOT / 'go.sum',
                Path(__file__).resolve(), Path(__file__).with_name('test_full_controller.py').resolve(),
                C_ROOT / 'capture.py', A_ROOT / 'official_adapter.py', B_ROOT / 'closure.py')

    def _owned_source_trees(self):
        # The fixture census follows this official target's build-constrained local imports.
        return (REPO_ROOT / 'cmd' / 'yolo-cglimit', REPO_ROOT / 'internal' / 'paths')

    def capture_parent(self):
        metadata = {name: self.full.metadata('full', target) for name, target in
                    (('base', None), ('lint-linux', 'linux'), ('lint-darwin', 'darwin'))}
        self.metadata = metadata
        self._parent_calls += 3
        parent = self.full.discover(metadata, dict(self.parent_before), None)
        return parent

    def _discover_parent(self, metadata, env):
        return self.full.discover(metadata, env, None)

    def _discover_child(self, child, metadata, env):
        return child.full_capture.discover(metadata, env, None)

    def _capture_child_metadata(self):
        if self.child_full is None:
            self.child_full = self.child_capture_factory(self.child_env, self.cwd,
                                                         runner=self.child_metadata_runner)
        metadata = {name: self.child_full.metadata('full', target) for name, target in
                    (('base', None), ('lint-linux', 'linux'), ('lint-darwin', 'darwin'))}
        self.child_metadata = metadata
        self._child_calls += 3
        return metadata

    def validate_hello(self, payload):
        required = ('source', 'host_os', 'host_arch', 'cwd', 'executable', 'provisional_downloader',
                    'provisional_downloader_status', 'downloader_binding', 'environment')
        hello = _object(payload, required)
        for key in ('source', 'host_os', 'host_arch', 'cwd', 'executable', 'provisional_downloader',
                    'provisional_downloader_status'):
            _str(hello[key])
        if hello['cwd'] != self.cwd:
            raise ProtocolError('child cwd differs from independent launch')
        if hello['executable'] != self.helper:
            raise ProtocolError('child executable differs from compiled fixture')
        logical_main = str(E_ROOT / 'main.go')
        if hello['source'] != logical_main:
            raise ProtocolError('runtime source path differs from the experimental package source')
        expected_go = shutil.which('go', path=self.child_env.get('PATH', ''))
        if not expected_go or str(Path(expected_go).absolute()) != hello['provisional_downloader']:
            raise ProtocolError('child downloader differs from selected direct PATH binding')
        if hello['provisional_downloader_status'] != 'resolved':
            raise ProtocolError('child downloader is not resolved')
        _check_binding(hello['downloader_binding'], hello['provisional_downloader'])
        selected = decode_selected_environment(hello['environment'], self.child_env)
        if self.metadata is None:
            raise ControllerError('parent metadata is unavailable')
        child_metadata = self._capture_child_metadata()
        parent_values = self.metadata['base']
        child_values = child_metadata['base']
        if (hello['host_os'] != child_values['GOHOSTOS'] or hello['host_arch'] != child_values['GOHOSTARCH'] or
                parent_values['GOHOSTOS'] != child_values['GOHOSTOS'] or
                parent_values['GOHOSTARCH'] != child_values['GOHOSTARCH']):
            raise ProtocolError('child runtime host differs from independently selected metadata')
        if self.full.pin != self.child_full.pin:
            raise ProtocolError('parent and child source pins differ')
        if (str(self.full.lexical_go) != str(self.child_full.lexical_go) or
                str(self.full.go) != str(self.child_full.go)):
            raise ProtocolError('parent and child downloader bindings differ')
        self._fixture_selection = _fixture_selection(self.helper, self.launch_argv, self.child_env)
        if self._fixture_selection != (self.fixture_root, self.fixture_mode):
            raise ProtocolError('independent fixture launch selection changed')
        paths = self._source_paths + (Path(self.helper), Path(expected_go),)
        source_fp = closure.identity(paths, trees=self._source_trees, limits=self.limits)
        env_identity = self.child_full.opaque(_canonical_json({k: selected[k] for k in sorted(selected)}).decode('utf-8'))
        child = ChildContext(hello=dict(hello), environment=selected, executable=self.helper, cwd=self.cwd,
                             downloader=expected_go, source_paths=paths, source_trees=self._source_trees,
                             launch_argv=self.launch_argv, fixture_root=self.fixture_root,
                             fixture_mode=self.fixture_mode, env_identity=env_identity,
                             direct_environment=dict(self.child_env), full_capture=self.child_full,
                             metadata=child_metadata)
        return child, source_fp

    def _merge_parent_child(self, parent, child, command_binding=None):
        parent_reader = self._discover_parent(self.metadata, dict(self.parent_before))
        child_reader = self._discover_child(child, child.metadata, dict(child.direct_environment))
        source_fp = closure.identity(child.source_paths, trees=child.source_trees, limits=self.limits)
        merged = merge_scopes((parent, parent_reader, child_reader, source_fp), self.limits)
        merged, identity = self._bind_reader_identity(merged, parent_reader, child_reader, child,
                                                     command_binding)
        source_union = merge_scopes((child_reader, source_fp), self.limits)
        return source_union, merged, identity

    def _bind_reader_identity(self, merged, parent_reader, child_reader, child, command_binding):
        command_id = '' if command_binding is None else self.full.opaque(
            _canonical_json(command_binding).decode('utf-8'))
        child_context = self.full.opaque(child.env_identity + child.executable + child.cwd +
                                         child.downloader + command_id)
        reader_identity = {
            'membershipDigest': merged.digest,
            'parentReaderDigest': parent_reader.digest,
            'childReaderDigest': child_reader.digest,
            'childContext': child_context,
            'commandBinding': command_id,
        }
        merged.digest = self.full.opaque(_canonical_json(reader_identity).decode('utf-8'))
        identity = self.full.opaque(_canonical_json({
            'combinedDescriptor': merged.digest,
            'parentReaderDigest': parent_reader.digest,
            'childReaderDigest': child_reader.digest,
            'commandBinding': command_id,
        }).decode('utf-8'))
        return merged, identity

    def reconcile(self, parent, child, command_binding=None):
        if self.metadata is None or child.metadata is None:
            raise ControllerError('independent parent/child metadata is unavailable')
        source_fp, merged, identity = self._merge_parent_child(parent, child, command_binding)
        provisional = OfficialAdapter(child.full_capture, lambda _request: None,
                                      limits=self.limits)._request_for(child.metadata, merged)
        request = replace(provisional, context_identity=identity)
        return ReconciledContext(child.metadata, self.metadata, parent, source_fp, merged, request, identity,
                                 child, dict(self.parent_before), dict(self.child_env), command_binding)

    def recapture(self, ctx, acquired=None):
        current_parent_env = dict(ctx.parent_env)
        current_child_env = dict(ctx.child_env)
        if current_parent_env != self.parent_before or current_child_env != self.child_env:
            raise ControllerUnavailable('captured environment mapping changed during owned session')
        parent_reader = self._discover_parent(ctx.parent_metadata, current_parent_env)
        child_reader = self._discover_child(ctx.child, ctx.child.metadata, current_child_env)
        source_fp = closure.identity(ctx.child.source_paths, trees=ctx.child.source_trees,
                                     limits=self.limits)
        merged = merge_scopes((parent_reader, child_reader, source_fp), self.limits)
        merged, identity = self._bind_reader_identity(merged, parent_reader, child_reader,
                                                     ctx.child, ctx.command_binding)
        if identity != ctx.descriptor_identity:
            raise ControllerMutation('reconciled source/context descriptor changed')
        if acquired is None:
            return merged
        adapter = OfficialAdapter(ctx.child.full_capture, lambda _request: None, limits=self.limits)
        adapter.acquired = acquired
        return adapter._scope_from_base(merged, acquired)


def merge_scopes(parts, limits):
    members = {}
    for part in parts:
        members.update(part.members)
    if len(members) > limits.entries:
        raise Unknown('merged metadata/read descriptor exceeds entry bound')
    files = [value for value in members.values() if value.startswith('file:')]
    import ast
    total = sum(ast.literal_eval(value.removeprefix('file:').rsplit(':', 1)[0])[3] for value in files)
    if total > limits.bytes:
        raise Unknown('merged metadata/read descriptor exceeds byte bound')
    digest = hashlib.sha256(json.dumps(sorted(members.items()), separators=(',', ':')).encode()).hexdigest()
    return Footprint(members, digest, len(files), sum(value.startswith('dir:') for value in members.values()), total)


@dataclass
class ParsedCommand:
    value: dict
    stdout: bytes
    stderr: bytes
    environment: dict


def _decode_base64(value, limit, allow_overflow=False, overflow=False, allow_null=False):
    if value is None and allow_null:
        return b''
    if not isinstance(value, str):
        raise ProtocolError('byte observation is not base64 text')
    try:
        decoded = base64.b64decode(value.encode('ascii'), validate=True)
    except (UnicodeError, binascii.Error):
        raise ProtocolError('invalid base64 observation') from None
    if base64.b64encode(decoded).decode('ascii') != value:
        raise ProtocolError('noncanonical base64 observation')
    maximum = limit + (1 if allow_overflow and overflow else 0)
    if len(decoded) > maximum:
        raise ProtocolError('decoded observation exceeds bound')
    return decoded


def _parse_command(value, stage, child, limits, must_succeed, chosen_binary=None):
    required = ('stage', 'path', 'args', 'selector', 'before', 'after', 'cwd', 'environment', 'started', 'status',
                'stdout', 'stderr', 'stdout_overflow', 'stderr_overflow')
    optional = ('pid', 'exit_code')
    _object(value, required, optional)
    if value['stage'] != stage:
        raise ProtocolError('command stage order differs')
    path = _str(value['path'])
    args = value['args']
    if not isinstance(args, list) or not args or any(not isinstance(item, str) for item in args):
        raise ProtocolError('invalid command argv')
    selector = _str(value['selector'])
    if selector != args[0]:
        raise ProtocolError('command selector differs from argv')
    if stage in ('effective-proxy', 'download') and path != child.downloader:
        raise ProtocolError('command path differs from selected downloader')
    if stage == 'version' and path != chosen_binary:
        raise ProtocolError('version path differs from chosen binary')
    if stage in ('effective-proxy', 'download') and selector != child.downloader:
        raise ProtocolError('command selector differs from downloader')
    if stage == 'version' and selector != path:
        raise ProtocolError('version selector differs from chosen binary')
    cwd = _str(value['cwd'])
    if stage == 'version':
        if cwd != child.cwd:
            raise ProtocolError('version command cwd differs from child cwd')
    elif not _is_source_temp_dir(cwd, child.direct_environment):
        raise ProtocolError('acquisition command cwd role differs')
    if stage == 'download' and len(args) != 5:
        raise ProtocolError('download argv shape differs')
    if stage == 'effective-proxy' and args[1:] != ['env', '-json', 'GOPROXY', 'GOSUMDB']:
        raise ProtocolError('proxy argv shape differs')
    if stage == 'download' and args[1:3] != ['mod', 'download'] or (stage == 'download' and args[3] != '-json'):
        raise ProtocolError('download argv shape differs')
    if stage == 'version' and args[1:] != ['version']:
        raise ProtocolError('version argv shape differs')
    _check_binding(value['before'], path)
    _check_binding(value['after'], path)
    env_values = decode_selected_environment(value['environment'])
    if value['started'] is not True and must_succeed:
        raise ProtocolError('required command did not start')
    if type(value['started']) is not bool:
        raise ProtocolError('invalid command started flag')
    status = _str(value['status'])
    exit_code = value.get('exit_code')
    if exit_code is not None:
        _int(exit_code)
    if 'pid' in value:
        _int(value['pid'], 1)
    out_overflow = _bool(value['stdout_overflow'])
    err_overflow = _bool(value['stderr_overflow'])
    stdout = _decode_base64(value['stdout'], limits[0], allow_overflow=True, overflow=out_overflow,
                            allow_null=not must_succeed)
    stderr = _decode_base64(value['stderr'], MAX_STDERR, allow_overflow=True, overflow=err_overflow,
                            allow_null=True)
    if must_succeed:
        if status != 'exited' or type(exit_code) is not int or exit_code != 0 or out_overflow or err_overflow:
            raise ProtocolError('required acquisition command was unsuccessful')
    elif status not in ('start-failed', 'exited', 'exit-failed', 'signal-failed', 'canceled'):
        raise ProtocolError('unknown command status')
    elif not value['started'] and (status != 'start-failed' or exit_code is not None):
        raise ProtocolError('start-failed command carries inconsistent status')
    elif value['started'] and status == 'start-failed':
        raise ProtocolError('started command has start-failed status')
    elif value['started'] and status in ('exited', 'exit-failed') and type(exit_code) is not int:
        raise ProtocolError('exited command omitted its actual exit code')
    return ParsedCommand(value, stdout, stderr, env_values)


def _is_source_temp_dir(cwd, direct_environment):
    # Go's os.TempDir on Unix uses TMPDIR when nonempty and otherwise /tmp.
    configured = direct_environment.get('TMPDIR') or '/tmp'
    if not Path(configured).is_absolute():
        return False
    expected_parent = Path(os.path.normpath(configured))
    path = Path(cwd)
    return (path.is_absolute() and path.parent == expected_parent and
            path.name.startswith('pack-binaries-toolchain-') and path.is_dir())


def _expected_proxy_environment(child_env):
    # runCommand copies the explicitly assigned command Env. Go's Cmd.Environ
    # only synthesizes PWD for a nil Env, so source-inherited PWD remains intact
    # even while Dir records the observed acquisition temporary directory.
    out = dict(child_env)
    out.pop('GOFLAGS', None); out.pop('GOTOOLCHAIN', None); out.pop('GOWORK', None)
    out['GOTOOLCHAIN'] = 'local'; out['GOWORK'] = 'off'
    return out


def _expected_download_environment(child_env, proxy, sumdb):
    drop = {'GOENV', 'GOFLAGS', 'GONOSUMDB', 'GONOSUMCHECK', 'GOPRIVATE', 'GONOPROXY', 'GOINSECURE',
            'GOTOOLCHAIN', 'GOWORK', 'GOSUMDB', 'GOPROXY'}
    out = {k: v for k, v in child_env.items() if k not in drop}
    if not sumdb:
        sumdb = child_env.get('GOSUMDB', '')
    if not proxy:
        proxy = child_env.get('GOPROXY', '')
    if not sumdb or sumdb == 'off':
        sumdb = 'sum.golang.org'
    out.update(GOENV='off', GOTOOLCHAIN='local', GOWORK='off', GOSUMDB=sumdb)
    if proxy:
        out['GOPROXY'] = proxy
    return out


def _expected_version_environment(child_env, goos, goarch):
    kept = {'GOCACHE', 'GOMODCACHE', 'GOPATH', 'GOTMPDIR'}
    out = {k: v for k, v in child_env.items()
           if not ((k.startswith('GO') and k not in kept) or k.startswith('CGO_'))}
    out.update(GOENV='off', GOTOOLCHAIN='local', GOWORK='off', CGO_ENABLED='0', GOOS=goos, GOARCH=goarch)
    if goarch == 'amd64':
        out['GOAMD64'] = 'v1'
    elif goarch == 'arm64':
        out['GOARM64'] = 'v8.0'
    return out


def _compare_selected(actual, expected):
    selected = _selected_keys(expected)
    mismatched = sorted(key for key in selected if actual.get(key) != expected.get(key))
    unexpected = sorted(set(actual) - selected)
    if mismatched or unexpected:
        # Key names are safe diagnostics; do not reveal values or captured environment.
        raise ProtocolError('command environment transformation differs: ' + ','.join(mismatched + unexpected))
    if set(actual) != selected:
        raise ProtocolError('command environment selected key set differs')


def reconcile_observation(observation, child, request):
    required = ('protocol', 'host_os', 'host_arch', 'downloader', 'toolchain', 'module', 'proxy', 'download_context',
                'version_context', 'commands', 'chosen_dir', 'module_sum', 'readiness', 'version_output', 'returned_go')
    _object(observation, required)
    if _int(observation['protocol']) != PROTOCOL:
        raise ProtocolError('observation protocol differs')
    if observation['host_os'] != request.host_os or observation['host_arch'] != request.host_arch:
        raise ProtocolError('observation host differs from parent metadata')
    if observation['downloader'] != request.selected_downloader_lexical:
        raise ProtocolError('observation downloader differs from selected PATH Go')
    if observation['toolchain'] != request.source_pin or observation['module'] != request.requested_module:
        raise ProtocolError('observation source pin/module differs')
    commands = observation['commands']
    if not isinstance(commands, list) or len(commands) != 2:
        raise ProtocolError('provider RETURN lacks ordered download/version commands')
    proxy = _object(observation['proxy'], ('fallback', 'command'), ('proxy', 'sumdb'))
    fallback = _str(proxy['fallback'])
    if fallback not in ('none', 'environment-fallback', 'command-failed', 'invalid-json'):
        raise ProtocolError('unsupported proxy fallback')
    proxy_cmd = _parse_command(proxy['command'], 'effective-proxy', child,
                               (OUTPUT_LIMITS['effective-proxy'],), False)
    if proxy_cmd.value['stdout_overflow'] or proxy_cmd.value['stderr_overflow']:
        raise ProtocolError('overflowed proxy command cannot yield provider RETURN')
    if fallback in ('none', 'environment-fallback', 'invalid-json'):
        if proxy_cmd.value['status'] != 'exited' or proxy_cmd.value.get('exit_code') != 0:
            raise ProtocolError('proxy fallback contradicts command status')
        try:
            parsed_proxy = strict_json(proxy_cmd.stdout)
            _object(parsed_proxy, (), ('GOPROXY', 'GOSUMDB'))
            if any(not isinstance(parsed_proxy[k], str) for k in parsed_proxy):
                raise ProtocolError('proxy JSON value type is unsupported')
            proxy_value = parsed_proxy.get('GOPROXY', '')
            sumdb_value = parsed_proxy.get('GOSUMDB', '')
        except ProtocolError:
            if fallback != 'invalid-json':
                raise
            proxy_value = sumdb_value = ''
        else:
            if fallback == 'invalid-json':
                raise ProtocolError('invalid JSON fallback carried valid JSON')
        if fallback == 'none' and (not proxy_value or not sumdb_value):
            raise ProtocolError('proxy fallback does not match empty effective settings')
        if fallback == 'environment-fallback' and proxy_value and sumdb_value:
            raise ProtocolError('proxy fallback does not match complete settings')
        if fallback != 'invalid-json':
            if 'proxy' in proxy and proxy['proxy'] != proxy_value:
                raise ProtocolError('proxy field differs from command output')
            if 'sumdb' in proxy and proxy['sumdb'] != sumdb_value:
                raise ProtocolError('sumdb field differs from command output')
    elif fallback == 'command-failed':
        if proxy_cmd.value['status'] == 'exited' and proxy_cmd.value.get('exit_code') == 0:
            raise ProtocolError('failed proxy fallback contradicts command')
        if 'proxy' in proxy or 'sumdb' in proxy:
            raise ProtocolError('failed proxy fallback carried effective settings')
        proxy_value = sumdb_value = ''
    _compare_selected(proxy_cmd.environment, _expected_proxy_environment(child.environment))

    download = _parse_command(commands[0], 'download', child,
                              (OUTPUT_LIMITS['download'],), True)
    raw_download = download.stdout
    if len(raw_download) > 1024 * 1024:
        raise ProtocolError('download JSON exceeds decoded bound')
    parsed_download = strict_json(raw_download)
    _object(parsed_download, ('Dir', 'Sum'), ('Error',))
    directory = _str(parsed_download['Dir']); checksum = _str(parsed_download['Sum'])
    if 'Error' in parsed_download:
        error = parsed_download['Error']
        if not isinstance(error, str):
            raise ProtocolError('download Error type is invalid')
        if error:
            raise ProtocolError('download result contains Error')
    if observation['chosen_dir'] != directory or observation['module_sum'] != checksum:
        raise ProtocolError('top-level result differs from actual download JSON')
    module = request.requested_module
    if download.value['args'] != [child.downloader, 'mod', 'download', '-json', module]:
        raise ProtocolError('download module argv differs')
    _compare_selected(download.environment, _expected_download_environment(child.environment, proxy_value, sumdb_value))
    if observation['download_context'] != commands[0]['environment']:
        raise ProtocolError('duplicated download context differs')

    ready = _object(observation['readiness'], ('directory', 'go_binary', 'status'))
    expected_binary = str(Path(directory) / 'bin' / 'go')
    if ready['directory'] != directory or ready['go_binary'] != expected_binary or ready['status'] not in ('success', 'no-op'):
        raise ProtocolError('readiness does not bind actual downloaded binary')
    if not Path(directory).is_absolute() or '\x00' in directory or not Path(expected_binary).is_file() or not os.access(expected_binary, os.X_OK):
        raise ProtocolError('returned distribution binding is unavailable')
    version_raw = _decode_base64(observation['version_output'], OUTPUT_LIMITS['version'])
    try:
        version_text = version_raw.decode('utf-8', errors='strict')
    except UnicodeError:
        raise ProtocolError('version output is not UTF-8') from None
    expected_version = 'go version ' + request.source_pin + ' ' + request.host_os + '/' + request.host_arch
    if version_text.removesuffix('\n') != expected_version or '\n' in version_text.removesuffix('\n') or '\r' in version_text:
        raise ProtocolError('version output differs from exact source pin')
    version = _parse_command(commands[1], 'version', child,
                             (OUTPUT_LIMITS['version'],), True, expected_binary)
    if version.stdout != version_raw or version.value['args'] != [expected_binary, 'version']:
        raise ProtocolError('version command output/binding differs')
    if observation['returned_go'] != expected_binary:
        raise ProtocolError('fetchToolchain return differs from actual Dir/bin/go')
    _compare_selected(version.environment, _expected_version_environment(child.environment,
                                                                          request.host_os, request.host_arch))
    if observation['version_context'] != commands[1]['environment']:
        raise ProtocolError('duplicated version context differs')
    if proxy_cmd.value['cwd'] != download.value['cwd']:
        raise ProtocolError('proxy/download cwd differs')
    cwd_binding = {'proxy_download_cwd': download.value['cwd'], 'version_cwd': version.value['cwd'],
                   'proxy_selector': proxy_cmd.value['before'], 'download_selector': download.value['before'],
                   'version_selector': version.value['before'],
                   'proxy_environment': proxy_cmd.value['environment'],
                   'download_environment': download.value['environment'],
                   'version_environment': version.value['environment'],
                   'proxy_fallback': fallback, 'module': module, 'directory': directory,
                   'checksum_shape': 'h1-valid-shape'}
    return ProviderReturn(
        observed_context_identity='',
        observed_downloader_lexical=request.selected_downloader_lexical,
        observed_downloader_resolved=request.selected_downloader_resolved,
        observed_host_os=request.host_os, observed_host_arch=request.host_arch,
        observed_module=module, download_json=raw_download, download_exit=0,
        readiness_directory=directory, readiness_succeeded=True,
        version_binary=expected_binary, version_output=version_text, version_exit=0,
        returned_binary=expected_binary), cwd_binding


class ProtectedSession:
    """Real accepted Monitor held through verify, terminal frames and final drains."""
    def __init__(self, initial, discover, monitor_factory=closure.Monitor, observer=None, limits=None):
        self.current = initial
        self.discover = discover
        self.monitor_factory = monitor_factory
        self.observer = observer
        self.monitor = None
        self.closed = False
        self.mutated = False
        self.registrations = 0
        self.rereads = 0
        self.releases = 0
        self.limits = limits or Limits()

    def _check_watch_budget(self):
        paths = getattr(self.monitor, 'paths', None)
        if paths is not None and len(paths) > self.limits.watches:
            raise ControllerUnavailable('observer watch budget exceeded')

    def start(self):
        self.monitor = self.monitor_factory()
        try:
            for _ in range(self.limits.stabilization_attempts):
                self.monitor.register(self.current)
                self._check_watch_budget()
                self.registrations += 1
                self.monitor.drain()
                if self.observer:
                    self.observer('registered', self.monitor, self.current)
                try:
                    reread = self.discover()
                except Exception:
                    self.monitor.drain()
                    raise
                self.rereads += 1
                self.monitor.drain()
                if reread.members == self.current.members and reread.digest == self.current.digest:
                    self.current = reread
                    return
                self.current = reread
            raise ControllerUnavailable('context did not stabilize within four attempts')
        except Exception:
            self.final_drain()
            raise

    def learn(self, footprint):
        self.current = footprint
        self.monitor.register(footprint)
        self._check_watch_budget()
        self.registrations += 1
        self.monitor.drain()
        if self.observer:
            self.observer('learned-registered', self.monitor, footprint)
        try:
            reread = self.discover()
        except Exception:
            self.final_drain()
            raise
        self.rereads += 1
        self.monitor.drain()
        if reread.members != footprint.members or reread.digest != footprint.digest:
            raise ControllerMutation('learned input changed before protected use')
        self.current = reread

    def verify_and_release(self, release):
        self.monitor.drain()
        release()
        self.releases += 1
        self.monitor.drain()

    def final_drain(self):
        if self.monitor is None or self.closed:
            return
        self.monitor.drain()

    def close(self, prior=None):
        if self.monitor is None or self.closed:
            return
        try:
            self.monitor.drain()
        except Mutated as exc:
            self.mutated = True
            prior = exc
        except Exception as exc:
            if prior is None:
                prior = ControllerUnavailable('observer drain unavailable')
        try:
            self.monitor.close()
        except Exception:
            if prior is None and not self.mutated:
                prior = ControllerUnavailable('observer close unavailable')
        self.closed = True
        if self.mutated:
            raise ControllerMutation('observed input mutation') from None
        if prior is not None:
            raise prior


@dataclass
class ControllerSnapshot:
    owner: object
    context: ReconciledContext
    acquired: Any
    footprint: Footprint
    report: dict


@dataclass
class RunResult:
    state: str
    error_code: str | None
    done_status: int | None
    wrapper_status: int | None
    snapshot: ControllerSnapshot | None
    report: dict
    transitions: tuple[str, ...]


class ControllerAdapter(OfficialAdapter):
    """Controller-owned orchestration around A's strict typed validation/readers."""
    def __init__(self, context_capture, executable, child_env, cwd, observer=None, monitor_factory=closure.Monitor,
                 stderr_joiner=None, scope_reader_hook=None):
        super().__init__(context_capture.full, lambda _request: None, route='full', limits=context_capture.limits)
        self.context = context_capture
        self.executable = str(Path(executable).resolve(strict=True))
        self.child_env = dict(child_env)
        if self.executable != context_capture.helper or self.child_env != context_capture.child_env:
            raise Refused('controller launch differs from its captured helper/environment mapping')
        self.fixture_root, self.fixture_mode = _fixture_selection(
            self.executable, context_capture.launch_argv, self.child_env)
        if any(not isinstance(value, str) for value in self.child_env.values()):
            raise ControllerError('direct child environment must contain strings')
        self.cwd = str(Path(cwd).resolve(strict=True))
        self.observer = observer
        self.monitor_factory = monitor_factory
        self.stderr_joiner = stderr_joiner or (lambda reader, timeout: reader.join(timeout))
        self.scope_reader_hook = scope_reader_hook
        self.process_launches = 0
        self.provider_returns = 0
        self.metadata_provider_calls = 0
        self.owned_snapshot = None
        self.phase = 'idle'
        self.error_observation = None
        self._process = None
        self._stderr = None
        self._protected = None
        self._transitions = []
        self.cancel_event = None
        self._streams_closed = False
        self._cleanup_attempted = False

    def _scope_from_base(self, base, acquired):
        if self.scope_reader_hook is not None:
            self.scope_reader_hook('enter', base, acquired, None)
        try:
            result = super()._scope_from_base(base, acquired)
        except Exception as exc:
            if self.scope_reader_hook is not None:
                self.scope_reader_hook('error', base, acquired, exc)
            raise
        if self.scope_reader_hook is not None:
            self.scope_reader_hook('return', base, acquired, result)
        return result

    def _launch(self, deadline, cancel_event=None):
        argv = list(self.context.launch_argv)
        if _fixture_selection(self.executable, argv, self.child_env) != (
                self.fixture_root, self.fixture_mode):
            raise Refused('controller launch fixture selection changed')
        # This is direct compiled-child execution; the helper's test rewrites os.Args later.
        self._process = subprocess.Popen(argv, cwd=self.cwd, env=self.child_env, stdin=subprocess.PIPE,
                                         stdout=subprocess.PIPE, stderr=subprocess.PIPE, bufsize=0,
                                         start_new_session=True, close_fds=True)
        self.process_launches += 1
        def stop_owned_group():
            try:
                os.killpg(self._process.pid, signal.SIGTERM)
            except (ProcessLookupError, OSError):
                pass
        self._stderr = BoundedStderr(self._process.stderr, on_overflow=stop_owned_group)
        return FramedSession(self._process, deadline, cancel_event=cancel_event)

    def _check_stderr(self):
        if self._stderr is None:
            return
        if not self.stderr_joiner(self._stderr, 1.0) or self._stderr.error is not None:
            raise ControllerUnavailable('owned stderr drain did not complete')
        if self._stderr.overflow:
            raise ControllerUnavailable('owned stderr exceeded bound')

    def _close_owned_streams(self):
        if self._streams_closed or self._process is None:
            return
        failures = False
        for stream in (self._process.stdin, self._process.stdout, self._process.stderr):
            if stream is not None and not stream.closed:
                try:
                    stream.close()
                except (OSError, ValueError):
                    failures = True
        self._streams_closed = True
        if failures:
            raise ControllerUnavailable('owned pipe close failed')

    def _cleanup(self, session, cause=None):
        process = self._process
        if process is None:
            return None
        if self._cleanup_attempted:
            return ControllerUnavailable('owned process cleanup was already attempted')
        self._cleanup_attempted = True
        session.close_input()
        try:
            os.killpg(process.pid, signal.SIGTERM)
        except ProcessLookupError:
            pass
        except OSError:
            return ControllerUnavailable('owned process cleanup signal failed')
        if process.poll() is None:
            try:
                process.wait(timeout=0.75)
            except subprocess.TimeoutExpired:
                try:
                    os.killpg(process.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                except OSError:
                    return ControllerUnavailable('owned process cleanup signal failed')
                try:
                    process.wait(timeout=1.0)
                except subprocess.TimeoutExpired:
                    return ControllerUnavailable('owned process cleanup did not complete')
        if self._stderr:
            joined = self.stderr_joiner(self._stderr, 0.5)
            if not joined:
                try:
                    os.killpg(process.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                except OSError:
                    return ControllerUnavailable('owned stderr cleanup signal failed')
                joined = self.stderr_joiner(self._stderr, 0.5)
            if not joined or self._stderr.error is not None or self._stderr.overflow:
                return ControllerUnavailable('owned stderr drainage is unavailable')
        return None

    def _new_adapter(self):
        return self

    def _discover(self, reconciled, adapter, acquired=None):
        return self.context.recapture(reconciled, acquired)

    def _handle_error(self, payload):
        _object(payload, ('stage', 'status', 'code'), ('observation',))
        if payload['status'] != 'failed':
            raise ProtocolError('ERROR status is not failure')
        _str(payload['stage']); _str(payload['code'])
        if 'observation' in payload and payload['observation'] is not None:
            if not isinstance(payload['observation'], dict):
                raise ProtocolError('partial ERROR observation has wrong type')
            # Preserve the decoded partial observation only in memory; never promote it.
            self.error_observation = payload['observation']
        return payload['stage'], payload['code']

    def _completed_fixture_snapshot(self, reconciled, acquired, footprint, report):
        if getattr(self, 'fixture_root', None) != COMPLETED_FIXTURE_ROOT:
            return None
        if (report.get('doneStatus') != 0 or report.get('wrapperStatus') != 0 or
                report.get('errorCode') is not None or 'ERROR' in self._transitions):
            return None
        if self.fixture_mode == 'matching':
            if acquired is None or self.provider_returns != 1 or self._transitions.count('RELEASE') != 1:
                raise ProtocolError('matching fixture completed without its accepted release')
            acquisition_state = 'synthetic fixture accepted by A; no provenance or integrity proof'
        elif self.fixture_mode == 'no-official':
            if acquired is not None or self.provider_returns != 0 or 'RELEASE' in self._transitions:
                raise ProtocolError('no-official fixture completed with acquisition activity')
            acquisition_state = 'none'
        else:
            return None
        report.pop('actualGoFamilyArgv', None)
        report.update(state='completed-fixture-only', profileState='Unknown', qualityOutcomes=[],
                      baselinePublished=False, contextOnly=acquired is None,
                      goFamilyExecutionScope='fixture child; its synthetic fake-command activity is separate from any zero-launch claim',
                      acquisitionState=acquisition_state,
                      fixtureBinding=dict(root=self.fixture_root, mode=self.fixture_mode,
                                          executable=self.executable, argv=list(self.context.launch_argv),
                                          sourceDigest=reconciled.sources.digest,
                                          sourceMemberCount=len(reconciled.sources.members)))
        snapshot = ControllerSnapshot(self, reconciled, acquired, footprint, dict(report))
        self.owned_snapshot = snapshot
        return snapshot

    def _finish_context_only(self, session, protected_session, reconciled, pre_error=None):
        terminal_error = pre_error
        if pre_error is None:
            frame = session.receive({'ERROR', 'DONE'}, MAX_FRAME)
            if frame.kind == 'ERROR':
                terminal_error = self._handle_error(frame.payload)
                self._transitions.append('ERROR')
                frame = session.receive({'DONE'}, MAX_CONTROL)
        else:
            frame = session.receive({'DONE'}, MAX_CONTROL)
        _object(frame.payload, ('exit_status',))
        status = _int(frame.payload['exit_status'], 0)
        if status == 0 and terminal_error is not None:
            raise ProtocolError('ERROR followed by successful DONE')
        if status != 0 and terminal_error is None:
            raise ProtocolError('nonzero DONE omitted terminal ERROR')
        if status != 0 and terminal_error != ('check', 'check_failed'):
            raise ProtocolError('ordinary helper failure classification differs')
        self._transitions.append('DONE')
        final = self.context.recapture(reconciled, None)
        if final.digest != protected_session.current.digest or final.members != protected_session.current.members:
            raise ControllerMutation('context-only pre-FINISH recapture differs')
        protected_session.monitor.register(final)
        protected_session.registrations += 1
        protected_session.final_drain()
        protected_session.rereads += 1
        session.send('FINISH', {})
        session.close_input()
        wrapper = self._wait_owned(session, max(0.001, session.deadline - time.monotonic()))
        if wrapper != status:
            raise ProtocolError('wrapper/DONE status disagreement')
        if protected_session.observer:
            protected_session.observer('wrapper-reaped', protected_session.monitor, protected_session.current)
        self._check_stderr()
        post = self.context.recapture(reconciled, None)
        if post.digest != final.digest or post.members != final.members:
            raise ControllerMutation('context-only final observation differs')
        protected_session.monitor.register(post)
        protected_session.registrations += 1
        protected_session.final_drain()
        self._transitions.extend(('FINISH', 'WAIT', 'FINAL-OBSERVATION'))
        if self._stderr and self._stderr.overflow:
            raise ControllerUnavailable('owned stderr exceeded bound')
        if self._has_extra_stdout(session):
            raise ProtocolError('trailing child output')
        self._close_owned_streams()
        protected_session.close()
        self._protected = None
        self.phase = 'complete'
        state = 'ordinary-failure' if status else 'context-only-complete'
        report = dict(state=state, errorCode=terminal_error[1] if terminal_error else None,
                      doneStatus=status, wrapperStatus=wrapper, profileState='Unknown',
                      qualityOutcomes=[], baselinePublished=False, releaseSent=False,
                      acquisitionState='none observed by this protocol branch', contextOnly=True,
                      actualGoFamilyArgv=[], helperProcessLaunches=self.process_launches,
                      metadataProviderCalls=self.metadata_provider_calls)
        snapshot = None
        if status == 0 and terminal_error is None:
            snapshot = self._completed_fixture_snapshot(reconciled, None, post, report)
        return RunResult(snapshot.report['state'] if snapshot is not None else state,
                         terminal_error[1] if terminal_error else None, status, wrapper, snapshot,
                         report, tuple(self._transitions))

    def run_full(self, verify_leaf=lambda: None, timeout_seconds=30, cancel_event=None):
        if self.phase != 'idle':
            raise ControllerError('controller session is one-shot')
        timeout_seconds = min(float(timeout_seconds), MAX_SESSION_MS / 1000)
        if timeout_seconds <= 0:
            raise ControllerUnavailable('invalid session deadline')
        deadline = time.monotonic() + timeout_seconds
        self.cancel_event = cancel_event
        if cancel_event is not None and cancel_event.is_set():
            raise ControllerUnavailable('session canceled')
        self.context.parent_before = dict(self.context.full.env)
        parent = self.context.capture_parent()
        if time.monotonic() >= deadline or (cancel_event is not None and cancel_event.is_set()):
            raise ControllerUnavailable('parent discovery exceeded session deadline')
        self.metadata_provider_calls = self.context._parent_calls
        session = None
        protected_session = None
        ordinary_error = None
        wrapper_status = None
        done_status = None
        snapshot = None
        adapter = self._new_adapter()
        try:
            self.phase = 'hello'
            session = self._launch(deadline, cancel_event)
            remaining_millis = max(1, min(MAX_SESSION_MS, int((session.deadline - time.monotonic()) * 1000)))
            session.send('INIT', {'deadline_millis': remaining_millis, 'verb': 'check'})
            hello_frame = session.receive({'HELLO'}, MAX_HELLO)
            child, _hello_fp = self.context.validate_hello(hello_frame.payload)
            self.metadata_provider_calls = self.context._parent_calls + self.context._child_calls
            adapter.capture = child.full_capture
            if tuple(session.launch_argv) != self.context.launch_argv:
                raise ProtocolError('compiled helper argv differs from captured anchored fixture')
            if _fixture_selection(self.executable, session.launch_argv, self.child_env) != (
                    self.context.fixture_root, self.context.fixture_mode):
                raise ProtocolError('compiled helper launch/reconciliation fixture mapping differs')
            source_fp, merged, identity = self.context._merge_parent_child(parent, child, None)
            reconciled = ReconciledContext(self.context.child_metadata, self.context.metadata,
                                            parent, source_fp, merged, None, identity,
                                            child, dict(self.context.parent_before), dict(self.child_env), None)
            active_acquired = [None]
            protected_session = ProtectedSession(merged,
                lambda: self._discover(reconciled, adapter, active_acquired[0]), self.monitor_factory,
                self.observer, self.context.limits)
            self._protected = protected_session
            protected_session.start()
            self.phase = 'started'
            self._transitions.extend(('INIT', 'HELLO', 'START'))
            session.send('START', {})
            frame = session.receive({'RETURN', 'ERROR'}, MAX_FRAME)
            if frame.kind == 'RETURN' and frame.payload.get('outcome') == 'no_acquisition' and len(frame.raw) > MAX_CONTROL:
                raise ProtocolError('zero-acquisition RETURN exceeds control bound')
            if frame.kind == 'ERROR':
                stage, code = self._handle_error(frame.payload)
                ordinary_error = (stage, code)
                self._transitions.append('ERROR-before-return')
                return self._finish_context_only(session, protected_session, reconciled, ordinary_error)
            else:
                _object(frame.payload, ('outcome', 'observation',) if frame.payload.get('outcome') == 'provider_return' else ('outcome', 'plan'))
                outcome = frame.payload.get('outcome')
                if outcome == 'no_acquisition':
                    plan = _object(frame.payload.get('plan'), ('source', 'want_count'))
                    if plan['source'] not in ('no-official', 'build-wanted') or _int(plan['want_count']) != 0:
                        raise ProtocolError('RETURN is not an actual zero-wants plan')
                    if self.fixture_root != COMPLETED_FIXTURE_ROOT or self.fixture_mode != 'no-official' or plan['source'] != 'no-official':
                        raise ProtocolError('zero-wants RETURN does not match the selected no-official fixture')
                    self._transitions.append('RETURN:no_acquisition')
                    return self._finish_context_only(session, protected_session, reconciled)
                elif outcome == 'provider_return':
                    if self.fixture_root == COMPLETED_FIXTURE_ROOT and self.fixture_mode != 'matching':
                        raise ProtocolError('provider RETURN does not match the selected matching fixture')
                    observation = frame.payload.get('observation')
                    # Request selection is based on the independently captured child downloader, host and pin.
                    provisional = adapter._request_for(child.metadata, merged)
                    request_seed = replace(provisional, context_identity=identity)
                    supplied, command_binding = reconcile_observation(observation, child, request_seed)
                    # Include actual selected command bindings in the independently derived identity.
                    source_fp, merged_with_commands, descriptor_id = self.context._merge_parent_child(parent, child, command_binding)
                    request_base = adapter._request_for(child.metadata, merged_with_commands)
                    request = replace(request_base, context_identity=descriptor_id)
                    supplied = replace(supplied, observed_context_identity=descriptor_id)
                    # A's one-shot typed acceptance remains authoritative.
                    adapter.phase = 'acquiring'
                    adapter.acquisition_calls = 1
                    acquired = adapter.accept_return(request, supplied)
                    self.provider_returns += 1
                    active_acquired[0] = acquired
                    reconciled = ReconciledContext(child.metadata, self.context.metadata, parent, source_fp,
                                                    merged_with_commands, request, descriptor_id, child,
                                                    dict(self.context.parent_before), dict(self.child_env), command_binding)
                    learned = adapter._scope_from_base(merged_with_commands, acquired)
                    # Include the actual distribution in the remaining combined context budget.
                    protected_session.learn(learned)
                    self.phase = 'verifying'
                    if protected_session.observer:
                        protected_session.observer('before-release', protected_session.monitor, learned)
                    verify_leaf()
                    protected_session.verify_and_release(lambda: session.send('RELEASE', {}))
                    self._transitions.append('RETURN:provider_return')
                    self._transitions.append('RELEASE')
                    self.phase = 'released'
                    # Go removes its private proxy/download cwd when fetchToolchain returns;
                    # the path is not a persistent descriptor requirement.
                    terminal_frame = session.receive({'ERROR', 'DONE'}, MAX_FRAME)
                    if terminal_frame.kind == 'ERROR':
                        ordinary_error = self._handle_error(terminal_frame.payload)
                        self._transitions.append('ERROR')
                        terminal_frame = session.receive({'DONE'}, MAX_CONTROL)
                    _object(terminal_frame.payload, ('exit_status',))
                    done_status = _int(terminal_frame.payload['exit_status'], 0)
                    if done_status == 0 and ordinary_error is not None:
                        raise ProtocolError('ERROR followed by successful DONE')
                    if done_status != 0 and ordinary_error is None:
                        raise ProtocolError('nonzero DONE omitted terminal ERROR')
                    if done_status != 0 and ordinary_error != ('check', 'check_failed'):
                        raise ProtocolError('ordinary helper failure classification differs')
                    self._transitions.append('DONE')
                    # Protect inputs through the pre-FINISH recapture and drain.
                    final_scope = self.context.recapture(reconciled, acquired)
                    if final_scope.digest != learned.digest or final_scope.members != learned.members:
                        raise ControllerMutation('final context differs before FINISH')
                    protected_session.monitor.register(final_scope)
                    protected_session.registrations += 1
                    protected_session.final_drain()
                    protected_session.rereads += 1
                    session.send('FINISH', {})
                    session.close_input()
                    wrapper_status = self._wait_owned(session, timeout_seconds)
                    if wrapper_status != done_status:
                        raise ProtocolError('wrapper/DONE status disagreement')
                    if protected_session.observer:
                        protected_session.observer('wrapper-reaped', protected_session.monitor, protected_session.current)
                    self._check_stderr()
                    # Required final observation remains inside Monitor lifetime.
                    post_scope = self.context.recapture(reconciled, acquired)
                    if post_scope.digest != learned.digest or post_scope.members != learned.members:
                        raise ControllerMutation('final delivery recapture differs')
                    protected_session.monitor.register(post_scope)
                    protected_session.registrations += 1
                    protected_session.final_drain()
                    self._transitions.extend(('FINISH', 'WAIT', 'FINAL-OBSERVATION'))
                    if self._stderr and self._stderr.overflow:
                        raise ControllerUnavailable('owned stderr exceeded bound')
                    if self._has_extra_stdout(session):
                        raise ProtocolError('trailing child output')
                    self._close_owned_streams()
                    protected_session.close()
                    self._protected = None
                    self.phase = 'complete'
                    report = dict(state='ordinary-failure' if done_status else 'completed-fixture-only',
                                  errorCode=ordinary_error[1] if ordinary_error else None,
                                  doneStatus=done_status, wrapperStatus=wrapper_status,
                                  profileState='Unknown', qualityOutcomes=[], baselinePublished=False,
                                  protectedReturnObserved=True, releaseAfterStableReread=True,
                                  returnedTreeRegisteredAndReread=True, metadataProviderCalls=self.metadata_provider_calls,
                                  helperProcessLaunches=self.process_launches, actualGoFamilyArgv=[],
                                  acquisitionState='fixture-observation-only; no provenance or integrity proof')
                    snapshot = None
                    if done_status == 0 and ordinary_error is None:
                        snapshot = self._completed_fixture_snapshot(reconciled, acquired, post_scope, report)
                    result = RunResult(snapshot.report['state'] if snapshot is not None else report['state'],
                                      report['errorCode'], done_status, wrapper_status, snapshot,
                                      report, tuple(self._transitions))
                    return result
                else:
                    raise ProtocolError('unknown RETURN outcome')
            raise ControllerUnavailable('unreachable context-only protocol state')
        except Exception as exc:
            try:
                cleanup_error = self._cleanup(session, exc) if session else None
            except Exception:
                cleanup_error = ControllerUnavailable('owned process cleanup did not complete')
            prior = exc
            if protected_session is not None:
                try:
                    protected_session.close(prior)
                except Mutated as mutation:
                    raise ControllerMutation('observed input mutation takes precedence') from None
                except Exception as close_error:
                    if isinstance(close_error, Mutated):
                        raise ControllerMutation('observed input mutation takes precedence') from None
                    if not isinstance(exc, Mutated):
                        prior = close_error
            if isinstance(exc, Mutated):
                raise ControllerMutation('observed input mutation') from None
            if cleanup_error is not None:
                raise ControllerUnavailable('owned process cleanup did not complete') from None
            if isinstance(exc, (ProtocolError, ControllerUnavailable, AcquisitionFailure, Refused)):
                raise
            if isinstance(exc, Unknown):
                raise ControllerUnavailable('context or returned scope is unavailable') from None
            raise ControllerUnavailable('controller session failed') from None
        finally:
            if self._process is not None:
                if self._process.poll() is None and session is not None and not self._cleanup_attempted:
                    try:
                        self._cleanup(session)
                    except Exception:
                        pass
                if not self._streams_closed:
                    for stream in (self._process.stdin, self._process.stdout, self._process.stderr):
                        if stream is not None and not stream.closed:
                            try:
                                stream.close()
                            except (OSError, ValueError):
                                pass
                    self._streams_closed = True

    def _wait_owned(self, session, timeout_seconds):
        deadline = min(session.deadline, time.monotonic() + timeout_seconds)
        while True:
            if self.cancel_event is not None and self.cancel_event.is_set():
                raise ControllerUnavailable('session canceled while reaping owned helper')
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise ControllerUnavailable('owned helper exceeded total deadline')
            try:
                return self._process.wait(timeout=min(0.1, remaining))
            except subprocess.TimeoutExpired:
                continue

    def _has_extra_stdout(self, session):
        if session.buffer:
            return True
        if self._process.stdout is None:
            return False
        ready, _, _ = __import__('select').select([self._process.stdout.fileno()], [], [], 0)
        if not ready:
            return False
        return bool(os.read(self._process.stdout.fileno(), 1))

    def compare_prose(self, snapshot, env=None):
        """Zero-launch recapture for an owned completed fixture component only."""
        if snapshot is not self.owned_snapshot or not isinstance(snapshot, ControllerSnapshot) or snapshot.owner is not self:
            raise Refused('Prose requires this controller session\'s completed component snapshot')
        before_launches = self.process_launches
        before_metadata = self.metadata_provider_calls
        current_env = dict(snapshot.context.parent_env if env is None else env)
        if current_env != snapshot.context.parent_env:
            raise ControllerUnavailable('prose parent mapping changed')
            current = self.context.recapture(snapshot.context, snapshot.acquired)
        if current.digest != snapshot.footprint.digest or current.members != snapshot.footprint.members:
            raise ControllerMutation('prose context changed')
        if self.process_launches != before_launches or self.metadata_provider_calls != before_metadata:
            raise AssertionError('prose route launched a process or refreshed metadata')
        return dict(sameContext=True, goFamilyArgv=[], processLaunches=0,
                    metadataProviderCalls=0, profileState='Unknown', qualityOutcomes=[], baselinePublished=False)


def _source_named_just_graph(path=REPO_ROOT / 'Justfile'):
    """Validate only the current source-named gate recipes; never interpret Just generally."""
    source = Path(path)
    try:
        with source.open('rb') as stream:
            raw = stream.read(JUSTFILE_LIMIT + 1)
        if len(raw) > JUSTFILE_LIMIT:
            raise Unknown('Justfile graph source exceeds bound')
        text = raw.decode('utf-8')
    except (OSError, UnicodeError):
        raise Unknown('Justfile graph source is unavailable') from None

    physical = text.splitlines()

    def code_line(line):
        if line.lstrip().startswith('#'):
            return ''
        return re.sub(r'\s+#.*$', '', line).rstrip()

    code = [code_line(line) for line in physical]

    def recipe_header(name):
        candidates = []
        for index, line in enumerate(code):
            content = line.strip()
            if re.match(rf'^{re.escape(name)}(?=\s|\(|:)', content):
                candidates.append((index, content))
        if len(candidates) != 1:
            raise Unknown('Justfile gate recipe is missing or duplicated')
        index, content = candidates[0]
        match = re.fullmatch(rf'{re.escape(name)}:\s*(.*)', content)
        if match is None:
            raise Unknown('Justfile gate recipe syntax is unsupported')
        return index, match.group(1).split()

    check_index, dependencies = recipe_header('check-ci')
    if dependencies != ['lint-ci', 'test-fast', 'check-pack-binaries']:
        raise Unknown('Justfile check-ci dependency graph differs')
    previous = check_index - 1
    while previous >= 0 and not code[previous].strip():
        previous -= 1
    if previous < 0 or code[previous].strip() != '[parallel]':
        raise Unknown('Justfile check-ci is not marked parallel')

    pack_index, pack_dependencies = recipe_header('check-pack-binaries')
    if pack_dependencies:
        raise Unknown('Justfile pack check has unsupported dependencies')
    body = []
    for index in range(pack_index + 1, len(physical)):
        line = physical[index]
        cleaned = code[index]
        if not cleaned.strip():
            continue
        if not line[:1].isspace():
            break
        body.append(cleaned.strip())
    if body != ['go run ./tools/pack-binaries check']:
        raise Unknown('Justfile pack check command differs')
    return closure.identity((source,))


def run_full(context_capture, executable, child_env, cwd, verify_leaf=lambda: None,
             timeout_seconds=30, observer=None, monitor_factory=closure.Monitor, cancel_event=None,
             stderr_joiner=None, scope_reader_hook=None):
    """Actual owned caller entrypoint; never invokes a Justfile quality recipe."""
    _source_named_just_graph()
    controller = ControllerAdapter(context_capture, executable, child_env, cwd,
                                   observer=observer, monitor_factory=monitor_factory,
                                   stderr_joiner=stderr_joiner, scope_reader_hook=scope_reader_hook)
    return controller.run_full(verify_leaf=verify_leaf, timeout_seconds=timeout_seconds,
                               cancel_event=cancel_event)


def compare_prose(controller, snapshot, env=None):
    if not isinstance(controller, ControllerAdapter):
        raise Refused('Prose requires a controller owner')
    return controller.compare_prose(snapshot, env)
