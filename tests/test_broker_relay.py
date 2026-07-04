"""Process-level tests for src/broker_relay.py and its supervision.

The relay runs AS A REAL SUBPROCESS, launched by file path exactly like
``_relay_ensure`` does (broker_relay.py is stdlib-only, so no package
context — and no cwd — is needed to run it);
the "broker" behind it is an in-test thread speaking just enough of the
framed-JSON loophole protocol (src/host_service.py) to record what
arrived and answer a pong.  This file owns the Round-2 regression: a
broker restart re-binds its socket (new inode) and the SAME relay
process must reach it on the next connection — the socket-file bind
mount it replaced pinned the dead inode forever.

Supervision-with-mocked-spawn tests live in tests/test_cli_unit.py
(TestBrokerRelay); here ``_relay_ensure``/``_relay_stop`` drive real
relay processes.
"""

import json
import os
import shutil
import signal
import socket
import struct
import subprocess
import sys
import tempfile
import threading
import time
from pathlib import Path

import pytest

REPO_ROOT = Path(__file__).parent.parent.resolve()
sys.path.insert(0, str(REPO_ROOT / "src"))

from cli.loopholes_runtime import (  # noqa: E402
    _broker_ping,
    _relay_ensure,
    _relay_lock_file,
    _relay_pid_file,
    _relay_read_pid,
    _relay_short_hash,
    _relay_stop,
)


@pytest.fixture
def relay_dir():
    """Short per-test dir under /tmp — AF_UNIX paths are capped at 108
    bytes on Linux / 104 on macOS; pytest's tmp_path is too long."""
    base = "/private/tmp" if sys.platform == "darwin" else "/tmp"
    d = Path(tempfile.mkdtemp(dir=base, prefix="yj-brelay-"))
    yield d
    shutil.rmtree(d, ignore_errors=True)


def _recv_exact(conn: socket.socket, n: int) -> "bytes | None":
    buf = b""
    while len(buf) < n:
        chunk = conn.recv(n - len(buf))
        if not chunk:
            return None
        buf += chunk
    return buf


class FakeBroker:
    """Framed-JSON broker double (protocol of src/host_service.py):
    reads one 4-byte-BE length-prefixed JSON request per connection,
    records it, replies with a pong frame on stream 0 and an exit frame
    on stream 2 — the exact shape ``_broker_ping`` expects.  Empty
    connections (liveness probes, connect-and-drop clients) are ignored.
    """

    def __init__(self, path: Path):
        self.path = path
        self.requests: list = []
        self._sock: "socket.socket | None" = None
        self._stop = threading.Event()
        self._thread: "threading.Thread | None" = None

    def start(self) -> "FakeBroker":
        self.path.unlink(missing_ok=True)
        self._sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self._sock.bind(str(self.path))
        self._sock.listen(8)
        self._sock.settimeout(0.1)
        self._stop.clear()
        self._thread = threading.Thread(target=self._serve, daemon=True)
        self._thread.start()
        return self

    def stop(self) -> None:
        """Close the listener and unlink the socket — after this, the
        path can be re-bound with a NEW inode (broker restart)."""
        self._stop.set()
        if self._sock is not None:
            self._sock.close()
        if self._thread is not None:
            self._thread.join(timeout=5.0)
        self.path.unlink(missing_ok=True)

    def _serve(self) -> None:
        while not self._stop.is_set():
            try:
                conn, _ = self._sock.accept()
            except socket.timeout:
                continue
            except OSError:
                return
            threading.Thread(target=self._handle, args=(conn,), daemon=True).start()

    def _handle(self, conn: socket.socket) -> None:
        try:
            conn.settimeout(5.0)
            hdr = _recv_exact(conn, 4)
            if hdr is None:
                return
            (length,) = struct.unpack(">I", hdr)
            body = _recv_exact(conn, length)
            if body is None:
                return
            request = json.loads(body.decode("utf-8"))
            self.requests.append(request)
            payload = json.dumps(
                {"pong": True, "jail_id_seen": request.get("jail_id")}
            ).encode()
            conn.sendall(struct.pack(">BI", 0, len(payload)) + payload)
            conn.sendall(struct.pack(">BI", 2, 4) + struct.pack(">i", 0))
        except (OSError, ValueError, struct.error):
            pass
        finally:
            try:
                conn.close()
            except OSError:
                pass


class RawUpstream:
    """Byte-level upstream double for the forwarded-verbatim path: each
    connection's bytes are appended to ``received``; once ``expect``
    bytes total have arrived it answers ``b"PONG"`` on that connection.
    No framing — this is what the relay's degraded pipe must reach."""

    def __init__(self, path: Path, expect: int):
        self.path = path
        self.expect = expect
        self.received = b""
        self._lock = threading.Lock()
        self._sock: "socket.socket | None" = None
        self._thread: "threading.Thread | None" = None
        self._stop = threading.Event()

    def start(self) -> "RawUpstream":
        self.path.unlink(missing_ok=True)
        self._sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self._sock.bind(str(self.path))
        self._sock.listen(8)
        self._sock.settimeout(0.1)
        self._thread = threading.Thread(target=self._serve, daemon=True)
        self._thread.start()
        return self

    def stop(self) -> None:
        self._stop.set()
        if self._sock is not None:
            self._sock.close()
        if self._thread is not None:
            self._thread.join(timeout=5.0)
        self.path.unlink(missing_ok=True)

    def _serve(self) -> None:
        while not self._stop.is_set():
            try:
                conn, _ = self._sock.accept()
            except socket.timeout:
                continue
            except OSError:
                return
            threading.Thread(target=self._handle, args=(conn,), daemon=True).start()

    def _handle(self, conn: socket.socket) -> None:
        try:
            conn.settimeout(5.0)
            while True:
                chunk = conn.recv(65536)
                if not chunk:
                    return
                with self._lock:
                    self.received += chunk
                    done = len(self.received) >= self.expect
                if done:
                    conn.sendall(b"PONG")
                    return
        except OSError:
            pass
        finally:
            try:
                conn.close()
            except OSError:
                pass


def _start_relay(
    socket_path: Path, broker_path: Path, jail: str, log_path: Path
) -> subprocess.Popen:
    with open(log_path, "wb") as log_f:
        proc = subprocess.Popen(
            [
                sys.executable,
                str(REPO_ROOT / "src" / "broker_relay.py"),
                "--socket",
                str(socket_path),
                "--broker",
                str(broker_path),
                "--jail",
                jail,
            ],
            stdout=log_f,
            stderr=log_f,
        )
    _wait_connectable(socket_path, proc)
    return proc


def _terminate(proc: subprocess.Popen) -> None:
    if proc.poll() is None:
        proc.terminate()
        try:
            proc.wait(timeout=5.0)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait(timeout=5.0)


def _wait_connectable(
    path: Path, proc: "subprocess.Popen | None" = None, timeout: float = 5.0
) -> None:
    """Spin on connect() past the bind→listen race (connecting between
    the two syscalls yields ECONNREFUSED, not a missing file)."""
    deadline = time.monotonic() + timeout
    last: "Exception | None" = None
    while time.monotonic() < deadline:
        if proc is not None and proc.poll() is not None:
            raise AssertionError(f"relay exited early rc={proc.returncode}")
        s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        s.settimeout(1.0)
        try:
            s.connect(str(path))
            return
        except OSError as e:
            last = e
            time.sleep(0.02)
        finally:
            s.close()
    raise AssertionError(f"{path} never connectable: {last!r}")


def _framed_roundtrip(path: Path, request: dict) -> dict:
    """Send one framed request through the relay, return the first
    stdout-frame JSON of the response."""
    s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    s.settimeout(5.0)
    s.connect(str(path))
    try:
        body = json.dumps(request).encode()
        s.sendall(struct.pack(">I", len(body)) + body)
        while True:
            hdr = _recv_exact(s, 5)
            assert hdr is not None, "EOF before a response frame"
            sid, length = struct.unpack(">BI", hdr)
            payload = _recv_exact(s, length)
            assert payload is not None, "truncated response frame"
            if sid == 0:
                return json.loads(payload.decode())
            if sid == 2:
                raise AssertionError("exit frame before any stdout frame")
    finally:
        s.close()


def test_broker_restart_new_inode_next_connect_succeeds(relay_dir):
    """THE Round-2 regression.  A socket-file bind mount pins the
    broker socket's inode, so ``yolo broker restart`` (unlink +
    re-bind) left every running jail piping into a dead socket.  The
    relay dials the broker path PER CONNECTION: kill the fake broker,
    re-bind the same path (new inode), and a second connection through
    the SAME relay process must succeed."""
    broker_path = relay_dir / "broker.sock"
    relay_path = relay_dir / "relay.sock"
    broker = FakeBroker(broker_path).start()
    proc = _start_relay(relay_path, broker_path, "jail-r", relay_dir / "relay.log")
    try:
        assert _broker_ping(relay_path), "ping through a fresh relay must work"

        broker.stop()  # old inode gone
        broker2 = FakeBroker(broker_path).start()  # same path, NEW inode
        try:
            assert _broker_ping(relay_path), (
                "the same relay process must reach the restarted broker"
            )
            assert broker2.requests, "second ping never reached the new broker"
        finally:
            broker2.stop()
    finally:
        _terminate(proc)
        broker.stop()


def test_jail_id_stamped_and_client_value_overridden(relay_dir):
    """The relay stamps ``jail_id`` on the first message of every
    connection — host-side, so the broker log's attribution can be
    trusted.  A client-supplied jail_id must be overridden, not
    honored (the jail doesn't get to name itself)."""
    broker_path = relay_dir / "broker.sock"
    relay_path = relay_dir / "relay.sock"
    broker = FakeBroker(broker_path).start()
    proc = _start_relay(relay_path, broker_path, "jail-abc", relay_dir / "relay.log")
    try:
        reply = _framed_roundtrip(relay_path, {"action": "ping"})
        assert reply["pong"] is True
        assert broker.requests[-1]["jail_id"] == "jail-abc"

        reply = _framed_roundtrip(relay_path, {"action": "ping", "jail_id": "spoofed"})
        assert reply["jail_id_seen"] == "jail-abc"
        assert broker.requests[-1]["jail_id"] == "jail-abc"
    finally:
        _terminate(proc)
        broker.stop()


def test_unparseable_first_message_forwarded_verbatim(relay_dir):
    """Attribution is best-effort: bytes that aren't a framed JSON
    request (here: a garbage length prefix over the size cap) must
    reach the broker verbatim and the pipe must keep working in both
    directions — the relay never kills traffic over logging metadata."""
    garbage = b"NOT A FRAME AT ALL"  # b"NOT " decodes to a >1GB length
    broker_path = relay_dir / "broker.sock"
    relay_path = relay_dir / "relay.sock"
    upstream = RawUpstream(broker_path, expect=len(garbage)).start()
    proc = _start_relay(relay_path, broker_path, "jail-v", relay_dir / "relay.log")
    try:
        s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        s.settimeout(5.0)
        s.connect(str(relay_path))
        try:
            s.sendall(garbage)
            assert _recv_exact(s, 4) == b"PONG"
        finally:
            s.close()
        assert upstream.received == garbage
    finally:
        _terminate(proc)
        upstream.stop()


def test_valid_frame_with_non_json_body_forwarded_verbatim(relay_dir):
    """A well-framed but non-JSON body takes the other verbatim path:
    the complete frame (header included) is forwarded untouched."""
    body = b"\x01\x02 not json \xff"
    msg = struct.pack(">I", len(body)) + body
    broker_path = relay_dir / "broker.sock"
    relay_path = relay_dir / "relay.sock"
    upstream = RawUpstream(broker_path, expect=len(msg)).start()
    proc = _start_relay(relay_path, broker_path, "jail-v", relay_dir / "relay.log")
    try:
        s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        s.settimeout(5.0)
        s.connect(str(relay_path))
        try:
            s.sendall(msg)
            assert _recv_exact(s, 4) == b"PONG"
        finally:
            s.close()
        assert upstream.received == msg
    finally:
        _terminate(proc)
        upstream.stop()


def test_broker_down_client_sees_eof(relay_dir):
    """Relay up, broker down: the relay ends the connection with a
    clean EOF and zero frames.  The terminator maps
    EOF-before-exit-frame to the broker layer — distinct from connect()
    failing, which is the relay layer.

    The client SENDS ITS FRAMED REQUEST first, like the real terminator
    does: the relay's dial-failure path must drain those bytes before
    closing.  Closing with the request unread discards the queue and
    raises ECONNRESET at the peer (Linux AF_UNIX) instead of EOF — the
    empirical 200/200 'Connection reset by peer' incident shape that
    made the broker-layer message dead code."""
    relay_path = relay_dir / "relay.sock"
    proc = _start_relay(
        relay_path, relay_dir / "no-broker.sock", "jail-e", relay_dir / "relay.log"
    )
    try:
        s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        s.settimeout(5.0)
        s.connect(str(relay_path))
        try:
            body = json.dumps({"action": "refresh"}).encode()
            s.sendall(struct.pack(">I", len(body)) + body)
            assert s.recv(1) == b"", "expected clean EOF, not ECONNRESET"
        finally:
            s.close()
    finally:
        _terminate(proc)


def test_broker_down_terminator_names_broker_layer_end_to_end(relay_dir):
    """The full incident path, no doubles: the REAL terminator client
    (``ask_host_broker``) through the REAL relay with the broker down
    must raise the broker-layer message — 'host broker unreachable
    through the relay' — not the generic ECONNRESET wrap and not the
    relay-layer wording.  Locks in that the fake-relay unit test isn't
    passing only because its double drains the request."""
    from src import oauth_broker_jail

    relay_path = relay_dir / "relay.sock"
    proc = _start_relay(
        relay_path, relay_dir / "no-broker.sock", "jail-e2e", relay_dir / "relay.log"
    )
    try:
        with pytest.raises(RuntimeError) as exc:
            oauth_broker_jail.ask_host_broker(str(relay_path), {"action": "refresh"})
    finally:
        _terminate(proc)
    msg = str(exc.value)
    assert "unreachable through the relay" in msg
    assert not msg.startswith("relay unreachable")


@pytest.mark.skipif(not sys.platform.startswith("linux"), reason="/proc fd accounting")
def test_no_fd_growth_across_sequential_connections(relay_dir):
    """Connect-and-drop clients and full round-trips alike must leave
    the relay's fd table at its baseline — the _pipe close discipline
    (shutdown alone doesn't release the fd) is what the old in-process
    relay got right and the standalone one must keep."""

    def fd_count(pid: int) -> int:
        return len(os.listdir(f"/proc/{pid}/fd"))

    def settled_fd_count(pid: int, timeout: float = 5.0) -> int:
        deadline = time.monotonic() + timeout
        prev = fd_count(pid)
        while time.monotonic() < deadline:
            time.sleep(0.05)
            cur = fd_count(pid)
            if cur == prev:
                return cur
            prev = cur
        return prev

    broker_path = relay_dir / "broker.sock"
    relay_path = relay_dir / "relay.sock"
    broker = FakeBroker(broker_path).start()
    proc = _start_relay(relay_path, broker_path, "jail-f", relay_dir / "relay.log")
    try:
        assert _broker_ping(relay_path)  # warm-up
        baseline = settled_fd_count(proc.pid)

        for _ in range(10):
            c = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
            c.connect(str(relay_path))
            c.close()
        for _ in range(10):
            assert _broker_ping(relay_path)

        deadline = time.monotonic() + 5.0
        while time.monotonic() < deadline and fd_count(proc.pid) > baseline:
            time.sleep(0.05)
        assert fd_count(proc.pid) <= baseline, (
            f"fd leak: {fd_count(proc.pid)} > baseline {baseline}"
        )
        assert _broker_ping(relay_path), "relay must still serve after the churn"
    finally:
        _terminate(proc)
        broker.stop()


def test_relay_ensure_idempotent_heals_after_kill9_and_stop_reaps(
    relay_dir, monkeypatch
):
    """The supervision loop against real relay processes: alive → noop
    (same pid twice); SIGKILL'd relay (stale socket file + zombie child
    left behind) → respawn under a new pid; ``_relay_stop`` reaps the
    process, its PID file, and — via the relay's SIGTERM handler — its
    socket.

    The relay is a direct, never-waited child of this process, exactly
    as in production where ``_relay_ensure`` Popens it from ``yolo
    run``: ``_relay_kill`` must do its own waitpid reaping.  Without
    it, the SIGTERM'd child sits as a kill(pid, 0)-visible zombie, the
    liveness poll spins its full 3s timeout, and every graceful jail
    exit stalls and then SIGKILLs a corpse."""
    monkeypatch.setattr("cli.loopholes_runtime.GLOBAL_STORAGE", relay_dir)
    cname = f"yj-relay-test-{os.urandom(4).hex()}"
    short_hash = _relay_short_hash(cname)
    pid_file = _relay_pid_file(short_hash)
    sockets_dir = relay_dir / "sockets"
    sock = sockets_dir / "claude-oauth-broker.sock"
    # The broker socket needn't exist: the relay dials it per
    # connection, so its absence is a broker-layer failure, not a
    # relay-liveness one.
    broker_socket = relay_dir / "broker.sock"
    try:
        _relay_ensure(cname, sockets_dir, broker_socket=broker_socket)
        pid1 = _relay_read_pid(pid_file)
        assert pid1 is not None
        assert sock.exists()
        _wait_connectable(sock)

        _relay_ensure(cname, sockets_dir, broker_socket=broker_socket)
        assert _relay_read_pid(pid_file) == pid1, "alive relay must not be respawned"

        # SIGKILL leaves pid1 as an unreaped zombie child of THIS
        # process — kill(pid, 0) still counts it as alive, so the heal
        # path must reap it itself (deliberately no waitpid here).
        # SIGKILL delivery is asynchronous: wait for the kernel to
        # close the dead relay's listener (zombies hold no fds) so the
        # liveness probe can't race a still-dying process — but wait
        # via the SOCKET, never waitpid, to keep the zombie in place.
        os.kill(pid1, signal.SIGKILL)
        deadline = time.monotonic() + 5.0
        while time.monotonic() < deadline:
            probe = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
            probe.settimeout(1.0)
            try:
                probe.connect(str(sock))
            except OSError:
                break  # listener gone — the process is dead (zombie)
            finally:
                probe.close()
            time.sleep(0.01)
        _relay_ensure(cname, sockets_dir, broker_socket=broker_socket)
        pid2 = _relay_read_pid(pid_file)
        assert pid2 is not None and pid2 != pid1, "dead relay must be respawned"
        _wait_connectable(sock)

        # No reaper thread: _relay_kill itself must waitpid the
        # SIGTERM'd child — and therefore return in milliseconds, not
        # after its 3s timeout + SIGKILL of a zombie.
        start = time.monotonic()
        _relay_stop(cname)
        elapsed = time.monotonic() - start
        assert elapsed < 2.0, (
            f"_relay_stop took {elapsed:.2f}s — the liveness poll is "
            "counting an unreaped zombie child as alive"
        )
        assert not pid_file.exists()
        with pytest.raises(ChildProcessError):
            os.waitpid(pid2, os.WNOHANG)  # already reaped by _relay_kill
        deadline = time.monotonic() + 5.0
        while time.monotonic() < deadline and sock.exists():
            time.sleep(0.02)
        assert not sock.exists(), "SIGTERM'd relay must unlink its socket"
    finally:
        _relay_stop(cname)
        pid_file.unlink(missing_ok=True)
        _relay_lock_file(short_hash).unlink(missing_ok=True)
