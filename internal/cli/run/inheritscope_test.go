package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// inheritHome sets up a scratch HOME with a user config, and returns (home, wsState).
// wsState is a real directory because the generated files are STAGED there — a test using a
// fictional path would silently exercise the write-failure branch and prove nothing (which
// is exactly what the frozen golden argv test was doing before keys==0 made an empty scope
// emit no file at all).
func inheritHome(t *testing.T, userConfig string) (string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if userConfig != "" {
		if err := os.WriteFile(filepath.Join(cfgDir, "config.jsonc"), []byte(userConfig), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	wsState := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(wsState, 0o755); err != nil {
		t.Fatal(err)
	}
	return home, wsState
}

// inheritOptions is an Options with a fixed clock (so the header timestamp is pinnable) and
// a workspace with no config of its own.
func inheritOptions(t *testing.T) *Options {
	t.Helper()
	return &Options{
		Workspace: t.TempDir(),
		Stdout:    &strings.Builder{},
		Now:       func() time.Time { return time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC) },
	}
}

// mountedBody reads back the file a `-v src:dst:ro` arg points at, so a test asserts what
// the JAIL WILL SEE rather than what the renderer returned. Returns the raw bytes and the
// host-side source path.
func mountedBody(t *testing.T, args []string, containerPath string) (body, src string, ok bool) {
	t.Helper()
	for i := 0; i+1 < len(args); i++ {
		if args[i] != "-v" {
			continue
		}
		parts := strings.Split(args[i+1], ":")
		if len(parts) < 2 || parts[1] != containerPath {
			continue
		}
		data, err := os.ReadFile(parts[0])
		if err != nil {
			t.Fatalf("mount source %q is not readable: %v", parts[0], err)
		}
		return string(data), parts[0], true
	}
	return "", "", false
}

// mountedKeys parses a delivered file THROUGH THE JAIL'S OWN LOADER and returns its
// top-level keys.
//
// Parsing rather than substring-matching, and the reason is a real trap this caught: the
// generated header explains WHY a key was filtered, so it legitimately contains the words
// "host_files" and "cache_relocations". A substring assertion therefore reports the
// explanation as a leak — the test fails while the code is correct. Keys are what an in-jail
// reader actually evaluates, so keys are what to assert on.
func mountedKeys(t *testing.T, args []string, containerPath string) ([]string, bool) {
	t.Helper()
	_, src, ok := mountedBody(t, args, containerPath)
	if !ok {
		return nil, false
	}
	parsed, err := config.LoadJSONCFile(src, containerPath, true, nil)
	if err != nil {
		t.Fatalf("the delivered file does not parse through the ordinary config loader "+
			"(every in-jail reader would fail): %v", err)
	}
	return parsed.Keys(), true
}

// hasKey reports membership in a key list.
func hasInheritKey(keys []string, want string) bool {
	for _, k := range keys {
		if k == want {
			return true
		}
	}
	return false
}

// THE FALSE-ERROR CLASS, at the delivery seam (OQ-LP9 R1). The old arrangement bound the
// human's real config.jsonc into the jail verbatim, so every host-only key crossed. What a
// jail sees now must carry `packs` and `loopholes` and must NOT carry the host-referent keys
// — measured 2026-08-14 to make an in-jail `yolo check` report problems the user does not
// have (gpu → four driver fails, cache_relocations → a missing target parent, mounts → a
// warning per entry).
func TestInheritedPreflightFileDropsHostOnlyKeys(t *testing.T) {
	_, wsState := inheritHome(t, `{
	  "packs": ["claude"],
	  "loopholes": {"acme": {"command": ["/opt/acmed"], "jail_endpoint": "/run/yolo-services/acme.sock"}},
	  "cache_relocations": {"npm": "/mnt/bigdisk/caches/npm"},
	  "gpu": {"enabled": true},
	  "mounts": ["/Volumes/bigdisk:/ctx/data:ro"],
	  "host_files": [{"path": ".config/acme/creds", "source": "~/.config/acme/creds"}],
	  "kvm": true
	}`)
	o := inheritOptions(t)

	args := o.userConfigMountArgs("podman", wsState, map[string]struct{}{})
	keys, found := mountedKeys(t, args, "/home/agent/"+inheritPreflightRel)
	if !found {
		t.Fatalf("no preflight user config was mounted; args: %v", args)
	}
	for _, banned := range []string{"cache_relocations", "gpu", "mounts", "host_files", "kvm"} {
		if hasInheritKey(keys, banned) {
			_, _, reason, _ := config.InheritDisposition(banned)
			t.Errorf("%q crossed into the jail's user scope — it is excluded because %s. An "+
				"in-jail `yolo check` will evaluate a host referent that does not exist in a "+
				"container. keys=%v", banned, reason, keys)
		}
	}
	for _, want := range []string{"packs", "loopholes"} {
		if !hasInheritKey(keys, want) {
			t.Errorf("%q did not cross — the in-jail commands that read it are blind. keys=%v",
				want, keys)
		}
	}
}

// R8: SINGLE-FILE delivery into a jail-owned directory. This is the property that made the
// old arrangement safe and the one the design says must survive — it is what makes writing
// BESIDE the inherited file (a --user-layer) jail-local rather than a reach at the host.
// Mounting the DIRECTORY would take it away silently, so pin it.
func TestInheritedScopeIsMountedPerFileNotAsADirectory(t *testing.T) {
	_, wsState := inheritHome(t, `{"packs": ["claude"]}`)
	o := inheritOptions(t)

	args := o.userConfigMountArgs("podman", wsState, map[string]struct{}{})
	if len(args) == 0 {
		t.Fatal("no mounts emitted for a config with a packs key")
	}
	for i := 0; i+1 < len(args); i += 2 {
		spec := args[i+1]
		parts := strings.Split(spec, ":")
		if len(parts) < 2 {
			t.Fatalf("malformed mount spec %q", spec)
		}
		src, dst := parts[0], parts[1]
		if dst == "/home/agent/.config/yolo-jail" || dst == "/home/agent/.config" {
			t.Fatalf("the config DIRECTORY was mounted (%q) — writing beside the inherited "+
				"file would then land on the host mount instead of the jail's own home, "+
				"which is the property R8 requires survive", spec)
		}
		fi, err := os.Stat(src)
		if err != nil {
			t.Fatalf("mount source %q does not exist — podman dies on a missing bind source", src)
		}
		if fi.IsDir() {
			t.Errorf("mount source %q is a directory; the inherited scope must be single-file", src)
		}
		if !strings.HasSuffix(spec, ":ro") {
			t.Errorf("the inherited scope must be read-only; got %q", spec)
		}
	}
}

// R2: the nested-launch file exists ONLY where nesting is possible — absent by construction
// on a backend that cannot nest, not written-then-ignored. `container` (Apple Container) and
// `macos-user` cannot nest; podman can (the image bakes a nested podman and the CLI has a
// whole podman-in-podman branch).
func TestNestedLaunchFileOnlyOnANestingBackend(t *testing.T) {
	for _, tc := range []struct {
		rt       string
		wantFile bool
	}{
		{"podman", true},
		{"container", false},
		{"macos-user", false},
	} {
		_, wsState := inheritHome(t, `{"packages": ["postgresql"], "packs": ["claude"]}`)
		o := inheritOptions(t)
		args := o.userConfigMountArgs(tc.rt, wsState, map[string]struct{}{})

		// On `container` nothing is bind-mounted (the whole wsState becomes /home/agent),
		// so look for the materialized file in the tree instead of in the argv.
		staged := filepath.Join(wsState, "inherit-nested.jsonc")
		materialized := filepath.Join(wsState, inheritNestedRel)
		_, stagedErr := os.Stat(staged)
		_, matErr := os.Stat(materialized)
		mounted := strings.Contains(strings.Join(args, " "), inheritNestedRel)
		got := stagedErr == nil || matErr == nil || mounted

		if got != tc.wantFile {
			t.Errorf("rt=%s: nested-launch file present = %v, want %v — on a backend that "+
				"cannot nest the file must be ABSENT, which is how the design answers the "+
				"\"this excludes some setups\" objection by construction rather than with a "+
				"conditional", tc.rt, got, tc.wantFile)
		}
	}
}

// The nested file carries the launch composition and the preflight file does not, at the
// delivery seam. `packages` is the case: nothing in-jail validates it (an in-jail
// `yolo check --no-build` skips the image section), and an inner launcher bakes it.
func TestTheTwoDeliveredFilesCarryDifferentKeys(t *testing.T) {
	_, wsState := inheritHome(t, `{"packages": ["postgresql"], "agents_md_extra": "hi\n", "packs": ["claude"]}`)
	o := inheritOptions(t)
	args := o.userConfigMountArgs("podman", wsState, map[string]struct{}{})

	preKeys, okPre := mountedKeys(t, args, "/home/agent/"+inheritPreflightRel)
	nestKeys, okNest := mountedKeys(t, args, "/home/agent/"+inheritNestedRel)
	if !okPre || !okNest {
		t.Fatalf("expected both files; preflight=%v nested=%v; args=%v", okPre, okNest, args)
	}
	if hasInheritKey(preKeys, "packages") {
		t.Error("`packages` reached the PREFLIGHT file — nothing in-jail validates it, so it " +
			"only invites a judgement about a host image")
	}
	if !hasInheritKey(nestKeys, "packages") {
		t.Error("`packages` missing from the NESTED file — an inner launcher bakes its image from it")
	}
	if hasInheritKey(nestKeys, "agents_md_extra") {
		t.Error("`agents_md_extra` reached the NESTED file — it is prose for THIS jail's briefing")
	}
	// Both must be self-describing: a reader with no design doc has to be able to tell which
	// consumer a file serves and why a key is missing.
	pre, _, _ := mountedBody(t, args, "/home/agent/"+inheritPreflightRel)
	nest, _, _ := mountedBody(t, args, "/home/agent/"+inheritNestedRel)
	if !strings.Contains(pre, "yolo check") {
		t.Error("the preflight file does not name its readers")
	}
	if !strings.Contains(nest, "JAIL-IN-JAIL") {
		t.Error("the nested file does not say it exists for nesting")
	}
}

// A config with NOTHING to inherit delivers NO file, and that is deliberate on two counts.
// It is honest (a `{}` under a header explaining what was filtered invites a hunt for a key
// the user never set), and it is what keeps the frozen golden argv byte-identical for a jail
// launched from a bare config — the contract TestAssembleRunCmdPodmanLinuxGolden pins.
func TestNothingToInheritDeliversNoFile(t *testing.T) {
	// Only host-referent keys: everything is filtered, so both scopes are empty.
	_, wsState := inheritHome(t, `{"gpu": {"enabled": true}, "kvm": true}`)
	o := inheritOptions(t)
	args := o.userConfigMountArgs("podman", wsState, map[string]struct{}{})
	if len(args) != 0 {
		t.Errorf("a config with nothing inheritable emitted argv %v — the golden argv for a "+
			"bare launch would drift", args)
	}
}

// config.lua still crosses, as the HOST'S OWN FILE. It is a Lua transform, not a config with
// keys to classify, and the entrypoint reads it as the user half of the documented
// "user then workspace" transform pair (A13: it used to have no channel into any jail while
// `yolo config-ref` advertised it as auto-loaded). Filtering has nothing to say about it, so
// this pins that OQ-LP9 did not silently drop it while replacing its neighbour.
func TestConfigLuaStillCrossesUnfiltered(t *testing.T) {
	home, wsState := inheritHome(t, `{"packs": ["claude"]}`)
	lua := filepath.Join(home, ".config", "yolo-jail", "config.lua")
	if err := os.WriteFile(lua, []byte("-- transform\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := inheritOptions(t)
	joined := strings.Join(o.userConfigMountArgs("podman", wsState, map[string]struct{}{}), " ")
	if !strings.Contains(joined, "/home/agent/.config/yolo-jail/config.lua") {
		t.Errorf("config.lua did not cross:\n%s", joined)
	}
}

// And an absent config.lua adds no argv, so the golden argv of a jail with no transform is
// unchanged (the invariant the A13 fix shipped with).
func TestAbsentConfigLuaAddsNoArgv(t *testing.T) {
	_, wsState := inheritHome(t, `{"packs": ["claude"]}`)
	o := inheritOptions(t)
	joined := strings.Join(o.userConfigMountArgs("podman", wsState, map[string]struct{}{}), " ")
	if strings.Contains(joined, "config.lua") {
		t.Errorf("absent config.lua must not be mounted: %s", joined)
	}
}

// RECURSION BY COMPOSITION, TWO LEVELS (OQ-LP9 R6). Jail A hands B one inherited file; B
// composes ITS effective config (which is that file) and hands C one inherited file. The
// bytes must be identical apart from the timestamp, because there is no rule that changes
// with depth — A does NOT hand down a stack of its ancestors' files.
//
// This drives the real path both times: level 2's HOME is set to a home containing exactly
// the file level 1 produced, and userConfigMountArgs is called again.
func TestInheritedScopeComposesAcrossTwoLevels(t *testing.T) {
	// --- level 1: the human's host config → jail A's inherited file.
	_, wsState1 := inheritHome(t, `{
	  "packs": ["claude"],
	  "packages": ["postgresql"],
	  "loopholes": {"acme": {"command": ["/opt/acmed"], "jail_endpoint": "/run/yolo-services/acme.sock"}},
	  "gpu": {"enabled": true},
	  "cache_relocations": {"npm": "/mnt/bigdisk/npm"}
	}`)
	o1 := inheritOptions(t)
	args1 := o1.userConfigMountArgs("podman", wsState1, map[string]struct{}{})
	level1, _, ok := mountedBody(t, args1, "/home/agent/"+inheritPreflightRel)
	if !ok {
		t.Fatalf("level 1 produced no preflight file; args=%v", args1)
	}

	// --- level 2: jail A's home IS that file → jail B's inherited file.
	_, wsState2 := inheritHome(t, level1)
	o2 := inheritOptions(t)
	args2 := o2.userConfigMountArgs("podman", wsState2, map[string]struct{}{})
	level2, _, ok := mountedBody(t, args2, "/home/agent/"+inheritPreflightRel)
	if !ok {
		t.Fatalf("level 2 produced no preflight file; args=%v", args2)
	}

	if level1 != level2 {
		t.Errorf("depth 1 and depth 2 differ, so the rule changes with nesting:\n"+
			"--- level1 ---\n%s\n--- level2 ---\n%s", level1, level2)
	}
	// And the host-only keys are still gone at depth 2 — they were never re-introduced by
	// the round trip. Keys, not substrings: the header names the filtered keys on purpose.
	keys2, _ := mountedKeys(t, args2, "/home/agent/"+inheritPreflightRel)
	for _, banned := range []string{"gpu", "cache_relocations"} {
		if hasInheritKey(keys2, banned) {
			t.Errorf("%q reappeared at depth 2; keys=%v", banned, keys2)
		}
	}
	if !hasInheritKey(keys2, "packs") {
		t.Errorf("`packs` was lost at depth 2 — a nested jail would have no packs at all; keys=%v", keys2)
	}
}

// A key the census does not know is DROPPED and NAMED (never silently passed through). The
// warning is the point: an unclassified key reaching a jail unreviewed is the failure the
// census exists to prevent, so its absence has to be visible to whoever added the key.
func TestUnclassifiedKeyIsDroppedAndReported(t *testing.T) {
	effective := jsonx.NewOrderedMap()
	effective.Set("packs", []any{"claude"})
	effective.Set("some_future_key", "value")
	files, unknown, err := inheritScopeFiles(effective, "podman", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(unknown) != 1 || unknown[0] != "some_future_key" {
		t.Errorf("unknown = %v, want [some_future_key]", unknown)
	}
	for _, f := range files {
		if strings.Contains(f.body, "some_future_key") {
			t.Errorf("%s file carried an unclassified key:\n%s", f.scope, f.body)
		}
	}
}

// The staging path must be REAL and the mount must point at a file that exists — podman dies
// on a missing bind source ("statfs …: no such file or directory"), so a render that failed
// to write must emit NO mount rather than a broken one. This is the regression for the
// stronger failure mode: a degraded user scope is a warning, a jail that will not start is
// an outage.
func TestFailedStagingEmitsNoMount(t *testing.T) {
	_, wsState := inheritHome(t, `{"packs": ["claude"]}`)
	// A wsState that is a FILE, not a directory: every write into it fails.
	bad := filepath.Join(wsState, "not-a-dir")
	if err := os.WriteFile(bad, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	o := inheritOptions(t)
	o.Stdout = &out
	args := o.userConfigMountArgs("podman", bad, map[string]struct{}{})
	if len(args) != 0 {
		t.Errorf("a failed stage emitted mounts %v — podman would refuse to start the jail", args)
	}
	if !strings.Contains(out.String(), "user scope") {
		t.Errorf("a failed stage must SAY so (an empty user scope otherwise looks like a "+
			"config problem to the agent inside); output was:\n%s", out.String())
	}
}

// canNest is keyed on the runtime of the jail being CREATED, and podman is the only backend
// that can nest. Pinned separately from the delivery test because it is the whole content of
// R2's "by construction" claim, and a future backend has to make this decision explicitly.
func TestCanNestOnlyPodman(t *testing.T) {
	for rt, want := range map[string]bool{
		"podman":     true,
		"container":  false,
		"macos-user": false,
		"":           false,
	} {
		if got := canNest(rt); got != want {
			t.Errorf("canNest(%q) = %v, want %v", rt, got, want)
		}
	}
}

// The preflight file lands at paths.UserConfigPath()'s in-jail location, which is what makes
// every existing in-jail reader find it with NO plumbing: config.LoadConfig, LoadPacks, the
// loopholes commands and `yolo check` all resolve ~/.config/yolo-jail/config.jsonc already.
// If this name drifted, the feature would silently stop being the inner user scope.
func TestPreflightFileIsTheInJailUserConfigPath(t *testing.T) {
	if inheritPreflightRel != ".config/yolo-jail/config.jsonc" {
		t.Errorf("inheritPreflightRel = %q — it must be the path every in-jail user-scope "+
			"reader already resolves, or none of them will see the generated scope",
			inheritPreflightRel)
	}
	if inheritNestedRel == inheritPreflightRel {
		t.Error("the two files must not share a path")
	}
}

// The delivered bytes are exactly what config.RenderInherit produces — the delivery layer
// adds nothing and rewrites nothing. That is what keeps "both files are renders of ONE
// computation" true at the seam rather than only in the renderer.
func TestDeliveredBytesAreTheRendererOutput(t *testing.T) {
	effective := jsonx.NewOrderedMap()
	effective.Set("packs", []any{"claude"})
	files, _, err := inheritScopeFiles(effective, "podman", "2026-08-14T12:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no files rendered")
	}
	for _, f := range files {
		want, _, err := config.RenderInherit(effective, f.scope, "2026-08-14T12:00:00Z")
		if err != nil {
			t.Fatal(err)
		}
		if f.body != want {
			t.Errorf("%s: delivered bytes differ from the renderer's:\n got: %q\nwant: %q",
				f.scope, f.body, want)
		}
	}
}
