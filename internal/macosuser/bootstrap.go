package macosuser

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// EntrypointBootstrapScript returns the Python script the sandbox user runs to
// generate its jail config (shims, agent launchers, bashrc, mise, MCP wrappers,
// git/jj identity, per-agent config writers) natively, plus the yolo-log helper
// and the login rc files that re-prepend the sandbox PATH after macОS
// path_helper.
//
// Mirrors entrypoint_bootstrap_script byte-for-byte, including the two subtle
// encodings: the nested json.dumps(json.dumps(agents)) for YOLO_AGENTS, and the
// Python-repr escaping ({x!r}) for the workspace/home/import paths and every
// baked env var. bakedOrder is git_identity updated-by bootstrap_env, emitted in
// sorted-key order.
//
// Args matches the keyword args:
// - repoSrc: the host checkout src dir (only appears in a comment).
// - workspace, sandboxHome: real macOS paths.
// - agents: the configured agent list.
// - macosLog: off/user/full (gates the yolo-log helper).
// - gitIdentity, bootstrapEnv: env baked into the script (ordered-map form so
// the sorted-merge is deterministic; nil is empty).
// - pathPrefix: darwin store bin dirs threaded into the login PATH.
// - stagedDir: root-owned STATE_DIR the entrypoint is imported from ("" =>
// default).
func EntrypointBootstrapScript(repoSrc, workspace, sandboxHome string, agents []string, macosLog string, gitIdentity, bootstrapEnv *jsonx.OrderedMap, pathPrefix []string, stagedDir string) string {
	if sandboxHome == "" {
		sandboxHome = SandboxHome()
	}
	if stagedDir == "" {
		stagedDir = stateDir
	}
	logHelper := MacosLogWrapperScript(macosLog)
	// import_path = staged_entrypoint_dir(staged_dir).parent == stagedDir.
	importPath := stagedDir
	// The SAME PATH baked into launch_argv (shims → darwin store dirs → system).
	loginPath := SandboxPath(sandboxHome, pathPrefix)

	// baked = dict(git_identity or {}); baked.update(bootstrap_env or {})
	baked := map[string]string{}
	if gitIdentity != nil {
		for _, k := range gitIdentity.Keys() {
			v, _ := gitIdentity.Get(k)
			baked[k] = asStr(v)
		}
	}
	if bootstrapEnv != nil {
		for _, k := range bootstrapEnv.Keys() {
			v, _ := bootstrapEnv.Get(k)
			baked[k] = asStr(v)
		}
	}
	var identity strings.Builder
	for _, k := range sortedKeys(baked) {
		identity.WriteString("os.environ[" + reprStr(k) + "] = " + reprStr(baked[k]) + "\n")
	}

	// json.dumps(json.dumps(agents)): inner compact JSON list, then the string
	// re-encoded (quoted + escaped).
	agentsAny := make([]any, len(agents))
	for i, a := range agents {
		agentsAny[i] = a
	}
	innerAgents, _ := jsonx.DumpsCompact(agentsAny)
	yoloAgents, _ := jsonx.DumpsCompact(innerAgents)
	logHelperJSON, _ := jsonx.DumpsCompact(logHelper)
	loginPathJSON, _ := jsonx.DumpsCompact(loginPath)

	// The f-string template, reproduced literally. `{{_login_path}}` → the
	// literal `{_login_path}`, and `\\n` in the Python source → the two-char
	// sequence backslash-n in the OUTPUT (the generated _rc line is itself an
	// f-string that Python-at-runtime expands).
	return "#!/usr/bin/env python3\n" +
		"# yolo-jail macOS-user entrypoint bootstrap (generated).  Runs AS the\n" +
		"# sandbox user to populate its home with shims + agent configs natively.\n" +
		"import os\n" +
		"import stat\n" +
		"import sys\n" +
		"from pathlib import Path\n" +
		"\n" +
		"# Point the entrypoint's HOME-derived path constants at the sandbox user's\n" +
		"# home BEFORE importing it — SHIM_DIR/NPM_BIN/CLAUDE_DIR/MISE_SHIMS/… are\n" +
		"# computed at import time, so rebinding after import would be too late.\n" +
		"# JAIL_HOME drives HOME; leaving NPM_CONFIG_PREFIX/GOPATH/MISE_DATA_DIR unset\n" +
		"# makes them derive from HOME (.npm-global / go / .local/share/mise), which\n" +
		"# is exactly the PATH the launch env expects.  No /mise, no /workspace mount\n" +
		"# on a native host.\n" +
		"home = Path(" + reprStr(sandboxHome) + ")\n" +
		"os.environ[\"JAIL_HOME\"] = str(home)\n" +
		"os.environ[\"HOME\"] = str(home)\n" +
		"os.environ[\"YOLO_AGENTS\"] = " + yoloAgents + "\n" +
		"os.environ.setdefault(\"YOLO_HOST_DIR\", " + reprStr(workspace) + ")\n" +
		identity.String() +
		"\n" +
		"# Import the stdlib-only entrypoint from the root-owned staged copy — the\n" +
		"# host checkout (" + reprStr(repoSrc) + ") may be unreadable to this uid.\n" +
		"sys.path.insert(0, " + reprStr(importPath) + ")\n" +
		"import entrypoint\n" +
		"\n" +
		"# The workspace path is a hardcoded /workspace mount in the container; point\n" +
		"# it at the real workspace so any workspace-relative entrypoint logic lines up.\n" +
		"entrypoint.WORKSPACE = Path(" + reprStr(workspace) + ")\n" +
		"\n" +
		"# Generate the same config the container entrypoint does.  The Linux-only\n" +
		"# boot steps the container entrypoint also runs are intentionally NOT\n" +
		"# called here — they are no-ops (or nonsensical) on a native macOS user.\n" +
		"entrypoint.generate_shims()\n" +
		"entrypoint.generate_agent_launchers()\n" +
		"entrypoint.generate_bashrc()\n" +
		"entrypoint.generate_mise_config()\n" +
		"entrypoint.generate_mcp_wrappers()\n" +
		"entrypoint.configure_git()\n" +
		"entrypoint.configure_jj()\n" +
		"from entrypoint.agent_configs import CONFIG_WRITERS\n" +
		"from entrypoint.agent_registry import AGENTS\n" +
		"\n" +
		"for _name in entrypoint._load_agents():\n" +
		"    _spec = AGENTS.get(_name)\n" +
		"    _writer = CONFIG_WRITERS.get(_name) if _spec is not None else None\n" +
		"    if _writer is not None:\n" +
		"        _writer()\n" +
		"\n" +
		"# Install the macOS unified-logging helper (yolo-log) — the native analog\n" +
		"# of the Linux jail's yolo-journalctl bridge, gated by `macos_log`.\n" +
		"_bin = home / \".local\" / \"bin\"\n" +
		"_bin.mkdir(parents=True, exist_ok=True)\n" +
		"_ylog = _bin / \"yolo-log\"\n" +
		"_ylog.write_text(" + logHelperJSON + ")\n" +
		"_ylog.chmod(_ylog.stat().st_mode | stat.S_IEXEC)\n" +
		"\n" +
		"# Re-prepend the sandbox PATH in the login rc files.  macOS path_helper\n" +
		"# (/etc/zprofile for zsh -l, /etc/profile for bash -lc) reorders PATH to put\n" +
		"# /usr/local/bin (Homebrew) first; these rc files run AFTER it, so the\n" +
		"# nix-store packages + agent shims win again.  Covers login zsh (the default\n" +
		"# REPL), interactive zsh, and login bash.  Bare binaries / plain `-c` shells\n" +
		"# don't read these and keep the correct baked env -i PATH.\n" +
		"_login_path = " + loginPathJSON + "\n" +
		"_rc = f'# yolo-jail: re-prepend the sandbox PATH AFTER macOS path_helper\\nexport PATH=\"{_login_path}:$PATH\"\\n'\n" +
		"for _f in (\".zprofile\", \".zshrc\", \".bash_profile\"):\n" +
		"    (home / _f).write_text(_rc)\n" +
		"\n" +
		"print(\"yolo-jail macos-user bootstrap ok\")\n"
}
