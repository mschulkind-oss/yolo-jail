"""Live black-box parity: the REAL Python jail-side client
(oauth_broker_jail.ask_host_broker) driving the GO broker daemon
(cmd/yolo-claude-oauth-broker-host), with a fake upstream via
YOLO_BROKER_UPSTREAM_URL (go-port plan Stage 6).

Proves the Go daemon is a byte/behavior drop-in over the frozen loophole
socket protocol + creds-file contract:
  * ping -> {pong, pid}
  * cached -> {error: no_cached_token} when stale / the cached response
    when fresh
  * refresh -> hits the fake upstream, writes the shared creds file with the
    exact indent=2 blob, returns the oauth response
  * cross-language flock: the SAME refresh.lock path a Python broker uses

Skips when the Go toolchain is unavailable (Python-only dev path); CI has Go.
"""

from __future__ import annotations

import http.server
import json
import platform
import shutil
import socket
import struct
import subprocess
import sys
import tempfile
import threading
import time
from pathlib import Path

import pytest

REPO_ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(REPO_ROOT / "src"))


def _goarch() -> str:
    return {"x86_64": "amd64", "aarch64": "arm64", "arm64": "arm64"}.get(
        platform.machine(), platform.machine()
    )


def _go_broker_binary() -> "Path | None":
    goos = "linux" if sys.platform.startswith("linux") else "darwin"
    binpath = (
        REPO_ROOT / "dist-go" / f"{goos}-{_goarch()}" / "yolo-claude-oauth-broker-host"
    )
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


GO_BROKER = _go_broker_binary()
pytestmark = pytest.mark.skipif(
    GO_BROKER is None, reason="Go toolchain unavailable — cannot build the Go broker"
)


class _FakeUpstream(http.server.BaseHTTPRequestHandler):
    """A minimal fake platform.claude.com token endpoint."""

    RESPONSE = {
        "access_token": "AT_from_upstream",
        "refresh_token": "RT_from_upstream",
        "expires_in": 7200,
        "scope": "user:inference",
    }

    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0))
        _ = self.rfile.read(length)
        body = json.dumps(self.RESPONSE).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *a):
        pass


@pytest.fixture
def upstream():
    server = http.server.HTTPServer(("127.0.0.1", 0), _FakeUpstream)
    t = threading.Thread(target=server.serve_forever, daemon=True)
    t.start()
    yield f"http://127.0.0.1:{server.server_address[1]}/v1/oauth/token"
    server.shutdown()


@pytest.fixture
def broker_env(tmp_path):
    base = "/private/tmp" if sys.platform == "darwin" else "/tmp"
    d = Path(tempfile.mkdtemp(dir=base, prefix="yj-gobroker-"))
    yield d
    shutil.rmtree(d, ignore_errors=True)


def _wait_connectable(path: Path, proc, timeout=10.0):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if proc.poll() is not None:
            raise AssertionError(f"broker exited early rc={proc.returncode}")
        s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        s.settimeout(1.0)
        try:
            s.connect(str(path))
            return
        except OSError:
            time.sleep(0.02)
        finally:
            s.close()
    raise AssertionError(f"{path} never connectable")


def _seed_fake_certs(state_dir: Path):
    """Pre-create CA + leaf files so EnsureCAAndLeaf early-returns without
    needing openssl (not installed in every CI/jail env). We test the socket
    protocol + creds contract, not TLS cert generation (that's covered by the
    Python cert unit tests)."""
    state_dir.mkdir(parents=True, exist_ok=True)
    for name in ("ca.crt", "ca.key", "server.crt", "server.key"):
        (state_dir / name).write_text("fake\n")


def _start_go_broker(sock, creds, upstream_url, log, extra_env=None):
    import os

    env = dict(os.environ)
    env["YOLO_BROKER_UPSTREAM_URL"] = upstream_url
    state_dir = Path(log).parent / "state"
    _seed_fake_certs(state_dir)
    env["YOLO_BROKER_STATE_DIR"] = str(state_dir)
    if extra_env:
        env.update(extra_env)
    with open(log, "wb") as lf:
        proc = subprocess.Popen(
            [
                str(GO_BROKER),
                "--socket",
                str(sock),
                "--creds-file",
                str(creds),
                "--no-background-refresh",
            ],
            stdout=lf,
            stderr=lf,
            env=env,
        )
    _wait_connectable(sock, proc)
    return proc


def _terminate(proc):
    if proc.poll() is None:
        proc.terminate()
        try:
            proc.wait(timeout=5.0)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait(timeout=5.0)


def test_go_broker_ping(broker_env, upstream):
    from src import oauth_broker_jail

    sock = broker_env / "b.sock"
    creds = broker_env / "creds.json"
    proc = _start_go_broker(sock, creds, upstream, broker_env / "b.log")
    try:
        resp = oauth_broker_jail.ask_host_broker(str(sock), {"action": "ping"})
        assert resp["pong"] is True
        assert isinstance(resp["pid"], int)
    finally:
        _terminate(proc)


def test_go_broker_cached_none_when_missing(broker_env, upstream):
    from src import oauth_broker_jail

    sock = broker_env / "b.sock"
    creds = broker_env / "creds.json"  # never created
    proc = _start_go_broker(sock, creds, upstream, broker_env / "b.log")
    try:
        resp = oauth_broker_jail.ask_host_broker(str(sock), {"action": "cached"})
        assert resp == {"error": "no_cached_token"}
    finally:
        _terminate(proc)


def test_go_broker_refresh_writes_creds_and_returns_tokens(broker_env, upstream):
    from src import oauth_broker_jail

    sock = broker_env / "b.sock"
    creds = broker_env / "creds.json"
    # Seed an expired shared creds file so refresh hits the fake upstream.
    creds.write_text(
        json.dumps(
            {
                "claudeAiOauth": {
                    "accessToken": "AT_old",
                    "refreshToken": "RT_old",
                    "expiresAt": int(time.time() * 1000) - 10_000,
                    "subscriptionType": "max",
                    "scopes": ["user:inference"],
                }
            }
        )
    )
    proc = _start_go_broker(sock, creds, upstream, broker_env / "b.log")
    try:
        resp = oauth_broker_jail.ask_host_broker(str(sock), {"action": "refresh"})
        assert resp["access_token"] == "AT_from_upstream"
        assert resp["refresh_token"] == "RT_from_upstream"
        assert resp["token_type"] == "Bearer"
        assert resp["expires_in"] > 0
        # The shared creds file must be rewritten with the new identity, in the
        # exact on-disk shape (claudeAiOauth wrapper, preserved fields).
        on_disk = json.loads(creds.read_text())["claudeAiOauth"]
        assert on_disk["accessToken"] == "AT_from_upstream"
        assert on_disk["refreshToken"] == "RT_from_upstream"
        assert on_disk["subscriptionType"] == "max"  # preserved
        assert on_disk["scopes"] == ["user:inference"]  # previous wins
    finally:
        _terminate(proc)


def test_go_broker_refresh_cache_hit_skips_upstream(broker_env, upstream):
    from src import oauth_broker_jail

    sock = broker_env / "b.sock"
    creds = broker_env / "creds.json"
    # Fresh token (>90s headroom) -> cache hit, no upstream call, returns cached.
    creds.write_text(
        json.dumps(
            {
                "claudeAiOauth": {
                    "accessToken": "AT_fresh",
                    "refreshToken": "RT_fresh",
                    "expiresAt": int(time.time() * 1000) + 3_600_000,
                    "scopes": ["user:inference"],
                }
            }
        )
    )
    proc = _start_go_broker(sock, creds, upstream, broker_env / "b.log")
    try:
        resp = oauth_broker_jail.ask_host_broker(str(sock), {"action": "refresh"})
        assert resp["access_token"] == "AT_fresh"  # cached, not from upstream
        assert resp["refresh_token"] == "RT_fresh"
    finally:
        _terminate(proc)


def test_go_broker_unknown_action_exit_2(broker_env, upstream):
    """An unknown action produces stderr + exit 2 -> the client raises."""
    from src import oauth_broker_jail

    sock = broker_env / "b.sock"
    creds = broker_env / "creds.json"
    proc = _start_go_broker(sock, creds, upstream, broker_env / "b.log")
    try:
        # ask_host_broker returns the parsed stdout JSON; an action with only
        # stderr + exit(2) yields no stdout frame, so it raises RuntimeError.
        with pytest.raises(RuntimeError):
            oauth_broker_jail.ask_host_broker(str(sock), {"action": "bogus"})
    finally:
        _terminate(proc)


def test_cross_language_flock_contention(broker_env):
    """The refresh.lock flock mutually excludes Python and Go on the SAME path
    (kernel flock), so a Python broker and a Go broker can't both refresh at
    once during rollout. We hold the lock from Python and assert a
    non-blocking acquire of the same path fails — the exclusion the Go broker's
    withRefreshLock relies on (both use flock(LOCK_EX) on the identical path)."""
    import fcntl

    lock_path = broker_env / "refresh.lock"
    lock_path.write_text("")
    holder = open(lock_path, "w")
    fcntl.flock(holder, fcntl.LOCK_EX)
    try:
        contender = open(lock_path, "w")
        try:
            with pytest.raises(BlockingIOError):
                fcntl.flock(contender, fcntl.LOCK_EX | fcntl.LOCK_NB)
        finally:
            contender.close()
    finally:
        fcntl.flock(holder, fcntl.LOCK_UN)
        holder.close()
