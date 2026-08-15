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

// loadUserScopeConfig is THE user-scope read: config.jsonc plus its includes, plus any
// --user-layer, in that precedence.
//
// Every user-scope reader goes through this, which is the invariant that makes the flag
// trustworthy. The direct-read boundary those callers depend on is preserved exactly: this
// still reads paths.UserConfigPath() and never the merged or workspace config, so workspace
// scope stays inexpressible by construction for `packs`, `host_files` and
// `cache_relocations`. The layer is not a workspace-reachable channel — it is an argv, and
// an argv is not the repo.
func loadUserScopeConfig(path, label string, strict bool, warn Warn) (*jsonx.OrderedMap, error) {
	cfg, err := LoadJSONCWithIncludes(path, label, strict, warn, nil)
	if err != nil {
		return nil, err
	}
	return applyUserLayer(cfg), nil
}
