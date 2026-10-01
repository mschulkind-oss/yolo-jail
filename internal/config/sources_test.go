package config

// sources_test.go pins the provenance record (sources.go): which file, line and column a
// refusal names, through every layer the composed config is merged from, and that recording
// it changes nothing about the merge. The call sites that print the located refusals are
// pinned in their own packages (run, check, cli); these cases drive the real loaders and
// ValidateConfig, then Annotate, over files on disk.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// sourcesHome is a scratch HOME, resolved where it is minted, and its user config dir.
func sourcesHome(t *testing.T) (home, cfgDir string) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv(UserLayerEnv, "")
	return home, filepath.Join(home, ".config", "yolo-jail")
}

// locatedErrors loads the config for ws as a launch does, validates it, and returns the
// errors and warnings located.
func locatedErrors(t *testing.T, ws string) (errs, warns []string) {
	t.Helper()
	cfg, src, err := LoadConfigWithSources(ws, true, discard)
	if err != nil {
		t.Fatal(err)
	}
	if src == nil {
		t.Fatal("LoadConfigWithSources returned no record for a config read from files")
	}
	e, w := ValidateConfig(cfg, ws, nil)
	return src.Annotate(e), src.Annotate(w)
}

// line returns the message mentioning needle, failing when none does.
func line(t *testing.T, msgs []string, needle string) string {
	t.Helper()
	for _, m := range msgs {
		if strings.Contains(m, needle) {
			return m
		}
	}
	t.Fatalf("no message mentions %q:\n%s", needle, strings.Join(msgs, "\n"))
	return ""
}

// AN INCLUDE OF AN INCLUDE: the key is written two includes down, and the refusal names that
// file, not the one the user config names.
func TestRefusalNamesAFileTwoIncludesDown(t *testing.T) {
	_, dir := sourcesHome(t)
	write(t, filepath.Join(dir, "config.jsonc"), `{"include_if_found": ["machine/local.jsonc"]}`)
	write(t, filepath.Join(dir, "machine", "local.jsonc"), `{"include_if_found": ["profiles.jsonc"]}`)
	write(t, filepath.Join(dir, "machine", "profiles.jsonc"), "{\n\t\"use_profiles\": \"zai\",\n}")
	errs, _ := locatedErrors(t, t.TempDir())
	got := line(t, errs, "config.use_profiles: RENAMED")
	if want := "~/.config/yolo-jail/machine/profiles.jsonc:2:18: config.use_profiles: RENAMED"; !strings.HasPrefix(got, want) {
		t.Errorf("got  %s\nwant a message starting %q", got, want)
	}
}

// yolo-jail.local.jsonc wins over yolo-jail.jsonc, so a key both write names the local file
// first and the other after it.
func TestRefusalNamesTheWorkspaceLocalFileFirst(t *testing.T) {
	sourcesHome(t)
	ws := t.TempDir()
	write(t, filepath.Join(ws, WorkspaceConfigName), `{"kvm": "yes"}`)
	write(t, filepath.Join(ws, WorkspaceLocalConfigName), "{\n  \"kvm\": \"no\"\n}")
	errs, _ := locatedErrors(t, ws)
	got := line(t, errs, "config.kvm: expected a boolean")
	local, main := filepath.Join(ws, WorkspaceLocalConfigName), filepath.Join(ws, WorkspaceConfigName)
	if !strings.HasPrefix(got, local+":2:10: config.kvm") {
		t.Errorf("the refusal does not lead with the local file, whose value won:\n%s", got)
	}
	if !strings.HasSuffix(got, "(also written at "+main+":1:9)") {
		t.Errorf("the refusal does not name the file the local one overrides:\n%s", got)
	}
}

// The workspace config wins over the user scope; the user file's copy is named after it.
func TestRefusalNamesTheWorkspaceOverTheUserConfig(t *testing.T) {
	_, dir := sourcesHome(t)
	write(t, filepath.Join(dir, "config.jsonc"), `{"runtime": "docker"}`)
	ws := t.TempDir()
	write(t, filepath.Join(ws, WorkspaceConfigName), `{"runtime": "docker"}`)
	errs, _ := locatedErrors(t, ws)
	got := line(t, errs, "config.runtime: 'docker'")
	if !strings.HasPrefix(got, filepath.Join(ws, WorkspaceConfigName)+":1:13: ") ||
		!strings.Contains(got, "also written at ~/.config/yolo-jail/config.jsonc:1:13") {
		t.Errorf("want the workspace file first and the user config after it:\n%s", got)
	}
}

// A --user-layer sits over config.jsonc and its includes, under the workspace.
func TestRefusalNamesTheUserLayer(t *testing.T) {
	home, dir := sourcesHome(t)
	write(t, filepath.Join(dir, "config.jsonc"), `{"packages": ["strace"]}`)
	layer := filepath.Join(home, "dev.jsonc")
	write(t, layer, `{"packages": ["strace", "no good"]}`)
	t.Setenv(UserLayerEnv, layer)
	errs, _ := locatedErrors(t, t.TempDir())
	if got := line(t, errs, "config.packages[1]"); !strings.HasPrefix(got, "~/dev.jsonc:1:25: config.packages[1]") {
		t.Errorf("the refusal does not name the layer's entry:\n%s", got)
	}
}

// In a jail the host's inherited nested-launch file sits UNDER config.jsonc; a key it carries
// is located in it.
func TestRefusalNamesTheInheritedLaunchFile(t *testing.T) {
	_, dir := sourcesHome(t)
	t.Setenv("YOLO_VERSION", "9.9.9-test")
	write(t, filepath.Join(dir, "config.jsonc"), `{}`)
	write(t, filepath.Join(dir, "inherited-launch.jsonc"), "{\n\"resources\": {\"memory\": \"lots\"}\n}")
	errs, _ := locatedErrors(t, t.TempDir())
	got := line(t, errs, "config.resources.memory")
	if !strings.HasPrefix(got, "~/.config/yolo-jail/inherited-launch.jsonc:2:25: config.resources.memory") {
		t.Errorf("the refusal does not name the inherited launch file:\n%s", got)
	}
}

// A list entry two files write is one entry of the merged list, written in both, so a refusal
// about it names both; an entry only one file wrote names that one.
func TestRefusalNamesEveryFileAListEntryIsWrittenIn(t *testing.T) {
	_, dir := sourcesHome(t)
	write(t, filepath.Join(dir, "config.jsonc"), `{"packages": ["bad one"], "include_if_found": ["more.jsonc"]}`)
	write(t, filepath.Join(dir, "more.jsonc"), "{\"packages\": [\n  \"bad two\",\n  \"bad one\"\n]}")
	errs, _ := locatedErrors(t, t.TempDir())
	one := line(t, errs, "config.packages[0]")
	if !strings.HasPrefix(one, "~/.config/yolo-jail/more.jsonc:3:3: config.packages[0]") ||
		!strings.HasSuffix(one, "(also written at ~/.config/yolo-jail/config.jsonc:1:15)") {
		t.Errorf("the entry both files write must name both:\n%s", one)
	}
	two := line(t, errs, "config.packages[1]")
	if !strings.HasPrefix(two, "~/.config/yolo-jail/more.jsonc:2:3: config.packages[1]") ||
		strings.Contains(two, "also written") {
		t.Errorf("the entry only the include writes must name it alone:\n%s", two)
	}
}

// A value the merge REPLACED with one of another type is still written in the replaced file,
// so it is named second; what the replaced value held is gone from the record, as from the map.
func TestATypeChangingOverrideKeepsBothWriters(t *testing.T) {
	sourcesHome(t)
	ws := t.TempDir()
	write(t, filepath.Join(ws, WorkspaceConfigName), `{"network": {"mode": "host", "bogus": 1}}`)
	write(t, filepath.Join(ws, WorkspaceLocalConfigName), `{"network": "host"}`)
	errs, _ := locatedErrors(t, ws)
	got := line(t, errs, "config.network: expected an object")
	if !strings.HasPrefix(got, filepath.Join(ws, WorkspaceLocalConfigName)+":1:13: ") ||
		!strings.Contains(got, "also written at "+filepath.Join(ws, WorkspaceConfigName)+":1:13") {
		t.Errorf("want the overriding file first and the overridden one after it:\n%s", got)
	}
	for _, e := range errs {
		if strings.Contains(e, "bogus") {
			t.Errorf("a key of the replaced value was reported, so the record kept it:\n%s", e)
		}
	}
}

// The path is matched against the record, not split on dots: a provider named with a dot, and
// a message path running past the last key into the value's own parts.
func TestAnnotateFollowsKeysTheRecordHolds(t *testing.T) {
	sourcesHome(t)
	ws := t.TempDir()
	write(t, filepath.Join(ws, WorkspaceConfigName), `{
  "network": {"ports": ["80:99999"]},
  "providers": {"my.local": {"models": {"m": 5}}}
}`)
	errs, _ := locatedErrors(t, ws)
	if got := line(t, errs, "config.network.ports[0].container"); !strings.Contains(got, WorkspaceConfigName+":2:25: config.network.ports[0].container") {
		t.Errorf("the port's part is not located at the port:\n%s", got)
	}
	if got := line(t, errs, "config.providers.my.local.models.m"); !strings.Contains(got, WorkspaceConfigName+":3:46: config.providers.my.local.models.m") {
		t.Errorf("a dotted provider name is not followed:\n%s", got)
	}
	src := sourcesOf(fileTree(&srcFile{path: "/x.jsonc", data: []byte(`{"a": 1}`)}, decode(t, `{"a": 1}`)))
	for _, msg := range []string{"config.b: unknown key", "Failed to parse x", "config.: nothing"} {
		if got := src.AnnotateOne(msg); got != msg {
			t.Errorf("AnnotateOne(%q) = %q; a message about no recorded key must pass unchanged", msg, got)
		}
	}
	var none *Sources
	if got := none.Annotate([]string{"config.a: x"}); got[0] != "config.a: x" {
		t.Errorf("a nil record annotated %q", got[0])
	}
}

// A key written twice in ONE file is one Locate will not guess at, so the refusal names the
// file without a line rather than a line that may be the wrong copy.
func TestAKeyWrittenTwiceInOneFileNamesTheFileAlone(t *testing.T) {
	sourcesHome(t)
	ws := t.TempDir()
	write(t, filepath.Join(ws, WorkspaceConfigName), `{"kvm": 1, "kvm": 2}`)
	errs, _ := locatedErrors(t, ws)
	got := line(t, errs, "config.kvm")
	if !strings.HasPrefix(got, filepath.Join(ws, WorkspaceConfigName)+": config.kvm") {
		t.Errorf("want the file alone:\n%s", got)
	}
}

// A problem with an include_if_found entry is located in the file that wrote it, in the
// validators' spelling.
func TestAnIncludeProblemNamesItsLine(t *testing.T) {
	_, dir := sourcesHome(t)
	write(t, filepath.Join(dir, "config.jsonc"), "{\n  \"include_if_found\": [\"ok.jsonc\", \"/etc/abs.jsonc\"]\n}")
	_, err := UserScopeConfig(true, discard)
	if err == nil {
		t.Fatal("an absolute include was accepted in strict mode")
	}
	want := "~/.config/yolo-jail/config.jsonc:2:36: config.include_if_found[1]: must be a relative path"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("got %v\nwant it to contain %q", err, want)
	}
}

// The workspace's preset/null conflict leads with where the null was written.
func TestPresetNullConflictLeadsWithTheNullsLine(t *testing.T) {
	ws := t.TempDir()
	p := filepath.Join(ws, WorkspaceConfigName)
	write(t, p, "{\"mcp_presets\": [\"chrome-devtools\"],\n \"mcp_servers\": {\"chrome-devtools\": null}}")
	cfg, src, err := LoadJSONCFileWithSources(p, WorkspaceConfigName, false, discard)
	if err != nil {
		t.Fatal(err)
	}
	got := PresetNullConflicts(cfg, WorkspaceConfigName, src)
	if len(got) != 1 || !strings.HasPrefix(got[0], p+":2:37: preset 'chrome-devtools'") {
		t.Errorf("got %q", got)
	}
	if got := PresetNullConflicts(cfg, WorkspaceConfigName, nil); len(got) != 1 ||
		!strings.HasPrefix(got[0], WorkspaceConfigName+": preset") {
		t.Errorf("with no record the label leads: %q", got)
	}
}

// `packs` problems reach verbs that run no validation (`yolo host`, `yolo pack`): LoadPacks'
// warning names where the skipped entry was written, and LoadPackEntries' problem keeps the
// `config.packs[i]: …` shape its callers split, with the user scope's record saying where.
func TestPackEntryProblemsNameTheirLine(t *testing.T) {
	_, dir := sourcesHome(t)
	write(t, filepath.Join(dir, "config.jsonc"), `{"include_if_found": ["packs.jsonc"]}`)
	write(t, filepath.Join(dir, "packs.jsonc"), "{\"packs\": [\n  5\n]}")
	var warned []string
	if _, err := LoadPacks(func(m string) { warned = append(warned, m) }); err != nil {
		t.Fatal(err)
	}
	if len(warned) != 1 || !strings.HasPrefix(warned[0], "~/.config/yolo-jail/packs.jsonc:2:3: config.packs[0]") {
		t.Errorf("LoadPacks warned %q", warned)
	}
	_, problems, err := LoadPackEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 1 || !strings.HasPrefix(problems[0], "config.packs[0]: ") {
		t.Fatalf("LoadPackEntries problems = %q, want the entry's place first", problems)
	}
	if got := UserScopeSources().Locations("config.packs[0]"); len(got) != 1 || got[0] != "~/.config/yolo-jail/packs.jsonc:2:3" {
		t.Errorf("UserScopeSources().Locations = %q", got)
	}
}

// A workspace-scope refusal about ONE workspace file's entry names that file, even where the
// merged record's winner is the other one: yolo-jail.jsonc installs the loophole (refused),
// yolo-jail.local.jsonc only describes it, and local wins the merge.
func TestWorkspaceLoopholeRefusalNamesTheFileThatWroteIt(t *testing.T) {
	sourcesHome(t)
	ws := t.TempDir()
	main, local := filepath.Join(ws, WorkspaceConfigName), filepath.Join(ws, WorkspaceLocalConfigName)
	write(t, main, "{\"loopholes\": {\n  \"foo\": {\"command\": [\"/bin/true\"]}\n}}")
	write(t, local, `{"loopholes": {"foo": {"description": "mine"}}}`)
	errs, _ := locatedErrors(t, ws)
	got := line(t, errs, "is INSTALLED here")
	if !strings.HasPrefix(got, main+":2:10: config.loopholes.foo") {
		t.Errorf("the refusal does not name the file that installs the loophole:\n%s", got)
	}
}

// A read-write mount in the workspace config is located at the element, in the workspace file.
func TestWorkspaceRWMountRefusalNamesTheElement(t *testing.T) {
	home, dir := sourcesHome(t)
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "config.jsonc"), `{"mounts": ["~/data"]}`)
	ws := t.TempDir()
	p := filepath.Join(ws, WorkspaceConfigName)
	write(t, p, "{\"mounts\": [\n  \"~/data:/ctx/ro\",\n  {\"host\": \"~/data\", \"at\": \"/ctx/rw\", \"mode\": \"rw\"}\n]}")
	errs, _ := locatedErrors(t, ws)
	got := line(t, errs, "is in the workspace config, and a read-write")
	if !strings.HasPrefix(got, p+":3:3: config.mounts") {
		t.Errorf("the refusal does not name the workspace element:\n%s", got)
	}
}

// The home is written as ~ whether a path spells it as HOME does or as its symlinks resolve.
func TestTildePathMatchesTheHomeThroughASymlink(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("cannot symlink here:", err)
	}
	resolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", link)
	for _, p := range []string{link + "/.config/x.jsonc", resolved + "/.config/x.jsonc"} {
		if got := tildePath(p); got != "~/.config/x.jsonc" {
			t.Errorf("tildePath(%q) = %q", p, got)
		}
	}
	if got := tildePath("/elsewhere/x.jsonc"); got != "/elsewhere/x.jsonc" {
		t.Errorf("a path outside the home changed: %q", got)
	}
}

// RECORDING CHANGES NOTHING ABOUT THE MERGE: over every branch MergeConfig takes — maps
// merged, lists unioned with a duplicate, a scalar overridden, a type replaced either way, a
// null — the merge with a record returns exactly the map the merge without one does.
func TestMergeWithSourcesIsMergeConfig(t *testing.T) {
	base := `{"a": {"x": 1, "l": [1, 2]}, "s": "one", "t": {"m": 1}, "u": 5, "n": 1, "l": [{"k": 1}]}`
	over := `{"a": {"y": 2, "l": [2, 3]}, "s": "two", "t": "flat", "u": {"m": 2}, "n": null, "l": [{"k": 1}, {"k": 2}], "new": [1]}`
	b, o := decode(t, base), decode(t, over)
	bs := sourcesOf(fileTree(&srcFile{path: "/b.jsonc", data: []byte(base)}, b))
	os := sourcesOf(fileTree(&srcFile{path: "/o.jsonc", data: []byte(over)}, o))
	plain, _ := jsonx.DumpsSnapshot(MergeConfig(b, o))
	for name, pair := range map[string][2]*Sources{
		"both recorded": {bs, os}, "base only": {bs, nil}, "override only": {nil, os}, "neither": {nil, nil},
	} {
		merged, _ := MergeConfigWithSources(b, pair[0], o, pair[1])
		got, _ := jsonx.DumpsSnapshot(merged)
		if got != plain {
			t.Errorf("%s: the merge with a record differs:\n got %s\nwant %s", name, got, plain)
		}
	}
	_, src := MergeConfigWithSources(b, bs, o, os)
	for path, want := range map[string]string{
		"config.a.x":    "/b.jsonc:1:13: config.a.x: m",
		"config.a.y":    "/o.jsonc:1:13: config.a.y: m",
		"config.a.l[1]": "/o.jsonc:1:22: config.a.l[1]: m (also written at /b.jsonc:1:25)",
		"config.a.l[2]": "/o.jsonc:1:25: config.a.l[2]: m",
		"config.t":      "/o.jsonc:1:47: config.t: m (also written at /b.jsonc:1:47)",
		"config.l[0]":   "/o.jsonc:1:87: config.l[0]: m (also written at /b.jsonc:1:79)",
		"config.l[1]":   "/o.jsonc:1:97: config.l[1]: m",
	} {
		if got := src.AnnotateOne(path + ": m"); got != want {
			t.Errorf("located as\n %q\nwant\n %q", got, want)
		}
	}
}

// THE PLAIN READERS' WARNINGS ARE LOCATED TOO. Each of these reads the user scope directly for
// verbs that run no validation (`yolo host`, `yolo pack`, `yolo check`'s own sections, the
// launch's staging), and warns about an entry it skips; the entry is written in one of the user
// scope's files, here an include.
func TestUserScopeReaderWarningsNameTheirLine(t *testing.T) {
	home, dir := sourcesHome(t)
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "config.jsonc"), `{"include_if_found": ["more.jsonc"]}`)
	write(t, filepath.Join(dir, "more.jsonc"), `{
  "profiles": {"a=b": {}},
  "adapters": {"x": 5},
  "host_files": [5],
  "cache_relocations": {"nix": 5}
}`)
	const at = "~/.config/yolo-jail/more.jsonc:"
	for _, tc := range []struct {
		name string
		read func(Warn) error
		want string
	}{
		{"LoadProfiles", func(w Warn) error { _, err := LoadProfiles(w); return err },
			at + "2:23: config.profiles.a=b: "},
		{"LoadAdapterAddresses", func(w Warn) error { _, err := LoadAdapterAddresses(w); return err },
			at + "3:21: config.adapters.x: "},
		{"LoadHostFiles", func(w Warn) error { _, err := LoadHostFiles(nil, w, false); return err },
			at + "4:18: config.host_files[0]: "},
		{"LoadCacheRelocations", func(w Warn) error { _, err := LoadCacheRelocations(w); return err },
			at + "5:32: config.cache_relocations.nix: "},
	} {
		var warned []string
		if err := tc.read(func(m string) { warned = append(warned, m) }); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if len(warned) != 1 || !strings.HasPrefix(warned[0], tc.want) {
			t.Errorf("%s warned %q\nwant one warning starting %q", tc.name, warned, tc.want)
		}
	}
}

// A read-write mount refused for its SOURCE names the element, by its index in the merged list,
// so the location is the element's own line rather than the `mounts` key's in every file
// writing one.
func TestRWMountSourceRefusalNamesTheElement(t *testing.T) {
	home, dir := sourcesHome(t)
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "config.jsonc"), `{"mounts": ["~/data:/ctx/ro"], "include_if_found": ["m.jsonc"]}`)
	write(t, filepath.Join(dir, "m.jsonc"), "{\"mounts\": [\n  {\"host\": \"~\", \"at\": \"/ctx/home\", \"mode\": \"rw\"}\n]}")
	errs, _ := locatedErrors(t, t.TempDir())
	got := line(t, errs, "is refused")
	if !strings.HasPrefix(got, "~/.config/yolo-jail/m.jsonc:2:3: config.mounts[1]: ") {
		t.Errorf("the refusal does not name the element's own line:\n%s", got)
	}
}

// The host_files two-writers refusal (a launch's, after the packs load) names the colliding
// entry by its place in the config and leads with the line it is written at.
func TestSurfaceCollisionNamesTheEntrysLine(t *testing.T) {
	_, dir := sourcesHome(t)
	write(t, filepath.Join(dir, "config.jsonc"), `{"include_if_found": ["files.jsonc"]}`)
	write(t, filepath.Join(dir, "files.jsonc"), `{"host_files": [
  {"path": "~/.config/a.json", "content": "{}"},
  {"path": "~/.config/b.json", "content": "{}"}
]}`)
	ws := t.TempDir()
	cfg, src, err := LoadConfigWithSources(ws, true, discard)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := LoadHostFiles(cfg, discard, false)
	if err != nil {
		t.Fatal(err)
	}
	got := SurfaceCollisions(entries, []string{"~/.config/b.json"}, cfg, src)
	want := "~/.config/yolo-jail/files.jsonc:3:3: config.host_files[1]: destination '~/.config/b.json' is also composed"
	if len(got) != 1 || !strings.HasPrefix(got[0], want) {
		t.Errorf("got %q\nwant one refusal starting %q", got, want)
	}
	if got := SurfaceCollisions(entries, []string{"~/.config/b.json"}, nil, nil); len(got) != 1 ||
		!strings.HasPrefix(got[0], "config.host_files: destination") {
		t.Errorf("with no config the key alone leads: %q", got)
	}
}

// A WORKSPACE host_files entry is named by its index in the workspace file even where an
// earlier entry there was refused: the validator used to count only the entries it accepted,
// so the source-bearing second entry below was reported as [0] — the line of the refused one.
func TestWorkspaceHostFilesRefusalCountsEveryEntry(t *testing.T) {
	sourcesHome(t)
	ws := t.TempDir()
	p := filepath.Join(ws, WorkspaceConfigName)
	write(t, p, "{\"host_files\": [\n  5,\n  \"~/.tool.conf\"\n]}")
	errs, _ := locatedErrors(t, ws)
	got := line(t, errs, "an entry that names a host source is user-scope only")
	if !strings.HasPrefix(got, p+":3:3: config.host_files[1]: ") {
		t.Errorf("the refusal does not name the workspace's second entry:\n%s", got)
	}
}

// A READER THAT DOES NOT REPORT KEEPS NO RECORD. LoadConfig and the other plain readers run many
// times a launch and would discard it, so recording it there is pure cost: the record is built
// from every value of every file. The plain read must allocate measurably less than the
// recording one over the same files; with the record kept on both they allocate the same.
func TestAPlainReadKeepsNoRecord(t *testing.T) {
	_, dir := sourcesHome(t)
	var incs []string
	for i := 0; i < 8; i++ {
		name := "inc" + strconv.Itoa(i) + ".jsonc"
		incs = append(incs, strconv.Quote(name))
		write(t, filepath.Join(dir, name), `{"packages": ["a`+strconv.Itoa(i)+`", "b"], "env": {"A": "1", "B": "2"},
  "mcp_servers": {"s`+strconv.Itoa(i)+`": {"command": "x", "args": ["1", "2", "3"]}}}`)
	}
	write(t, filepath.Join(dir, "config.jsonc"), `{"include_if_found": [`+strings.Join(incs, ", ")+`]}`)
	ws := t.TempDir()
	plain := testing.AllocsPerRun(20, func() { _, _ = LoadConfig(ws, true, discard) })
	recorded := testing.AllocsPerRun(20, func() { _, _, _ = LoadConfigWithSources(ws, true, discard) })
	if plain >= recorded*0.9 {
		t.Errorf("LoadConfig allocates %v per read, LoadConfigWithSources %v: the plain read is "+
			"building the record it discards", plain, recorded)
	}
}
