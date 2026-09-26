package entrypoint

// packhooks.go is where the imperative residue lives: the things a pack needs done that
// are NOT surface content and therefore cannot be declared as layers.
//
// Each is a SHAPE rather than one tool's errand, and each was reached by an agent NAME
// before:
//
//	shared_credentials  symlink a credentials FILE out to the machine-global tier
//	shared_directory    symlink a whole DIRECTORY out to the machine-global tier
//	per_jail_history    per-workspace history file, so two jails do not interleave
//
// There WAS a third, `claude_plugins`, and its retirement is the rule this set now keeps
// rather than an exception it admitted. It shelled out to `claude plugins install` with a
// plugin-id mapping that was one agent's alone — a hook named for a TOOL, which its own
// comment called "a deliberate admission rather than an oversight" — and when a second
// tool wanted the same shape the question was re-ruled instead of cited as precedent:
// retire it, and add nothing like it (docs/design/pi-pack-extensions.md, OQ-2,
// 2026-09-19). YOLO places a plugin tree; no vendor install verb runs in a jail. So the
// bar for a new hook is not "a pack needs it" but "the thing it does is not one tool's":
// a name only one agent could ever request belongs in that agent's content, not here.
// packdecl.retiredHooks carries the message an author still declaring it gets.
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
	"fmt"
	"os"
	"path/filepath"
	"time"

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
	// HookSharedDirectory symlinks a whole home subdirectory at the pack's declared
	// shared dir, so one store serves every workspace on the machine instead of N
	// copies that drift apart.
	//
	// NAMED FOR THE SHAPE, NOT FOR WHAT ANY PACK PUTS IN IT. The design that asked for
	// it (docs/design/pi-extension-lifecycle.md §3.1) proposed `shared_extension_storage`
	// for pi's npm store, and an "extension storage" hook is the `claude_plugins` shape
	// this file's header rules out one step removed — a name only one pack could ever
	// want, for a mechanism any pack can use. What it does is the DIRECTORY twin of
	// shared_credentials, so that is what it is called.
	HookSharedDirectory = "shared_directory"
	// HookUnshareDirectory undoes a shared_directory link a pack no longer makes: the
	// symlink at `from` whose target is exactly the one linkIntoSharedDir wrote for `at`
	// is replaced by an empty real directory. Anything else at `from` is left alone.
	HookUnshareDirectory = "unshare_directory"
	// HookPerJailHistory points the tool's history file at a per-workspace file, so two
	// jails on one machine do not interleave their history.
	HookPerJailHistory = "per_jail_history"
)

// RunPackHooks honors each pack's requested hooks. Failures go through genStep, so a
// broken hook fails the boot with every other problem reported alongside it (A12).
func RunPackHooks(e *Env, packs []*packload.Pack) {
	for _, p := range packs {
		for _, h := range p.Decl.HookContributions() {
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
	case HookSharedDirectory:
		return e.linkSharedDirectory(p, h)
	case HookUnshareDirectory:
		return e.unshareDirectory(p, h)
	case HookPerJailHistory:
		return e.isolateHistoryFile(h)
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
// shared dir, moving an existing real file there first IF AND ONLY IF the shared file is
// empty.
//
// This used to say "harvesting an existing real file first so a login already performed in
// this jail is not lost", and that has been FALSE since the harvest was deleted (eb12125):
// a login performed in this jail IS discarded when the shared file is populated, by design.
// The rule and its accepted failure mode are stated once, at linkThroughShared — read it
// before concluding a reverted login is a bug.
//
// The MACHINE-GLOBAL tier exists because re-authenticating in every workspace is wrong
// behavior, not an inconvenience — so this is the one hook that deliberately leaks state
// between jails. It only reaches a directory the pack declared in sharedDirs, so the leak
// is bounded by a declaration the user can read.
func (e *Env) linkSharedCredential(p *packload.Pack, h packdecl.Hook) error {
	return e.linkIntoSharedDir(p, h, sharedFileNode)
}

// linkSharedDirectory replaces a home subdirectory with a symlink to the pack's declared
// shared dir, migrating an existing real directory into it IF AND ONLY IF the shared dir is
// empty — the DIRECTORY twin of linkSharedCredential, on the same rule and through the same
// function (sharedlink.go, where the rule and its price are stated once).
//
// What it buys is stated as a ruling rather than a convenience: N workspaces each keeping
// their own copy of a package store waste disk, and — the load-bearing half — leave one jail
// silently running a different version of a tool's extensions from its neighbour
// (docs/design/pi-extension-lifecycle.md, OQ-1).
//
// SAME LEAK, SAME BOUND as the credential hook: it reaches only a directory the pack declared
// `scope: machine`, so what crosses between workspaces is readable from the manifest — and for
// this payload the leak is also what the user asked for.
func (e *Env) linkSharedDirectory(p *packload.Pack, h packdecl.Hook) error {
	return e.linkIntoSharedDir(p, h, sharedTreeNode)
}

// unshareDirectory is how a pack STOPS sharing a directory it once linked into the machine
// tier. A home that booted under the old shared_directory hook holds `from` as a symlink to
// `at`; once the pack no longer declares `at`, that directory is no longer mounted, the link
// dangles, and the tool's next write into it fails. So the link is replaced with an empty real
// directory, and the tool repopulates its own copy per workspace.
//
// It removes EXACTLY the link linkIntoSharedDir wrote, recognised by its target (the same
// relative path, computed the same way) and never by following it. A real directory, a link
// to any other target, or an absent path are all left untouched, and the store the link
// pointed at is never read or removed: on a machine it is every other workspace's view too,
// until each of them boots once. First used for pi's git checkouts
// (docs/design/pi-git-extension-caching.md §3.5), reverting c402dd43's shared store.
func (e *Env) unshareDirectory(p *packload.Pack, h packdecl.Hook) error {
	if h.File == "" || h.SharedDir == "" {
		return &badHookError{pack: p.Name, name: h.Name, why: "needs both \"from\" and \"at\""}
	}
	link := filepath.Join(e.Home, filepath.FromSlash(h.File))
	shared := sharedTreeNode.sharedPath(filepath.Join(e.Home, filepath.FromSlash(h.SharedDir)), h.File)
	target, err := filepath.Rel(filepath.Dir(link), shared)
	if err != nil {
		return err
	}
	fi, err := os.Lstat(link)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		return nil
	}
	cur, err := os.Readlink(link)
	if err != nil {
		return err
	}
	if cur != target {
		return nil
	}
	if err := os.Remove(link); err != nil {
		return err
	}
	if err := os.Mkdir(link, 0o755); err != nil {
		return err
	}
	e.logSharedHook(h.Name, p.Name, h.File, h.SharedDir,
		"removed the link to the shared store this pack no longer uses; an empty directory takes its place")
	return nil
}

// linkIntoSharedDir is the part both shared-tier hooks share: validate the declaration,
// refuse an undeclared shared dir, create both parents, compute the RELATIVE target, apply
// the rule, and log what it decided. The payload shape is the only difference, and it
// travels as a sharedNode (sharedlink.go).
func (e *Env) linkIntoSharedDir(p *packload.Pack, h packdecl.Hook, n sharedNode) error {
	if h.File == "" || h.SharedDir == "" {
		// The MANIFEST spellings, not the Go field names: `from` is the home path the hook
		// acts on and `at` is the shared dir (HookContributions adapts one to the other),
		// and an author reading this message is looking at pack.json.
		return &badHookError{pack: p.Name, name: h.Name,
			why: "needs both \"from\" and \"at\""}
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
	shared := n.sharedPath(sharedDir, h.File)
	// Create both parents. The version this replaced ran only after the tool's config dir
	// had been created by that tool's own writer, so it could assume both existed; a hook
	// has no such guarantee about ordering, and os.Symlink into a missing directory fails
	// with a bare ENOENT that reads as a broken jail rather than a missing mkdir.
	//
	// It creates the link's PARENT, never the link's own path: on macos-user a real
	// directory where the layout wants a link is a launch refusal with no migration
	// (darwinhomelayout.go, OQ-HT2), and the only thing that belongs at `link` is a symlink.
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(sharedDir, 0o755); err != nil {
		return err
	}
	// Relative target, so the link stays valid whatever the home is mounted as. It is
	// depth-agnostic by construction (filepath.Rel): `.pi/agent/npm` gets
	// `../../.pi-shared-npm`, which resolves through a bind on the container backends and
	// through the sidecar mirror on macos-user.
	target, err := filepath.Rel(filepath.Dir(link), shared)
	if err != nil {
		return err
	}
	decision, err := e.linkThroughShared(link, shared, target, n)
	e.logSharedHook(h.Name, p.Name, h.File, h.SharedDir, decision)
	return err
}

// sharedCredsLog is the persistent per-jail log of shared-TIER hook decisions, so a
// cross-workspace login problem is diagnosable from inside the jail after the fact. It is a
// hook side effect, not a rendered surface, so it is out of scope for the render byte-gate
// (renderfingerprint_test.go skips it by name).
//
// The NAME is the credential hook's and now carries the directory hook's lines too. Kept:
// renaming it would orphan every existing jail's log and the two records belong together
// anyway — each is one hook deciding what crossed into the machine tier. The hook NAME leads
// every line, so the two are separable by grep.
const sharedCredsLog = ".yolo-shared-creds.log"

// logSharedHook records a shared-tier hook decision to stderr and to the persistent
// sharedCredsLog, so a cross-workspace login problem is diagnosable from inside the jail
// after the fact. The entrypoint's stderr is discarded under podman's --log-driver none, so
// stderr alone would leave no trace for the common non-TTY launch; the file is what survives.
func (e *Env) logSharedHook(hook, pack, file, sharedDir, decision string) {
	line := fmt.Sprintf("%s[%s]: %s -> %s: %s", hook, pack, file, sharedDir, decision)
	e.warn(line)
	logPath := filepath.Join(e.Home, sharedCredsLog)
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	// Both dropped, and the justification is that e.warn above ALREADY carried this
	// line to stderr and therefore into boot.log. This file is the redundant durable
	// copy, so a failure to write it loses the second of two records, and reporting it
	// would mean emitting a line about a log on the very channel that already holds
	// the content the log would have had.
	_, _ = fmt.Fprintf(f, "%s %s\n", time.Now().Format("2006-01-02 15:04:05"), line)
	_ = f.Close()
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
		// Closing a file we just created and wrote nothing to has no error worth
		// reporting; and the touch itself is only a convenience — the symlink below is
		// what matters, and it is created whether or not the target file exists yet.
		if f, err := os.OpenFile(perJail, os.O_CREATE, 0o644); err == nil {
			_ = f.Close()
		}
	}
	if target, err := os.Readlink(historyFile); err == nil && target == perJail {
		return nil
	}
	// Dropped because the NEXT line reports the same fact better: if the remove failed
	// for a reason that matters, os.Symlink returns EEXIST and that error is returned
	// to genStep, which makes it a fatal, named hook failure. A remove that failed
	// because there was nothing to remove is the normal first-boot case.
	_ = os.Remove(historyFile)
	return os.Symlink(perJail, historyFile)
}

func declaresSharedDir(p *packload.Pack, dir string) bool {
	for _, d := range p.Decl.SharedDirContributions() {
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
