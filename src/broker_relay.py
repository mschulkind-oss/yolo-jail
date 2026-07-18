"""Per-jail Claude OAuth broker relay — a raw byte proxy with one trick.

One relay process runs per jail, spawned and supervised by
``src/cli/loopholes_runtime._relay_ensure``.  It listens on
``claude-oauth-broker.sock`` inside the jail's host-services sockets
dir (visible in-jail through the existing directory mount — no
socket-file bind mount) and dials the real broker socket **per
connection**, so a restarted broker (new socket inode) is picked up on
the very next request.  A socket-file bind mount pins the inode — the
"one jail 502s after ``yolo broker restart``" bug — and on macOS
Podman Machine can't bind-mount a socket file at all (EOPNOTSUPP).

It is a standalone process, not a thread in ``yolo run``: conmon keeps
the container alive independently of any single yolo invocation, so
the relay must survive the death of whichever process spawned it.

The one protocol-aware trick: the loophole protocol is exactly one
4-byte-BE length-prefixed UTF-8 JSON request per connection, sent
client-first (``src/host_service.py``).  The relay reads that first
message, stamps ``request["jail_id"]`` with the jail's container name
(host-side injection — trustworthy, unlike an in-jail self-report;
docs/design/loophole-protocol.md: daemons must not trust a client-supplied
value), re-frames it, and then degrades to a dumb bidirectional pipe.
Attribution is best-effort: an unparseable, oversized, or slow first
message is forwarded verbatim and the connection keeps working — the
relay never kills traffic over logging metadata.

Failure semantics the jail-side terminator relies on
(``src/oauth_broker_jail.py``): relay socket missing/refused = relay
layer; relay accepts but ends the connection with zero response frames
= broker layer (the per-connection dial failed).  On dial failure the
relay drains the client's pending request before closing so the client
sees a CLEAN EOF — closing with unread bytes queued would surface as
ECONNRESET (Linux AF_UNIX discards the queue), which the terminator
cannot attribute to a layer.

Not a ``host_service`` handler — this is a byte proxy *in front of*
the frame protocol, not a participant in it.  Logs go to stderr; the
spawner redirects them under ``GLOBAL_STORAGE/logs/``.  Payloads carry
OAuth tokens and are never logged.  SIGTERM unlinks the relay socket
and exits, so a stopped relay reads as "socket absent", not "socket
dead".
"""

from __future__ import annotations

import argparse
import json
import logging
import os
import signal
import socket
import struct
import sys
import threading
import time
from pathlib import Path

log = logging.getLogger("broker_relay")

# First-message bounds for the attribution read.  Requests on this
# protocol are small JSON objects (see host_service._read_request); the
# cap exists so a garbage length prefix can't make us buffer gigabytes,
# and the timeout so a silent client can't park the stamp path forever.
# Blowing either bound downgrades to verbatim forwarding, never failure.
FIRST_MSG_MAX = 4 * 1024 * 1024
FIRST_MSG_TIMEOUT = 5.0


def _pipe(src: socket.socket, dst: socket.socket) -> None:
    """Copy bytes src→dst until EOF or error, then shut down and close.

    Each direction runs one of these; both close BOTH sockets so fds
    never outlive the connection (shutdown() alone doesn't release the
    fd).  The sibling thread's double-close is swallowed.
    """
    try:
        while True:
            data = src.recv(65536)
            if not data:
                break
            dst.sendall(data)
    except OSError:
        pass
    finally:
        for s, how in [(src, socket.SHUT_RD), (dst, socket.SHUT_WR)]:
            try:
                s.shutdown(how)
            except OSError:
                pass
        for s in (src, dst):
            try:
                s.close()
            except OSError:
                pass


def _read_first_message(client: socket.socket) -> "tuple[bytes | None, bytes]":
    """Try to read the connection's single framed request.

    Returns ``(body, raw)`` where ``raw`` is every byte consumed so far.
    ``body`` is the frame payload iff a complete frame arrived within
    FIRST_MSG_TIMEOUT / FIRST_MSG_MAX; on EOF, timeout, or an oversized
    length prefix it is None and the caller forwards ``raw`` verbatim.
    """
    raw = bytearray()
    client.settimeout(FIRST_MSG_TIMEOUT)
    try:
        while len(raw) < 4:
            chunk = client.recv(4 - len(raw))
            if not chunk:
                return None, bytes(raw)
            raw += chunk
        (length,) = struct.unpack(">I", raw[:4])
        if length > FIRST_MSG_MAX:
            return None, bytes(raw)
        while len(raw) < 4 + length:
            chunk = client.recv(4 + length - len(raw))
            if not chunk:
                return None, bytes(raw)
            raw += chunk
        return bytes(raw[4:]), bytes(raw)
    except OSError:
        # Timeout or client error mid-message — hand back what we have.
        return None, bytes(raw)
    finally:
        client.settimeout(None)


def _stamp_jail_id(body: bytes, jail_id: str) -> "bytes | None":
    """Re-frame ``body`` with ``jail_id`` stamped, or None if it isn't a
    JSON object (caller then forwards the original bytes verbatim).

    The stamp OVERRIDES any client-supplied jail_id — attribution must
    come from the host side, not from the jail naming itself.
    """
    try:
        request = json.loads(body.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError):
        return None
    if not isinstance(request, dict):
        return None
    request["jail_id"] = jail_id
    new_body = json.dumps(request).encode("utf-8")
    return struct.pack(">I", len(new_body)) + new_body


def _handle(client: socket.socket, broker_path: str, jail_id: str) -> None:
    """Serve one client connection: dial the broker, stamp, pipe."""
    try:
        upstream = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        upstream.connect(broker_path)
    except OSError as e:
        # Broker layer: end the connection with a clean EOF and zero
        # response frames — the terminator reads EOF-before-exit-frame
        # as "broker unreachable through the relay".  Closing while the
        # client's request sits unread in our receive queue would
        # DISCARD those bytes and raise ECONNRESET at the peer (Linux
        # AF_UNIX) instead of EOF, so the terminator could never
        # attribute the failure to a layer.  Order matters: shut down
        # our write side first (the client's recv sees EOF and it
        # closes), then drain whatever it already sent, then close.
        log.warning("dial %s failed: %s", broker_path, e)
        try:
            client.shutdown(socket.SHUT_WR)
        except OSError:
            pass
        try:
            # Bounded drain — a wedged client can't park this thread.
            deadline = time.monotonic() + FIRST_MSG_TIMEOUT
            while time.monotonic() < deadline:
                client.settimeout(max(0.05, deadline - time.monotonic()))
                if not client.recv(65536):
                    break
        except OSError:
            pass
        try:
            client.close()
        except OSError:
            pass
        return

    body, raw = _read_first_message(client)
    framed = _stamp_jail_id(body, jail_id) if body is not None else None
    if framed is None:
        if raw:
            # Payloads may carry tokens — log the length, never the bytes.
            log.warning(
                "first message not a framed JSON object (%d bytes) — "
                "forwarding unstamped",
                len(raw),
            )
        framed = raw
    try:
        if framed:
            upstream.sendall(framed)
    except OSError:
        for s in (client, upstream):
            try:
                s.close()
            except OSError:
                pass
        return

    threading.Thread(target=_pipe, args=(client, upstream), daemon=True).start()
    _pipe(upstream, client)


def serve(socket_path: Path, broker_path: Path, jail_id: str) -> int:
    """Accept-loop until SIGTERM/SIGINT; one handler thread per client."""
    socket_path.parent.mkdir(parents=True, exist_ok=True)
    # A stale file at our path (crashed predecessor) would EADDRINUSE.
    socket_path.unlink(missing_ok=True)
    srv = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    bound_id: "tuple[int, int] | None" = None
    try:
        srv.bind(str(socket_path))
        # Remember which FILE we bound, so exit cleanup can tell "our
        # socket" from a successor's that re-bound the same path.
        try:
            st = os.stat(socket_path)
            bound_id = (st.st_dev, st.st_ino)
        except OSError:
            bound_id = None
        srv.listen(32)
        log.info("relaying %s -> %s (jail=%s)", socket_path, broker_path, jail_id)

        stop = threading.Event()

        def _graceful(signo, _frame):
            log.info("signal %d — shutting down", signo)
            stop.set()
            try:
                srv.shutdown(socket.SHUT_RDWR)
            except OSError:
                # BSD/macOS: shutdown() on a LISTENING socket raises
                # ENOTCONN, and PEP 475 transparently retries the
                # interrupted accept() — the loop would never observe
                # ``stop``.  close() below is what actually breaks
                # accept() out there (EBADF); on Linux it's a no-op
                # after the successful shutdown.
                pass
            try:
                srv.close()
            except OSError:
                pass

        # Signal handlers can only be installed from the main thread;
        # they always are when we run as the spawned daemon process.
        if threading.current_thread() is threading.main_thread():
            signal.signal(signal.SIGTERM, _graceful)
            signal.signal(signal.SIGINT, _graceful)

        while not stop.is_set():
            try:
                conn, _ = srv.accept()
            except OSError:
                break
            threading.Thread(
                target=_handle,
                args=(conn, str(broker_path), jail_id),
                daemon=True,
            ).start()
    finally:
        try:
            srv.close()
        except OSError:
            pass
        # The socket file must not outlive the process: a leftover file
        # reads as "relay dead" (ECONNREFUSED) instead of "relay absent"
        # to doctor and the terminator.  But only unlink the file WE
        # bound: if this relay leaked (e.g. its /tmp pidfile was aged
        # away and a successor healed over it, re-binding the same
        # path), the path now names the successor's LIVE socket and
        # removing it would 502 the jail until the next heal.
        try:
            st = os.stat(socket_path)
            if bound_id is not None and (st.st_dev, st.st_ino) == bound_id:
                socket_path.unlink()
        except OSError:
            pass
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Per-jail relay: pipes a jail's broker socket to the "
        "host-wide singleton, dialing it per connection."
    )
    parser.add_argument(
        "--socket",
        required=True,
        type=Path,
        help="relay listen socket (inside the jail's host-services dir)",
    )
    parser.add_argument(
        "--broker",
        required=True,
        type=Path,
        help="real broker socket, dialed per connection",
    )
    parser.add_argument(
        "--jail",
        required=True,
        help="container name stamped as jail_id on each request",
    )
    args = parser.parse_args()
    logging.basicConfig(
        stream=sys.stderr,
        level=logging.INFO,
        format="%(asctime)s %(levelname)s %(name)s: %(message)s",
    )
    return serve(args.socket, args.broker, args.jail)


if __name__ == "__main__":
    sys.exit(main())
