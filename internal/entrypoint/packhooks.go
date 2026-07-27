package entrypoint

// packhooks.go is where the imperative residue lives: the things a pack needs done that
// are NOT surface content and therefore cannot be declared as layers.
//
// There are three, all currently claude's, and each was reached by an agent NAME before:
//
//	shared_credentials  symlink a credentials file out to the machine-global tier
//	per_jail_history    per-workspace history file, so two jails do not interleave
//	claude_plugins      reconcile installed plugins against configured LSP servers
//
// A pack REQUESTS a hook by name; core decides whether and how to honor it. That is the
// same shape as `install` — a declaration, not a command — and it is deliberately NOT a
// script the pack supplies: a pack that could run arbitrary code at boot would make the
// origin gate meaningless, since shipping content and executing code would be one grant.
//
// WHY THIS IS BETTER THAN THE SWITCH IT REPLACED, since the code volume is similar: the
// switch keyed on "claude", so the behavior was unreachable for anything else and invisible
// unless you knew to look for that name. A named capability is reachable by any pack that
// needs the same thing, and it appears in the pack's own manifest — so what a pack does to
// your jail is readable from the pack.
//
// The honest limitation: the hook set is CLOSED (packdecl.KnownHooks), so a third-party pack
// needing a genuinely new side effect cannot ship one. That is the accepted cost of not
// executing pack code at boot. When a real second case appears, the hook joins this file;
// inventing a general mechanism before then would be speculation.

import (
	"os"
	"os/exec"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// The hook names, matching packdecl.KnownHooks (which validates them on the host, where
// importing this package would be a dependency inversion). packdecl.TestHookSetsAgree pins
// the two together.
const (
	// HookSharedCredentials symlinks the pack's credentials file into its declared
	// shared dir, so one login serves every workspace on the machine.
	HookSharedCredentials = "shared_credentials"
	// HookPerJailHistory points the tool's history file at a per-workspace file, so two
	// jails on one machine do not interleave their history.
	HookPerJailHistory = "per_jail_history"
	// HookClaudePlugins reconciles claude's installed plugins against the configured LSP
	// servers.
	//
	// Named for the tool, unlike its two siblings, and that is a deliberate admission
	// rather than an oversight: it shells out to `claude plugins install/uninstall` with a
	// plugin-id mapping that is claude's alone. Calling it "lsp_plugins" would imply a
	// generality it does not have. It stays until a second tool wants something like it,
	// at which point the shape they share is the thing worth naming.
	HookClaudePlugins = "claude_plugins"
)

// RunPackHooks honors each pack's requested hooks. Failures go through genStep, so a
// broken hook fails the boot with every other problem reported alongside it (A12).
func RunPackHooks(e *Env, packs []*packload.Pack) {
	for _, p := range packs {
		for _, h := range p.Decl.Hooks {
			hook, pack := h, p
			genStep(e, "hook_"+pack.Name+"_"+hook.Name, func() error {
				return runPackHook(e, pack, hook)
			})
		}
	}
}

func runPackHook(e *Env, p *packload.Pack, h packdecl.Hook) error {
	switch h.Name {
	case HookSharedCredentials:
		return e.linkSharedCredential(p, h)
	case HookPerJailHistory:
		return e.isolateHistoryFile(h)
	case HookClaudePlugins:
		installClaudePlugins(e)
		return nil
	default:
		// Unknown hook names are rejected at manifest decode, so reaching here means the
		// known-set and the switch disagree — a yolo bug, surfaced rather than ignored.
		return &unknownHookError{pack: p.Name, name: h.Name}
	}
}

type unknownHookError struct{ pack, name string }

func (e *unknownHookError) Error() string {
	return "pack " + e.pack + ": unimplemented hook " + e.name
}

// linkSharedCredential replaces the pack's credentials file with a symlink into its
// shared dir, harvesting an existing real file first so a login already performed in this
// jail is not lost.
//
// The MACHINE-GLOBAL tier exists because re-authenticating in every workspace is wrong
// behavior, not an inconvenience — so this is the one hook that deliberately leaks state
// between jails. It only reaches a directory the pack declared in sharedDirs, so the leak
// is bounded by a declaration the user can read.
func (e *Env) linkSharedCredential(p *packload.Pack, h packdecl.Hook) error {
	if h.File == "" || h.SharedDir == "" {
		return &badHookError{pack: p.Name, name: h.Name,
			why: "needs both \"file\" and \"sharedDir\""}
	}
	if !declaresSharedDir(p, h.SharedDir) {
		// A hook may only link into a dir the pack DECLARED shared. Otherwise a pack
		// could reach the machine-global tier without saying so in its manifest, which
		// is the one thing that tier's "declaring one is a real decision" rests on.
		return &badHookError{pack: p.Name, name: h.Name,
			why: "sharedDir " + h.SharedDir + " is not in the pack's sharedDirs"}
	}
	link := filepath.Join(e.Home, filepath.FromSlash(h.File))
	sharedDir := filepath.Join(e.Home, filepath.FromSlash(h.SharedDir))
	shared := filepath.Join(sharedDir, filepath.Base(h.File))
	// Create both parents. The version this replaced ran only after the tool's config dir
	// had been created by that tool's own writer, so it could assume both existed; a hook
	// has no such guarantee about ordering, and os.Symlink into a missing directory fails
	// with a bare ENOENT that reads as a broken jail rather than a missing mkdir.
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(sharedDir, 0o755); err != nil {
		return err
	}
	// Relative target, so the link stays valid whatever the home is mounted as.
	target, err := filepath.Rel(filepath.Dir(link), shared)
	if err != nil {
		return err
	}
	return e.linkThroughShared(link, shared, target)
}

// isolateHistoryFile points a history file at a per-workspace file, keyed by the host
// workspace path. Absent YOLO_HOST_DIR there is nothing to key on, so it is a no-op —
// the same fail-open the claude-specific version had.
func (e *Env) isolateHistoryFile(h packdecl.Hook) error {
	if h.File == "" {
		return &badHookError{name: h.Name, why: "needs a \"file\""}
	}
	hostDir := e.Getenv("YOLO_HOST_DIR")
	if hostDir == "" {
		return nil
	}
	historyFile := filepath.Join(e.Home, filepath.FromSlash(h.File))
	historyDir := filepath.Join(filepath.Dir(historyFile), "jail-history")
	if err := os.MkdirAll(historyDir, 0o755); err != nil {
		return err
	}
	perJail := filepath.Join(historyDir, sha256Hex(hostDir)[:12]+filepath.Ext(h.File))
	if !pathExists(perJail) {
		if f, err := os.OpenFile(perJail, os.O_CREATE, 0o644); err == nil {
			_ = f.Close()
		}
	}
	if target, err := os.Readlink(historyFile); err == nil && target == perJail {
		return nil
	}
	_ = os.Remove(historyFile)
	return os.Symlink(perJail, historyFile)
}

func declaresSharedDir(p *packload.Pack, dir string) bool {
	for _, d := range p.Decl.SharedDirs {
		if d == dir {
			return true
		}
	}
	return false
}

type badHookError struct{ pack, name, why string }

func (e *badHookError) Error() string {
	return "pack " + e.pack + ": hook " + e.name + ": " + e.why
}

// runClaudeCLI runs the claude binary with a bounded timeout and no inherited output.
// Kept here (rather than inline) because HookClaudePlugins is the only caller and the
// 30-second bound is the load-bearing part: a hung agent CLI must not wedge the boot.
func runClaudeCLI(e *Env, args ...string) {
	claudeBin := filepath.Join(e.Home, ".local", "bin", "claude")
	if !pathExists(claudeBin) {
		claudeBin = "claude"
	}
	cmd := exec.Command(claudeBin, args...)
	cmd.Env = envWith(os.Environ(), "YOLO_BYPASS_SHIMS", "1")
	cmd.Stdout = nil
	cmd.Stderr = nil
	_ = runWithTimeoutSeconds(cmd, 30)
}
