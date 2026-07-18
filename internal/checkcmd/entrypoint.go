package checkcmd

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// entrypointPreflightCode mirrors the `python3 -c` body _entrypoint_preflight
// runs: import the repo's entrypoint package, run every generator, then validate
// each SELECTED agent's config output.
const entrypointPreflightCode = `
import json
import sys
import tomllib
from pathlib import Path

sys.path.insert(0, %SRC%)
import entrypoint
from entrypoint.agent_configs import CONFIG_WRITERS

entrypoint.generate_shims()
entrypoint.generate_agent_launchers()
entrypoint.generate_bashrc()
entrypoint.generate_bootstrap_script()
entrypoint.generate_venv_precreate_script()
entrypoint.generate_mise_config()
entrypoint.generate_mcp_wrappers()

def _load_json(p):
    json.loads(p.read_text())

def _load_toml(p):
    tomllib.loads(p.read_text())

_agent_outputs = {
    "copilot": [
        (entrypoint.COPILOT_DIR / "mcp-config.json", _load_json),
        (entrypoint.COPILOT_DIR / "lsp-config.json", _load_json),
    ],
    "gemini": [(entrypoint.GEMINI_DIR / "settings.json", _load_json)],
    "claude": [(entrypoint.CLAUDE_DIR / "settings.json", _load_json)],
    "opencode": [(entrypoint.OPENCODE_DIR / "opencode.json", _load_json)],
    "pi": [(entrypoint.PI_DIR / "settings.json", _load_json)],
    "codex": [(entrypoint.CODEX_DIR / "config.toml", _load_toml)],
}
for _agent in entrypoint._load_agents():
    CONFIG_WRITERS[_agent]()
    for _out, _parse in _agent_outputs.get(_agent, []):
        _parse(_out)
print("ok")
`

// runEntrypointPreflight spawns the Python entrypoint dry-run in a temp home
// with the same YOLO_* environment _entrypoint_preflight builds. Returns "" on
// success, or the combined stdout/stderr detail on failure.
func (o *Options) runEntrypointPreflight(r *reporter, repoRoot, workspace string, merged *jsonx.OrderedMap) string {
	srcDir := filepath.Join(repoRoot, "src")
	tmp, err := os.MkdirTemp("", "yolo-check-")
	if err != nil {
		return "could not create temp home: " + err.Error()
	}
	defer os.RemoveAll(tmp)

	normalizedBlocked := securitySection(merged)
	blockedJSON := jsonDump(config.NormalizeBlockedTools(normalizedBlocked))
	miseJSON := jsonDump(config.MergeMiseTools(merged))
	lspJSON := jsonDumpOrEmptyObj(mapOrNil(merged, "lsp_servers"))
	mcpJSON := jsonDumpOrEmptyObj(mapOrNil(merged, "mcp_servers"))
	presetsJSON := jsonDumpOrEmptyList(listOrNil(merged, "mcp_presets"))
	agentsJSON := jsonDumpStrings(config.SelectedAgents(merged))

	workspaceResolved := workspace
	if r, e := filepath.Abs(workspace); e == nil {
		workspaceResolved = r
	}

	env := []string{
		"JAIL_HOME=" + tmp,
		"HOME=" + tmp,
		"NPM_CONFIG_PREFIX=" + tmp + "/.npm-global",
		"GOPATH=" + tmp + "/go",
		"MISE_DATA_DIR=/mise",
		"YOLO_HOST_DIR=" + workspaceResolved,
		"YOLO_BLOCK_CONFIG=" + blockedJSON,
		"YOLO_MISE_TOOLS=" + miseJSON,
		"YOLO_LSP_SERVERS=" + lspJSON,
		"YOLO_MCP_SERVERS=" + mcpJSON,
		"YOLO_MCP_PRESETS=" + presetsJSON,
		"YOLO_AGENTS=" + agentsJSON,
	}
	// env_sources overrides (resolved against the workspace). The resolver's
	// warn callback surfaces missing/unreadable files as non-counting yellow
	// "Warning:" lines, matching the console.print in _resolve_env_sources.
	resolvedEnv := config.ResolveEnvSources(workspace, merged, r.warningLine)
	for _, k := range resolvedEnv.Keys() {
		v, _ := resolvedEnv.Get(k)
		env = append(env, k+"="+asString(v))
	}
	// Drop inherited PYTHONPATH so the subprocess imports entrypoint from srcDir
	// only. The Exec seam appends env to os.Environ(); an explicit empty
	// PYTHONPATH shadows any inherited one.
	env = append(env, "PYTHONPATH=")

	code := strings.ReplaceAll(entrypointPreflightCode, "%SRC%", pyRepr(srcDir))
	python := o.pythonExecutable()
	res := o.Exec([]string{python, "-c", code}, workspace, env, 120*time.Second)
	if !res.Ran {
		return "entrypoint dry-run could not start (" + python + " not found)"
	}
	if res.Timeout {
		return "entrypoint dry-run timed out"
	}
	if res.RC != 0 {
		details := strings.TrimSpace(res.Stdout)
		errPart := strings.TrimSpace(res.Stderr)
		if details != "" && errPart != "" {
			details = details + "\n" + errPart
		} else if errPart != "" {
			details = errPart
		}
		if details == "" {
			details = "entrypoint dry-run failed"
		}
		return details
	}
	return ""
}

// pythonExecutable resolves the interpreter for the dry-run: YOLO_PYTHON (set by
// the jail shim / front door) else python3.
func (o *Options) pythonExecutable() string {
	if p := o.Getenv("YOLO_PYTHON"); p != "" {
		return p
	}
	return "python3"
}

func securitySection(merged *jsonx.OrderedMap) *jsonx.OrderedMap {
	if v, _ := merged.Get("security"); v != nil {
		if m, ok := v.(*jsonx.OrderedMap); ok {
			return m
		}
	}
	return nil
}

func mapOrNil(m *jsonx.OrderedMap, key string) *jsonx.OrderedMap {
	if v, _ := m.Get(key); v != nil {
		if mm, ok := v.(*jsonx.OrderedMap); ok {
			return mm
		}
	}
	return nil
}

func listOrNil(m *jsonx.OrderedMap, key string) []any {
	if v, _ := m.Get(key); v != nil {
		if l, ok := v.([]any); ok {
			return l
		}
	}
	return nil
}

func jsonDump(v any) string {
	s, _ := jsonx.DumpsCompact(v)
	return s
}

func jsonDumpOrEmptyObj(m *jsonx.OrderedMap) string {
	if m == nil {
		return "{}"
	}
	s, _ := jsonx.DumpsCompact(m)
	return s
}

func jsonDumpOrEmptyList(l []any) string {
	if l == nil {
		return "[]"
	}
	s, _ := jsonx.DumpsCompact(l)
	return s
}

func jsonDumpStrings(ss []string) string {
	arr := make([]any, len(ss))
	for i, s := range ss {
		arr[i] = s
	}
	s, _ := jsonx.DumpsCompact(arr)
	return s
}

// pyRepr renders a Python string literal for embedding a path into the -c code
// ({str_dir!r}). Reuse jsonx string encoding then swap to single quotes is
// fragile; instead emit a repr with single quotes and backslash-escaping.
func pyRepr(s string) string {
	var b strings.Builder
	b.WriteByte('\'')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '\'':
			b.WriteString(`\'`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('\'')
	return b.String()
}
