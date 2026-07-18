#!/usr/bin/env python3
"""Stage 9 entrypoint tree oracle (go-port plan §9).

Runs the LIVE src/entrypoint PURE content-generators into a fake ``$HOME`` under
a fixed, committed ``YOLO_*`` env matrix and emits, as one canonical JSON
document on stdout::

    {"<scenario>": {"files": {"<relpath>": "<sha256>", ...},
                    "symlinks": {"<relpath>": "<raw-target>", ...},
                    "modes": {"<relpath>": "0o755", ...}}}

The Go test (internal/entrypoint/entrypoint_parity_test.go) runs the Go
generators into an IDENTICAL fake HOME with the SAME env and byte-diffs the
trees (relpath set + per-file sha256 + symlink targets + exec modes).

Scope: the PURE generation functions (Stage 9/10 generators lib) — shims,
.bashrc, bootstrap/venv-precreate/cglimit/journalctl/yolo-ps/yolo-wrapper
scripts, MCP wrappers, mise config.toml, the six agents' config files +
managed-MCP sidecars, and the CA bundle. NOT boot orchestration (main()),
which does subprocess/network work.

DYNAMIC-OUTPUT NORMALIZATION (committed exclusion list, plan §9 requires this be
deliberate, not ad-hoc). None of the pure generators here emit dynamic content:
they take env + input files only. The dynamic surfaces that the FULL boot would
touch — and that this oracle deliberately DOES NOT invoke — are:

  * ~/.yolo-perf.log            — wall-clock timings (_perf_dump); boot only.
  * ~/.cache/yolo-agent-stamps/ — launcher stamp files; written at agent RUN
                                   time by the generated launcher, not at gen.
  * /workspace/.yolo/startup.log — provisioning timestamps; boot only.
  * ~/.bash_history / history   — session history; never generated here.
  * ~/.claude/history.jsonl     — a SYMLINK is created (target is a per-jail
                                   hash of YOLO_HOST_DIR — deterministic given
                                   the matrix, so it IS included as a symlink,
                                   not excluded); the target FILE's contents are
                                   session data and are never asserted.

Every generated file this oracle emits is a pure function of (env matrix, seed
input files). The HOME prefix is stripped from all paths and normalized inside
file bodies so the two languages agree regardless of the temp dir chosen.

Skips gracefully is N/A here (this IS the Python side); the Go test skips when
python3/uv is unavailable.
"""

from __future__ import annotations

import hashlib
import json
import os
import shutil
import stat
import sys
import tempfile
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent.parent
sys.path.insert(0, str(REPO_ROOT / "src"))

# The committed env matrix. Each scenario is a name -> {env, files, agents}.
# ``files`` seeds pre-existing files under HOME (relpath -> contents) to exercise
# merge/reconcile paths. ``home_token`` is the literal string the fake HOME is
# replaced with inside every emitted file body so absolute paths agree across
# languages (both sides use the SAME token).
from entrypoint_matrix import SCENARIOS  # noqa: E402


def _rebind(entrypoint, home: Path) -> None:
    """Point every entrypoint path constant at ``home`` (like the test fixture)."""
    ep = entrypoint
    ep.HOME = home
    ep.SHIM_DIR = home / ".yolo-shims"
    ep.NPM_PREFIX = home / ".npm-global"
    ep.NPM_BIN = home / ".npm-global" / "bin"
    ep.GOPATH = home / "go"
    ep.GO_BIN = home / "go" / "bin"
    ep.MISE_SHIMS = home / ".local" / "share" / "mise" / "shims"
    ep.MCP_WRAPPERS_BIN = home / ".local" / "bin" / "mcp-wrappers"
    ep.BASHRC_PATH = home / ".bashrc"
    ep.COPILOT_DIR = home / ".copilot"
    ep.GEMINI_DIR = home / ".gemini"
    ep.GEMINI_MANAGED_MCP_PATH = home / ".gemini" / "yolo-managed-mcp-servers.json"
    ep.CLAUDE_DIR = home / ".claude"
    ep.CLAUDE_MANAGED_MCP_PATH = home / ".claude" / "yolo-managed-mcp-servers.json"
    ep.CLAUDE_HOST_SETTINGS_SNAPSHOT_PATH = (
        home / ".claude" / "yolo-host-synced-settings.json"
    )
    ep.CLAUDE_SHARED_CREDENTIALS_DIR = home / ".claude-shared-credentials"
    ep.OPENCODE_DIR = home / ".config" / "opencode"
    ep.PI_DIR = home / ".pi" / "agent"
    ep.CODEX_DIR = home / ".codex"
    ep.MISE_CONFIG_DIR = home / ".config" / "mise"


def _run_scenario(name: str, spec: dict) -> dict:
    home = Path(tempfile.mkdtemp(prefix=f"yolo-oracle-{name}-"))
    home_token = spec.get("home_token", "@HOME@")

    # HERMETICITY: the Go generators read ONLY their explicit env matrix (an
    # *Env map), so they never see ambient host vars. The Python generators read
    # os.environ directly, so the oracle MUST present the identical minimal
    # environment — otherwise an ambient var the matrix happens to reference
    # (e.g. a real TAVILY_API_KEY in the dev jail's env) silently flips a
    # requires_env gate on the Python side only. Clear everything except a
    # system safelist that the interpreter/uv need, then apply the matrix.
    _SAFELIST = {
        "PATH",
        "HOME",
        "USER",
        "LOGNAME",
        "SHELL",
        "TERM",
        "LANG",
        "LC_ALL",
        "TMPDIR",
        "PWD",
        "PYTHONPATH",
        "PYTHONHASHSEED",
        "VIRTUAL_ENV",
        "UV_CACHE_DIR",
        "XDG_CACHE_HOME",
        "XDG_DATA_HOME",
        "XDG_CONFIG_HOME",
    }
    for var in list(os.environ):
        if var not in _SAFELIST:
            del os.environ[var]
    os.environ["JAIL_HOME"] = str(home)
    for k, v in spec.get("env", {}).items():
        # Substitute @HOME@ in env values (e.g. SSL_CERT_FILE seed paths).
        os.environ[k] = v.replace(home_token, str(home))

    # Seed pre-existing files.
    for rel, contents in spec.get("files", {}).items():
        p = home / rel
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(contents.replace(home_token, str(home)))

    # Fresh import so rebound module paths / env are picked up cleanly.
    for mod in list(sys.modules):
        if mod == "entrypoint" or mod.startswith("entrypoint."):
            del sys.modules[mod]
    import entrypoint  # noqa: E402
    import entrypoint.agent_configs as agent_configs  # noqa: E402

    _rebind(entrypoint, home)
    # Stub the plugin-installer subprocess (side effect, not content).
    agent_configs._install_claude_plugins = lambda *a, **k: None

    # Host claude settings come from a seeded /ctx file? We can't write /ctx
    # (read-only), so a scenario needing host settings provides them via a
    # monkeypatch hook value in spec["host_claude_settings"].
    if "host_claude_settings" in spec:
        hs = spec["host_claude_settings"]
        agent_configs._load_host_claude_settings = lambda: json.loads(json.dumps(hs))

    # Run the pure generators in boot order (minus orchestration).
    entrypoint.generate_shims()
    entrypoint.generate_agent_launchers()
    entrypoint.generate_package_manager_launchers()
    entrypoint.generate_ca_bundle()
    entrypoint.generate_bashrc()
    entrypoint.generate_bootstrap_script()
    entrypoint.generate_venv_precreate_script()
    entrypoint.generate_mise_config()
    entrypoint.generate_mcp_wrappers()
    for agent in entrypoint._load_agents():
        writer = getattr(entrypoint, {
            "claude": "configure_claude",
            "copilot": "configure_copilot",
            "gemini": "configure_gemini",
            "opencode": "configure_opencode",
            "pi": "configure_pi",
            "codex": "configure_codex",
        }[agent])
        writer()
    entrypoint.generate_cglimit_script()
    entrypoint.generate_journalctl_script()
    entrypoint.generate_yolo_ps_script()
    entrypoint.generate_yolo_wrapper()

    result = _walk_tree(home, str(home), home_token)
    shutil.rmtree(home, ignore_errors=True)
    return result


def _walk_tree(home: Path, home_str: str, home_token: str) -> dict:
    files: dict[str, str] = {}
    symlinks: dict[str, str] = {}
    modes: dict[str, str] = {}
    for dirpath, dirnames, filenames in os.walk(home):
        dirnames.sort()
        for fn in sorted(filenames):
            full = Path(dirpath) / fn
            rel = str(full.relative_to(home))
            if full.is_symlink():
                target = os.readlink(full)
                symlinks[rel] = target.replace(home_str, home_token)
                continue
            data = full.read_bytes()
            # Normalize the absolute HOME prefix inside file bodies so the two
            # languages agree regardless of the temp dir chosen.
            data = data.replace(home_str.encode(), home_token.encode())
            files[rel] = hashlib.sha256(data).hexdigest()
            if os.access(full, os.X_OK):
                modes[rel] = oct(stat.S_IMODE(full.stat().st_mode))
    # Also capture symlinks that point OUTSIDE home (relative targets).
    return {"files": files, "symlinks": symlinks, "modes": modes}


def main() -> int:
    out = {name: _run_scenario(name, spec) for name, spec in SCENARIOS.items()}
    sys.stdout.write(json.dumps(out, indent=2, sort_keys=True) + "\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
