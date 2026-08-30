// Package entrypoint generates the in-jail PID-1 bootstrap content — shims,
// .bashrc, the six agents' config files, managed-MCP sidecars, mise
// config.toml, MCP wrappers, and the bootstrap/venv-precreate/cglimit/
// journalctl/yolo-ps/yolo-wrapper script bodies.
// This package is dependency-light: it builds only on internal/* foundation
// packages (jsonx, tomlx, shquote,
// agents, fsx) — no third-party deps beyond what those vendor.
package entrypoint

import (
	"io"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// Env captures the container environment the pure generators read. Modeling the
// path constants and env lookups as an explicit struct — instead of reading
// os.Getenv globally — makes the generators pure functions of their inputs,
// which is exactly what the tree golden harness needs to drive two
// implementations into fake HOMEs under an identical, committed env matrix.
// The Vars map holds the YOLO_* / other environment variables each generator
// consults (YOLO_BLOCK_CONFIG, YOLO_AGENTS, YOLO_MCP_*, YOLO_LSP_SERVERS,
// YOLO_MISE_TOOLS, YOLO_HOST_DIR, YOLO_REPO_ROOT, etc.). Getenv returns "" for
// an absent key; Lookup reports presence.
type Env struct {
	// Home is $JAIL_HOME (falling back to $HOME, then /home/agent) — the base
	// of every path constant below.
	Home string
	// MiseData is $MISE_DATA_DIR (or $HOME/.local/share/mise) — MISE_SHIMS is
	// MiseData/shims.
	MiseData string
	// NpmPrefix is $NPM_CONFIG_PREFIX (or $HOME/.npm-global). NPM_BIN is
	NpmPrefix string
	// GoPath is $GOPATH (or $HOME/go). GO_BIN is GoPath/bin.
	GoPath string
	// Workspace is the mounted workspace root. In the container this is the
	// literal "/workspace"; the native macOS (macos-user) bootstrap sets it to
	// the real host workspace path. Generators that used to hardcode "/workspace"
	// read this instead so the same code is correct on both platforms (J2 §1).
	// Empty resolves to "/workspace" (the container default) via WorkspaceDir().
	Workspace string
	// ShimBinDir is the directory a generated shim exec's the real tool from —
	// "/bin" in the Linux container, "/usr/bin" on macOS. Empty resolves to
	// "/bin" via ShimBinPath(). (J2 §1: shims.go hardcoded "/bin/".)
	ShimBinDir string
	// GNUStat reports whether `stat` takes GNU flags (`-c`, Linux container) vs
	// BSD flags (`-f`, macOS). Generated launcher templates branch on this.
	// Defaults to true (the container) via the zero value + StatIsGNU().
	GNUStat bool
	// Vars is the environment-variable matrix the generators consult.
	Vars map[string]string
	// Stderr receives the warning/notice lines the generators emit
	// (undefined-var warnings, requires_env skips, dropped codex tables, "Error
	// configuring X" lines). Nil discards them. These are NOT part of the
	// file-content golden but ARE part of behavioral parity.
	Stderr io.Writer

	// LogOnly receives lines that belong in the boot log but NOT on the launch
	// terminal — the record that a check ran and found nothing wrong. Nil discards
	// them, which is every caller that is not a real boot.
	//
	// It exists because silence is ambiguous exactly where it is most expensive. A
	// healthy jail's reachability witness says nothing, and "ran and was silent" then
	// reads identically to "never ran" — a distinction that stops being academic the
	// moment that witness can refuse a launch. Sending the affirmation to Stderr
	// instead would put a line on every healthy launch, which is how the ONE line
	// that matters gets skimmed past.
	LogOnly io.Writer

	// hostTarget marks this Env as driving the HOST render (`yolo host apply`) rather
	// than the in-jail boot, so renderTarget() projects it onto render.Host instead of
	// render.Jail.
	//
	// It cannot be inferred from Workspace, and the reason is worth stating because the
	// inference LOOKED available: the host render builds its Env with Workspace unset, but
	// WorkspaceDir() resolves an unset Workspace to the container default "/workspace" —
	// so an empty Workspace means "the container default", not "no workspace". A host Env
	// was therefore indistinguishable from a jail Env, and every Target-keyed path
	// (sidecars, provenance, the workspace config.lua) silently resolved against the
	// jail's tree. Unexported: only this package's host entries set it, and a caller
	// outside should be reaching for RenderHostPack, not assembling a host Env by hand.
	hostTarget bool

	// genFailures accumulates the config-generator failures collected by genStep.
	// A12: a generator failure is FATAL — boot must not hand the agent a
	// half-configured home — but each step still runs so one run reports every
	// problem instead of one-per-restart. Main turns a non-empty slice into the
	// error that aborts the jail. See genStep.
	genFailures []string

	// warnedOnce remembers the lines warnOnce has already emitted, so a finding whose
	// SOURCE the boot re-reads is stated once rather than once per read. See warnOnce.
	//
	// A plain map, no mutex: the boot path is sequential where this is written. Checked
	// rather than assumed (2026-08-25) — the only goroutines the entrypoint starts are the
	// cmd.Wait() reapers in runtime.go / system_boot.go and the reachability probes, and
	// probeService takes a serviceEndpoint and a deadline, touching no Env at all.
	warnedOnce map[string]struct{}
}

// genFailure records a fatal config-generator failure (A12). Collected rather
// than returned immediately so a single boot reports every broken step.
func (e *Env) genFailure(msg string) {
	e.genFailures = append(e.genFailures, msg)
}

// GenFailures returns the accumulated fatal generator failures, in order.
func (e *Env) GenFailures() []string { return e.genFailures }

// warn writes a line to e.Stderr (if set).
func (e *Env) warn(msg string) {
	if e.Stderr != nil {
		_, _ = io.WriteString(e.Stderr, msg+"\n")
	}
}

// warnOnce writes a line the way warn does, but at most once per Env for any given text.
//
// It exists for findings that are properties of the STAGED TREE rather than of the step that
// noticed them: the boot reads the pack tree five times (surfaces, requires, launchers, the
// bootstrap, the catalog) and every read re-derives the same skew notes, so one dropped
// contribution printed five identical warnings — a repetition that reads as five problems
// and trains the reader to skim exactly the lines that exist to be read. Deduping at the
// SINK rather than at each call site is what keeps a sixth reader from reintroducing it.
//
// Deduping by the message text is deliberate: two identical lines are, by construction, the
// same finding said twice. A caller that wants a genuine repetition on the terminal wants
// warn.
func (e *Env) warnOnce(msg string) {
	if e.warnedOnce == nil {
		e.warnedOnce = make(map[string]struct{})
	} else if _, seen := e.warnedOnce[msg]; seen {
		return
	}
	e.warnedOnce[msg] = struct{}{}
	e.warn(msg)
}

// note writes a line to the boot log ONLY, never the terminal. Use it for the
// positive record — "this check ran, and here is what it found" — that a reader of
// the log needs and a user watching a healthy launch does not. See Env.LogOnly.
func (e *Env) note(msg string) {
	if e.LogOnly != nil {
		_, _ = io.WriteString(e.LogOnly, msg+"\n")
	}
}

// NewEnv builds an Env from a variable map, resolving Home, MiseData, NpmPrefix,
// and GoPath with these defaults:
// - HOME: JAIL_HOME || HOME || /home/agent
// - MISE_DATA: MISE_DATA_DIR || HOME/.local/share/mise (shims appended)
// - NPM: NPM_CONFIG_PREFIX || HOME/.npm-global
// - GOPATH: GOPATH || HOME/go
func NewEnv(vars map[string]string) *Env {
	if vars == nil {
		vars = map[string]string{}
	}
	home := firstNonEmpty(vars["JAIL_HOME"], vars["HOME"], "/home/agent")
	// An empty MISE_DATA_DIR is treated the same as unset — it falls back to
	// the HOME default.
	miseData := vars["MISE_DATA_DIR"]
	if miseData == "" {
		miseData = filepath.Join(home, ".local", "share", "mise")
	}
	// The default only fires when NPM_CONFIG_PREFIX is ABSENT — an explicit
	// empty value is used verbatim, so we branch on presence.
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
		// Container defaults; the macos-user bootstrap overrides these. GNUStat
		// defaults true (the Linux container) so an Env built the normal way is
		// unchanged — the darwin path explicitly sets GNUStat=false.
		Workspace:  firstNonEmpty(vars["YOLO_WORKSPACE"], "/workspace"),
		ShimBinDir: "/bin",
		GNUStat:    true,
		Vars:       vars,
	}
}

// WorkspaceDir returns the workspace root, defaulting to the container's
// "/workspace" when Workspace is unset. Generators call this instead of
// hardcoding the literal so they are correct on a native macOS home too.
func (e *Env) WorkspaceDir() string {
	if e.Workspace == "" {
		return "/workspace"
	}
	return e.Workspace
}

// renderTarget projects this Env onto the render.Target the surface writers key on
// (env-manager plan Phase 1): the boot render is the KindJail instance of the one
// Target-parameterized renderer. The writers touch exactly these three Env fields —
// Home (surface dest + user config.lua), WorkspaceDir (sidecar root + ${workspace} +
// workspace config.lua), and Stderr (capture/dropped-entry notices) — so the whole of
// what a render needs from the environment is this projection; everything else
// (host bytes, the computed layer) is passed to the writers as arguments. Making the
// Target explicit here is what lets the host verbs (internal/cli) and
// `yolo host apply` render through the SAME contract instead of a hand-copied mirror.
func (e *Env) renderTarget() render.Target {
	if e.hostTarget {
		// render.Host leaves Workspace empty ON PURPOSE — a host render has no
		// per-workspace referent, which is why a ${workspace} surface is refused there
		// rather than bound to an arbitrary dir. So this must NOT pass WorkspaceDir(),
		// which would hand it the container default and make KindOf() call it a jail.
		return render.Host(e.Home, e.Stderr)
	}
	return render.Jail(e.Home, e.WorkspaceDir(), e.Stderr)
}

// ShimBinPath returns the directory a shim exec's the real tool from, defaulting
// to "/bin" (the container) when unset.
func (e *Env) ShimBinPath() string {
	if e.ShimBinDir == "" {
		return "/bin"
	}
	return e.ShimBinDir
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

// Getenv "").
func (e *Env) Getenv(key string) string { return e.Vars[key] }

func (e *Env) Lookup(key string) (string, bool) {
	v, ok := e.Vars[key]
	return v, ok
}

// --- Path constants (home-relative) ---
// ShimDir is HOME/.yolo-shims — the BLOCKER dir, ordered FIRST on PATH.
//
// It holds only what GenerateShims writes: the blocked-tool shims (`grep`, `find`, …)
// generated from YOLO_BLOCK_CONFIG. Interception is their entire job, so preceding the
// real binary is a requirement, not a convenience.
//
// Lazy INSTALLERS live in LauncherDir instead (see there for why the split exists).
func (e *Env) ShimDir() string { return filepath.Join(e.Home, ".yolo-shims") }

// LauncherDir is HOME/.yolo-launchers — the lazy-INSTALLER dir, ordered LAST on PATH
// (after /bin and /usr/bin).
//
// Blockers and lazy installers used to share ~/.yolo-shims because both are "a script
// named after a binary, early on PATH". They are not the same mechanism, and conflating
// them was a live defect: a pack declaring `program fzf` wrote ~/.yolo-shims/fzf, which
// PRECEDED the image's perfectly good /bin/fzf, and the launcher only ever execs
// $NPM_CONFIG_PREFIX/bin/<bin> — it never consults PATH — so it exited 1. Declaring a
// dependency honestly BROKE it.
//
// A blocker must shadow the real tool. An installer must not: it only needs to run when
// nothing else provides the name. Ordering this dir after /bin makes that structural
// rather than something the launcher has to defend against at runtime — a launcher is
// simply unreachable while any real binary of that name exists.
//
// One consequence, stated because it is a real trade: a tool the IMAGE bakes now wins
// over a pack's declared version. For `fzf` that is right. For an agent CLI it might not
// be — if the image ever baked an older `claude`, that pack's lazy-updating launcher would
// stop being reached. No shipped pack collides today (the six agent CLIs are not in
// flake.nix's corePackages/fullPackages), but it is the case to re-check when adding a
// baked package whose name a pack also claims.
//
// Like ShimDir this is a bind-mount ANCHOR (mounted from <ws>/.yolo/home/yolo-launchers
// under a read-only /home/agent), so it is cleared CONTENTS-ONLY. See GenerateShims.
func (e *Env) LauncherDir() string { return filepath.Join(e.Home, ".yolo-launchers") }

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

// GeminiDir is HOME/.gemini. The `gemini` AGENT was removed (A1), but this tree
// is still live: it is where agy (Google Antigravity CLI) keeps its state, under
// the antigravity-cli subdir. Kept for AgyDir, not for gemini.
func (e *Env) GeminiDir() string { return filepath.Join(e.Home, ".gemini") }

// AgyDir is HOME/.gemini/antigravity-cli — the Google Antigravity CLI's state
// dir. It sits under the ~/.gemini tree (a Google convention agy inherits) but is
// a distinct subdir, so
// the two agents never collide (see the agy AgentSpec / agySettings surface).
func (e *Env) AgyDir() string { return filepath.Join(e.GeminiDir(), "antigravity-cli") }

// ClaudeDir is HOME/.claude.
func (e *Env) ClaudeDir() string { return filepath.Join(e.Home, ".claude") }

// ClaudeHostSettingsSnapshotPath is HOME/.claude/yolo-host-synced-settings.json.
func (e *Env) ClaudeHostSettingsSnapshotPath() string {
	return filepath.Join(e.ClaudeDir(), "yolo-host-synced-settings.json")
}

// PiHostSettingsSnapshotPath is HOME/.pi/agent/yolo-host-synced-settings.json
// .
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
