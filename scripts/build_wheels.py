#!/usr/bin/env python3
"""Build per-platform PyPI wheels that wrap the Go host binaries.

Derived from Simon Willison's go-to-wheel (Apache-2.0) — same wheel layout
and platform tags — but yolo-jail can't use it directly: go-to-wheel runs
``go build .`` in the module root (yolo-jail's mains live under ``cmd/``) and
emits exactly one binary + one console script per wheel, while the yolo-jail
package contract is FOUR console scripts (the Python era's
``[project.scripts]``: yolo, yolo-claude-oauth-broker-host, yolo-ps,
yolo-host-processes — the broker name doubles as the loophole manifest/doctor
contract, so it must stay a real command on PATH).

Windows is deliberately absent: the Go tree uses unix-only syscalls and the
tool has no Windows story.

Usage (CI: .github/workflows/publish.yml; also runnable locally):

    python scripts/build_wheels.py --version 0.7.0 [--output-dir dist]
        [--platforms linux-amd64,darwin-arm64]
"""

from __future__ import annotations

import argparse
import base64
import csv
import hashlib
import io
import os
import stat
import subprocess
import sys
import tempfile
import zipfile
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent

PACKAGE_NAME = "yolo-jail"
IMPORT_NAME = "yolo_jail"

# Console-script name -> (cmd/ package, wrapper function name). Order matters:
# the first entry is the primary binary and backs both `main` and __main__.py.
BINARIES: dict[str, str] = {
    "yolo": "main",
    "yolo-claude-oauth-broker-host": "claude_oauth_broker_host",
    "yolo-host-processes": "host_processes",
    "yolo-ps": "ps",
}

# platform key -> (GOOS, GOARCH, wheel platform tag). Same keys/tags as
# go-to-wheel. glibc and musl share one CGO_ENABLED=0 static binary; the two
# tags exist so pip resolves on both libc families.
PLATFORMS: dict[str, tuple[str, str, str]] = {
    "linux-amd64": ("linux", "amd64", "manylinux_2_17_x86_64"),
    "linux-arm64": ("linux", "arm64", "manylinux_2_17_aarch64"),
    "linux-amd64-musl": ("linux", "amd64", "musllinux_1_2_x86_64"),
    "linux-arm64-musl": ("linux", "arm64", "musllinux_1_2_aarch64"),
    "darwin-amd64": ("darwin", "amd64", "macosx_10_9_x86_64"),
    "darwin-arm64": ("darwin", "arm64", "macosx_11_0_arm64"),
}

VERSION_PKG = "github.com/mschulkind-oss/yolo-jail/internal/version"

METADATA_FIELDS = {
    "Summary": (
        "Secure container jail for AI coding agents — run Claude Code, "
        "Copilot, Gemini, opencode, or pi in YOLO mode safely"
    ),
    "Author": "Matt Schulkind",
    "License": "Apache-2.0",
    "Home-page": "https://github.com/mschulkind-oss/yolo-jail",
}


def compile_binaries(
    version: str, goos: str, goarch: str, go_binary: str, out_dir: Path
) -> dict[str, bytes]:
    """Cross-compile every host binary for one platform; return name->bytes."""
    commit = subprocess.run(
        ["git", "rev-parse", "--short", "HEAD"],
        cwd=REPO_ROOT,
        capture_output=True,
        text=True,
    ).stdout.strip()
    ldflags = f"-s -w -X {VERSION_PKG}.buildVersion={version}" + (
        f" -X {VERSION_PKG}.GitCommit={commit}" if commit else ""
    )

    env = os.environ.copy()
    env.update({"GOOS": goos, "GOARCH": goarch, "CGO_ENABLED": "0"})

    out: dict[str, bytes] = {}
    for name in BINARIES:
        target = out_dir / f"{name}_{goos}_{goarch}"
        result = subprocess.run(
            [
                go_binary,
                "build",
                f"-ldflags={ldflags}",
                "-o",
                str(target),
                f"./cmd/{name}",
            ],
            cwd=REPO_ROOT,
            env=env,
            capture_output=True,
            text=True,
        )
        if result.returncode != 0:
            raise RuntimeError(
                f"go build failed for {name} ({goos}/{goarch}):\n{result.stderr}"
            )
        out[name] = target.read_bytes()
    return out


def generate_init_py(version: str) -> str:
    wrappers = "\n\n".join(
        f'def {func}():\n    _run("{script}")' for script, func in BINARIES.items()
    )
    return f'''"""yolo-jail Go binaries packaged as a Python wheel."""

import os
import stat
import sys

__version__ = "{version}"


def _run(name):
    binary = os.path.join(os.path.dirname(__file__), "bin", name)
    # Some installers drop the exec bit; restore it before exec.
    mode = os.stat(binary).st_mode
    if not (mode & stat.S_IXUSR):
        os.chmod(binary, mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)
    os.execvp(binary, [binary] + sys.argv[1:])


{wrappers}
'''


def generate_entry_points() -> str:
    lines = "\n".join(
        f"{script} = {IMPORT_NAME}:{func}" for script, func in BINARIES.items()
    )
    return f"[console_scripts]\n{lines}\n"


def generate_metadata(version: str, readme: str) -> str:
    lines = [
        "Metadata-Version: 2.1",
        f"Name: {PACKAGE_NAME}",
        f"Version: {version}",
    ]
    lines += [f"{k}: {v}" for k, v in METADATA_FIELDS.items()]
    lines += [
        "Requires-Python: >=3.10",
        "Description-Content-Type: text/markdown",
        "",
        readme,
    ]
    return "\n".join(lines) + "\n"


def generate_wheel_metadata(platform_tag: str) -> str:
    return (
        "Wheel-Version: 1.0\n"
        "Generator: yolo-jail build_wheels (go-to-wheel-derived)\n"
        "Root-Is-Purelib: false\n"
        f"Tag: py3-none-{platform_tag}\n"
    )


def record_hash(data: bytes) -> str:
    digest = hashlib.sha256(data).digest()
    return "sha256=" + base64.urlsafe_b64encode(digest).rstrip(b"=").decode("ascii")


def generate_record(files: dict[str, bytes]) -> str:
    out = io.StringIO()
    writer = csv.writer(out)
    for path, content in files.items():
        if path.endswith("RECORD"):
            writer.writerow([path, "", ""])
        else:
            writer.writerow([path, record_hash(content), len(content)])
    return out.getvalue()


def build_wheel(
    binaries: dict[str, bytes], version: str, platform_tag: str, output_dir: Path
) -> Path:
    readme = (REPO_ROOT / "README.md").read_text(encoding="utf-8")

    files: dict[str, bytes] = {}
    files[f"{IMPORT_NAME}/__init__.py"] = generate_init_py(version).encode()
    files[f"{IMPORT_NAME}/__main__.py"] = b"from . import main\nmain()\n"
    for name, content in binaries.items():
        files[f"{IMPORT_NAME}/bin/{name}"] = content

    dist_info = f"{IMPORT_NAME}-{version}.dist-info"
    files[f"{dist_info}/METADATA"] = generate_metadata(version, readme).encode()
    files[f"{dist_info}/WHEEL"] = generate_wheel_metadata(platform_tag).encode()
    files[f"{dist_info}/entry_points.txt"] = generate_entry_points().encode()
    # Apache-2.0 §4 redistribution obligations: ship the license + NOTICE text
    # inside the wheel (same dist-info/licenses/ layout `uv build` produced
    # for the Python-era wheels).
    for name in ("LICENSE", "NOTICE"):
        path = REPO_ROOT / name
        if path.exists():
            files[f"{dist_info}/licenses/{name}"] = path.read_bytes()
    record_path = f"{dist_info}/RECORD"
    files[record_path] = b""
    files[record_path] = generate_record(files).encode()

    output_dir.mkdir(parents=True, exist_ok=True)
    wheel_path = output_dir / f"{IMPORT_NAME}-{version}-py3-none-{platform_tag}.whl"
    # S_IFREG matters: without the file-type bits some installers treat the
    # external_attr as garbage and extract the binaries non-executable (the
    # _run() wrapper self-heals with chmod, but installs should be right).
    exec_mode = (
        stat.S_IFREG
        | stat.S_IRWXU
        | stat.S_IRGRP
        | stat.S_IXGRP
        | stat.S_IROTH
        | stat.S_IXOTH
    )
    with zipfile.ZipFile(wheel_path, "w", zipfile.ZIP_DEFLATED) as whl:
        for path, content in files.items():
            if "/bin/" in path:
                info = zipfile.ZipInfo(path)
                info.external_attr = exec_mode << 16
                whl.writestr(info, content)
            else:
                whl.writestr(path, content)
    return wheel_path


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--version", required=True, help="wheel version, no leading v")
    parser.add_argument("--output-dir", default="dist", type=Path)
    parser.add_argument(
        "--platforms",
        default=",".join(PLATFORMS),
        help=f"comma-separated subset of: {', '.join(PLATFORMS)}",
    )
    parser.add_argument("--go-binary", default="go")
    args = parser.parse_args()

    requested = [p for p in args.platforms.split(",") if p]
    unknown = [p for p in requested if p not in PLATFORMS]
    if unknown:
        parser.error(f"unknown platforms: {', '.join(unknown)}")

    # glibc/musl pairs share a build — compile once per (goos, goarch).
    compiled: dict[tuple[str, str], dict[str, bytes]] = {}
    with tempfile.TemporaryDirectory() as tmp:
        for key in requested:
            goos, goarch, platform_tag = PLATFORMS[key]
            if (goos, goarch) not in compiled:
                compiled[(goos, goarch)] = compile_binaries(
                    args.version, goos, goarch, args.go_binary, Path(tmp)
                )
            wheel = build_wheel(
                compiled[(goos, goarch)], args.version, platform_tag, args.output_dir
            )
            print(f"built {wheel}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
