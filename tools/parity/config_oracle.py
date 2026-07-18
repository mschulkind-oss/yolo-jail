#!/usr/bin/env python3
"""Config engine oracle for internal/config parity (go-port plan §13).

Drives the LIVE src/cli/config.py over a corpus of operations read as a JSON
array from stdin, and emits — for each — the byte-exact output the Go port must
reproduce. The Go test (internal/config/config_parity_test.go) runs the same
operations through internal/config and byte-compares:

  - merged config (order-preserving `compact` form + the byte-critical
    `snapshot` form),
  - the config-snapshot bytes,
  - the full ordered error/warning lists for every validation branch,
  - the derived helpers (normalize_blocked_tools, effective_packages,
    effective_mcp_server_names, selected_agents, merge_mise_tools,
    merge_mise_disabled_tools, parse_dotenv).

Each case is a dict with an "op" and op-specific args. Results are emitted as one
canonical JSON document (indent=2, sort_keys, ensure_ascii) so the Go test can
decode it with its own order-preserving decoder.

Loophole validation is made hermetic: a case may carry "known_loopholes"
mapping name -> {"has_host_daemon": bool}; the oracle monkeypatches
_known_loopholes to a fixed set built from it, and the Go test builds a matching
resolver. Without the field, no loopholes are known (empty set) — matching a Go
nil resolver.
"""

from __future__ import annotations

import json
import sys
import types
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent.parent
sys.path.insert(0, str(REPO_ROOT))
sys.path.insert(0, str(REPO_ROOT / "src"))

import cli.config as cfg  # noqa: E402


def _compact(value) -> str:
    # json.dumps default separators (", "/": "), insertion order preserved,
    # ensure_ascii — matches jsonx.DumpsCompact.
    return json.dumps(value)


def _snapshot(value) -> str:
    # json.dumps(indent=2, sort_keys, ensure_ascii) — matches jsonx.DumpsSnapshot.
    return json.dumps(value, indent=2, sort_keys=True, ensure_ascii=True)


def _fake_known(spec: dict) -> dict:
    """Build a name->fake-Loophole map from {name: {has_host_daemon: bool}}."""
    out = {}
    for name, info in (spec or {}).items():
        has_daemon = bool(info.get("has_host_daemon"))
        daemon = object() if has_daemon else None
        out[name] = types.SimpleNamespace(name=name, host_daemon=daemon)
    return out


def _run_validate(case) -> dict:
    config = case["config"]
    known = case.get("known_loopholes")
    if known is not None:
        cfg._known_loopholes = lambda spec=_fake_known(known): spec
    else:
        cfg._known_loopholes = lambda: {}
    # workspace defaults to cwd; mount/device existence checks depend on it.
    # Use a fixed, guaranteed-absent workspace so those warnings are stable
    # unless the case supplies its own.
    ws = case.get("workspace")
    workspace = Path(ws) if ws else Path("/nonexistent-parity-workspace")
    errors, warnings = cfg._validate_config(config, workspace=workspace)
    return {"errors": errors, "warnings": warnings}


def _run(case) -> dict:
    op = case["op"]
    if op == "merge":
        merged = cfg.merge_config(case["base"], case["override"])
        return {"compact": _compact(merged), "snapshot": _snapshot(merged)}
    if op == "snapshot":
        return {"snapshot": _snapshot(case["config"])}
    if op == "validate":
        return _run_validate(case)
    if op == "normalize_blocked_tools":
        result = cfg._normalize_blocked_tools(case.get("security"))
        return {"compact": _compact(result)}
    if op == "effective_packages":
        result = cfg._effective_packages(case["config"])
        return {"compact": _compact(result)}
    if op == "effective_mcp_server_names":
        result = cfg._effective_mcp_server_names(
            case.get("mcp_servers"), case.get("mcp_presets")
        )
        return {"compact": _compact(result)}
    if op == "selected_agents":
        result = cfg.selected_agents(case["config"])
        return {"compact": _compact(result)}
    if op == "merge_mise_tools":
        result = cfg._merge_mise_tools(case["config"])
        return {"compact": _compact(result)}
    if op == "merge_mise_disabled_tools":
        result = cfg._merge_mise_disabled_tools(case.get("value", ""))
        return {"result": result}
    if op == "parse_dotenv":
        result = cfg._parse_dotenv(case["text"])
        return {"compact": _compact(result)}
    if op == "write_snapshot":
        # Reproduce _check_config_changes's first-run write: current + "\n".
        current = json.dumps(case["config"], indent=2, sort_keys=True)
        p = Path(case["path"])
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(current + "\n")
        return {"ok": True}
    if op == "check_unchanged":
        # Reproduce _check_config_changes's unchanged branch condition exactly:
        # old_json = read_text().rstrip(); old_json == current.
        current = json.dumps(case["config"], indent=2, sort_keys=True)
        old = Path(case["path"]).read_text().rstrip()
        return {"unchanged": old == current}
    raise ValueError(f"unknown op: {op}")


def main() -> int:
    cases = json.loads(sys.stdin.read())
    out = [_run(c) for c in cases]
    sys.stdout.write(_snapshot(out))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
