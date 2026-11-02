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
// The PER-WORKSPACE FILE (workspacefile.go) is not that file coming back, and the maintainer
// ruled for it (docs/design/boundary-broker.md OQ-BB12). It is named by the one workspace it
// governs rather than merged into every launch, holds loophole switches and refuses every other
// key, is written by a command whose output names it, and `yolo check` names it too.
//
// WHY THERE IS NO APPROVAL GATE ON IT, and this is a ruling rather than an omission
// (docs/reference/gate-placement-principle.md Test 1 — the authority test): passing an argv to
// `yolo` requires the ability to run commands, which already exceeds anything the argument
// grants. An actor who can pass `--user-layer` can equally write the user config file, or
// run the daemon directly. A gate here would refuse an actor who has already passed a
// stronger one — pure ceremony, and the kind that teaches people to click through prompts.
// So: no prompt, on the host and in a jail alike.
//
// AND IT IS THE NESTED-DEVELOPMENT PATH (Test 2 — the blast-radius test). The placement
// rule's ruling deleted the dev-friction escape hatch and sends loophole development into
// a nested jail instead — and that has nowhere to send anyone unless an in-jail agent can
// INSTALL a loophole, which means writing something at user scope. With this flag it can:
// write a layer file in its
// own home, pass --user-layer, launch. What it thereby gains authority over is a container
// it can throw away — jail A is the blast radius, and nothing reaches the human's host.
// The single-file delivery of the inherited scope (run/inheritscope.go) is the other half:
// the DIRECTORY the inherited file sits in is the jail's own, so writing beside it is
// jail-local by construction.
//
// ⚠ The dev-friction half of that ruling did NOT graduate into
// docs/reference/loophole-system.md#the-placement-rule, which keeps the placement rule but
// not the argument for where a loophole is developed. It lives in git history only:
// `git show 9190a4d1^:docs/design/loophole-packaging.md`, under the ruled placement rule —
// "the escape hatch draft 3 wanted was separately RULED AGAINST: develop the loophole in a
// jail instead."
//
// VERIFIED END TO END in a real nested container, 2026-08-14, and it works: an in-jail agent
// wrote a pack shipping a loophole, wrote a layer naming it, and `pack install`,
// `loopholes list` (dev-probe active, pack/none), `check` ("devpack: 2 file(s) stage") and a
// NESTED launch all saw it.
//
// ONE CONSTRAINT worth knowing, because it is the first thing an agent will trip over: the
// jail's home ROOT is mounted :ro, so `mkdir ~/mypack` fails. The pack has to go somewhere
// writable — `~/.local/share/...` is the natural home (it is one of the rw anchors), or the
// workspace. That is jail-home policy (docs/reference/jail-home.md), not a limit of this flag,
// and the layer file itself lands fine because ~/.config is writable — which is exactly the
// R8 property doing its job.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
		if twin := convertedLayerTwin(path); twin != "" && os.IsNotExist(err) {
			return "--user-layer: " + path + " is not there; `yolo config migrate` converted it to " + twin +
				". Pass `--user-layer " + twin + "` instead"
		}
		return "--user-layer: cannot read " + path + ": " + err.Error() +
			" (an explicitly-passed layer is never skipped silently)"
	}
	if fi.IsDir() {
		return "--user-layer: " + path + " is a directory; pass a single JSONC or TOML config file"
	}
	if _, _, err := loadWithIncludesScope(abs, "--user-layer "+path, true, nil, nil, false, SchemaScopeUserOnly, readAt{}); err != nil {
		return err.Error()
	}
	return ""
}

// userLayerIsTOML is whether this invocation's --user-layer is a TOML file.
func userLayerIsTOML() bool {
	return strings.EqualFold(filepath.Ext(UserLayerPath()), ".toml")
}

// convertedLayerTwin is the TOML twin of a JSONC layer path that is not there, spelled the way
// the path was given (work.jsonc → work.toml), when that twin exists; "" otherwise. `yolo config
// migrate FILE` keeps a converted layer's original only as <name>.pre-migrate, so a command still
// naming the old file is told the new one.
func convertedLayerTwin(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".jsonc" && ext != ".json" {
		return ""
	}
	twin := path[:len(path)-len(ext)] + ".toml"
	abs := twin
	if !filepath.IsAbs(abs) {
		if wd, err := os.Getwd(); err == nil {
			abs = filepath.Join(wd, abs)
		}
	}
	if fi, err := os.Stat(abs); err != nil || fi.IsDir() {
		return ""
	}
	return twin
}

// UserScopeConfig is the standard format-aware user-scope read: the selected user file,
// includes, config.d fragments, inherited launch layer and explicit --user-layer.
func UserScopeConfig(strict bool, warn Warn) (*jsonx.OrderedMap, error) {
	return LoadReadableUserScopeConfig(strict, warn)
}

// LoadReadableUserScopeConfig reads the selected user format, relative includes, sorted TOML
// config.d fragments, inherited launch layer and explicit --user-layer.
func LoadReadableUserScopeConfig(strict bool, warn Warn) (*jsonx.OrderedMap, error) {
	p := paths.UserConfigPath()
	m, _, err := loadReadableUserScope(p, strict, warn, false)
	return m, err
}

// LoadReadableUserScopeConfigWithSources adds source positions to the readable user graph.
func LoadReadableUserScopeConfigWithSources(strict bool, warn Warn) (*jsonx.OrderedMap, *Sources, error) {
	p := paths.UserConfigPath()
	m, node, err := loadReadableUserScope(p, strict, warn, true)
	return m, sourcesOf(node), err
}

// UserScopeConfigWithSources is UserScopeConfig plus where each value was written
// (sources.go), for a caller that reports validation problems over the user scope.
func UserScopeConfigWithSources(strict bool, warn Warn) (*jsonx.OrderedMap, *Sources, error) {
	return LoadReadableUserScopeConfigWithSources(strict, warn)
}

// UserScopeSources is the provenance of the user scope UserScopeConfigOrEmpty reads, nil
// when it cannot be read: for a caller that already holds that config, validated it, and
// wants the refusals located. It reads the files again, so it belongs on an error path.
func UserScopeSources() *Sources {
	_, src, err := UserScopeConfigWithSources(false, func(string) {})
	if err != nil {
		return nil
	}
	return src
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
	m, _, err := loadUserScope(path, label, strict, warn, false)
	return m, err
}

// loadUserScope is loadUserScopeConfig with the provenance (sources.go) beside the map when
// record is set.
func loadUserScope(path, label string, strict bool, warn Warn, record bool) (*jsonx.OrderedMap, *srcNode, error) {
	return loadReadableUserScope(path, strict, warn, record)
}

func loadReadableUserScope(path string, strict bool, warn Warn, record bool) (*jsonx.OrderedMap, *srcNode, error) {
	return loadReadableUserScopeWithReadInput(path, strict, warn, record, nil)
}

func loadReadableUserScopeWithReadInput(path string, strict bool, warn Warn, record bool, input *ConfigReadInput) (*jsonx.OrderedMap, *srcNode, error) {
	return loadReadableUserScopeWithInputUse(path, strict, warn, record, input, nil)
}

func loadReadableUserScopeWithInputUse(path string, strict bool, warn Warn, record bool, input *ConfigReadInput, inputUsed *bool) (*jsonx.OrderedMap, *srcNode, error) {
	selectedPath, selectedLabel, readable, err := resolveUserConfigPath(path)
	if err != nil {
		return nil, nil, err
	}
	seen := map[string]struct{}{}
	cfg, node, err := loadWithIncludesScope(selectedPath, selectedLabel, strict, warn, seen, record, SchemaScopeUserOnly, readAt{input: input, inputUsed: inputUsed, role: editRoleUser, witness: currentEditWitness(input)})
	if err != nil {
		return nil, nil, err
	}
	if readable {
		cfg, node, err = mergeUserConfigFragmentsWithInputUse(cfg, node, filepath.Dir(selectedPath), strict, warn, seen, record, input, inputUsed)
		if err != nil {
			return nil, nil, err
		}
	}
	cfg, node, err = applyInheritedLaunchWithStrictInputUse(cfg, node, record, true, strings.EqualFold(filepath.Ext(selectedPath), ".toml"), input, inputUsed, strict)
	if err != nil {
		return nil, nil, err
	}
	cfg, node, err = applyReadableUserLayerWithInputUse(cfg, node, readable && cfg != nil && cfg.Len() > 0, record, input, inputUsed)
	if err != nil {
		return nil, nil, err
	}
	return cfg, node, nil
}

// readableBase is whether the user config the layer applies over is the TOML one: a legacy JSONC
// layer over it is read in its spelling (projectLegacyLayerOverReadable).
func applyReadableUserLayerWithInputUse(base *jsonx.OrderedMap, baseNode *srcNode, readableBase, record bool, input *ConfigReadInput, inputUsed *bool) (*jsonx.OrderedMap, *srcNode, error) {
	path := UserLayerPath()
	if path == "" {
		return base, baseNode, nil
	}
	layer, layerNode, err := loadWithIncludesScope(path, "--user-layer "+path, false, func(string) {}, nil, record, SchemaScopeUserOnly, readAt{input: input, inputUsed: inputUsed, role: editRoleUser, witness: currentEditWitness(input)})
	if err != nil {
		return nil, nil, err
	}
	if layer == nil || layer.Len() == 0 {
		return base, baseNode, nil
	}
	if base == nil {
		base = jsonx.NewOrderedMap()
	}
	layerIsTOML := strings.EqualFold(filepath.Ext(path), ".toml")
	if readableBase && !layerIsTOML {
		layer, layerNode = projectLegacyLayerOverReadable(layer, layerNode, base)
	}
	merged, mergedNode := mergeReadableConfigMode(base, layer, baseNode, layerNode, nil, layerIsTOML)
	return merged, mergedNode, nil
}

// InheritedLaunchPath is the nested-launch file a jail's host wrote beside its user config:
// inherited-launch.toml when the current launch can write readable TOML, otherwise the retained
// legacy JSONC file. Empty outside a jail — the file only ever exists inside one.
//
// Kept beside the layer logic because the two are the same shape of thing: an ADDITIONAL
// user-level input that the ordinary user-scope read has to fold in, or the readers that
// bypass the merged config would never see it.
func InheritedLaunchPath() string {
	if !inJail() {
		return ""
	}
	dir := filepath.Dir(paths.UserConfigPath())
	toml := filepath.Join(dir, "inherited-launch.toml")
	if pathExists(toml) {
		return toml
	}
	return filepath.Join(dir, "inherited-launch.jsonc")
}

func inheritedLaunchPathForRead(strict bool) (string, error) {
	if !strict {
		return InheritedLaunchPath(), nil
	}
	if !inJail() {
		return "", nil
	}
	dir := filepath.Dir(paths.UserConfigPath())
	var found []string
	for _, name := range []string{"inherited-launch.toml", "inherited-launch.jsonc", "inherited-launch.json"} {
		path := filepath.Join(dir, name)
		if _, err := os.Lstat(path); err == nil {
			found = append(found, path)
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("cannot inspect inherited user config %s: %w; preserve the file and restore a readable config directory before continuing", path, err)
		}
	}
	if len(found) > 1 {
		return "", fmt.Errorf("inherited-launch siblings %s exist; these formats are never merged. Preserve their contents, choose exactly one file to keep active, then rerun the command", strings.Join(found, ", "))
	}
	if len(found) == 1 {
		return found[0], nil
	}
	return filepath.Join(dir, "inherited-launch.jsonc"), nil
}

func applyInheritedLaunchWithStrictInputUse(base *jsonx.OrderedMap, baseNode *srcNode, record, readable, clearEmptyLists bool, input *ConfigReadInput, inputUsed *bool, strict bool) (*jsonx.OrderedMap, *srcNode, error) {
	path, err := inheritedLaunchPathForRead(strict)
	if err != nil {
		return nil, nil, err
	}
	if path == "" {
		return base, baseNode, nil
	}
	nestedReadable := strings.EqualFold(filepath.Ext(path), ".toml")
	inherited, _, inheritedNode, err := loadConfigFile(path, path, strict, func(string) {}, record, SchemaScopeUserOnly, readAt{input: input, inputUsed: inputUsed, role: editRoleInherited, witness: currentEditWitness(input)})
	if err != nil {
		if strict {
			return nil, nil, err
		}
		return base, baseNode, nil
	}
	if inherited == nil || inherited.Len() == 0 {
		return base, baseNode, nil
	}
	// The inherited file is the one config read OUTSIDE LoadJSONCWithIncludes, so it
	// anchors itself: its relative env files resolve beside it. Legacy JSONC inherits the
	// ordinary anchors; TOML has already crossed through the readable projection.
	readableMerge := readable || nestedReadable
	if readableMerge {
		if !nestedReadable {
			inherited, inheritedNode = projectLegacyB2Config(inherited, inheritedNode)
		}
		if nestedReadable && base != nil {
			base, baseNode = projectLegacyB2Config(base, baseNode)
		}
		anchorReadableEnvSources(inherited, filepath.Dir(path))
	} else {
		AnchorEnvSources(inherited, filepath.Dir(path))
	}
	if base == nil {
		return inherited, inheritedNode, nil
	}
	if readableMerge {
		merged, mergedNode := mergeReadableConfigMode(inherited, base, inheritedNode, baseNode, nil, clearEmptyLists || nestedReadable)
		return merged, mergedNode, nil
	}
	merged, mergedNode := mergeConfig(inherited, base, inheritedNode, baseNode)
	return merged, mergedNode, nil
}
