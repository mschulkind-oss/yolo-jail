package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// layerHome sets up a scratch HOME with a user config and returns the home.
func layerHome(t *testing.T, userConfig string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if userConfig != "" {
		if err := os.WriteFile(filepath.Join(dir, "config.jsonc"), []byte(userConfig), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

// writeLayer writes a layer file and points the loader at it.
func writeLayer(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "layer.jsonc")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(UserLayerEnv, p)
	return p
}

// The layer sits at USER-LEVEL precedence: it wins over config.jsonc (a layer that lost to
// the file it adjusts could not adjust anything) and a WORKSPACE config still wins over it
// (the flag layers in AS user-level, it does not promote itself above the workspace).
func TestUserLayerPrecedence(t *testing.T) {
	home := layerHome(t, `{"agents_md_extra": "from the user file\n", "journal": "off"}`)
	_ = home
	writeLayer(t, `{"agents_md_extra": "from the layer\n"}`)

	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, WorkspaceConfigName),
		[]byte(`{"journal": "user"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(ws, false, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := cfg.Get("agents_md_extra"); got != "from the layer\n" {
		t.Errorf("agents_md_extra = %v — the layer must win over the user config file", got)
	}
	if got, _ := cfg.Get("journal"); got != "user" {
		t.Errorf("journal = %v — the WORKSPACE config must still win over a user-level layer", got)
	}
}

// THE KEY THE LAYER EXISTS FOR (OQ-LP9 R5). `packs` is read from the user file DIRECTLY, not
// from the merged config — that direct read is its security boundary — so a layer that only
// reached the merged map would be useless for exactly the case the design cites: an in-jail
// agent installing a loophole, which means naming the pack that carries it.
//
// This is why the layer travels through the loader rather than as a parameter: the three
// direct-read keys (packs, host_files, cache_relocations) each have their own reader, and a
// threaded parameter would reach only the ones someone remembered to update.
func TestUserLayerReachesTheDirectReadPacksKey(t *testing.T) {
	layerHome(t, `{}`)
	writeLayer(t, `{"packs": ["claude"]}`)

	entries, err := LoadPacks(nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if e.Name == "claude" {
			found = true
		}
	}
	if !found {
		t.Errorf("a --user-layer `packs` entry did not reach LoadPacks (entries=%v) — so an "+
			"in-jail agent cannot install a pack-shipped loophole, which is the whole "+
			"nested-development path OQ-LP9 R5 depends on", entries)
	}
}

// And the boundary the layer must NOT breach: a workspace config still cannot express
// `packs`. The layer is an ARGV, not the repo — an agent that can pass it could already edit
// the user file, whereas a committed workspace config travels with the repo. So adding the
// layer must not have widened the direct-read rule.
func TestUserLayerDoesNotMakeWorkspaceScopeExpressible(t *testing.T) {
	layerHome(t, `{}`)
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, WorkspaceConfigName),
		[]byte(`{"packs": ["claude"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// No layer set.
	t.Setenv(UserLayerEnv, "")

	entries, err := LoadPacks(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name == "claude" && !e.Implicit {
			t.Error("a WORKSPACE `packs` entry was honored — the direct-read boundary broke")
		}
	}
	var errs []string
	validatePacks(ws, &errs)
	if len(errs) == 0 {
		t.Error("a workspace `packs` key must still be a `yolo check` error")
	}
}

// `loopholes` through the layer, which is the other half of R5: having installed the pack, the
// agent enables/configures the loophole and the in-jail commands must SEE it in the same
// invocation. UserScopeConfig is what `yolo loopholes` reads, so this pins the path those
// commands take rather than a generic merge.
func TestUserLayerReachesTheLoopholesCommandsView(t *testing.T) {
	layerHome(t, `{}`)
	writeLayer(t, `{"loopholes": {"acme": {"enabled": true}}}`)

	cfg, err := UserScopeConfig(false, nil)
	if err != nil {
		t.Fatal(err)
	}
	v, present := cfg.Get("loopholes")
	if !present {
		t.Fatal("a --user-layer `loopholes` block did not reach UserScopeConfig — " +
			"`yolo loopholes list` would not show what the agent just declared")
	}
	m, ok := v.(interface{ Keys() []string })
	if !ok {
		t.Fatalf("loopholes is %T, want a mapping", v)
	}
	if len(m.Keys()) != 1 || m.Keys()[0] != "acme" {
		t.Errorf("loopholes keys = %v, want [acme]", m.Keys())
	}
}

// The layer survives into the GENERATED INNER SCOPE, which is what makes R6's recursion
// claim true for the layer as well: jail A composes inherited + its layer, and hands the
// RESULT to B. B therefore sees one inherited file, not a chain — so a loophole an in-jail
// agent declared via --user-layer is inherited by the jail it launches.
func TestUserLayerFlowsIntoTheGeneratedInnerScope(t *testing.T) {
	layerHome(t, `{"packs": ["claude"]}`)
	writeLayer(t, `{"loopholes": {"acme": {"enabled": true}}}`)

	effective, err := LoadConfig(t.TempDir(), false, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	filtered, unknown := FilterInherit(effective, InheritPreflight)
	if len(unknown) > 0 {
		t.Fatalf("unknown keys: %v", unknown)
	}
	if _, present := filtered.Get("loopholes"); !present {
		t.Error("a layered `loopholes` block did not reach the generated inner scope — the " +
			"jail this one launches would not inherit the loophole the agent just declared")
	}
	if _, present := filtered.Get("packs"); !present {
		t.Error("the layer displaced the user file's `packs` instead of merging over it")
	}
}

// No layer set = no behaviour change anywhere. The flag is INERT unless passed, which is the
// property that distinguishes it from the withdrawn conventional-filename design.
func TestNoLayerIsInert(t *testing.T) {
	layerHome(t, `{"agents_md_extra": "just the file\n"}`)
	t.Setenv(UserLayerEnv, "")

	cfg, err := UserScopeConfig(false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := cfg.Get("agents_md_extra"); got != "just the file\n" {
		t.Errorf("agents_md_extra = %v with no layer set", got)
	}
	if UserLayerPath() != "" {
		t.Error("UserLayerPath must be empty with no layer set")
	}
}

// IN-JAIL, THE SNAPSHOT SHORT-CIRCUIT MUST YIELD TO THE LAYER. LoadConfig normally returns
// the host-written config-assembled.json verbatim for a jail's own workspace — a FROZEN
// artifact of a previous launch, which cannot contain a layer passed to THIS invocation. If
// it won, `yolo --user-layer x.jsonc check` would silently ignore the file the caller named,
// which is the exact invisibility the flag exists to avoid.
func TestUserLayerBeatsTheInJailSnapshotShortCircuit(t *testing.T) {
	layerHome(t, `{}`)
	ws := t.TempDir()
	t.Setenv("YOLO_VERSION", "9.9.9-test") // inJail()
	t.Setenv("YOLO_WORKSPACE", ws)         // this jail's OWN workspace
	if err := os.MkdirAll(filepath.Join(ws, ".yolo"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A snapshot that does NOT contain the layered key — a previous launch's frozen view.
	if err := os.WriteFile(WorkspaceAssembledConfigPath(ws),
		[]byte(`{"agents_md_extra": "from the frozen snapshot\n"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Without a layer the short-circuit stands (that behaviour is load-bearing: it is what
	// keeps host-only include_if_found overrides visible in-jail).
	t.Setenv(UserLayerEnv, "")
	cfg, err := LoadConfig(ws, false, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := cfg.Get("agents_md_extra"); got != "from the frozen snapshot\n" {
		t.Fatalf("without a layer the snapshot must still short-circuit; got %v", got)
	}

	// With a layer, the layer is visible.
	writeLayer(t, `{"packs": ["claude"]}`)
	cfg, err = LoadConfig(ws, false, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if _, present := cfg.Get("packs"); !present {
		t.Error("the frozen snapshot swallowed the --user-layer — an explicitly-named file " +
			"was silently ignored, which is worse than having no flag")
	}
}

// THE DEFECT TWO REAL NESTING LEVELS FOUND (OQ-LP9 R2/R6). The host writes the
// nested-launch file, and for a while NOTHING READ IT — so `packages`, `env_sources`,
// `resources` and `network` reached a jail and stopped there. Measured in a real two-level
// nested run: the file at depth 2 had LOST `packages` and `env_sources` relative to depth 1,
// because depth 1's effective config never contained them. That is exactly the "a rule
// changes with nesting" failure R6 forbids, and it made R2's file inert.
//
// This is the unit regression: with an inherited-launch file present, its keys must be in
// the user scope, and re-filtering must therefore preserve them at the next level.
func TestInheritedLaunchFileIsActuallyRead(t *testing.T) {
	home := layerHome(t, `{"packs": ["claude"]}`)
	t.Setenv(UserLayerEnv, "")
	t.Setenv("YOLO_VERSION", "9.9.9-test") // the file only exists inside a jail
	if err := os.WriteFile(
		filepath.Join(home, ".config", "yolo-jail", "inherited-launch.jsonc"),
		[]byte(`{"packages": ["hello"], "env_sources": ["~/.x.env"]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := UserScopeConfig(false, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"packages", "env_sources"} {
		if _, present := cfg.Get(want); !present {
			t.Errorf("%q from the inherited-launch file did not reach the user scope — the "+
				"file the host generated for nesting would be inert, and the next level down "+
				"would silently lose the key", want)
		}
	}
	// And the round trip: re-filtering the effective config must still carry them, which is
	// what makes depth N identical to depth 1.
	filtered, _ := FilterInherit(cfg, InheritNested)
	for _, want := range []string{"packages", "env_sources", "packs"} {
		if _, present := filtered.Get(want); !present {
			t.Errorf("%q was lost when re-filtering for the next nesting level", want)
		}
	}
}

// The inherited file sits UNDER the jail's own config.jsonc: what the outer scope handed
// down loses to the more local statement, the same direction as user-under-workspace one
// level up. Otherwise an in-jail edit could never override anything it inherited.
func TestInheritedLaunchFileLosesToTheJailsOwnConfig(t *testing.T) {
	home := layerHome(t, `{"packages": ["local-wins"]}`)
	t.Setenv(UserLayerEnv, "")
	t.Setenv("YOLO_VERSION", "9.9.9-test")
	if err := os.WriteFile(
		filepath.Join(home, ".config", "yolo-jail", "inherited-launch.jsonc"),
		[]byte(`{"resources": {"memory": "8g"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := UserScopeConfig(false, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The inherited-only key survives...
	if _, present := cfg.Get("resources"); !present {
		t.Error("an inherited-only key was dropped")
	}
	// ...and the local file's own value is present (list keys union-merge by
	// MergeConfig's contract, so the assertion is presence of the local entry).
	pkgs, _ := cfg.Get("packages")
	list, ok := pkgs.([]any)
	if !ok || len(list) == 0 || list[0] != "local-wins" {
		t.Errorf("packages = %v — the jail's own config must lead over what it inherited", pkgs)
	}
}

// Outside a jail there is no inherited-launch file, and yolo must not go looking for one:
// inventing a second host-side user-config location is exactly the accident this design
// rejected for `config.local.jsonc`.
func TestInheritedLaunchIsJailOnly(t *testing.T) {
	home := layerHome(t, `{"packs": ["claude"]}`)
	t.Setenv("YOLO_VERSION", "")
	if err := os.WriteFile(
		filepath.Join(home, ".config", "yolo-jail", "inherited-launch.jsonc"),
		[]byte(`{"packages": ["should-not-be-read"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := InheritedLaunchPath(); got != "" {
		t.Errorf("InheritedLaunchPath() = %q on the host, want \"\"", got)
	}
	cfg, err := UserScopeConfig(false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := cfg.Get("packages"); present {
		t.Error("an inherited-launch file was read on the HOST — that file is a jail artifact, " +
			"and reading it off-container would invent a second user-config location")
	}
}

// ValidateUserLayer accepts a relative path (resolved against the cwd), because that is how
// an agent will type it: it writes ./layer.jsonc in its own home and passes what it typed.
func TestValidateUserLayerAcceptsARelativePath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "layer.jsonc"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	if msg := ValidateUserLayer("layer.jsonc"); msg != "" {
		t.Errorf("a relative layer path was refused: %s", msg)
	}
}

// An empty argument is "no layer", not an error — applyUserLayerFlag only calls this with a
// non-empty path, and the guard keeps a direct caller from having to special-case it.
func TestValidateUserLayerEmptyIsNoLayer(t *testing.T) {
	if msg := ValidateUserLayer(""); msg != "" {
		t.Errorf("empty path should validate as no-layer, got %q", msg)
	}
}

// The layer is read from the path given, not from anywhere near the user config dir — i.e.
// this is NOT a conventional-filename mechanism wearing a flag. Pinned because that is the
// design that was withdrawn with cause.
func TestUserLayerIsNotAConventionalSibling(t *testing.T) {
	home := layerHome(t, `{}`)
	// A file at the conventional-looking location must have NO effect unless passed.
	sibling := filepath.Join(filepath.Dir(paths.UserConfigPath()), "config.local.jsonc")
	if err := os.WriteFile(sibling, []byte(`{"packs": ["claude"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = home
	t.Setenv(UserLayerEnv, "")

	cfg, err := UserScopeConfig(false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := cfg.Get("packs"); present {
		t.Error("a conventionally-named sibling file was auto-merged — that mechanism was " +
			"WITHDRAWN WITH CAUSE (it activates because a file exists, invisibly at the call " +
			"site). Only an explicit --user-layer may layer a file in.")
	}
}
