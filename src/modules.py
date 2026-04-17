"""yolo-jail host-side module system.

A module is a self-contained unit of host-side functionality that plugs into
the jail's networking and trust store. Examples:

- ``claude-oauth-broker`` — MITM proxy that serializes Claude OAuth refreshes.
- ``llm-audit`` (hypothetical) — logs every inference request from a jail.

A module lives under ``~/.local/share/yolo-jail/modules/<name>/`` with at
minimum a ``manifest.jsonc``.  The loader discovers every installed module at
jail startup and applies its declared integrations — CA cert mount, DNS
overrides, extra jail env — to the docker run command.  Modules own their own
daemon lifecycle (systemd/launchd) and are expected to install themselves via
their own setup scripts.

Manifest schema (v1)
--------------------

.. code-block:: jsonc

    {
      "name": "claude-oauth-broker",            // required, must match dir name
      "description": "…",                        // required
      "version": 1,                              // manifest format version
      "enabled": true,                           // default true; false = skip at load
      "intercepts": [                            // optional
        {"host": "platform.claude.com"}          // DNS-overridden to broker_ip
      ],
      "broker_ip": "host-gateway",               // podman/docker magic value; see below
      "ca_cert": "ca.crt",                       // relative path; auto-mounted + trusted
      "jail_env": {"FOO": "bar"},                // extra env vars for the jail
      "doctor_cmd": ["bin-name", "--self-check"] // optional health check
    }

The loader is stateless — every jail boot re-reads manifests.  Disabling a
module is a one-line edit (``"enabled": false``) and takes effect on the next
``yolo run``.
"""

from __future__ import annotations

import os
import subprocess
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Dict, List, Optional

import pyjson5


# Both podman and docker translate the literal "host-gateway" into the
# right host-reachable-from-container address for the active runtime
# (pasta tunnel, CNI bridge, Docker Desktop VM gateway, …). Hardcoding a
# specific IP like 169.254.1.2 only works on one runtime/config combination.
DEFAULT_BROKER_IP = "host-gateway"


def modules_dir() -> Path:
    """Return the host-side modules directory, honoring the same root as the
    rest of yolo-jail's persistent storage."""
    return Path.home() / ".local" / "share" / "yolo-jail" / "modules"


@dataclass
class Intercept:
    host: str


@dataclass
class Module:
    """A loaded, validated module manifest."""

    name: str
    description: str
    path: Path
    enabled: bool = True
    intercepts: List[Intercept] = field(default_factory=list)
    broker_ip: str = DEFAULT_BROKER_IP
    ca_cert: Optional[Path] = None
    jail_env: Dict[str, str] = field(default_factory=dict)
    doctor_cmd: Optional[List[str]] = None

    @property
    def has_ca(self) -> bool:
        return self.ca_cert is not None and self.ca_cert.is_file()


class ModuleError(ValueError):
    """Raised when a manifest is malformed."""


def _load_manifest(module_path: Path) -> Module:
    manifest_path = module_path / "manifest.jsonc"
    if not manifest_path.is_file():
        raise ModuleError(f"{manifest_path} not found")

    try:
        data: Dict[str, Any] = pyjson5.loads(manifest_path.read_text())
    except (OSError, ValueError, pyjson5.Json5Exception) as e:
        raise ModuleError(f"{manifest_path}: {e}") from e

    name = data.get("name")
    if not isinstance(name, str) or not name:
        raise ModuleError(f"{manifest_path}: 'name' is required")
    if name != module_path.name:
        raise ModuleError(
            f"{manifest_path}: name='{name}' disagrees with directory "
            f"'{module_path.name}' — they must match"
        )

    description = data.get("description", "")
    if not isinstance(description, str):
        raise ModuleError(f"{manifest_path}: 'description' must be a string")

    intercepts_raw = data.get("intercepts") or []
    if not isinstance(intercepts_raw, list):
        raise ModuleError(f"{manifest_path}: 'intercepts' must be a list")
    intercepts: List[Intercept] = []
    for entry in intercepts_raw:
        if not isinstance(entry, dict) or not isinstance(entry.get("host"), str):
            raise ModuleError(f"{manifest_path}: each intercept needs a string 'host'")
        intercepts.append(Intercept(host=entry["host"]))

    ca_cert: Optional[Path] = None
    ca_cert_rel = data.get("ca_cert")
    if isinstance(ca_cert_rel, str) and ca_cert_rel:
        ca_cert = (module_path / ca_cert_rel).resolve()

    jail_env_raw = data.get("jail_env") or {}
    if not isinstance(jail_env_raw, dict):
        raise ModuleError(f"{manifest_path}: 'jail_env' must be a mapping")
    jail_env = {str(k): str(v) for k, v in jail_env_raw.items()}

    doctor_cmd_raw = data.get("doctor_cmd")
    doctor_cmd: Optional[List[str]] = None
    if doctor_cmd_raw is not None:
        if not isinstance(doctor_cmd_raw, list) or not all(
            isinstance(x, str) for x in doctor_cmd_raw
        ):
            raise ModuleError(
                f"{manifest_path}: 'doctor_cmd' must be a list of strings"
            )
        doctor_cmd = list(doctor_cmd_raw)

    return Module(
        name=name,
        description=description,
        path=module_path,
        enabled=bool(data.get("enabled", True)),
        intercepts=intercepts,
        broker_ip=str(data.get("broker_ip") or DEFAULT_BROKER_IP),
        ca_cert=ca_cert,
        jail_env=jail_env,
        doctor_cmd=doctor_cmd,
    )


def discover_modules(
    root: Optional[Path] = None,
    *,
    include_disabled: bool = False,
) -> List[Module]:
    """Scan the modules directory and return every validated manifest.

    Invalid manifests are skipped silently — a broken third-party module
    should not prevent ``yolo run`` from starting.  Use ``validate_modules``
    (below) for operator-facing diagnostics.
    """
    root = root or modules_dir()
    if not root.is_dir():
        return []
    out: List[Module] = []
    for child in sorted(root.iterdir()):
        if not child.is_dir():
            continue
        if child.name.startswith("."):
            continue
        try:
            module = _load_manifest(child)
        except ModuleError:
            continue
        if not include_disabled and not module.enabled:
            continue
        out.append(module)
    return out


def validate_modules(
    root: Optional[Path] = None,
) -> List["tuple[Path, Optional[Module], Optional[str]]"]:
    """Return one entry per module directory: (path, module_or_None, error_or_None)."""
    root = root or modules_dir()
    if not root.is_dir():
        return []
    out: List["tuple[Path, Optional[Module], Optional[str]]"] = []
    for child in sorted(root.iterdir()):
        if not child.is_dir() or child.name.startswith("."):
            continue
        try:
            out.append((child, _load_manifest(child), None))
        except ModuleError as e:
            out.append((child, None, str(e)))
    return out


# ---------------------------------------------------------------------------
# docker run integration
# ---------------------------------------------------------------------------


def docker_args_for(modules: List[Module]) -> List[str]:
    """Translate modules into docker run flags.

    Returns a flat list suitable for ``docker_cmd.extend(…)`` at the caller.
    Idempotent and side-effect free — does not check for the existence of
    the daemons the modules describe; that's the operator's job, surfaced
    via ``yolo doctor``.
    """
    args: List[str] = []
    trusted_ca_paths: List[str] = []
    for m in modules:
        for intercept in m.intercepts:
            args.extend(["--add-host", f"{intercept.host}:{m.broker_ip}"])
        if m.has_ca:
            container_path = f"/etc/yolo-jail/modules/{m.name}/ca.crt"
            args.extend(["-v", f"{m.ca_cert}:{container_path}:ro"])
            trusted_ca_paths.append(container_path)
        for k, v in m.jail_env.items():
            args.extend(["-e", f"{k}={v}"])
    if trusted_ca_paths:
        args.extend(["-e", f"NODE_EXTRA_CA_CERTS={os.pathsep.join(trusted_ca_paths)}"])
    return args


# ---------------------------------------------------------------------------
# doctor integration
# ---------------------------------------------------------------------------


@dataclass
class DoctorResult:
    module: Module
    returncode: Optional[int]  # None if doctor_cmd absent or could not run
    output: str


def run_doctor_checks(
    modules: List[Module], *, timeout: float = 10.0
) -> List[DoctorResult]:
    """Execute each module's ``doctor_cmd`` and collect results."""
    results: List[DoctorResult] = []
    for m in modules:
        if not m.doctor_cmd:
            results.append(DoctorResult(module=m, returncode=None, output=""))
            continue
        try:
            proc = subprocess.run(
                m.doctor_cmd,
                capture_output=True,
                text=True,
                timeout=timeout,
            )
            output = (proc.stdout or proc.stderr).strip()
            results.append(
                DoctorResult(module=m, returncode=proc.returncode, output=output)
            )
        except (subprocess.TimeoutExpired, FileNotFoundError, OSError) as e:
            results.append(DoctorResult(module=m, returncode=None, output=str(e)))
    return results


# ---------------------------------------------------------------------------
# enable / disable
# ---------------------------------------------------------------------------


def set_enabled(module_path: Path, enabled: bool) -> None:
    """Toggle ``enabled`` in a module's manifest without disturbing other keys.

    Preserves comments when pyjson5 can round-trip them; when it can't (comments
    strip on re-serialization), emits a plain JSON manifest with a header
    comment explaining.
    """
    manifest_path = module_path / "manifest.jsonc"
    text = manifest_path.read_text()
    data = pyjson5.loads(text)
    data["enabled"] = bool(enabled)
    # pyjson5 has no dumper; fall back to json.
    import json as _json

    header = (
        "// yolo-jail module manifest. See src/modules.py for schema.\n"
        "// 'enabled' toggled via `yolo modules {enable,disable}`.\n"
    )
    manifest_path.write_text(header + _json.dumps(data, indent=2) + "\n")
