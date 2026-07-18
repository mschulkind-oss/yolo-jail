#!/usr/bin/env python3
"""Generate tools/parity/corpus/config_cases.json — the internal/config
differential corpus (go-port plan §13).

Each case is a dict with an "op" consumed by tools/parity/config_oracle.py and
by internal/config/config_parity_test.go. The corpus exercises:

  - merge_config: list union-merge + dedup, agents replace-not-union, dict
    recursive merge, scalar/type-mismatch override, mcp_servers null-disable,
    env_sources concat.
  - snapshot bytes (the byte-critical gate).
  - validate: every rejection branch (errors + warnings, in order), incl.
    loophole overrides (with a hermetic known_loopholes spec).
  - the derived helpers.

Run: python tools/parity/corpus/gen_config_cases.py (regenerate only when the
corpus intentionally changes — it's committed and diffed).
"""

from __future__ import annotations

import json
from pathlib import Path


def merge(base, override):
    return {"op": "merge", "base": base, "override": override}


def snap(config):
    return {"op": "snapshot", "config": config}


def validate(config, known=None, workspace=None):
    c = {"op": "validate", "config": config}
    if known is not None:
        c["known_loopholes"] = known
    if workspace is not None:
        c["workspace"] = workspace
    return c


CASES: list = [
    # ---- merge_config ----
    merge(
        {
            "packages": ["sqlite", "postgresql"],
            "mounts": ["~/code/shared:/ctx/shared"],
            "network": {"mode": "bridge"},
            "security": {"blocked_tools": ["wget", {"name": "grep"}]},
        },
        {
            "packages": ["postgresql", "redis"],
            "mounts": ["~/code/extra:/ctx/extra"],
            "network": {"mode": "host"},
            "security": {"blocked_tools": [{"name": "grep"}, "curl"]},
        },
    ),
    merge({"agents": ["claude", "gemini"]}, {"agents": ["claude"]}),
    merge({"agents": ["opencode", "pi"]}, {"packages": ["ripgrep"]}),
    merge(
        {"mcp_servers": {"foo": {"command": "/bin/foo", "args": ["--a"]}}},
        {"mcp_servers": {"foo": None}},
    ),
    merge(
        {"mcp_servers": {"foo": {"command": "/bin/foo"}, "bar": {"command": "/b"}}},
        {"mcp_servers": {"bar": {"command": "/ws/bar"}, "baz": {"command": "/z"}}},
    ),
    merge(
        {"env_sources": [{"A": "1", "B": "2"}]},
        {"env_sources": [{"B": "override", "C": "3"}]},
    ),
    merge({"per_side_paths": [".cargo", ".venv-x"]}, {"per_side_paths": [".venv-x", "models"]}),
    # dedup with dict entries + nested objects (canonical-key equality classes).
    merge(
        {"security": {"blocked_tools": [{"name": "grep", "block_flags": ["-r"]}]}},
        {"security": {"blocked_tools": [{"block_flags": ["-r"], "name": "grep"}]}},
    ),
    merge({"packages": [{"name": "a"}, {"name": "b"}]}, {"packages": [{"name": "a"}]}),
    merge({"x": [1, 2, 3]}, {"x": [3, 2, 1, 4]}),
    merge({"scalar": "old"}, {"scalar": "new"}),
    merge({"typ": [1, 2]}, {"typ": {"now": "dict"}}),
    merge({}, {"a": 1}),
    merge({"deep": {"a": {"b": {"c": 1}}}}, {"deep": {"a": {"b": {"d": 2}}}}),
    # ---- snapshot ----
    snap({"packages": ["strace"]}),
    snap({"z": 1, "a": 2, "m": {"y": 3, "b": 4}}),
    snap({"unicode": "café ☃", "list": [1, True, None, 2.5]}),
    snap({}),
    # ---- validate: clean configs ----
    validate({}),
    validate({"agents": ["claude", "copilot", "gemini", "opencode", "pi", "codex"]}),
    validate({"runtime": "podman"}),
    validate({"runtime": "container"}),
    validate({"runtime": "macos-user"}),
    validate({"packages": ["postgresql", "gtk4.dev"]}),
    validate({"packages": [{"name": "freetype", "nixpkgs": "abc123"}]}),
    validate(
        {"packages": [{"name": "f", "version": "2.1", "url": "mirror://x", "hash": "sha256-y"}]}
    ),
    validate({"packages": [{"name": "gtk4", "outputs": ["out", "dev"]}]}),
    validate(
        {
            "env_sources": [
                {"DATABASE_URL": "postgres://localhost/dev", "DEBUG": "1"},
                "~/.config/yolo-jail/secrets.env",
            ]
        }
    ),
    validate({"ephemeral_storage": "volume"}),
    validate({"ephemeral_storage": "tmpfs"}),
    validate({"agents_md_extra": "## My MCPs\n\nUse cerebras-mcp."}),
    validate({"per_side_paths": [".venv-alt", ".cargo", "data/models"]}),
    validate({"include_if_found": ["overrides.jsonc", "secret/local.jsonc"]}),
    validate(
        {
            "mcp_servers": {
                "tavily": {
                    "command": "npx",
                    "args": ["-y", "tavily-mcp"],
                    "requires_env": ["TAVILY_API_KEY"],
                }
            }
        }
    ),
    validate(
        {
            "network": {
                "mode": "bridge",
                "ports": ["8000:8000", "127.0.0.1:9000:9000", "53:53/udp"],
                "forward_host_ports": [8080, "8080:9090"],
            }
        }
    ),
    validate(
        {
            "gpu": {
                "enabled": True,
                "vendor": "nvidia",
                "devices": "all",
                "capabilities": "compute,utility",
            }
        }
    ),
    validate({"gpu": {"enabled": True, "vendor": "amd", "vaapi": True, "mode": "cdi"}}),
    validate({"resources": {"memory": "8g", "cpus": 4, "pids_limit": 4096}}),
    validate({"resources": {"cpus": "0.5"}}),
    validate({"resources": {"cpus": 2.5}}),
    validate({"host_processes": {"visible": ["nginx"], "fields": ["pid", "comm"]}}),
    validate(
        {"lsp_servers": {"python": {"command": "/p", "args": ["--stdio"], "fileExtensions": {".py": "python"}}}}
    ),
    validate({"devices": [{"usb": "0bda:2838", "description": "sdr"}]}),
    validate({"devices": [{"cgroup_rule": "c 189:* rwm"}]}),
    validate({"host_claude_files": ["settings.json", "config.json"]}),
    validate({"journal": "full"}),
    validate({"journal": True}),
    validate({"kvm": True}),
    validate({"workspace_readonly": ["node_modules", ".git"]}),
    # ---- validate: rejection branches (errors + warnings, in order) ----
    validate({"foo": "bar"}),
    validate({"mcp_server": {}}),
    validate({"runtime": "docker"}),
    validate({"runtime": "containerd"}),
    validate({"repo_path": 42}),
    validate({"agents": "claude"}),
    validate({"agents": ["nope"]}),
    validate({"agents": [42]}),
    validate({"agents": []}),
    validate({"packages": "postgresql"}),
    validate({"packages": ["foo/bar"]}),
    validate({"packages": [{"name": "foo", "nixpkgs": "abc", "bogus": True}]}),
    validate({"packages": [{"name": "freetype", "nixpkgs": "abc", "version": "2.1"}]}),
    validate({"packages": [{"name": "freetype"}]}),
    validate({"packages": [{"name": "gtk4.dev", "nixpkgs": "x"}]}),
    validate({"packages": [{"name": "x", "outputs": "notlist"}]}),
    validate({"packages": [{"name": "x", "outputs": ["good", "1bad"]}]}),
    validate({"packages": [123]}),
    validate({"packages": [{"nixpkgs": 5}]}),
    validate({"mounts": "notlist"}),
    validate({"mounts": [42]}),
    validate({"mounts": ["/host:relative"]}),
    validate({"mounts": [":/container"]}),
    validate({"workspace_readonly": ".venv"}),
    validate({"workspace_readonly": ["/abs"]}),
    validate({"workspace_readonly": ["a/../b"]}),
    validate({"per_side_paths": ".venv"}),
    validate({"per_side_paths": [42]}),
    validate({"per_side_paths": ["/abs/path"]}),
    validate({"per_side_paths": ["a/../b"]}),
    validate({"per_side_paths": [".", ""]}),
    validate({"host_claude_files": "settings.json"}),
    validate({"host_claude_files": [42]}),
    validate({"host_claude_files": ["dir/file.json"]}),
    validate({"journal": "bogus"}),
    validate({"journal": 3}),
    validate({"kvm": "yes"}),
    validate({"ephemeral_storage": "ramdisk"}),
    validate({"ephemeral_storage": True}),
    validate({"network": "notobj"}),
    validate({"network": {"mode": "bogus"}}),
    validate({"network": {"badkey": 1}}),
    validate({"network": {"ports": "notlist"}}),
    validate({"network": {"ports": ["notaport"]}}),
    validate({"network": {"ports": ["70000:70000"]}}),
    validate({"network": {"ports": ["8000:8000/sctp"]}}),
    validate({"network": {"ports": ["a:b:c:d"]}}),
    validate({"network": {"forward_host_ports": "notlist"}}),
    validate({"network": {"forward_host_ports": [{}]}}),
    validate({"network": {"forward_host_ports": ["1:2:3"]}}),
    validate({"network": {"mode": "host", "ports": ["8000:8000"], "forward_host_ports": [8080]}}),
    validate({"security": "notobj"}),
    validate({"security": {"badkey": 1}}),
    validate({"security": {"blocked_tools": "notlist"}}),
    validate({"security": {"blocked_tools": [42]}}),
    validate({"security": {"blocked_tools": [{"name": 5}]}}),
    validate({"security": {"blocked_tools": [{"name": "x", "message": 5}]}}),
    validate({"security": {"blocked_tools": [{"name": "x", "block_flags": "notlist"}]}}),
    validate({"security": {"blocked_tools": [{"name": "x", "badkey": 1}]}}),
    validate({"host_processes": "notobj"}),
    validate({"host_processes": {"badkey": 1}}),
    validate({"host_processes": {"visible": "notlist"}}),
    validate({"host_processes": {"fields": [42]}}),
    validate({"mise_tools": "notobj"}),
    validate({"mise_tools": {"typst": 5}}),
    validate({"lsp_servers": "notobj"}),
    validate({"lsp_servers": {"python": "notobj"}}),
    validate({"lsp_servers": {"python": {"command": 5, "args": "x", "fileExtensions": {}}}}),
    validate({"lsp_servers": {"python": {"command": "/p"}}}),
    validate({"lsp_servers": {"python": {"command": "/p", "fileExtensions": {".py": 5}}}}),
    validate({"lsp_servers": {"python": {"command": "/p", "badkey": 1, "fileExtensions": {}}}}),
    validate({"mcp_presets": "notlist"}),
    validate({"mcp_presets": [42]}),
    validate({"mcp_presets": ["bogus"]}),
    validate({"mcp_servers": "notobj"}),
    validate({"mcp_servers": {"x": "notobj"}}),
    validate({"mcp_servers": {"x": {"command": 5}}}),
    validate({"mcp_servers": {"x": {"command": "c", "args": [5]}}}),
    validate({"mcp_servers": {"x": {"command": "c", "env": "notobj"}}}),
    validate({"mcp_servers": {"x": {"command": "c", "env": {"K": 5}}}}),
    validate({"mcp_servers": {"x": {"command": "c", "requires_env": "K"}}}),
    validate({"mcp_servers": {"x": {"command": "c", "requires_env": ["123BAD"]}}}),
    validate({"mcp_servers": {"x": {"command": "c", "badkey": 1}}}),
    validate({"devices": "notlist"}),
    validate({"devices": [42]}),
    validate({"devices": [{"usb": "0bda:2838", "cgroup_rule": "c 1:1 rwm"}]}),
    validate({"devices": [{}]}),
    validate({"devices": [{"usb": "bad"}]}),
    validate({"devices": [{"usb": 5}]}),
    validate({"devices": [{"usb": "0bda:2838", "description": 5}]}),
    validate({"devices": [{"cgroup_rule": 5}]}),
    validate({"devices": [{"badkey": 1}]}),
    validate({"gpu": "notobj"}),
    validate({"gpu": {"badkey": 1}}),
    validate({"gpu": {"enabled": "yes"}}),
    validate({"gpu": {"vendor": "intel"}}),
    validate({"gpu": {"devices": 5}}),
    validate({"gpu": {"mode": "cdi"}}),
    validate({"gpu": {"vendor": "amd", "mode": "bogus"}}),
    validate({"gpu": {"vendor": "amd", "capabilities": "compute"}}),
    validate({"gpu": {"capabilities": 5}}),
    validate({"gpu": {"capabilities": "compute,bogus"}}),
    validate({"gpu": {"hsa_override_gfx_version": "11.0.0"}}),
    validate({"gpu": {"vendor": "amd", "hsa_override_gfx_version": 5}}),
    validate({"gpu": {"seccomp_unconfined": "yes"}}),
    validate({"gpu": {"vaapi": "yes"}}),
    validate({"gpu": {"vendor": "nvidia", "vaapi": True}}),
    validate({"gpu": {"vendor": "amd", "vaapi": True}}),
    validate({"resources": "notobj"}),
    validate({"resources": {"badkey": 1}}),
    validate({"resources": {"memory": 5}}),
    validate({"resources": {"memory": "8gigs"}}),
    validate({"resources": {"cpus": 0}}),
    validate({"resources": {"cpus": -1}}),
    validate({"resources": {"cpus": "abc"}}),
    validate({"resources": {"cpus": []}}),
    validate({"resources": {"pids_limit": 0}}),
    validate({"resources": {"pids_limit": "4096"}}),
    validate({"include_if_found": "single.jsonc"}),
    validate({"include_if_found": [42]}),
    validate({"include_if_found": [""]}),
    validate({"include_if_found": ["/etc/passwd"]}),
    validate({"include_if_found": ["~/x.jsonc"]}),
    validate({"agents_md_extra": ["a", "b"]}),
    validate({"env": {"FOO": "bar"}}),
    validate({"env_sources": {"FOO": "bar"}}),
    validate({"env_sources": [{"DEBUG": 1}]}),
    validate({"env_sources": [{"123BAD": "val"}]}),
    validate({"env_sources": [""]}),
    validate({"env_sources": [42]}),
    validate({"env_sources": [{"": "empty-key"}]}),
    # ---- validate: loopholes (hermetic known set) ----
    validate({"loopholes": {"audio": {"enabled": False}}}, known={"audio": {}}),
    validate(
        {"loopholes": {"my-hole": {"enabled": False, "env": {"F": "b"}, "jail_env": {"B": "q"}}}},
        known={"my-hole": {"has_host_daemon": True}, "no-daemon-hole": {}},
    ),
    validate(
        {"loopholes": {"my-hole": {"enabled": "yes"}}},
        known={"my-hole": {"has_host_daemon": True}},
    ),
    validate(
        {"loopholes": {"my-hole": {"command": ["echo", "hi"]}}},
        known={"my-hole": {"has_host_daemon": True}},
    ),
    validate(
        {"loopholes": {"my-hole": {"enabled": True, "transport": "none"}}},
        known={"my-hole": {"has_host_daemon": True}},
    ),
    validate(
        {"loopholes": {"my-hole": {"env": {"F": 1}}}},
        known={"my-hole": {"has_host_daemon": True}},
    ),
    validate(
        {"loopholes": {"my-hole": {"jail_env": "PATH=/x"}}},
        known={"my-hole": {"has_host_daemon": True}},
    ),
    validate(
        {"loopholes": {"no-daemon-hole": {"env": {"F": "b"}}}},
        known={"no-daemon-hole": {}},
    ),
    validate(
        {"loopholes": {"no-daemon-hole": {"jail_env": {"F": "b"}}}},
        known={"no-daemon-hole": {}},
    ),
    validate({"loopholes": {"other-svc": {}}}, known={}),
    validate({"loopholes": {"other-svc": {"command": ["run"], "env": {"F": 1}}}}, known={}),
    validate({"loopholes": {"other-svc": {"command": ["run"], "jail_env": {}}}}, known={}),
    validate({"loopholes": {"host-only-hole": {"enabled": False}}}, known={}),
    validate({"loopholes": {"host-only-hole": {"enabled": "yes"}}}, known={}),
    validate({"loopholes": "notobj"}),
    validate({"loopholes": {"BadName!": {"command": ["x"]}}}, known={}),
    validate({"loopholes": {"cgroup-delegate": {"command": ["x"]}}}, known={}),
    validate({"loopholes": {"svc": "notobj"}}, known={}),
    validate({"loopholes": {"svc": {"command": "notlist"}}}, known={}),
    validate({"loopholes": {"svc": {"command": []}}}, known={}),
    validate({"loopholes": {"svc": {"command": ["ok", 5]}}}, known={}),
    validate({"loopholes": {"svc": {"command": ["ok"], "jail_socket": "/bad/path"}}}, known={}),
    validate(
        {"loopholes": {"svc": {"command": ["ok"], "jail_socket": "/run/yolo-services/x.sock"}}},
        known={},
    ),
    validate({"loopholes": {"svc": {"badkey": 1}}}, known={}),
    # ---- derived helpers ----
    {"op": "normalize_blocked_tools", "security": None},
    {"op": "normalize_blocked_tools", "security": {"blocked_tools": ["grep"]}},
    {"op": "normalize_blocked_tools", "security": {"blocked_tools": [{"name": "grep"}]}},
    {
        "op": "normalize_blocked_tools",
        "security": {"blocked_tools": [{"name": "grep", "message": "custom msg"}]},
    },
    {
        "op": "normalize_blocked_tools",
        "security": {"blocked_tools": [{"name": "grep", "block_flags": []}]},
    },
    {"op": "normalize_blocked_tools", "security": {"blocked_tools": ["strace"]}},
    {"op": "normalize_blocked_tools", "security": {"blocked_tools": None}},
    {
        "op": "normalize_blocked_tools",
        "security": {"blocked_tools": [{"name": "curl", "message": "Use wget", "suggestion": "wget URL"}]},
    },
    {"op": "effective_packages", "config": {}},
    {"op": "effective_packages", "config": {"packages": ["a", "b"]}},
    {
        "op": "effective_packages",
        "config": {"packages": ["a"], "gpu": {"enabled": True, "vaapi": True, "vendor": "amd"}},
    },
    {
        "op": "effective_packages",
        "config": {"packages": ["mesa"], "gpu": {"enabled": True, "vaapi": True, "vendor": "amd"}},
    },
    {
        "op": "effective_packages",
        "config": {"packages": ["a"], "gpu": {"enabled": True, "vaapi": True, "vendor": "nvidia"}},
    },
    {"op": "effective_mcp_server_names", "mcp_servers": None, "mcp_presets": None},
    {"op": "effective_mcp_server_names", "mcp_servers": None, "mcp_presets": ["chrome-devtools"]},
    {
        "op": "effective_mcp_server_names",
        "mcp_servers": {"chrome-devtools": None, "extra": {"command": "c"}},
        "mcp_presets": ["chrome-devtools", "sequential-thinking"],
    },
    {
        "op": "effective_mcp_server_names",
        "mcp_servers": {"foo": {"command": "c"}, "bar": None},
        "mcp_presets": [],
    },
    {"op": "selected_agents", "config": {}},
    {"op": "selected_agents", "config": {"agents": ["gemini", "gemini", "bogus", "pi"]}},
    {"op": "selected_agents", "config": {"agents": []}},
    {"op": "selected_agents", "config": {"agents": ["claude", "codex"]}},
    {"op": "merge_mise_tools", "config": {}},
    {"op": "merge_mise_tools", "config": {"mise_tools": {"neovim": "nightly"}}},
    {"op": "merge_mise_tools", "config": {"mise_tools": {"typst": "latest"}}},
    {"op": "merge_mise_disabled_tools", "value": ""},
    {"op": "merge_mise_disabled_tools", "value": "ruby, terraform pnpm"},
    {"op": "merge_mise_disabled_tools", "value": None},
    {"op": "merge_mise_disabled_tools", "value": ["ruby"]},
    {"op": "merge_mise_disabled_tools", "value": 42},
    {"op": "merge_mise_disabled_tools", "value": "pnpm, pnpm, node, node"},
    {"op": "merge_mise_disabled_tools", "value": ",,, ,"},
    {"op": "parse_dotenv", "text": "FOO=bar\nBAZ=qux\n"},
    {"op": "parse_dotenv", "text": "# a comment\n\nFOO=bar\n   # indented\nBAZ=qux\n"},
    {"op": "parse_dotenv", "text": "export FOO=bar\n"},
    {"op": "parse_dotenv", "text": 'A="double"\nB=\'single\'\nC=no_quotes\n'},
    {"op": "parse_dotenv", "text": "TOKEN=a=b=c\n"},
    {"op": "parse_dotenv", "text": "EMPTY=\n"},
    {"op": "parse_dotenv", "text": "123BAD=x\nGOOD=y\nalso-bad=z\n"},
    {"op": "parse_dotenv", "text": "just a line\nFOO=bar\n"},
]


def main() -> int:
    out = Path(__file__).parent / "config_cases.json"
    text = json.dumps(CASES, ensure_ascii=True, indent=2) + "\n"
    out.write_text(text, encoding="utf-8")
    json.loads(out.read_text(encoding="utf-8"))
    print(f"wrote {out} ({len(CASES)} cases)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
