// Package entrypoint generates the in-jail PID-1 bootstrap content — shims,
// .bashrc, the six agents' config files, managed-MCP sidecars, mise
// config.toml, MCP wrappers, and the bootstrap/venv-precreate/cglimit/
// journalctl/yolo-ps/yolo-wrapper script bodies.
//
// This package is dependency-light: it builds only on internal/* foundation
// packages (jsonx, tomlx, shquote,
// agents, fsx) — no third-party deps beyond what those vendor.
package entrypoint

import (
	"io"
	"os"
	"path/filepath"
)

// Env captures the container environment the pure generators read. In Python
// these come from module-level path constants (computed from $HOME/$JAIL_HOME
// at import time) plus os.environ lookups inside each generator. Modeling them
// as an explicit struct — instead of reading os.Getenv globally — makes the
// generators pure functions of their inputs, which is exactly what the tree
// golden harness needs to drive two implementations into fake HOMEs under an
// identical, committed env matrix.
//
// The Vars map holds the YOLO_* / other environment variables each generator
// consults (YOLO_BLOCK_CONFIG, YOLO_AGENTS, YOLO_MCP_*, YOLO_LSP_SERVERS,
// YOLO_MISE_TOOLS, YOLO_HOST_DIR, YOLO_REPO_ROOT, etc.). Getenv mirrors
// os.environ.get(key, "") and Lookup mirrors `key in os.environ`.
type Env struct {
	// Home is $JAIL_HOME (falling back to $HOME, then /home/agent) — the base
	// of every path constant below. Mirrors entrypoint.HOME.
	Home string
	// MiseData is $MISE_DATA_DIR (or $HOME/.local/share/mise) — MISE_SHIMS is
	// MiseData/shims. Mirrors the MISE_SHIMS computation in entrypoint/__init__.
	MiseData string
	// NpmPrefix is $NPM_CONFIG_PREFIX (or $HOME/.npm-global). NPM_BIN is
	// NpmPrefix/bin. Mirrors entrypoint.NPM_PREFIX / NPM_BIN.
	NpmPrefix string
	// GoPath is $GOPATH (or $HOME/go). GO_BIN is GoPath/bin.
	GoPath string
	// Vars is the environment-variable matrix the generators consult.
	Vars map[string]string
	// Stderr receives the warning/notice lines the Python generators print to
	// sys.stderr (undefined-var warnings, requires_env skips, dropped codex
	// tables, "Error configuring X" lines). Nil discards them. These are NOT
	// part of the file-content golden but ARE part of behavioral parity.
	Stderr io.Writer
}

// warn writes a line to e.Stderr (if set), mirroring print(..., file=sys.stderr).
func (e *Env) warn(msg string) {
	if e.Stderr != nil {
		_, _ = io.WriteString(e.Stderr, msg+"\n")
	}
}

// NewEnv builds an Env from a variable map, resolving Home, MiseData, NpmPrefix,
// and GoPath with the same defaults the Python module constants use.
//
// - HOME: JAIL_HOME || HOME || /home/agent
// - MISE_DATA: MISE_DATA_DIR || HOME/.local/share/mise (shims appended)
// - NPM: NPM_CONFIG_PREFIX || HOME/.npm-global
// - GOPATH: GOPATH || HOME/go
func NewEnv(vars map[string]string) *Env {
	if vars == nil {
		vars = map[string]string{}
	}
	home := firstNonEmpty(vars["JAIL_HOME"], vars["HOME"], "/home/agent")
	// Python: Path(MISE_DATA_DIR or HOME/.local/share/mise). Note the `or`
	// treats an empty string the same as unset — an empty MISE_DATA_DIR falls
	// back to the HOME default.
	miseData := vars["MISE_DATA_DIR"]
	if miseData == "" {
		miseData = filepath.Join(home, ".local", "share", "mise")
	}
	// Python: Path(os.environ.get("NPM_CONFIG_PREFIX", HOME/.npm-global)). The
	// `.get` default only fires when the key is ABSENT (an explicit empty value
	// would be used verbatim); we mirror that with a presence check.
	npmPrefix, ok := vars["NPM_CONFIG_PREFIX"]
	if !ok {
		npmPrefix = filepath.Join(home, ".npm-global")
	}
	goPath, ok := vars["GOPATH"]
	if !ok {
		goPath = filepath.Join(home, "go")
	}
	return &Env{
		Home:      home,
		MiseData:  miseData,
		NpmPrefix: npmPrefix,
		GoPath:    goPath,
		Vars:      vars,
	}
}

// EnvFromOS builds an Env from the real process environment. Used by the actual
// PID-1 binary; the generators themselves take an
// explicit *Env so tests can drive a fixed matrix.
func EnvFromOS() *Env {
	vars := map[string]string{}
	for _, kv := range os.Environ() {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				vars[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	return NewEnv(vars)
}

// Getenv mirrors os.environ.get(key, "").
func (e *Env) Getenv(key string) string { return e.Vars[key] }

// Lookup mirrors `key in os.environ` plus the value.
func (e *Env) Lookup(key string) (string, bool) {
	v, ok := e.Vars[key]
	return v, ok
}

// --- Path constants (home-relative), mirroring entrypoint/__init__.py ---

// ShimDir is HOME/.yolo-shims.
func (e *Env) ShimDir() string { return filepath.Join(e.Home, ".yolo-shims") }

// MiseShims is MISE_DATA/shims.
func (e *Env) MiseShims() string { return filepath.Join(e.MiseData, "shims") }

// NpmBin is NPM_PREFIX/bin.
func (e *Env) NpmBin() string { return filepath.Join(e.NpmPrefix, "bin") }

// GoBin is GOPATH/bin.
func (e *Env) GoBin() string { return filepath.Join(e.GoPath, "bin") }

// McpWrappersBin is HOME/.local/bin/mcp-wrappers.
func (e *Env) McpWrappersBin() string {
	return filepath.Join(e.Home, ".local", "bin", "mcp-wrappers")
}

// BashrcPath is HOME/.bashrc.
func (e *Env) BashrcPath() string { return filepath.Join(e.Home, ".bashrc") }

// CopilotDir is HOME/.copilot.
func (e *Env) CopilotDir() string { return filepath.Join(e.Home, ".copilot") }

// GeminiDir is HOME/.gemini.
func (e *Env) GeminiDir() string { return filepath.Join(e.Home, ".gemini") }

// GeminiManagedMCPPath is HOME/.gemini/yolo-managed-mcp-servers.json.
func (e *Env) GeminiManagedMCPPath() string {
	return filepath.Join(e.GeminiDir(), "yolo-managed-mcp-servers.json")
}

// ClaudeDir is HOME/.claude.
func (e *Env) ClaudeDir() string { return filepath.Join(e.Home, ".claude") }

// ClaudeManagedMCPPath is HOME/.claude/yolo-managed-mcp-servers.json.
func (e *Env) ClaudeManagedMCPPath() string {
	return filepath.Join(e.ClaudeDir(), "yolo-managed-mcp-servers.json")
}

// ClaudeHostSettingsSnapshotPath is HOME/.claude/yolo-host-synced-settings.json.
func (e *Env) ClaudeHostSettingsSnapshotPath() string {
	return filepath.Join(e.ClaudeDir(), "yolo-host-synced-settings.json")
}

// PiHostSettingsSnapshotPath is HOME/.pi/agent/yolo-host-synced-settings.json
// (mirrors PI_HOST_SETTINGS_SNAPSHOT_PATH).
func (e *Env) PiHostSettingsSnapshotPath() string {
	return filepath.Join(e.PiDir(), "yolo-host-synced-settings.json")
}

// ClaudeSharedCredentialsDir is HOME/.claude-shared-credentials.
func (e *Env) ClaudeSharedCredentialsDir() string {
	return filepath.Join(e.Home, ".claude-shared-credentials")
}

// ClaudeJSONPath is HOME/.claude.json (user-scoped MCP config).
func (e *Env) ClaudeJSONPath() string { return filepath.Join(e.Home, ".claude.json") }

// OpencodeDir is HOME/.config/opencode.
func (e *Env) OpencodeDir() string { return filepath.Join(e.Home, ".config", "opencode") }

// PiDir is HOME/.pi/agent.
func (e *Env) PiDir() string { return filepath.Join(e.Home, ".pi", "agent") }

// CodexDir is HOME/.codex.
func (e *Env) CodexDir() string { return filepath.Join(e.Home, ".codex") }

// MiseConfigDir is HOME/.config/mise.
func (e *Env) MiseConfigDir() string { return filepath.Join(e.Home, ".config", "mise") }

// LocalBin is HOME/.local/bin.
func (e *Env) LocalBin() string { return filepath.Join(e.Home, ".local", "bin") }

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
