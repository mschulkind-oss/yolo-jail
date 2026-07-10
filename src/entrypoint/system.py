"""Container-level system setup that runs before any user shell.

  * configure_timezone — populate /run/localtime + /run/timezone from $TZ
    so anything that reads ``/etc/localtime`` directly (Go's time pkg,
    some Java/Ruby paths, ``date`` after ``env -i``) agrees with the
    host wall clock.  $TZ already covers glibc, Python, Node, and bash.
  * generate_ld_cache — populate /run/ld.so.cache (the target of the
    image's /etc/ld.so.cache symlink) from the /lib + /usr/lib farm so
    tools that read the FHS cache path see real entries.
  * generate_ca_bundle — combine the image's baseline trust store and
    every loophole's CA into ~/.yolo-ca-bundle.crt and point
    SSL_CERT_FILE / REQUESTS_CA_BUNDLE / CURL_CA_BUNDLE / GIT_SSL_CAINFO
    at it so child processes inherit the right trust store without
    knowing about this file.

These look up HOME / TZ_RUN_DIR via the parent package at call time so
test fixtures that rebind ``entrypoint.HOME = tmp_path`` keep working.
"""

import os
import shutil
import subprocess
from pathlib import Path


def _read_bundle_bytes(path: Path) -> bytes:
    """Read a PEM file, returning b'' on any error.  Not finding a cert
    source is a warn, not a fatal — the combined bundle is best-effort
    and we always keep going.  The baseline bundle is usually present
    via the image env var; individual loophole CAs can be absent if the
    loophole hasn't been primed yet."""
    try:
        return path.read_bytes()
    except OSError:
        return b""


def configure_timezone():
    """Populate ``/run/localtime`` and ``/run/timezone`` from ``$TZ``.

    The image bakes ``/etc/localtime -> /run/localtime`` and
    ``/etc/timezone -> /run/timezone`` symlinks because the root
    filesystem is mounted read-only.  cli.py forwards the host zone via
    ``$TZ`` (plus ``$TZDIR`` pointing at the Nix tzdata), which covers
    glibc, Python, Node, and bash.  But anything that reads
    ``/etc/localtime`` directly — Go's ``time`` package, some Java/Ruby
    paths, ``date`` after ``env -i``, ``ls -l`` when libc can't parse
    $TZ — otherwise falls back to UTC and disagrees with the host.

    Best-effort: if $TZ is unset, or the zone file can't be found, leave
    the dangling symlinks alone.  Callers of those paths will see ENOENT
    and fall back to UTC, which matches the pre-fix behavior.
    """
    from . import TZ_RUN_DIR

    tz = os.environ.get("TZ")
    if not tz:
        return
    tzdir = os.environ.get("TZDIR") or "/usr/share/zoneinfo"
    zone_file = Path(tzdir) / tz
    if not zone_file.is_file():
        return
    run_dir = TZ_RUN_DIR
    try:
        run_dir.mkdir(parents=True, exist_ok=True)
        localtime = run_dir / "localtime"
        if localtime.is_symlink() or localtime.exists():
            localtime.unlink()
        localtime.symlink_to(zone_file)
        (run_dir / "timezone").write_text(f"{tz}\n")
    except OSError:
        # /run is a tmpfs on every runtime we support, so write failures
        # here are unexpected — but a broken TZ symlink shouldn't abort
        # jail startup.  The $TZ env var still gives the right answer
        # for everything that reads it.
        pass


def generate_ld_cache():
    """Populate ``/run/ld.so.cache`` from the /lib + /usr/lib farm.

    The image bakes ``/etc/ld.so.cache -> /run/ld.so.cache`` (root fs is
    read-only, same pattern as /etc/localtime) and ships /etc/ld.so.conf
    listing the farm directories.  Generation has to happen here rather
    than at image build time: the farm derivation builds natively on
    darwin for macOS hosts, where the Linux ldconfig binary cannot run —
    a build-time cache was silently empty for every macOS-built image.

    The cache only serves tools that read the FHS path directly
    (``ldconfig -p``, non-nix glibc binaries built for the standard cache
    path); the nix loader ignores it and uses LD_LIBRARY_PATH, so failure
    here is a diagnostics gap, not a startup error.
    """
    ldconfig = shutil.which("ldconfig")
    if not ldconfig:
        return
    try:
        subprocess.run(
            [ldconfig, "-C", "/run/ld.so.cache", "-f", "/etc/ld.so.conf"],
            capture_output=True,
            timeout=30,
        )
    except (OSError, subprocess.TimeoutExpired):
        pass


def generate_ca_bundle() -> Path:
    """Build ``$HOME/.yolo-ca-bundle.crt`` from the image baseline +
    every loophole CA, and point the standard env vars at it.

    Order of contents:
      1. the image's baseline bundle (``$SSL_CERT_FILE``; set at image
         build time to the Nix cacert Mozilla bundle), if readable.
      2. each path in ``$NODE_EXTRA_CA_CERTS`` — that's the colon-
         separated list cli.py assembles from every active loophole's
         ``ca_cert`` field.

    The resulting bundle is exported via ``os.environ`` so any child
    process spawned from the entrypoint (jail-daemon supervisor, bash,
    etc.) sees the combined trust store under the usual var names.
    The bashrc re-exports the same vars for interactive shells.
    """
    from . import HOME

    # Refresh CA_BUNDLE_PATH in case HOME was monkeypatched (tests).
    bundle_path = HOME / ".yolo-ca-bundle.crt"

    chunks: list[bytes] = []
    baseline = os.environ.get("SSL_CERT_FILE", "")
    if baseline and baseline != str(bundle_path):
        data = _read_bundle_bytes(Path(baseline))
        if data:
            chunks.append(data)

    extras = os.environ.get("NODE_EXTRA_CA_CERTS", "")
    if extras:
        seen: set[str] = set()
        for raw in extras.split(os.pathsep):
            p = raw.strip()
            if not p or p in seen:
                continue
            seen.add(p)
            data = _read_bundle_bytes(Path(p))
            if data:
                chunks.append(data)

    # Always write a file, even if empty — env vars pointing at a
    # nonexistent path confuse some tools (curl prints a warning on
    # every request).  An empty bundle is harmless: baseline-only
    # verification still works via the image default if set.
    body = b"\n".join(c.rstrip(b"\n") for c in chunks)
    if body and not body.endswith(b"\n"):
        body += b"\n"
    bundle_path.write_bytes(body)
    os.chmod(bundle_path, 0o644)

    # Point the standard vars at the combined bundle so children inherit
    # the right trust store without having to know about this file.
    bundle_str = str(bundle_path)
    os.environ["SSL_CERT_FILE"] = bundle_str
    os.environ["REQUESTS_CA_BUNDLE"] = bundle_str
    os.environ["CURL_CA_BUNDLE"] = bundle_str
    os.environ["GIT_SSL_CAINFO"] = bundle_str
    return bundle_path
