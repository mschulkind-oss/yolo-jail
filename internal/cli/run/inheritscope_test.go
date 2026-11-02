package run

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
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
		Stderr:    &strings.Builder{},
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
	body, src, ok := mountedBody(t, args, containerPath)
	if !ok {
		return nil, false
	}
	if filepath.Ext(src) == ".toml" {
		doc, err := configdoc.Parse([]byte(body), configdoc.TOML)
		if err != nil {
			t.Fatalf("delivered TOML does not parse: %v", err)
		}
		return doc.Values.Keys(), true
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

func TestInheritedUserTOMLIsDeliveredByTheLaunchCaller(t *testing.T) {
	for _, tc := range []struct {
		name, filename, input string
	}{
		{"legacy JSONC", "config.jsonc", `{"packs": ["claude"]}`},
		{"readable TOML", "config.toml", "packs = ['claude']\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, wsState := inheritHome(t, "")
			userConfig := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail", tc.filename)
			if err := os.WriteFile(userConfig, []byte(tc.input), 0o644); err != nil {
				t.Fatal(err)
			}
			o := inheritOptions(t)
			args := o.userConfigMountArgs("podman", wsState)

			for _, dest := range []string{
				"/home/agent/.config/yolo-jail/config.toml",
				"/home/agent/.config/yolo-jail/inherited-launch.toml",
			} {
				body, source, found := mountedBody(t, args, dest)
				if !found {
					t.Fatalf("the launch caller did not deliver %s as TOML; argv=%v", dest, args)
				}
				info, err := os.Stat(source)
				if err != nil {
					t.Fatal(err)
				}
				if got := info.Mode().Perm(); got != 0o644 {
					t.Errorf("staged %s mode = %04o, want 0644", source, got)
				}
				if filepath.Ext(source) != ".toml" {
					t.Errorf("TOML destination %s was staged from %q", dest, source)
				}
				parsed, err := configdoc.Parse([]byte(body), configdoc.TOML)
				if err != nil {
					t.Errorf("the bytes mounted for %s are not TOML: %v\n%s", dest, err, body)
					continue
				}
				if _, ok := parsed.Values.Get("packs"); !ok {
					t.Errorf("delivered %s lost user packs: %s", dest, body)
				}
			}
			for i := 0; i+1 < len(args); i++ {
				if args[i] != "-v" || !strings.HasSuffix(args[i+1], ":ro") {
					continue
				}
				parts := strings.Split(args[i+1], ":")
				if len(parts) < 2 || parts[1] == "/home/agent/.config/yolo-jail" || parts[1] == "/home/agent/.config" {
					t.Errorf("generated user scope must remain a single-file read-only bind: %q", args[i+1])
				}
			}
		})
	}
}

func TestRunAssemblesInheritedTOMLForLegacyAndReadableUserConfigs(t *testing.T) {
	for _, tc := range []struct {
		name, filename, input string
	}{
		{"legacy JSONC", "config.jsonc", `{"packs": ["claude"]}`},
		{"readable TOML", "config.toml", "packs = ['claude']\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _ = inheritHome(t, "")
			userConfig := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail", tc.filename)
			if err := os.WriteFile(userConfig, []byte(tc.input), 0o644); err != nil {
				t.Fatal(err)
			}
			workspace := t.TempDir()
			argv, printed := fakePodmanLaunchIn(t, workspace, "", func(o *Options) {
				o.AcceptConfigChanges = true
			})
			if len(argv) == 0 {
				t.Fatalf("Run did not reach the mocked Podman launch; output:\n%s", printed)
			}
			var found bool
			for i := 0; i+1 < len(argv); i++ {
				if argv[i] == "-v" && strings.HasSuffix(argv[i+1], ":/home/agent/.config/yolo-jail/config.toml:ro") {
					found = true
					parts := strings.Split(argv[i+1], ":")
					body, err := os.ReadFile(parts[0])
					if err != nil {
						t.Fatal(err)
					}
					parsed, err := configdoc.Parse(body, configdoc.TOML)
					if err != nil {
						t.Fatalf("Run mounted non-TOML inherited user bytes: %v\n%s", err, body)
					}
					if _, ok := parsed.Values.Get("packs"); !ok {
						t.Errorf("Run's assembled preflight file lost packs: %s", body)
					}
				}
			}
			if !found {
				t.Fatalf("Run reached Podman without assembling the inherited TOML user scope; argv=%v\n%s", argv, printed)
			}
		})
	}
}

// THE FALSE-ERROR CLASS, at the delivery seam (OQ-LP9 R1). The old arrangement bound the
// human's real config.jsonc into the jail verbatim, so every host-only key crossed. What a
// jail sees now must carry `packs` and `loopholes` and must NOT carry the host-referent keys
// — measured 2026-08-14 to make an in-jail `yolo check` report problems the user does not
// have (gpu → four driver fails, cache_relocations → a missing target parent, mounts → a
// warning per entry).
func TestAssemblePreservesExistingLegacyUserConfigSibling(t *testing.T) {
	_, _ = inheritHome(t, `{"packs": ["claude"]}`)
	workspace := t.TempDir()
	wsState := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(wsState, 0o755); err != nil {
		t.Fatal(err)
	}
	// Ground the destination in the actual Podman home bind, not the staging filename:
	// podmanEarlyHomeBinds maps its config subtree at /home/agent/.config.
	var configSubtree string
	for _, bind := range podmanEarlyHomeBinds() {
		if bind.HomeRel == ".config" {
			configSubtree = bind.Subtree
		}
	}
	if configSubtree == "" {
		t.Fatal("Podman no longer declares the .config home bind; re-ground this test")
	}
	configDir := filepath.Join(wsState, configSubtree, "yolo-jail")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(configDir, "config.jsonc")
	ownerBytes := []byte(`{"packs": ["copilot"]}` + "\n")
	if err := os.WriteFile(legacy, ownerBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	o := goldenOptions(workspace, os.Getenv("HOME"))
	in := relocationInput(t, "podman", wsState, nil)
	argv := o.assembleRunCmd(in)
	if in.assemblyErr != nil {
		t.Fatalf("one regular legacy sibling should retain old JSONC delivery, not refuse: %v", in.assemblyErr)
	}
	var source string
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] != "-v" {
			continue
		}
		spec := argv[i+1]
		if strings.HasSuffix(spec, ":/home/agent/.config/yolo-jail/config.jsonc:ro") {
			source = strings.Split(spec, ":")[0]
		}
		if strings.HasSuffix(spec, ":/home/agent/.config/yolo-jail/config.toml:ro") {
			t.Errorf("assembly added a TOML sibling beside the owner's legacy JSONC: %q", spec)
		}
	}
	if source == "" {
		t.Fatalf("assembly did not retain the original JSONC destination; argv=%v", argv)
	}
	body, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadJSONCFile(source, "/home/agent/.config/yolo-jail/config.jsonc", true, nil); err != nil {
		t.Errorf("the legacy delivery route did not stage JSONC bytes: %v\n%s", err, body)
	}
	if !strings.Contains(string(body), `"claude"`) {
		t.Errorf("the generated JSONC fallback lost the host scope:\n%s", body)
	}
	after, err := os.ReadFile(legacy)
	if err != nil || string(after) != string(ownerBytes) {
		t.Errorf("the owner file changed (err %v): %q", err, after)
	}
	if info, err := os.Stat(legacy); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the owner mode changed (info=%v err=%v)", info, err)
	}
}

func TestAssembleRefusesMixedLegacyAndTOMLUserConfigSiblings(t *testing.T) {
	_, _ = inheritHome(t, `{"packs": ["claude"]}`)
	workspace := t.TempDir()
	wsState := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(wsState, 0o755); err != nil {
		t.Fatal(err)
	}
	var configSubtree string
	for _, bind := range podmanEarlyHomeBinds() {
		if bind.HomeRel == ".config" {
			configSubtree = bind.Subtree
		}
	}
	if configSubtree == "" {
		t.Fatal("Podman no longer declares the .config home bind; re-ground this test")
	}
	configDir := filepath.Join(wsState, configSubtree, "yolo-jail")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(configDir, "config.jsonc")
	toml := filepath.Join(configDir, "config.toml")
	legacyBytes, tomlBytes := []byte(`{"packs": ["copilot"]}`+"\n"), []byte("packs = [\"codex\"]\n")
	if err := os.WriteFile(legacy, legacyBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(toml, tomlBytes, 0o640); err != nil {
		t.Fatal(err)
	}

	o := goldenOptions(workspace, os.Getenv("HOME"))
	in := relocationInput(t, "podman", wsState, nil)
	argv := o.assembleRunCmd(in)
	if in.assemblyErr == nil {
		t.Errorf("mixed sibling families were not refused before runtime execution; argv=%v", argv)
	}
	for _, spec := range inheritedScopeMounts(argv) {
		t.Errorf("a user-scope file was staged or bound despite refusal: %q", spec)
	}
	assertNoInheritedScopeStages(t, wsState)
	for path, want := range map[string][]byte{legacy: legacyBytes, toml: tomlBytes} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != string(want) {
			t.Errorf("refusal changed owner file %s (err %v): %q", path, err, got)
		}
	}
	for path, wantMode := range map[string]os.FileMode{legacy: 0o600, toml: 0o640} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != wantMode {
			t.Errorf("refusal changed %s mode (info=%v err=%v), want %04o", path, info, err, wantMode)
		}
	}
}

func TestAssembleRefusesLegacyFallbackBesideExistingTOML(t *testing.T) {
	_, _ = inheritHome(t, `{"packs": ["claude"], "loopholes": {"acme": {"command": ["/bin/true"], "jail_endpoint": "/run/yolo-services/acme.sock"}}}`)
	workspace := t.TempDir()
	wsState := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(wsState, 0o755); err != nil {
		t.Fatal(err)
	}
	var configSubtree string
	for _, bind := range podmanEarlyHomeBinds() {
		if bind.HomeRel == ".config" {
			configSubtree = bind.Subtree
		}
	}
	if configSubtree == "" {
		t.Fatal("Podman no longer declares the .config home bind; re-ground this test")
	}
	configDir := filepath.Join(wsState, configSubtree, "yolo-jail")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	toml := filepath.Join(configDir, "config.toml")
	ownerBytes := []byte("packs = [\"copilot\"]\n")
	if err := os.WriteFile(toml, ownerBytes, 0o640); err != nil {
		t.Fatal(err)
	}

	o := goldenOptions(workspace, os.Getenv("HOME"))
	in := relocationInput(t, "podman", wsState, nil)
	argv := o.assembleRunCmd(in)
	if in.assemblyErr == nil || !strings.Contains(in.assemblyErr.Error(), toml) {
		t.Fatalf("an unrepresentable legacy F/G scope beside TOML was not refused with its path: %v", in.assemblyErr)
	}
	for _, spec := range inheritedScopeMounts(argv) {
		t.Errorf("a scope file was bound before refusing the mixed format family: %q", spec)
	}
	assertNoInheritedScopeStages(t, wsState)
	got, err := os.ReadFile(toml)
	if err != nil || string(got) != string(ownerBytes) {
		t.Errorf("refusal changed the owner's TOML file (err %v): %q", err, got)
	}
	if info, err := os.Stat(toml); err != nil || info.Mode().Perm() != 0o640 {
		t.Errorf("refusal changed the owner's TOML mode (info=%v err=%v)", info, err)
	}
}

func TestAssembleRefusesLinkedLegacyUserConfigSibling(t *testing.T) {
	_, _ = inheritHome(t, `{"packs": ["claude"]}`)
	workspace := t.TempDir()
	wsState := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(wsState, 0o755); err != nil {
		t.Fatal(err)
	}
	var configSubtree string
	for _, bind := range podmanEarlyHomeBinds() {
		if bind.HomeRel == ".config" {
			configSubtree = bind.Subtree
		}
	}
	if configSubtree == "" {
		t.Fatal("Podman no longer declares the .config home bind; re-ground this test")
	}
	configDir := filepath.Join(wsState, configSubtree, "yolo-jail")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(configDir, "config.jsonc")
	target := filepath.Join(t.TempDir(), "owner.jsonc")
	targetBytes := []byte(`{"packs": ["copilot"]}` + "\n")
	if err := os.WriteFile(target, targetBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, legacy); err != nil {
		t.Fatal(err)
	}

	o := goldenOptions(workspace, os.Getenv("HOME"))
	in := relocationInput(t, "podman", wsState, nil)
	argv := o.assembleRunCmd(in)
	if in.assemblyErr == nil || !strings.Contains(in.assemblyErr.Error(), legacy) {
		t.Fatalf("a linked legacy sibling was not refused with its path: %v", in.assemblyErr)
	}
	for _, spec := range inheritedScopeMounts(argv) {
		t.Errorf("a scope file was mounted despite a linked destination: %q", spec)
	}
	assertNoInheritedScopeStages(t, wsState)
	link, err := os.Readlink(legacy)
	if err != nil || link != target {
		t.Errorf("refusal changed the legacy link (got %q err %v), want %q", link, err, target)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != string(targetBytes) {
		t.Errorf("refusal changed the link target (err %v): %q", err, got)
	}
}
func inheritedScopeMounts(argv []string) []string {
	var out []string
	for _, spec := range mountSpecs(argv) {
		if strings.Contains(spec, ":/home/agent/.config/yolo-jail/") && strings.HasSuffix(spec, ":ro") {
			out = append(out, spec)
		}
	}
	return out
}

func assertNoInheritedScopeStages(t *testing.T, wsState string) {
	t.Helper()
	for _, name := range []string{
		"inherit-preflight.jsonc", "inherit-preflight.toml",
		"inherit-nested.jsonc", "inherit-nested.toml",
	} {
		if _, err := os.Lstat(filepath.Join(wsState, name)); err == nil {
			t.Errorf("refusal left staged inherited file %s", name)
		}
	}
}

func mountSpecs(argv []string) []string {
	var specs []string
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == "-v" {
			specs = append(specs, argv[i+1])
		}
	}
	return specs
}

// A legacy user config's env_sources once had no TOML spelling, so the nested scope (the only
// one carrying it) fell back to JSONC while the preflight scope crossed as TOML. The read now
// lowers env_sources to env.sources / env.files (RC-D50), so both scopes cross as TOML and the
// nested one keeps every entry, in order.
func TestInheritedLegacyEnvSourcesCrossesAsTOMLInEveryScope(t *testing.T) {
	_, wsState := inheritHome(t, `{"packs": ["claude"], "env_sources": [".env", {"A": "1", "B": null}]}`)
	o := inheritOptions(t)
	args := o.userConfigMountArgs("podman", wsState)
	preflight, _, hasTOMLPreflight := mountedBody(t, args, "/home/agent/"+inheritPreflightRel)
	if !hasTOMLPreflight {
		t.Fatalf("preflight did not retain its readable TOML projection; argv=%v", args)
	}
	if _, err := configdoc.Parse([]byte(preflight), configdoc.TOML); err != nil {
		t.Fatalf("preflight output is not TOML: %v\n%s", err, preflight)
	}
	nested, _, hasTOMLNested := mountedBody(t, args, "/home/agent/"+inheritNestedRel)
	if !hasTOMLNested {
		t.Fatalf("nested legacy env_sources did not cross as TOML; argv=%v", args)
	}
	if _, _, found := mountedBody(t, args, "/home/agent/"+inheritNestedLegacyRel); found {
		t.Errorf("the nested scope still fell back to JSONC: %v", args)
	}
	doc, err := configdoc.Parse([]byte(nested), configdoc.TOML)
	if err != nil {
		t.Fatalf("nested output is not TOML: %v\n%s", err, nested)
	}
	entries := config.EnvEntries(doc.Values)
	if len(entries) != 2 {
		t.Fatalf("nested env entries = %v, want the dotenv path then the inline table:\n%s", entries, nested)
	}
	if path, ok := entries[0].(string); !ok || filepath.Base(path) != ".env" {
		t.Errorf("first nested env entry = %v, want the .env path:\n%s", entries[0], nested)
	}
	inline, ok := entries[1].(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("second nested env entry = %v, want the inline table:\n%s", entries[1], nested)
	}
	if a, _ := inline.Get("A"); a != "1" {
		t.Errorf("inline A = %v, want 1:\n%s", a, nested)
	}
	if b, present := inline.Get("B"); !present || b != nil {
		t.Errorf("inline B = %v (present %v), want a removal:\n%s", b, present, nested)
	}
}

func TestMappedHomePathUsesLongestComponentBoundaryBind(t *testing.T) {
	wsState := t.TempDir()
	parent := filepath.Join(wsState, "config")
	deeper := filepath.Join(wsState, "override")
	for _, dir := range []string{parent, deeper} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	containerPath := "/home/agent/.config/yolo-jail/config.jsonc"
	argv := []string{
		"-v", parent + ":/home/agent/.config:rw",
		"-v", deeper + ":/home/agent/.config/yolo-jail:rw",
	}
	got, err := mappedHomePath(wsState, argv, containerPath)
	if err != nil || got != filepath.Join("override", "config.jsonc") {
		t.Fatalf("mappedHomePath() = %q, %v; want deepest actual bind override/config.jsonc", got, err)
	}
	if _, err := mappedHomePath(wsState, []string{"-v", parent + ":/home/agent/.config-other:rw"}, containerPath); err == nil {
		t.Fatal("component-prefix-only bind incorrectly covered .config/yolo-jail")
	}
	if _, err := mappedHomePath(wsState, []string{
		"-v", parent + ":/home/agent/.config:rw",
		"-v", deeper + ":/home/agent/.config:rw",
	}, containerPath); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("equal-specificity destination binds were not refused: %v", err)
	}
}

func TestAppleContainerAssemblyRefusesExistingScopeDestinationsBeforeStaging(t *testing.T) {
	for _, tc := range []struct {
		name      string
		legacy    []byte
		toml      []byte
		link      bool
		wantNames []string
	}{
		{name: "owner TOML", toml: []byte("# operator comment\npacks = [\"copilot\"]\n"), wantNames: []string{"config.toml"}},
		{name: "both owner formats", legacy: []byte(`{"packs": ["copilot"]}` + "\n"), toml: []byte("packs = [\"codex\"]\n"), wantNames: []string{"config.jsonc", "config.toml"}},
		{name: "linked owner", link: true, wantNames: []string{"config.jsonc"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _ = inheritHome(t, `{"packs": ["claude"]}`)
			workspace := t.TempDir()
			wsState := filepath.Join(t.TempDir(), "home")
			configDir := filepath.Join(wsState, ".config", "yolo-jail")
			if err := os.MkdirAll(configDir, 0o755); err != nil {
				t.Fatal(err)
			}
			legacy := filepath.Join(configDir, "config.jsonc")
			toml := filepath.Join(configDir, "config.toml")
			owners := map[string][]byte{}
			if len(tc.legacy) > 0 {
				owners[legacy] = tc.legacy
				if err := os.WriteFile(legacy, tc.legacy, 0o640); err != nil {
					t.Fatal(err)
				}
			}
			if len(tc.toml) > 0 {
				owners[toml] = tc.toml
				if err := os.WriteFile(toml, tc.toml, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var target string
			if tc.link {
				target = filepath.Join(t.TempDir(), "owner.jsonc")
				owners[target] = []byte(`{"packs": ["codex"]}` + "\n")
				if err := os.WriteFile(target, owners[target], 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, legacy); err != nil {
					t.Fatal(err)
				}
			}

			o := goldenOptions(workspace, os.Getenv("HOME"))
			in := relocationInput(t, "container", wsState, nil)
			argv := o.assembleRunCmd(in)
			if in.assemblyErr == nil {
				t.Fatal("Apple Container assembled a scope that would replace or follow an existing destination")
			}
			for _, name := range tc.wantNames {
				if !strings.Contains(in.assemblyErr.Error(), filepath.Join(configDir, name)) {
					t.Errorf("refusal does not name %s: %v", name, in.assemblyErr)
				}
			}
			if !strings.Contains(in.assemblyErr.Error(), "preserv") {
				t.Errorf("refusal has no safe preservation step: %v", in.assemblyErr)
			}
			if len(inheritedScopeMounts(argv)) != 0 {
				t.Errorf("Apple Container emitted a single-file scope mount despite refusal: %v", inheritedScopeMounts(argv))
			}
			assertNoInheritedScopeStages(t, wsState)
			for path, want := range owners {
				got, err := os.ReadFile(path)
				if err != nil || string(got) != string(want) {
					t.Errorf("Apple Container refusal changed owner bytes at %s (err %v): %q", path, err, got)
				}
			}
			for path, wantMode := range map[string]os.FileMode{toml: 0o600, legacy: 0o640} {
				if _, exists := owners[path]; !exists {
					continue
				}
				if info, err := os.Stat(path); err != nil || info.Mode().Perm() != wantMode {
					t.Errorf("Apple Container refusal changed mode at %s (info=%v err=%v)", path, info, err)
				}
			}
			if tc.link {
				link, err := os.Readlink(legacy)
				if err != nil || link != target {
					t.Errorf("Apple Container refusal changed link %s (got %q err %v), want %q", legacy, link, err, target)
				}
			}
		})
	}
}

func TestAppleContainerAssemblesInheritedTOMLWhenDestinationIsAbsent(t *testing.T) {
	_, _ = inheritHome(t, `{"packs": ["claude"]}`)
	workspace := t.TempDir()
	wsState := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(wsState, 0o755); err != nil {
		t.Fatal(err)
	}
	o := goldenOptions(workspace, os.Getenv("HOME"))
	in := relocationInput(t, "container", wsState, nil)
	_ = o.assembleRunCmd(in)
	if in.assemblyErr != nil {
		t.Fatalf("absent Apple Container destination should receive TOML: %v", in.assemblyErr)
	}
	path := filepath.Join(wsState, ".config", "yolo-jail", "config.toml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Apple Container did not stage its absent TOML destination: %v", err)
	}
	if _, err := configdoc.Parse(body, configdoc.TOML); err != nil {
		t.Fatalf("Apple Container staged invalid TOML: %v\n%s", err, body)
	}
	if _, err := os.Lstat(filepath.Join(filepath.Dir(path), "config.jsonc")); err == nil {
		t.Fatal("Apple Container unexpectedly created a legacy sibling for a representable scope")
	}
}

func TestReadableInheritedTOMLFlowsThroughThePublicUserLoader(t *testing.T) {
	_, wsState := inheritHome(t, `{"packs": ["claude"], "packages": ["postgresql"]}`)
	o := inheritOptions(t)
	args := o.userConfigMountArgs("podman", wsState)
	preflight, _, foundPreflight := mountedBody(t, args, "/home/agent/"+inheritPreflightRel)
	nested, _, foundNested := mountedBody(t, args, "/home/agent/"+inheritNestedRel)
	if !foundPreflight || !foundNested {
		t.Fatalf("caller did not produce both TOML user-scope files; args=%v", args)
	}

	jailHome := t.TempDir()
	configDir := filepath.Join(jailHome, ".config", "yolo-jail")
	for name, body := range map[string]string{
		"config.toml":           preflight,
		"inherited-launch.toml": nested,
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(configDir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(configDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	layer := filepath.Join(jailHome, "local-layer.toml")
	if err := os.WriteFile(layer, []byte("packs = [\"copilot\"]\ninstall.nix = [\"jq\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", jailHome)
	t.Setenv("YOLO_VERSION", "nested-test")
	t.Setenv(config.UserLayerEnv, layer)
	merged, sources, err := config.UserScopeConfigWithSources(true, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	packs, _ := merged.Get("packs")
	packList, _ := packs.([]any)
	var packNames []string
	for _, pack := range packList {
		if name, ok := pack.(string); ok {
			packNames = append(packNames, name)
		}
	}
	for _, want := range []string{"claude", "copilot"} {
		if !slices.Contains(packNames, want) {
			t.Errorf("public TOML user loader lost pack %q: %v", want, packNames)
		}
	}
	install, _ := merged.Get("install")
	installMap, _ := install.(*jsonx.OrderedMap)
	nixValue, _ := installMap.Get("nix")
	nixPackages, _ := nixValue.([]any)
	var packages []string
	for _, item := range nixPackages {
		if name, ok := item.(string); ok {
			packages = append(packages, name)
		}
	}
	for _, want := range []string{"postgresql", "jq"} {
		if !slices.Contains(packages, want) {
			t.Errorf("nested and explicit TOML layers did not compose install.nix %q: %v", want, packages)
		}
	}
	if got := sources.Locations("config.packs[1]"); !strings.Contains(strings.Join(got, " "), "local-layer.toml") {
		t.Errorf("the explicit TOML layer lost its provenance: %v", got)
	}
}

func TestRunPreservesLegacyFGPayloadAsJSONCFallback(t *testing.T) {
	_, _ = inheritHome(t, `{"packs": ["claude"], "loopholes": {"acme": {"command": ["/bin/true"], "jail_endpoint": "/run/yolo-services/acme.sock"}}}`)
	workspace := t.TempDir()
	argv, printed := fakePodmanLaunchIn(t, workspace, "", func(o *Options) {
		o.AcceptConfigChanges = true
	})
	if len(argv) == 0 {
		t.Fatalf("Run refused a valid legacy F/G user config instead of preserving it; output:\n%s", printed)
	}
	var preflight, nested string
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] != "-v" {
			continue
		}
		mount := argv[i+1]
		switch {
		case strings.HasSuffix(mount, ":/home/agent/.config/yolo-jail/config.jsonc:ro"):
			preflight = strings.Split(mount, ":")[0]
		case strings.HasSuffix(mount, ":/home/agent/.config/yolo-jail/inherited-launch.jsonc:ro"):
			nested = strings.Split(mount, ":")[0]
		}
	}
	for label, path := range map[string]string{"preflight": preflight, "nested": nested} {
		if path == "" {
			t.Fatalf("Run omitted the %s legacy JSONC fallback mount; argv=%v", label, argv)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := config.LoadJSONCFile(path, "/home/agent/.config/yolo-jail/config.jsonc", true, nil)
		if err != nil {
			t.Fatalf("the %s fallback is not loadable JSONC: %v\n%s", label, err, body)
		}
		if _, ok := parsed.Get("loopholes"); !ok {
			t.Errorf("the %s fallback erased the legacy F/G root:\n%s", label, body)
		}
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o644 {
			t.Errorf("the %s fallback mode is not 0644 (info=%v err=%v)", label, info, err)
		}
	}
}

func TestRunRefusesMixedInheritedUserConfigBeforeRuntime(t *testing.T) {
	_, _ = inheritHome(t, `{"packs": ["claude"]}`)
	workspace := t.TempDir()
	wsState := paths.WorkspaceHomeState(workspace)
	configDir := filepath.Join(wsState, "config", "yolo-jail")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(configDir, "config.jsonc")
	toml := filepath.Join(configDir, "config.toml")
	legacyBytes, tomlBytes := []byte(`{"packs": ["copilot"]}`+"\n"), []byte("packs = [\"codex\"]\n")
	if err := os.WriteFile(legacy, legacyBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(toml, tomlBytes, 0o640); err != nil {
		t.Fatal(err)
	}
	argv, printed := fakePodmanLaunchIn(t, workspace, "", func(o *Options) {
		o.AcceptConfigChanges = true
	})
	if len(argv) != 0 {
		t.Fatalf("Run executed the mocked runtime despite an ambiguous inherited config family: %v", argv)
	}
	assertNoInheritedScopeStages(t, wsState)
	if !strings.Contains(printed, legacy) || !strings.Contains(printed, toml) || !strings.Contains(printed, "preserve both") {
		t.Errorf("the refusal did not name both files and a safe next step:\n%s", printed)
	}
	for path, want := range map[string][]byte{legacy: legacyBytes, toml: tomlBytes} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != string(want) {
			t.Errorf("Run refusal changed %s (err %v): %q", path, err, got)
		}
	}
}

func TestRunRefusesMalformedReadableConfigBeforeExecutingRuntime(t *testing.T) {
	_, _ = inheritHome(t, "")
	userConfig := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail", "config.toml")
	if err := os.WriteFile(userConfig, []byte("packs = [\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	argv, printed := fakePodmanLaunchIn(t, workspace, "", func(o *Options) {
		o.AcceptConfigChanges = true
	})
	if len(argv) != 0 {
		t.Fatalf("malformed readable config reached the mocked runtime: %v", argv)
	}
	if !strings.Contains(printed, "config.toml") {
		t.Errorf("the preflight refusal did not name the malformed config:\n%s", printed)
	}
}

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

	args := o.userConfigMountArgs("podman", wsState)
	keys, found := mountedKeys(t, args, "/home/agent/"+inheritPreflightLegacyRel)
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

// The per-workspace file's switches stay out of what a jail inherits
// (docs/design/boundary-broker.md BB-D56): they are for this workspace alone, and the inherited
// files are the jail's user scope, which applies to every workspace a launch inside it opens —
// and which refuses a brokered loophole's switch. The user config's own switch still crosses.
func TestInheritedScopeLeavesOutThePerWorkspaceFile(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	_, wsState := inheritHome(t, `{"packs": ["claude"], "loopholes": {"journal": {"enabled": true}}}`)
	o := inheritOptions(t)
	if _, err := config.SetWorkspaceLoophole(o.Workspace, "github-broker", true); err != nil {
		t.Fatal(err)
	}
	if _, err := config.SetWorkspaceLoophole(o.Workspace, "journal", false); err != nil {
		t.Fatal(err)
	}
	args := o.userConfigMountArgs("podman", wsState)
	for _, dest := range []string{inheritPreflightLegacyRel, inheritNestedLegacyRel} {
		_, src, found := mountedBody(t, args, "/home/agent/"+dest)
		if !found {
			t.Fatalf("no %s was mounted; args: %v", dest, args)
		}
		cfg, err := config.LoadJSONCFile(src, dest, true, nil)
		if err != nil {
			t.Fatal(err)
		}
		block, _ := cfg.Get("loopholes")
		lh, _ := block.(*jsonx.OrderedMap)
		if _, has := lh.Get("github-broker"); has {
			t.Errorf("%s carries the per-workspace switch of github-broker: %v", dest, lh.Keys())
		}
		if v, set := config.LoopholeEnabledOverride(lh, "journal"); !set || !v {
			t.Errorf("%s: journal enabled=%v set=%v, want the user config's true, not the "+
				"per-workspace file's false", dest, v, set)
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

	args := o.userConfigMountArgs("podman", wsState)
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
		args := o.userConfigMountArgs(tc.rt, wsState)

		// On `container` nothing is bind-mounted (the whole wsState becomes /home/agent),
		// so look for the materialized file in the tree instead of in the argv.
		staged := filepath.Join(wsState, "inherit-nested.toml")
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
	args := o.userConfigMountArgs("podman", wsState)

	preBody, _, okPre := mountedBody(t, args, "/home/agent/"+inheritPreflightRel)
	nestBody, _, okNest := mountedBody(t, args, "/home/agent/"+inheritNestedRel)
	if !okPre || !okNest {
		t.Fatalf("expected both files; preflight=%v nested=%v; args=%v", okPre, okNest, args)
	}
	preDoc, err := configdoc.Parse([]byte(preBody), configdoc.TOML)
	if err != nil {
		t.Fatalf("preflight TOML did not parse: %v", err)
	}
	if _, ok := preDoc.Values.Get("install"); ok {
		t.Error("nested-only package input reached the PREFLIGHT TOML install section")
	}
	nestDoc, err := configdoc.Parse([]byte(nestBody), configdoc.TOML)
	if err != nil {
		t.Fatalf("nested TOML did not parse: %v", err)
	}
	installValue, ok := nestDoc.Values.Get("install")
	install, valid := installValue.(*jsonx.OrderedMap)
	if !ok || !valid {
		t.Fatalf("nested output has no TOML install table: %#v", installValue)
	}
	nixValue, ok := install.Get("nix")
	nixPackages, valid := nixValue.([]any)
	if !ok || !valid || !slices.Contains(nixPackages, "postgresql") {
		t.Errorf("nested TOML did not project legacy packages into install.nix: %#v", nixValue)
	}
	if hasInheritKey(nestDoc.Values.Keys(), "agents_md_extra") {
		t.Error("`agents_md_extra` reached the NESTED file — it is prose for THIS jail's briefing")
	}
	if !strings.Contains(preBody, "yolo check") {
		t.Error("the preflight file does not name its readers")
	}
	if !strings.Contains(nestBody, "JAIL-IN-JAIL") {
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
	args := o.userConfigMountArgs("podman", wsState)
	if len(args) != 0 {
		t.Errorf("a config with nothing inheritable emitted argv %v — the golden argv for a "+
			"bare launch would drift", args)
	}
}

// The host's own ~/.config/yolo-jail/config.lua used to cross here UNFILTERED — the only
// file this function bound straight off the human's real disk rather than generating — as
// the user half of the "user then workspace" Lua transform pair (A13). The transform is
// gone (docs/reference/pack-system.md#oq-lt1), and so is the bind.
//
// Pinned in the NEGATIVE, with the file actually present, because that is the direction
// the mistake runs: nothing in the jail reads it any more, so re-adding the mount would be
// a host path crossing into a jail for no reader — the ungated-crossing row this removal
// closes (docs/design/trust-paths.md row 13).
func TestUserConfigLuaNoLongerCrosses(t *testing.T) {
	home, wsState := inheritHome(t, `{"packs": ["claude"]}`)
	lua := filepath.Join(home, ".config", "yolo-jail", "config.lua")
	if err := os.WriteFile(lua, []byte("-- a stale transform\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := inheritOptions(t)
	joined := strings.Join(o.userConfigMountArgs("podman", wsState), " ")
	if strings.Contains(joined, "config.lua") {
		t.Errorf("a leftover config.lua must not be mounted:\n%s", joined)
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
	args1 := o1.userConfigMountArgs("podman", wsState1)
	level1, _, ok := mountedBody(t, args1, "/home/agent/"+inheritPreflightLegacyRel)
	if !ok {
		t.Fatalf("level 1 produced no preflight file; args=%v", args1)
	}
	level1Nested, _, ok := mountedBody(t, args1, "/home/agent/"+inheritNestedLegacyRel)
	if !ok {
		t.Fatalf("level 1 produced no nested file; args=%v", args1)
	}

	// --- level 2: jail A's two generated files become jail B's user-layer inputs.
	_, wsState2 := inheritHome(t, "")
	childConfigDir := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail")
	for name, body := range map[string]string{
		filepath.Base(inheritPreflightLegacyRel): level1,
		filepath.Base(inheritNestedLegacyRel):    level1Nested,
	} {
		if err := os.WriteFile(filepath.Join(childConfigDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("YOLO_VERSION", "nested-test")
	o2 := inheritOptions(t)
	args2 := o2.userConfigMountArgs("podman", wsState2)
	level2, _, ok := mountedBody(t, args2, "/home/agent/"+inheritPreflightLegacyRel)
	if !ok {
		t.Fatalf("level 2 produced no preflight file; args=%v", args2)
	}

	if level1 != level2 {
		t.Errorf("depth 1 and depth 2 differ, so the rule changes with nesting:\n"+
			"--- level1 ---\n%s\n--- level2 ---\n%s", level1, level2)
	}
	// And the host-only keys are still gone at depth 2 — they were never re-introduced by
	// the round trip. Keys, not substrings: the header names the filtered keys on purpose.
	keys2, _ := mountedKeys(t, args2, "/home/agent/"+inheritPreflightLegacyRel)
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
	files, unknown, err := inheritScopeFiles(effective, nil, "podman", "")
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
	o.Stderr = &out
	args := o.userConfigMountArgs("podman", bad)
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

// The preflight file is the new readable user config name; the legacy JSONC route remains
// available only for an unrepresentable legacy payload during the dual-reader window.
func TestPreflightFileIsTheInJailUserConfigPath(t *testing.T) {
	if inheritPreflightRel != ".config/yolo-jail/config.toml" {
		t.Errorf("TOML preflight path = %q, want the readable user config path", inheritPreflightRel)
	}
	if inheritPreflightLegacyRel != ".config/yolo-jail/config.jsonc" {
		t.Errorf("legacy preflight path = %q, want the retained legacy filename", inheritPreflightLegacyRel)
	}
	if inheritNestedRel == inheritPreflightRel || inheritNestedLegacyRel == inheritPreflightLegacyRel {
		t.Error("preflight and nested files must remain distinct")
	}
}

// The delivered bytes are exactly what config.RenderInherit produces — the delivery layer
// adds nothing and rewrites nothing. That is what keeps "both files are renders of ONE
// computation" true at the seam rather than only in the renderer.
func TestDeliveredBytesAreTheRendererOutput(t *testing.T) {
	effective := jsonx.NewOrderedMap()
	effective.Set("packs", []any{"claude"})
	files, _, err := inheritScopeFiles(effective, nil, "podman", "2026-08-14T12:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no files rendered")
	}
	for _, f := range files {
		want, _, _, err := config.RenderFilteredForDelivery(effective, nil, f.scope, "2026-08-14T12:00:00Z")
		if err != nil {
			t.Fatal(err)
		}
		if f.body != want {
			t.Errorf("%s: delivered bytes differ from the renderer's:\n got: %q\nwant: %q",
				f.scope, f.body, want)
		}
	}
}
