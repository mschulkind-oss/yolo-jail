package config

// userlayer.go implements `--user-layer <file>`: layer a config file in at USER-LEVEL
// precedence, explicitly, for one invocation (OQ-LP9 R4).
//
// WHY AN ARGUMENT AND NOT A CONVENTIONAL FILENAME. The alternative on the table was
// `config.local.jsonc` — a conventionally-named file auto-merged when present. That was
// WITHDRAWN WITH CAUSE and must not come back: it is the same mechanism as the
// `include_if_found`/`overrides.jsonc` accident, one notch more designed. It activates
// because a file EXISTS, invisibly at the call site, so reading a command tells you nothing
// about what config it ran under. An argument is the opposite — visible in the command line,
// testable, and inert unless passed.
//
// WHY THERE IS NO APPROVAL GATE ON IT, and this is a ruling rather than an omission
// (docs/design/gate-placement-principle.md Test 1 — the authority test): passing an argv to
// `yolo` requires the ability to run commands, which already exceeds anything the argument
// grants. An actor who can pass `--user-layer` can equally write the user config file, or
// run the daemon directly. A gate here would refuse an actor who has already passed a
// stronger one — pure ceremony, and the kind that teaches people to click through prompts.
// So: no prompt, on the host and in a jail alike.
//
// AND IT IS THE NESTED-DEVELOPMENT PATH (Test 2 — the blast-radius test). §4.3a's ruling
// deleted the dev-friction escape hatch and sends loophole development into a nested jail.
// That has nowhere to send anyone unless an in-jail agent can INSTALL a loophole, which
// means writing something at user scope. With this flag it can: write a layer file in its
// own home, pass --user-layer, launch. What it thereby gains authority over is a container
// it can throw away — jail A is the blast radius, and nothing reaches the human's host.
// The single-file delivery of the inherited scope (run/inheritscope.go) is the other half:
// the DIRECTORY the inherited file sits in is the jail's own, so writing beside it is
// jail-local by construction.
//
// VERIFIED END TO END in a real nested container, 2026-08-14, and it works: an in-jail agent
// wrote a pack shipping a loophole, wrote a layer naming it, and `pack install`,
// `loopholes list` (dev-probe active, pack/none), `check` ("devpack: 2 file(s) stage") and a
// NESTED launch all saw it.
//
// ONE CONSTRAINT worth knowing, because it is the first thing an agent will trip over: the
// jail's home ROOT is mounted :ro, so `mkdir ~/mypack` fails. The pack has to go somewhere
// writable — `~/.local/share/...` is the natural home (it is one of the rw anchors), or the
// workspace. That is jail-home policy (docs/design/jail-home.md), not a limit of this flag,
// and the layer file itself lands fine because ~/.config is writable — which is exactly the
// R8 property doing its job.

import (
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// UserLayerEnv carries an explicit user-level layer from an invocation into the config
// loader. It is an ENV VAR rather than a parameter threaded through LoadConfig's ~10 call
// sites for one reason: the layer must reach EVERY user-scope reader in the process,
// including the ones that deliberately bypass the merged config and read the user file
// directly (LoadPacks, LoadHostFiles, LoadCacheRelocations — each reads
// paths.UserConfigPath() itself, which is their security boundary). A parameter would reach
// only the readers someone remembered to update, and a `--user-layer` that silently failed
// to carry `packs` — the key nested loophole development needs most — would be worse than
// no flag at all.
//
// It is read, never written, by library code: the CLI front door sets it from the flag.
const UserLayerEnv = "YOLO_USER_LAYER"

// UserLayerPath returns the layer file for this invocation, or "".
func UserLayerPath() string { return os.Getenv(UserLayerEnv) }

// ValidateUserLayer checks a --user-layer argument BEFORE anything is loaded, returning a
// message on refusal.
//
// The checks are about being a usable file, not about authority — there is no authority
// question here (see the file header). A missing or unparseable file is refused LOUDLY
// rather than skipped, because the whole point of an explicit argument is that the caller
// asked for this file: silently ignoring it would reproduce the invisibility that got the
// conventional-filename design rejected.
func ValidateUserLayer(path string) string {
	if path == "" {
		return ""
	}
	abs := path
	if !filepath.IsAbs(abs) {
		if wd, err := os.Getwd(); err == nil {
			abs = filepath.Join(wd, abs)
		}
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return "--user-layer: cannot read " + path + ": " + err.Error() +
			" (an explicitly-passed layer is never skipped silently)"
	}
	if fi.IsDir() {
		return "--user-layer: " + path + " is a directory; pass a single JSONC config file"
	}
	if _, err := LoadJSONCFile(abs, "--user-layer "+path, true, nil); err != nil {
		return err.Error()
	}
	return ""
}

// applyUserLayer merges the --user-layer file OVER a user-scope config, at user-level
// precedence. Returns base unchanged when no layer is set or it cannot be read.
//
// PRECEDENCE, stated exactly: the layer sits at user level, so a WORKSPACE config still
// wins over it (LoadConfig merges workspace over user, and this only changes the user
// half). Within the user level the layer wins over config.jsonc and its includes, which is
// what "layer this in as if it were user-level" has to mean for the flag to be useful —
// a layer that lost to the file it is meant to adjust could not adjust anything.
func applyUserLayer(base *jsonx.OrderedMap) *jsonx.OrderedMap {
	path := UserLayerPath()
	if path == "" {
		return base
	}
	layer, err := LoadJSONCWithIncludes(path, "--user-layer "+path, false, func(string) {}, nil)
	if err != nil || layer == nil || layer.Len() == 0 {
		return base
	}
	if base == nil {
		base = jsonx.NewOrderedMap()
	}
	return MergeConfig(base, layer)
}

// UserScopeConfig is the exported user-scope read for callers outside this package (the
// `yolo loopholes` commands, `yolo check`'s Config Files section): paths.UserConfigPath()
// plus its includes, plus any --user-layer.
//
// Exported so those callers cannot accidentally get a layer-less view by reaching for
// LoadJSONCFile — which is what they did before, and would have made `--user-layer` a flag
// that changed a launch but not the command you run to verify it.
func UserScopeConfig(strict bool, warn Warn) (*jsonx.OrderedMap, error) {
	p := paths.UserConfigPath()
	return loadUserScopeConfig(p, p, strict, warn)
}

// loadUserScopeConfig is THE user-scope read: config.jsonc plus its includes, plus the
// inherited nested-launch file, plus any --user-layer, in that precedence.
//
// Every user-scope reader goes through this, which is the invariant that makes both the flag
// and the inherited scope trustworthy. The direct-read boundary those callers depend on is
// preserved exactly: this still reads paths.UserConfigPath() and never the merged or
// workspace config, so workspace scope stays inexpressible by construction for `packs`,
// `host_files` and `cache_relocations`. Neither addition is a workspace-reachable channel —
// one is an argv, the other is a file the HOST generated and mounted :ro.
func loadUserScopeConfig(path, label string, strict bool, warn Warn) (*jsonx.OrderedMap, error) {
	cfg, err := LoadJSONCWithIncludes(path, label, strict, warn, nil)
	if err != nil {
		return nil, err
	}
	return applyUserLayer(applyInheritedLaunch(cfg)), nil
}

// InheritedLaunchPath is the nested-launch file a jail's host wrote beside its user config:
// $HOME/.config/yolo-jail/inherited-launch.jsonc. Empty outside a jail — the file only ever
// exists inside one, and looking for it on the host would invent a config location.
//
// Kept beside the layer logic because the two are the same shape of thing: an ADDITIONAL
// user-level input that the ordinary user-scope read has to fold in, or the readers that
// bypass the merged config would never see it.
func InheritedLaunchPath() string {
	if !inJail() {
		return ""
	}
	return filepath.Join(filepath.Dir(paths.UserConfigPath()), "inherited-launch.jsonc")
}

// applyInheritedLaunch merges the inherited nested-launch file UNDER config.jsonc.
//
// WHY THIS EXISTS, and it was a real defect caught by running two real nesting levels: the
// host writes this file (run/inheritscope.go) but nothing read it, so its keys —
// `packages`, `env_sources`, `resources`, `network`, and the rest of the launch composition —
// reached a jail and stopped there. Measured: at depth 2 the nested file had LOST `packages`
// and `env_sources` relative to depth 1, because depth 1's effective config never contained
// them. That is precisely the "a rule changes with nesting" failure R6 forbids, and it made
// R2's whole file inert.
//
// PRECEDENCE: UNDER config.jsonc, not over it. The inherited file is what the OUTER scope
// handed down; a jail's own config.jsonc (and any --user-layer on top) is the more local
// statement and must win — the same direction as user-under-workspace one level up.
func applyInheritedLaunch(base *jsonx.OrderedMap) *jsonx.OrderedMap {
	path := InheritedLaunchPath()
	if path == "" {
		return base
	}
	inherited, err := LoadJSONCFile(path, "inherited launch config", false, func(string) {})
	if err != nil || inherited == nil || inherited.Len() == 0 {
		return base
	}
	// The inherited file is the one config read OUTSIDE LoadJSONCWithIncludes, so it
	// anchors itself: its relative env_sources entries resolve beside it, which is the
	// same directory the user config's would have. (Entries usually arrive absolute
	// already — the parent launch's loader anchored them before writing the file.)
	AnchorEnvSources(inherited, filepath.Dir(path))
	if base == nil {
		return inherited
	}
	return MergeConfig(inherited, base)
}
