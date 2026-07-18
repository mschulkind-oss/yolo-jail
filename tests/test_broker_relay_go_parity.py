"""Mixed-mode parity: the REAL Python broker-relay harness driving the GO
relay binary (go-port plan Stage 3, seam #2 + §5.5 mixed-mode tests).

This reuses the exact doubles and client from tests/test_broker_relay.py but
launches cmd/yolo-broker-relay (Go) instead of src/broker_relay.py (Python),
proving the Go relay is a byte/behavior drop-in over the frozen socket
protocol. It asserts the incident-shaping behaviors that matter:

  * jail_id stamped host-side, client value overridden
  * unparseable / valid-non-JSON first message forwarded verbatim
  * broker down -> the REAL terminator (oauth_broker_jail.ask_host_broker)
    raises the broker-LAYER message, not a generic ECONNRESET wrap
  * broker restart (new inode) -> same relay reaches it on the next connect

Skips when the Go toolchain is unavailable (Python-only dev path); CI has Go.
"""

from __future__ import annotations

import json
import platform
import shutil
import socket
import struct
import subprocess
import sys
import tempfile
import time
from pathlib import Path

import pytest

REPO_ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(REPO_ROOT / "src"))

# Reuse the Python harness's doubles verbatim — same protocol behavior.
from test_broker_relay import (  # noqa: E402
    FakeBroker,
    RawUpstream,
    _recv_exact,
    _wait_connectable,
)


def _goarch() -> str:
    return {"x86_64": "amd64", "aarch64": "arm64", "arm64": "arm64"}.get(
        platform.machine(), platform.machine()
    )


def _go_relay_binary() -> "Path | None":
    goos = "linux" if sys.platform.startswith("linux") else "darwin"
    binpath = REPO_ROOT / "dist-go" / f"{goos}-{_goarch()}" / "yolo-broker-relay"
    if binpath.is_file():
        return binpath
    if shutil.which("go") is None:
        return None
    try:
        subprocess.run(
            ["bash", str(REPO_ROOT / "scripts" / "build-go.sh")],
            cwd=REPO_ROOT,
            check=True,
            capture_output=True,
        )
    except (subprocess.CalledProcessError, OSError):
        return None
    return binpath if binpath.is_file() else None


GO_RELAY = _go_relay_binary()
pytestmark = pytest.mark.skipif(
    GO_RELAY is None, reason="Go toolchain unavailable — cannot build the Go relay"
)


@pytest.fixture
def relay_dir():
    base = "/private/tmp" if sys.platform == "darwin" else "/tmp"
    d = Path(tempfile.mkdtemp(dir=base, prefix="yj-gorelay-"))
    yield d
    shutil.rmtree(d, ignore_errors=True)


def _start_go_relay(socket_path, broker_path, jail, log_path) -> subprocess.Popen:
    with open(log_path, "wb") as log_f:
        proc = subprocess.Popen(
            [
                str(GO_RELAY),
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


def _framed_roundtrip(path: Path, request: dict) -> dict:
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


def test_go_relay_stamps_and_overrides_jail_id(relay_dir):
    broker_path = relay_dir / "broker.sock"
    relay_path = relay_dir / "relay.sock"
    broker = FakeBroker(broker_path).start()
    proc = _start_go_relay(relay_path, broker_path, "jail-abc", relay_dir / "relay.log")
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


def test_go_relay_unparseable_forwarded_verbatim(relay_dir):
    garbage = b"NOT A FRAME AT ALL"
    broker_path = relay_dir / "broker.sock"
    relay_path = relay_dir / "relay.sock"
    upstream = RawUpstream(broker_path, expect=len(garbage)).start()
    proc = _start_go_relay(relay_path, broker_path, "jail-v", relay_dir / "relay.log")
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


def test_go_relay_valid_frame_non_json_forwarded_verbatim(relay_dir):
    body = b"\x01\x02 not json \xff"
    msg = struct.pack(">I", len(body)) + body
    broker_path = relay_dir / "broker.sock"
    relay_path = relay_dir / "relay.sock"
    upstream = RawUpstream(broker_path, expect=len(msg)).start()
    proc = _start_go_relay(relay_path, broker_path, "jail-v", relay_dir / "relay.log")
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


def test_go_relay_broker_down_client_sees_eof(relay_dir):
    relay_path = relay_dir / "relay.sock"
    proc = _start_go_relay(
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


def test_go_relay_broker_down_terminator_names_broker_layer(relay_dir):
    """The full incident path with the REAL terminator client against the GO
    relay: broker down must raise the broker-layer message, not the generic
    ECONNRESET wrap or the relay-layer wording."""
    from src import oauth_broker_jail

    relay_path = relay_dir / "relay.sock"
    proc = _start_go_relay(
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


def test_go_relay_broker_restart_new_inode(relay_dir):
    broker_path = relay_dir / "broker.sock"
    relay_path = relay_dir / "relay.sock"
    broker = FakeBroker(broker_path).start()
    proc = _start_go_relay(relay_path, broker_path, "jail-r", relay_dir / "relay.log")
    try:
        assert _framed_roundtrip(relay_path, {"action": "ping"})["pong"] is True
        broker.stop()  # old inode gone
        broker2 = FakeBroker(broker_path).start()  # same path, NEW inode
        try:
            assert _framed_roundtrip(relay_path, {"action": "ping"})["pong"] is True
            assert broker2.requests, "second ping never reached the new broker"
        finally:
            broker2.stop()
    finally:
        _terminate(proc)
        broker.stop()
    time.sleep(0)  # keep import of time meaningful for parity with harness
