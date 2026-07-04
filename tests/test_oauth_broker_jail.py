"""Tests for src.oauth_broker_jail — the in-jail TLS terminator.

The big regressions we lock in here:

1.  Only ``grant_type=refresh_token`` requests route through the host
    broker's refresh flow.  Every other grant (most importantly
    ``authorization_code`` from ``/login``) and any non-
    ``/v1/oauth/token`` path must proxy upstream, or ``/login`` returns
    400 with ``no_refresh_token`` on a logged-out jail.

2.  The upstream proxy goes *through* the host broker via the unix
    socket — not via ``urllib`` direct from the jail.  ``--add-host``
    maps ``platform.claude.com`` back to this daemon, so a direct
    upstream dial loops back and the whole request returns 502
    ``upstream_unreachable``.  The host has real DNS.
"""

from __future__ import annotations

import base64
import json
import socket
import struct
import threading

import pytest

from src import oauth_broker_jail


# ---------------------------------------------------------------------------
# _is_refresh_grant — the routing predicate
# ---------------------------------------------------------------------------


def test_is_refresh_grant_true_for_refresh_token():
    body = json.dumps({"grant_type": "refresh_token", "refresh_token": "abc"}).encode()
    assert oauth_broker_jail._is_refresh_grant(body) is True


def test_is_refresh_grant_false_for_authorization_code():
    """/login posts ``grant_type=authorization_code`` — the routing bug
    treated this as a refresh and returned 400.  Must route to the
    proxy, not the broker."""
    body = json.dumps({"grant_type": "authorization_code", "code": "xyz"}).encode()
    assert oauth_broker_jail._is_refresh_grant(body) is False


def test_is_refresh_grant_false_for_empty_body():
    assert oauth_broker_jail._is_refresh_grant(b"") is False


def test_is_refresh_grant_false_for_non_json_body():
    """A malformed body (e.g. form-urlencoded) must not accidentally
    match — let upstream return its own error."""
    assert oauth_broker_jail._is_refresh_grant(b"grant_type=refresh_token") is False


def test_is_refresh_grant_false_for_json_non_object():
    assert oauth_broker_jail._is_refresh_grant(b'"refresh_token"') is False
    assert oauth_broker_jail._is_refresh_grant(b"[]") is False


def test_is_refresh_grant_false_when_grant_type_missing():
    body = json.dumps({"refresh_token": "abc"}).encode()
    assert oauth_broker_jail._is_refresh_grant(body) is False


# ---------------------------------------------------------------------------
# _proxy_upstream — routes through the host broker, not urllib
# ---------------------------------------------------------------------------


def test_proxy_upstream_sends_proxy_action_to_host_broker(monkeypatch):
    """The whole point of this change: the jail never dials upstream
    directly.  Confirm the request we build carries ``action=proxy`` and
    base64-encoded body, and that the host broker's response (status,
    headers, body) round-trips verbatim to the caller."""
    captured: dict = {}

    def fake_ask(socket_path, request):
        captured["socket_path"] = socket_path
        captured["request"] = request
        return {
            "status": 200,
            "headers": {"Content-Type": "application/json", "X-Trace": "abc"},
            "body_b64": base64.b64encode(b'{"access_token":"tok"}').decode(),
        }

    monkeypatch.setattr(oauth_broker_jail, "ask_host_broker", fake_ask)
    status, headers, body = oauth_broker_jail._proxy_upstream(
        "/run/yolo-services/claude-oauth-broker.sock",
        "POST",
        "/v1/oauth/token",
        {"Content-Type": "application/json"},
        b'{"grant_type":"authorization_code","code":"x"}',
    )
    assert captured["socket_path"] == "/run/yolo-services/claude-oauth-broker.sock"
    assert captured["request"]["action"] == "proxy"
    assert captured["request"]["method"] == "POST"
    assert captured["request"]["path"] == "/v1/oauth/token"
    assert (
        base64.b64decode(captured["request"]["body_b64"])
        == b'{"grant_type":"authorization_code","code":"x"}'
    )
    assert status == 200
    assert headers["Content-Type"] == "application/json"
    assert body == b'{"access_token":"tok"}'


def test_proxy_upstream_returns_502_when_host_broker_fails(monkeypatch):
    """If the host broker connection itself breaks (socket gone, protocol
    error), surface a 502 so Claude Code sees a real failure — and include
    the detail so the operator can debug."""

    def fake_ask(_socket_path, _request):
        raise RuntimeError("host broker closed without an exit frame")

    monkeypatch.setattr(oauth_broker_jail, "ask_host_broker", fake_ask)
    status, headers, body = oauth_broker_jail._proxy_upstream(
        "/tmp/nope.sock", "GET", "/whatever", {}, b""
    )
    assert status == 502
    assert headers["Content-Type"] == "application/json"
    parsed = json.loads(body)
    assert parsed["error"] == "broker_unavailable"
    assert "host broker closed" in parsed["detail"]


def test_proxy_upstream_returns_502_on_upstream_error_dict(monkeypatch):
    """Host broker surfacing ``{error: "upstream_unreachable"}`` means the
    real ``platform.claude.com`` was unreachable.  Pass that back as 502
    with the detail so the user sees the real network error."""

    def fake_ask(_socket_path, _request):
        return {"error": "upstream_unreachable", "message": "name or service not known"}

    monkeypatch.setattr(oauth_broker_jail, "ask_host_broker", fake_ask)
    status, _headers, body = oauth_broker_jail._proxy_upstream(
        "/tmp/nope.sock", "GET", "/whatever", {}, b""
    )
    assert status == 502
    parsed = json.loads(body)
    assert parsed["error"] == "upstream_unreachable"


def test_ask_host_broker_wraps_oserror_as_runtimeerror(tmp_path):
    """Regression: ``ask_host_broker`` used to leak ``OSError``/
    ``FileNotFoundError``/``ConnectionRefusedError`` from ``conn.connect``
    when the host daemon was dead or the socket bind-mount was stale.
    Callers (``_proxy_upstream``, the refresh path) catch only
    ``RuntimeError``; the OSError escaped past the HTTP handler and
    Claude saw a torn TLS connection mid-response.  During ``/login``
    that surfaced as "socket closed too soon after I pasted the code".

    Lock in: any transport failure becomes a ``RuntimeError`` so the
    single ``except RuntimeError`` in callers catches it and returns a
    proper 502 instead of aborting the connection."""
    missing_sock = tmp_path / "definitely-not-here.sock"
    with pytest.raises(RuntimeError) as exc:
        oauth_broker_jail.ask_host_broker(str(missing_sock), {"action": "ping"})
    assert str(missing_sock) in str(exc.value)


# ---------------------------------------------------------------------------
# Layer discrimination — the jail log must answer "which layer failed":
# the per-jail relay (connect fails) or the broker behind it (relay
# answers but closes before an exit frame).
# ---------------------------------------------------------------------------


def test_ask_host_broker_names_relay_layer_on_enoent(sock_dir):
    """Relay socket missing entirely (relay never started, or the
    sockets dir was recreated under it) → connect raises ENOENT → the
    message must name the RELAY layer, not the broker."""
    missing_sock = sock_dir / "relay-not-here.sock"
    with pytest.raises(RuntimeError) as exc:
        oauth_broker_jail.ask_host_broker(str(missing_sock), {"action": "ping"})
    msg = str(exc.value)
    assert msg.startswith("relay unreachable")
    assert str(missing_sock) in msg


def test_ask_host_broker_names_relay_layer_on_connection_refused(sock_dir):
    """Socket file present but no relay process listening behind it →
    connect raises ECONNREFUSED → relay-layer wording."""
    dead_sock = sock_dir / "relay-dead.sock"
    s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    s.bind(str(dead_sock))
    s.close()  # leaves the socket file with nothing accepting on it
    with pytest.raises(RuntimeError) as exc:
        oauth_broker_jail.ask_host_broker(str(dead_sock), {"action": "ping"})
    msg = str(exc.value)
    assert msg.startswith("relay unreachable")
    assert str(dead_sock) in msg


def test_ask_host_broker_names_broker_layer_on_eof_before_exit_frame(sock_dir):
    """The relay accepted the connection but closed it without any
    response frames — its per-connection dial of the real broker failed.
    That is the BROKER layer (reached through the relay), and the
    message must say so instead of blaming the relay."""
    sock_path = sock_dir / "relay-live.sock"
    server = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    server.bind(str(sock_path))
    server.listen(1)

    def _recv_exact(conn: socket.socket, n: int) -> bytes:
        buf = b""
        while len(buf) < n:
            chunk = conn.recv(n - len(buf))
            if not chunk:
                return buf
            buf += chunk
        return buf

    def _accept_drain_close():
        conn, _ = server.accept()
        # Read the full framed request so the client's sendall completes,
        # then close with zero response frames — exactly what the relay
        # does when the broker socket is unreachable.
        (length,) = struct.unpack(">I", _recv_exact(conn, 4))
        _recv_exact(conn, length)
        conn.close()

    t = threading.Thread(target=_accept_drain_close, daemon=True)
    t.start()
    try:
        with pytest.raises(RuntimeError) as exc:
            oauth_broker_jail.ask_host_broker(str(sock_path), {"action": "ping"})
    finally:
        server.close()
        t.join(timeout=5)
    msg = str(exc.value)
    assert "unreachable through the relay" in msg
    assert not msg.startswith("relay unreachable")


def test_ask_host_broker_names_broker_layer_on_send_phase_reset(sock_dir):
    """The send-phase twin of the EOF case: the relay accepted, its
    per-connection dial of the broker failed, and it tore the
    connection down while our frame was still in flight — a large
    ``action=proxy`` body spends real time in ``sendall``, which then
    raises EPIPE/ECONNRESET instead of the recv path ever seeing EOF.
    That is still the BROKER layer and the message must say so, not
    fall into the generic ``host broker socket …: [Errno 32]`` wrap."""
    sock_path = sock_dir / "relay-reset.sock"
    server = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    server.bind(str(sock_path))
    server.listen(1)

    def _accept_close_without_reading():
        conn, _ = server.accept()
        # Close with the request unread — pending bytes are discarded
        # and the client's in-flight sendall raises EPIPE/ECONNRESET.
        conn.close()

    t = threading.Thread(target=_accept_close_without_reading, daemon=True)
    t.start()
    # Body far larger than the socket buffers so sendall is still
    # writing when the close lands.
    big_request = {"action": "proxy", "body_b64": "x" * (8 * 1024 * 1024)}
    try:
        with pytest.raises(RuntimeError) as exc:
            oauth_broker_jail.ask_host_broker(str(sock_path), big_request)
    finally:
        server.close()
        t.join(timeout=5)
    msg = str(exc.value)
    assert "unreachable through the relay" in msg
    assert not msg.startswith("relay unreachable")


def test_proxy_upstream_relay_layer_keeps_502_shape(sock_dir):
    """The HTTP-facing contract is unchanged by layer discrimination:
    a relay-layer failure still surfaces as 502 ``broker_unavailable``;
    only the detail string names the relay."""
    missing_sock = sock_dir / "relay-not-here.sock"
    status, headers, body = oauth_broker_jail._proxy_upstream(
        str(missing_sock), "POST", "/v1/oauth/token", {}, b'{"grant_type":"x"}'
    )
    assert status == 502
    assert headers["Content-Type"] == "application/json"
    parsed = json.loads(body)
    assert parsed["error"] == "broker_unavailable"
    assert parsed["detail"].startswith("relay unreachable")
    assert str(missing_sock) in parsed["detail"]


def test_proxy_upstream_returns_502_when_host_socket_missing(tmp_path):
    """End-to-end check of the same regression at the call-site level:
    a missing host socket should produce a 502 ``broker_unavailable``,
    not propagate an exception out of the HTTP handler."""
    missing_sock = tmp_path / "definitely-not-here.sock"
    status, headers, body = oauth_broker_jail._proxy_upstream(
        str(missing_sock), "POST", "/v1/oauth/token", {}, b'{"grant_type":"x"}'
    )
    assert status == 502
    assert headers["Content-Type"] == "application/json"
    parsed = json.loads(body)
    assert parsed["error"] == "broker_unavailable"
    assert str(missing_sock) in parsed["detail"]


def test_proxy_upstream_handles_empty_body(monkeypatch):
    """GETs have no body; we shouldn't send a stray base64 ``=`` chunk."""
    captured: dict = {}

    def fake_ask(_socket_path, request):
        captured["body_b64"] = request["body_b64"]
        return {"status": 204, "headers": {}, "body_b64": ""}

    monkeypatch.setattr(oauth_broker_jail, "ask_host_broker", fake_ask)
    status, _headers, body = oauth_broker_jail._proxy_upstream(
        "/tmp/s.sock", "GET", "/v1/me", {}, b""
    )
    assert captured["body_b64"] == ""
    assert status == 204
    assert body == b""
