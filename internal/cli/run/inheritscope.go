package run

// inheritscope.go writes the two GENERATED user-scope files a jail inherits, replacing the
// raw `:ro` bind of the human's real config.jsonc that used to serve as an inner jail's
// user scope (OQ-LP9, docs/design/loophole-packaging.md).
//
// WHAT CHANGED, in one sentence: the inner user scope is now COMPOSED from the effective
// config and FILTERED per consumer (internal/config/inherit.go), rather than being whatever
// happened to be in the human's top config file.
//
// FOUR PROPERTIES THIS FILE IS RESPONSIBLE FOR, all four load-bearing:
//
//  1. SINGLE-FILE DELIVERY INTO A JAIL-OWNED DIRECTORY (R8). Each generated file is
//     mounted as its own `:ro` single file, and the DIRECTORY around it
//     (~/.config/yolo-jail) stays the jail's own writable home. That is what makes
//     writing BESIDE the file jail-local: an in-jail agent can drop a layer file next to
//     it and pass --user-layer, and nothing it writes can reach the host. Mounting the
//     DIRECTORY instead would take that away, which is why this never does.
//  2. THE NESTED FILE IS WRITTEN ONLY WHERE NESTING IS POSSIBLE (R2). Not gated by a
//     conditional inside a file that is always written — genuinely absent. On a backend
//     that cannot nest there is no file, so nothing is mounted that serves a capability
//     the setup lacks.
//  3. RECURSION IS BY COMPOSITION (R6). This runs from the EFFECTIVE config of whatever
//     jail is launching, which at depth 2 already includes what depth 1 inherited plus any
//     --user-layer it was passed. So jail A hands B one inherited file; it never stacks a
//     chain of ancestors' files, and there is no rule that changes with depth.
//  4. LAUNCH-FROZEN (R7). These are rendered once at assembly, so a host-side config edit
//     lands at the next launch rather than live. That is a REAL behaviour change from the
//     live bind, named in the file header, and it is the jail's normal contract — env, the
//     image and the relay wiring are all frozen at container start. `yolo config drift`
//     is the existing mechanism for noticing, and it now covers this file too.

import (
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// The in-jail names of the two generated files. Both live in the jail's own
// ~/.config/yolo-jail, beside where a --user-layer file would go.
//
// The PREFLIGHT file takes the name `config.jsonc` deliberately: it IS the inner user
// scope, so every existing in-jail reader (config.LoadConfig, LoadPacks, the loopholes
// commands, `yolo check`) finds it at paths.UserConfigPath() with no plumbing at all. The
// nested file gets its own name because nothing reads it as "the user config" — an inner
// launcher merges it explicitly.
const (
	inheritPreflightRel = ".config/yolo-jail/config.jsonc"
	inheritNestedRel    = ".config/yolo-jail/inherited-launch.jsonc"
)

// inheritScopeFile is one generated file to deliver: its rendered bytes and where it goes.
type inheritScopeFile struct {
	scope config.InheritScope
	rel   string // path under /home/agent
	body  string
	// keys is how many config keys survived the filter. Zero means there is nothing to
	// deliver — see inheritScopeFiles.
	keys int
}

// canNest reports whether a jail on this backend can launch a jail of its own — the gate on
// whether the nested-launch file is written at all (R2).
//
// The answer is a property of the BACKEND, not of the current process: podman bakes a
// nested podman into the image (flake.nix corePackages: podman, fuse-overlayfs,
// slirp4netns) and the CLI has a whole podman-in-podman branch (--userns=host, forced
// --net=host, --cgroups=disabled). Apple Container has no nested container runtime inside
// its VM, and macos-user has no container at all — a Seatbelt-confined native process
// cannot start one, and the macos-user census (docs/design/macos-user-nix-and-features.md)
// already states that nothing is bind-mounted there.
//
// Deliberately keyed on the runtime STRING rather than on a probe: this decides whether to
// write a file for the jail we are about to start, so it must answer "can the container we
// are creating nest?", which no probe of the current process can tell us.
func canNest(rt string) bool { return rt == "podman" }

// inheritScopeFiles renders the generated user-scope files for a launch.
//
// effective is the already-composed config — the same value `yolo config dump` serializes
// (config.LoadConfig → SnapshotJSON), which is what makes both files renders of ONE
// computation rather than a second composer that could drift from the first.
//
// unknown collects config keys the census does not classify. They are DROPPED from every
// generated file and returned so the caller can say so: silently passing an unclassified
// key into a jail would defeat the census, and silently dropping it would hide a schema
// addition. (In a shipped binary the set is always empty — validate.go rejects an unknown
// top-level key long before assembly — so this is the seam that keeps a future key from
// slipping through, not a live path.)
//
// A scope whose filter kept NOTHING yields keys==0, and the caller then delivers no file at
// all. That matters twice over. It is honest — a user with no relevant config had no user
// scope before this change either, and a file saying `{}` under a header explaining what was
// filtered would invite the reader to hunt for a key they never set. And it keeps the
// promise the golden argv makes: a jail launched from a config with nothing to inherit emits
// byte-identical argv to the one it emitted before this feature existed.
func inheritScopeFiles(effective *jsonx.OrderedMap, rt, launchedAt string) (files []inheritScopeFile, unknown []string, err error) {
	seen := map[string]bool{}
	addUnknown := func(keys []string) {
		for _, k := range keys {
			if !seen[k] {
				seen[k] = true
				unknown = append(unknown, k)
			}
		}
	}
	render := func(scope config.InheritScope, rel string) error {
		filtered, unk, ferr := config.FilterInheritErr(effective, scope)
		if ferr != nil {
			return ferr
		}
		addUnknown(unk)
		if filtered.Len() == 0 {
			return nil
		}
		body, rerr := config.RenderFiltered(filtered, scope, launchedAt)
		if rerr != nil {
			return rerr
		}
		files = append(files, inheritScopeFile{scope: scope, rel: rel, body: body, keys: filtered.Len()})
		return nil
	}

	if err := render(config.InheritPreflight, inheritPreflightRel); err != nil {
		return nil, nil, err
	}
	// R2: absent, not empty, on a backend that cannot nest.
	if canNest(rt) {
		if err := render(config.InheritNested, inheritNestedRel); err != nil {
			return nil, nil, err
		}
	}
	return files, unknown, nil
}

// userConfigMountArgs delivers the generated inner user scope (and config.lua), returning
// the container argv for it.
//
// It REPLACED a raw `:ro` bind of paths.UserConfigPath(). The old comment said the mount
// was "for nested jails"; measured, its readers were broader (`yolo check`, `yolo loopholes
// list`, `yolo pack` all read user scope in-jail on every setup) and its content was
// narrower (only config.jsonc and config.lua crossed, so `include_if_found` files stayed
// host-side). So it was neither the effective config nor a designed subset — it filtered by
// accident. Now it filters on purpose, per consumer.
//
// config.lua still crosses AS THE HOST'S FILE, unfiltered, and that is not an oversight:
// it is a Lua transform script, not a config with keys to classify, and the entrypoint
// reads it from $HOME/.config/yolo-jail/config.lua (loadPrismTransformScript) as the user
// half of the documented "user then workspace" transform pair. There is nothing to filter
// and no false-error class to kill — a transform that references a host path simply does
// not fire on a surface that is not there. (A13: this file used to have no channel into any
// jail at all, while `yolo config-ref` advertised it as auto-loaded.)
func (o *Options) userConfigMountArgs(rt, wsState string, mountTargets map[string]struct{}) []string {
	var args []string

	// The GENERATED files. Written into wsState (the jail's own per-workspace state dir,
	// which is where every other composed file goes — the gitconfig, the user-env script)
	// and mounted from there, one bind per file.
	out := o.pr(o.Stdout)
	files, unknown, err := inheritScopeFiles(o.effectiveConfigForInherit(), rt, o.inheritStamp())
	if err != nil {
		// LOUD, and the reason matters: a jail whose user scope silently failed to render
		// looks like a jail whose user config is empty — `yolo pack ls` shows nothing, a
		// loophole the human installed is missing — and the agent inside debugs the wrong
		// thing. Not fatal, because a jail with no user scope still boots and works.
		out.print("[yellow]Warning: could not render this jail's inherited user config (" +
			err.Error() + ") — in-jail `yolo check`, `pack` and `loopholes` will see no " +
			"user scope[/yellow]")
		return args
	}
	if len(unknown) > 0 {
		// Named, not swallowed: an unclassified key reaching a jail unreviewed is the
		// failure the census exists to prevent, so its absence has to be visible.
		out.print("[yellow]Warning: config keys with no inherit classification were " +
			"dropped from the jail's user scope: " + joinComma(unknown) +
			" — classify them in internal/config/inherit.go[/yellow]")
	}
	for _, f := range files {
		staged := filepath.Join(wsState, "inherit-"+f.scope.String()+".jsonc")
		if err := os.WriteFile(staged, []byte(f.body), 0o644); err != nil {
			// Same reasoning as above, per file. The mount is SKIPPED rather than emitted
			// anyway, because podman dies on a bind source that does not exist ("statfs
			// …: no such file or directory") — so emitting it would turn a degraded user
			// scope into a jail that will not start.
			out.print("[yellow]Warning: could not stage the " + f.scope.String() +
				" user config at " + staged + " (" + err.Error() + ") — this jail gets no " +
				f.scope.String() + " user scope[/yellow]")
			continue
		}
		if rt == "container" {
			// Apple Container mounts the whole wsState at /home/agent and cannot
			// bind a single file, so the content is copied to its destination inside
			// that tree instead.
			dst := filepath.Join(wsState, f.rel)
			if os.MkdirAll(filepath.Dir(dst), 0o755) == nil {
				_ = os.WriteFile(dst, []byte(f.body), 0o644)
			}
			continue
		}
		// SINGLE-FILE bind (R8): the enclosing ~/.config/yolo-jail stays the jail's
		// own writable dir, so a --user-layer file written beside this one is
		// jail-local and cannot reach the host.
		args = append(args, "-v", staged+":/home/agent/"+f.rel+":ro")
	}

	// config.lua, still the host's own file (see the doc comment above). Mounted only when
	// present, so a user with no transform adds no argv.
	userLua := filepath.Join(filepath.Dir(paths.UserConfigPath()), "config.lua")
	if isFile(userLua) {
		const rel = ".config/yolo-jail/config.lua"
		if rt == "container" {
			acMaterialize(userLua, rel, wsState)
		} else {
			args = append(args, ROFileMountArg(
				userLua, "/home/agent/"+rel, wsState, rel, mountTargets, nil)...)
		}
	}
	return args
}

// effectiveConfigForInherit returns the config the generated files are rendered FROM.
//
// It re-reads through config.LoadConfig rather than taking the run pipeline's already-loaded
// map as a parameter, for one reason: this is the ONE computation `yolo config dump` renders,
// and going through the same function is what guarantees they cannot diverge. LoadConfig is
// also where a --user-layer (config.UserLayerPath) is folded in, so the recursion property
// (R6) comes for free — at depth 2 the effective config already contains what depth 1
// inherited plus any layer it was passed.
//
// A load failure yields nil, which FilterInherit renders as an empty file rather than
// failing the launch: the run pipeline has ALREADY loaded and validated this config
// (loadAndValidateConfig, strict) and refused the launch on error, so a failure here means
// the file changed underneath a launch in progress — and an empty inner scope degrades to
// "this jail knows of no user config", which is what a user with no config has.
func (o *Options) effectiveConfigForInherit() *jsonx.OrderedMap {
	cfg, err := config.LoadConfig(o.Workspace, false, func(string) {})
	if err != nil {
		return nil
	}
	return cfg
}

// inheritStamp is the human-readable launch time for the generated files' headers. It uses
// the injected clock so a golden test can pin the bytes.
func (o *Options) inheritStamp() string {
	now := o.Now
	if now == nil {
		return ""
	}
	return now().UTC().Format("2006-01-02T15:04:05Z")
}
