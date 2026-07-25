package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// Unit tests for the host_files config layer (docs/plans/host-file-staging.md
// Phase 1): shape validation, codec auto-detect, the source⊕content rule, path
// and source rejection, directory entries, mode defaults, layer-shape checks, the
// per-entry credential-boundary scope rule, cross-scope collisions, and the
// injective Slug derivation.

// hostFilesValue decodes a `host_files` list literal and returns the value the
// validators consume (checkHostFiles takes the list, not the enclosing map).
func hostFilesValue(t *testing.T, listJSON string) any {
	t.Helper()
	m := decode(t, `{"host_files": `+listJSON+`}`)
	v, ok := m.Get("host_files")
	if !ok {
		t.Fatal("host_files key missing after decode")
	}
	return v
}

// oneEntry asserts the list validates to exactly one accepted entry and returns
// it. probeSource is false — filesystem probing is covered by its own tests.
func oneEntry(t *testing.T, listJSON string) HostFileEntry {
	t.Helper()
	entries, problems := checkHostFiles(hostFilesValue(t, listJSON), "user", false)
	if len(problems) != 0 {
		t.Fatalf("unexpected problems for %s: %v", listJSON, problems)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 entry for %s, got %d: %+v", listJSON, len(entries), entries)
	}
	return entries[0]
}

// oneProblem asserts the list is rejected with exactly one problem naming substr,
// and that nothing was accepted.
func oneProblem(t *testing.T, listJSON, substr string) {
	t.Helper()
	entries, problems := checkHostFiles(hostFilesValue(t, listJSON), "user", false)
	if len(entries) != 0 {
		t.Fatalf("want no accepted entries for %s, got %+v", listJSON, entries)
	}
	if len(problems) != 1 {
		t.Fatalf("want 1 problem for %s, got %d: %v", listJSON, len(problems), problems)
	}
	if !strings.Contains(problems[0], substr) {
		t.Errorf("problem for %s = %q, want it to contain %q", listJSON, problems[0], substr)
	}
}

// ---- shape ----

func TestHostFilesNotAList(t *testing.T) {
	_, problems := checkHostFiles(hostFilesValue(t, `{"path": "~/.x"}`), "user", false)
	if len(problems) != 1 || !strings.Contains(problems[0], "expected a list") {
		t.Fatalf("problems = %v, want one 'expected a list'", problems)
	}
}

func TestHostFilesSugarString(t *testing.T) {
	e := oneEntry(t, `["~/.config/tool/conf.json"]`)
	if e.Path != ".config/tool/conf.json" {
		t.Errorf("Path = %q", e.Path)
	}
	if !e.SourceBearing() {
		t.Errorf("sugar string must be source-bearing")
	}
	if !filepath.IsAbs(e.Source) {
		t.Errorf("Source = %q, want absolute (expanded)", e.Source)
	}
	if e.Codec != "json" {
		t.Errorf("Codec = %q, want json (auto-detected)", e.Codec)
	}
	if e.Mode != HostFileModeReadonly {
		t.Errorf("Mode = %q, want readonly (source-bearing default)", e.Mode)
	}
	if e.IsDir {
		t.Errorf("IsDir = true, want false")
	}
}

func TestHostFilesObjectMinimalSourceless(t *testing.T) {
	e := oneEntry(t, `[{"path": "~/.config/tool/conf.json", "content": "{}"}]`)
	if e.SourceBearing() {
		t.Errorf("content-only entry must not be source-bearing")
	}
	if !e.HasContent || e.Content != "{}" {
		t.Errorf("Content = %q HasContent=%v", e.Content, e.HasContent)
	}
	if e.Mode != HostFileModeOnce {
		t.Errorf("Mode = %q, want once (source-less default)", e.Mode)
	}
}

func TestHostFilesEmptyContentIsAValidFile(t *testing.T) {
	// "content": "" is the documented way to declare an empty file — it must NOT
	// trip the "nothing to compose" guard.
	e := oneEntry(t, `[{"path": "~/.config/tool/empty", "content": ""}]`)
	if !e.HasContent || e.Content != "" {
		t.Errorf("Content=%q HasContent=%v, want empty-but-present", e.Content, e.HasContent)
	}
}

func TestHostFilesUnknownKey(t *testing.T) {
	oneProblem(t, `[{"path": "~/.x", "content": "y", "colour": "blue"}]`, "unknown key")
}

func TestHostFilesMissingPath(t *testing.T) {
	oneProblem(t, `[{"content": "y"}]`, "path")
}

func TestHostFilesPathWrongType(t *testing.T) {
	oneProblem(t, `[{"path": 42, "content": "y"}]`, "home-relative path string")
}

func TestHostFilesEntryWrongType(t *testing.T) {
	oneProblem(t, `[42]`, "expected a path string or an object")
}

func TestHostFilesNothingToCompose(t *testing.T) {
	// A source-less file entry with no content/defaults/managed composes to an
	// empty file — almost always a mistake, and rejected.
	oneProblem(t, `[{"path": "~/.config/tool/conf.json"}]`, "nothing to compose")
}

// ---- codec auto-detect + override ----

func TestHostFilesCodecAutoDetect(t *testing.T) {
	cases := map[string]string{
		"~/.config/x.json":     "json",
		"~/.config/x.toml":     "toml",
		"~/.config/x.yaml":     "raw",
		"~/.config/x.yml":      "raw",
		"~/.config/x.jsonc":    "raw",
		"~/.config/x.sh":       "raw",
		"~/.config/tool/noext": "raw",
		"~/.myrc":              "raw",
	}
	for path, want := range cases {
		e := oneEntry(t, `[{"path": "`+path+`", "content": ""}]`)
		if e.Codec != want {
			t.Errorf("codec for %q = %q, want %q", path, e.Codec, want)
		}
	}
}

func TestHostFilesCodecExplicitOverride(t *testing.T) {
	// A .json path with an explicit raw codec: the file is JSON-shaped but the
	// user wants byte-preserving handling (comments, key order).
	e := oneEntry(t, `[{"path": "~/.config/x.json", "content": "// hi\n{}", "codec": "raw"}]`)
	if e.Codec != "raw" {
		t.Errorf("Codec = %q, want raw (explicit override)", e.Codec)
	}
}

func TestHostFilesCodecUnknownRejected(t *testing.T) {
	oneProblem(t, `[{"path": "~/.config/x.conf", "content": "", "codec": "yaml"}]`, "no 'yaml' codec")
}

// ---- source ⊕ content ----

func TestHostFilesSourceAndContentMutuallyExclusive(t *testing.T) {
	oneProblem(t, `[{"path": "~/.x", "source": "/abs/x", "content": "y"}]`, "mutually exclusive")
}

func TestHostFilesSourceWrongType(t *testing.T) {
	oneProblem(t, `[{"path": "~/.x", "source": 5}]`, "host path string")
}

// ---- path rejection ----

func TestHostFilesPathRejection(t *testing.T) {
	cases := []struct{ path, substr string }{
		{"", "must not be empty"},
		{"~", "not the home directory itself"},
		{"/etc/passwd", "not an absolute path"},
		{"~/../escape", "escape $HOME"},
		{"~/.", "not the home directory itself"},
		{"~/a:b", "':'"},
	}
	for _, c := range cases {
		// content keeps the entry otherwise-valid so the PATH is what's rejected.
		oneProblem(t, `[{"path": "`+c.path+`", "content": "y"}]`, c.substr)
	}
}

func TestHostFilesReservedDestinations(t *testing.T) {
	// Files yolo mounts/materializes directly, and builtin composed surfaces —
	// writing any of them from config would clobber yolo's own file.
	reserved := []string{
		"~/.bashrc",
		"~/.gitconfig",
		"~/.claude.json",
		"~/.claude/settings.json",
		"~/.config/mise/config.toml",
	}
	for _, p := range reserved {
		oneProblem(t, `[{"path": "`+p+`", "content": "y"}]`, "managed by yolo")
	}
}

func TestHostFilesDotConfigSubpathAllowed(t *testing.T) {
	// The whole point of exact-path (not first-segment) reservation: a NEW file
	// under ~/.config is the central use case and must be accepted.
	e := oneEntry(t, `[{"path": "~/.config/mytool/config.json", "content": "{}"}]`)
	if e.Path != ".config/mytool/config.json" {
		t.Errorf("Path = %q", e.Path)
	}
}

// ---- source rejection ----

func TestHostFilesSourceRejection(t *testing.T) {
	oneProblem(t, `[{"path": "~/.x", "source": "relative/path"}]`, "absolute host path")
	oneProblem(t, `[{"path": "~/.x", "source": "/abs/a:b"}]`, "':'")
}

// ---- directories ----

func TestHostFilesDirSugarTrailingSlash(t *testing.T) {
	e := oneEntry(t, `["~/.config/tool/"]`)
	if !e.IsDir {
		t.Errorf("IsDir = false, want true (trailing slash)")
	}
	if e.Codec != "" {
		t.Errorf("Codec = %q, want empty (a dir is not a codec)", e.Codec)
	}
	if e.Mode != HostFileModeCopy {
		t.Errorf("Mode = %q, want copy (dir default)", e.Mode)
	}
}

func TestHostFilesDirSourcePromotesIsDir(t *testing.T) {
	// A source ending in "/" is a directory copy even when the DEST has no
	// trailing slash — the source's shape decides.
	e := oneEntry(t, `[{"path": "~/dst", "source": "/abs/src/"}]`)
	if !e.IsDir {
		t.Errorf("IsDir = false, want true (dir source)")
	}
}

func TestHostFilesDirRejectsCompositionKeys(t *testing.T) {
	cases := []struct{ json, substr string }{
		{`[{"path": "~/d/", "source": "/s/", "codec": "json"}]`, "'codec' cannot be used with a directory"},
		{`[{"path": "~/d/", "content": "x"}]`, "'content' cannot be used with a directory"},
		{`[{"path": "~/d/", "source": "/s/", "transform": "~/t.lua"}]`, "'transform' cannot be used with a directory"},
		{`[{"path": "~/d/", "source": "/s/", "managed": {"a": 1}}]`, "'managed' cannot be used with a directory"},
		{`[{"path": "~/d/", "source": "/s/", "defaults": {"a": 1}}]`, "'defaults' cannot be used with a directory"},
	}
	for _, c := range cases {
		oneProblem(t, c.json, c.substr)
	}
}

func TestHostFilesDirModeOnlyCopy(t *testing.T) {
	oneProblem(t, `[{"path": "~/d/", "source": "/s/", "mode": "readonly"}]`, "only supports")
	// copy is explicitly allowed.
	e := oneEntry(t, `[{"path": "~/d/", "source": "/s/", "mode": "copy"}]`)
	if e.Mode != HostFileModeCopy {
		t.Errorf("Mode = %q, want copy", e.Mode)
	}
}

func TestHostFilesDirNeedsSource(t *testing.T) {
	// A directory has no composition layers, so a source is the only thing it can
	// copy from — one with none is rejected (Phase 2 would get an empty src path).
	oneProblem(t, `[{"path": "~/d/"}]`, "a directory entry needs a 'source'")
}

// ---- modes ----

func TestHostFilesModeExplicitCapture(t *testing.T) {
	// capture is never a default but is legal when asked for explicitly.
	e := oneEntry(t, `[{"path": "~/.config/x.json", "content": "{}", "mode": "capture"}]`)
	if e.Mode != HostFileModeCapture {
		t.Errorf("Mode = %q, want capture", e.Mode)
	}
}

func TestHostFilesModeUnknownRejected(t *testing.T) {
	oneProblem(t, `[{"path": "~/.x", "content": "y", "mode": "sometimes"}]`, "expected one of")
}

// TestHostFileDefaultModeNeverCapture is the §"capture is opt-in" invariant at
// the source: no combination of (sourceBearing, isDir) may resolve to capture,
// because a captured edit outranks the host file forever.
func TestHostFileDefaultModeNeverCapture(t *testing.T) {
	for _, sb := range []bool{true, false} {
		for _, dir := range []bool{true, false} {
			if got := hostFileDefaultMode(sb, dir); got == HostFileModeCapture {
				t.Errorf("hostFileDefaultMode(sourceBearing=%v, isDir=%v) = capture, want anything else", sb, dir)
			}
		}
	}
}

// ---- layer shape per codec kind ----

func TestHostFilesLayerShapeMatches(t *testing.T) {
	// The layer's shape must match the codec's KIND, or the render would silently
	// drop it or blow up.
	good := []string{
		`[{"path": "~/.config/x.json", "defaults": {"a": 1}}]`,             // object for json
		`[{"path": "~/x.list", "codec": "lines", "defaults": ["a", "b"]}]`, // list for lines
		`[{"path": "~/x.txt", "defaults": "hello"}]`,                       // string for raw
	}
	for _, g := range good {
		oneEntry(t, g)
	}
	bad := []struct{ json, substr string }{
		{`[{"path": "~/.config/x.json", "defaults": [1, 2]}]`, "expected an object"},
		{`[{"path": "~/x.list", "codec": "lines", "defaults": {"a": 1}}]`, "expected a list"},
		{`[{"path": "~/x.txt", "defaults": {"a": 1}}]`, "expected a string"},
	}
	for _, b := range bad {
		oneProblem(t, b.json, b.substr)
	}
}

func TestHostFilesManagedLowersToPlain(t *testing.T) {
	// The engine type-switches on map[string]any, so a jsonx.*OrderedMap must be
	// lowered — assert the stored layer is the plain model, not a jsonx type.
	e := oneEntry(t, `[{"path": "~/.config/x.json", "managed": {"a": {"b": 1}}}]`)
	m, ok := e.Managed.(map[string]any)
	if !ok {
		t.Fatalf("Managed is %T, want map[string]any (plain model)", e.Managed)
	}
	if _, ok := m["a"].(map[string]any); !ok {
		t.Errorf("Managed[a] is %T, want a nested map[string]any", m["a"])
	}
}

// ---- collisions ----

func TestHostFilesDuplicateDestination(t *testing.T) {
	// The first claim of a destination is accepted; the second is the problem.
	entries, problems := checkHostFiles(hostFilesValue(t,
		`[{"path": "~/.config/x.json", "content": "a"}, {"path": "~/.config/x.json", "content": "b"}]`),
		"user", false)
	if len(entries) != 1 || entries[0].Content != "a" {
		t.Fatalf("entries = %+v, want only the first (content 'a')", entries)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "already declared by entry [0]") {
		t.Fatalf("problems = %v, want one naming entry [0]", problems)
	}
}

// ---- Slug: injective and non-empty ----

// TestHostFileSlugInjective pins the property the whole sidecar scheme depends
// on: distinct destination Paths yield distinct, non-empty slugs. It carries the
// exact collision families that falsified the previous lossy-flatten derivation,
// plus the ".x20" vs " " pair that broke the first _x-prefixed escape attempt.
func TestHostFileSlugInjective(t *testing.T) {
	paths := []string{
		".config/mytool/config.json",
		".config/mytool.config.json", // vs the above: the lossy-flatten collision
		".config/my_tool/x",
		".config/my/tool/x", // vs the above
		".bashrc.extra",
		".bashrc_extra",            // vs the above
		"a.b", "a_b", "a/b", "a b", // the 4-way flatten collision
		".x", "..x",
		"...", "_", "/", ".",
		".x20", " ", // the pair that broke the _x-prefix scheme
		"_5f", "z_5f", // an escaped-underscore output vs a literal that reads like one
	}
	seen := map[string]string{}
	for _, p := range paths {
		s := HostFileEntry{Path: p}.Slug()
		if s == "" {
			t.Errorf("Slug(%q) is empty", p)
		}
		if prev, dup := seen[s]; dup {
			t.Errorf("collision: %q and %q both -> %q", prev, p, s)
		}
		seen[s] = p
	}
}

// TestHostFileSlugCoversSuitePaths asserts the derivation is injective across
// EVERY destination path used elsewhere in this suite too — the standing rule
// that a distinctness regression is caught wherever a path appears, not only in
// the hand-picked adversarial set above.
func TestHostFileSlugCoversSuitePaths(t *testing.T) {
	paths := []string{
		".config/tool/conf.json", ".config/tool/empty", ".x",
		".config/x.json", ".config/x.toml", ".config/x.yaml", ".config/x.yml",
		".config/x.jsonc", ".config/x.sh", ".config/tool/noext", ".myrc",
		".config/mytool/config.json", "dst", "x.list", "x.txt", "d",
	}
	seen := map[string]string{}
	for _, p := range paths {
		s := HostFileEntry{Path: p}.Slug()
		if s == "" {
			t.Errorf("Slug(%q) is empty", p)
		}
		if prev, dup := seen[s]; dup {
			t.Errorf("collision: %q and %q both -> %q", prev, p, s)
		}
		seen[s] = p
	}
}

// ---- scope: the credential boundary ----
//
// The whole feature turns on ONE rule: a SOURCE-BEARING entry (it crosses a host
// file into the jail) is legal only in the USER config; a repo's own
// yolo-jail.jsonc — agent-editable, travelling with the repo — must never widen
// which host files cross. A SOURCE-LESS entry copies nothing from the host and is
// legal at any scope.

// TestLoadHostFilesUserSourceBearing: a source-bearing entry in the USER config
// is loaded. probeSource=false so a nonexistent source path is not an error.
func TestLoadHostFilesUserSourceBearing(t *testing.T) {
	userConfigHome(t, `{"host_files": ["~/.config/tool/conf.json"]}`)
	got, err := LoadHostFiles(nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].SourceBearing() || got[0].Path != ".config/tool/conf.json" {
		t.Fatalf("got %+v, want one source-bearing entry", got)
	}
	if got[0].Scope != "user" {
		t.Errorf("Scope = %q, want user", got[0].Scope)
	}
}

// TestLoadHostFilesWorkspaceSourceBearingIgnored is the boundary enforced BY
// CONSTRUCTION: a source-bearing entry that reaches LoadHostFiles only through the
// merged (workspace-inclusive) map is dropped, because the source-bearing half is
// read from the user config ALONE. This is the case validateHostFiles turns into
// a loud error; here we prove the loader itself never crosses it.
func TestLoadHostFilesWorkspaceSourceBearingIgnored(t *testing.T) {
	userConfigHome(t, "") // user config has no host_files
	// A merged map carrying a source-bearing entry (as a workspace config would).
	merged := decode(t, `{"host_files": ["~/.ssh/id_ed25519"]}`)
	got, err := LoadHostFiles(merged, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want none — a source-bearing entry from the merged map must be dropped", got)
	}
}

// TestLoadHostFilesSourcelessFromMerged: a source-less entry IS taken from the
// merged map, so a workspace may contribute one. It crosses no host file.
func TestLoadHostFilesSourcelessFromMerged(t *testing.T) {
	userConfigHome(t, "")
	merged := decode(t, `{"host_files": [{"path": "~/.config/tool/conf.json", "content": "{}"}]}`)
	got, err := LoadHostFiles(merged, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SourceBearing() {
		t.Fatalf("got %+v, want one source-less entry", got)
	}
}

// TestLoadHostFilesUserSourcelessNotDoubled: a user config's source-less entry is
// present in BOTH the user config and the merged map (the merge includes it). It
// must appear exactly once, not twice.
func TestLoadHostFilesUserSourcelessNotDoubled(t *testing.T) {
	home := userConfigHome(t, `{"host_files": [{"path": "~/.config/tool/conf.json", "content": "{}"}]}`)
	// Simulate the merged map by re-reading the user config (the merge is a
	// superset of it). One entry, one destination.
	merged, err := LoadConfig(home, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := LoadHostFiles(merged, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d entries, want exactly 1 (the source-less user entry must not double)", len(got))
	}
}

// TestLoadHostFilesSorted: entries come back sorted by Path so the mount argv and
// render order are deterministic.
func TestLoadHostFilesSorted(t *testing.T) {
	userConfigHome(t, `{"host_files": ["~/.config/zeta.json", "~/.config/alpha.json"]}`)
	got, err := LoadHostFiles(nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Path != ".config/alpha.json" || got[1].Path != ".config/zeta.json" {
		t.Fatalf("got %+v, want sorted by Path", got)
	}
}

// TestLoadHostFilesUnparseableUserConfigErrors: a broken user config is a hard
// error, never a silently empty list (a dropped host_files entry looks exactly
// like the feature not working).
func TestLoadHostFilesUnparseableUserConfigErrors(t *testing.T) {
	userConfigHome(t, "{not valid jsonc")
	if _, err := LoadHostFiles(nil, nil, false); err == nil {
		t.Error("expected an error for an unparseable user config")
	}
}

// TestLoadHostFilesNilWarnDoesNotPanic: a nil warn is discarded, not dereferenced.
func TestLoadHostFilesNilWarnDoesNotPanic(t *testing.T) {
	userConfigHome(t, `{"host_files": [{"path": "~/.config/x.json"}]}`) // a malformed (empty) entry -> warns
	if _, err := LoadHostFiles(nil, nil, false); err != nil {
		t.Fatal(err)
	}
}

// TestValidateHostFilesWorkspaceSourceBearingErrors is the defense-in-depth half:
// a source-bearing entry written in the WORKSPACE config is a hard `yolo check`
// error, so it fails loudly instead of being a silent no-op.
func TestValidateHostFilesWorkspaceSourceBearingErrors(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("YOLO_VERSION", "") // host behavior: probe + workspace re-read run
	write(t, filepath.Join(ws, WorkspaceConfigName), `{"host_files": ["~/.ssh/id_ed25519"]}`)

	// The merged map ValidateConfig would hand us carries the same entry.
	merged := decode(t, `{"host_files": ["~/.ssh/id_ed25519"]}`)
	var errs []string
	validateHostFiles(merged, ws, &errs)
	if len(errs) == 0 {
		t.Fatal("want an error for a source-bearing workspace entry")
	}
	joined := strings.Join(errs, "\n")
	if !strings.Contains(joined, "user-scope only") {
		t.Errorf("errors = %v, want one saying 'user-scope only'", errs)
	}
}

// TestValidateHostFilesWorkspaceSourcelessOK: a source-less entry in the
// workspace config is legal — it crosses no host file.
func TestValidateHostFilesWorkspaceSourcelessOK(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("YOLO_VERSION", "")
	write(t, filepath.Join(ws, WorkspaceConfigName),
		`{"host_files": [{"path": "~/.config/tool/conf.json", "content": "{}"}]}`)

	merged := decode(t, `{"host_files": [{"path": "~/.config/tool/conf.json", "content": "{}"}]}`)
	var errs []string
	validateHostFiles(merged, ws, &errs)
	if len(errs) != 0 {
		t.Errorf("errors = %v, want none (a source-less workspace entry is allowed)", errs)
	}
}

// ---- source probing (host-only) ----

// TestProbeHostFileSourceMissingIsFine: an absent source is a normal state (a
// dotfile the user has not created), not an error.
func TestProbeHostFileSourceMissingIsFine(t *testing.T) {
	e := HostFileEntry{Source: filepath.Join(t.TempDir(), "nope"), Codec: "json"}
	if msg := probeHostFileSource(e); msg != "" {
		t.Errorf("probe of a missing source = %q, want no error", msg)
	}
}

// TestProbeHostFileSourceMismatch: a file/directory shape mismatch is silently
// wrong (a dir composed as a file, or vice versa), so it IS caught.
func TestProbeHostFileSourceMismatch(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f")
	write(t, file, "x")

	// dir source, file entry.
	if msg := probeHostFileSource(HostFileEntry{Source: dir, Codec: "json"}); msg == "" {
		t.Error("dir source composed as a single file: want a mismatch error")
	}
	// file source, dir entry.
	if msg := probeHostFileSource(HostFileEntry{Source: file, IsDir: true}); msg == "" {
		t.Error("file source declared as a directory: want a mismatch error")
	}
	// matched shapes: no error.
	if msg := probeHostFileSource(HostFileEntry{Source: file, Codec: "json"}); msg != "" {
		t.Errorf("file source, file entry = %q, want no error", msg)
	}
	if msg := probeHostFileSource(HostFileEntry{Source: dir, IsDir: true}); msg != "" {
		t.Errorf("dir source, dir entry = %q, want no error", msg)
	}
}

// TestCheckHostFilesInJailSkipsProbe: with probeSource=false (the in-jail path),
// a source pointing at a nonexistent host file is accepted — the host path is
// deliberately not in the jail's mount namespace, so stat'ing it would turn a
// valid host config into a fatal error on every nested run.
func TestCheckHostFilesInJailSkipsProbe(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	missing := filepath.Join(home, "does-not-exist.json")
	entries, problems := checkHostFiles(
		hostFilesValue(t, `[{"path": "~/.config/x.json", "source": "`+missing+`"}]`),
		"user", false)
	if len(problems) != 0 || len(entries) != 1 {
		t.Fatalf("in-jail (no probe): entries=%+v problems=%v, want one accepted entry", entries, problems)
	}
}

// ---- YOLO_HOST_FILES wire form ----

// TestMarshalHostFilesEmptyIsBlank pins the contract UnmarshalHostFiles and the
// entrypoint rely on: no entries is the empty string, not "[]" or "null". An
// unset feature must leave the env var empty so the entrypoint's is-it-set check
// (and macos-user's) reads it back as no entries with no allocation.
func TestMarshalHostFilesEmptyIsBlank(t *testing.T) {
	for _, in := range [][]HostFileEntry{nil, {}} {
		got, err := MarshalHostFiles(in)
		if err != nil {
			t.Fatalf("MarshalHostFiles(%v): %v", in, err)
		}
		if got != "" {
			t.Errorf("MarshalHostFiles(%v) = %q, want empty string", in, got)
		}
	}
	// The mirror: a blank or whitespace-only var is no entries, no error.
	for _, s := range []string{"", "   ", "\n\t"} {
		entries, err := UnmarshalHostFiles(s)
		if err != nil {
			t.Fatalf("UnmarshalHostFiles(%q): %v", s, err)
		}
		if entries != nil {
			t.Errorf("UnmarshalHostFiles(%q) = %+v, want nil", s, entries)
		}
	}
}

// TestHostFilesWireRoundTrip proves the resolved entries survive the trip through
// the env var intact — including the two traps: HasContent must distinguish an
// explicit empty file from an absent one (a boolean that json-omits when false),
// and the Managed layer must land back in the engine's PLAIN value model
// (map[string]any / float64), never a jsonx type, since the compose engine
// type-switches on that model and would treat anything else as an opaque scalar.
func TestHostFilesWireRoundTrip(t *testing.T) {
	// Build the entries through the real validator so Managed is lowered exactly as
	// production does (jsonx.Plain), not hand-constructed.
	in := []HostFileEntry{
		oneEntry(t, `[{"path": "~/.config/a.json", "source": "/etc/a.json", "mode": "capture"}]`),
		oneEntry(t, `[{"path": "~/.config/b.json", "content": ""}]`),
		oneEntry(t, `[{"path": "~/.config/c.json", "managed": {"n": 5, "nested": {"k": "v"}}}]`),
		oneEntry(t, `[{"path": "~/lib/", "source": "/opt/lib/"}]`),
	}

	wire, err := MarshalHostFiles(in)
	if err != nil {
		t.Fatalf("MarshalHostFiles: %v", err)
	}
	out, err := UnmarshalHostFiles(wire)
	if err != nil {
		t.Fatalf("UnmarshalHostFiles: %v", err)
	}
	if len(out) != len(in) {
		t.Fatalf("round-trip changed entry count: got %d, want %d", len(out), len(in))
	}

	// b.json carries "content": "" — HasContent must survive as true, else the
	// entrypoint would treat it as source-less-with-nothing and skip it.
	if !out[1].HasContent || out[1].Content != "" {
		t.Errorf("empty-content entry lost HasContent: %+v", out[1])
	}
	// c.json's managed layer must be plain map[string]any with a float64 leaf.
	m, ok := out[2].Managed.(map[string]any)
	if !ok {
		t.Fatalf("managed round-tripped to %T, want map[string]any", out[2].Managed)
	}
	if n, ok := m["n"].(float64); !ok || n != 5 {
		t.Errorf("managed[n] = %#v, want float64(5)", m["n"])
	}
	if nested, ok := m["nested"].(map[string]any); !ok || nested["k"] != "v" {
		t.Errorf("managed[nested] = %#v, want plain map", m["nested"])
	}
	// The directory entry's IsDir and Mode (copy) survive.
	if !out[3].IsDir || out[3].Mode != HostFileModeCopy {
		t.Errorf("dir entry mangled: IsDir=%v Mode=%q", out[3].IsDir, out[3].Mode)
	}
}

// TestSourceLessHostFiles is the macos-user gate: only source-less entries pass
// (there is no /ctx/host-user mount there to carry a source into), and the ones
// that do are returned unchanged and in order.
func TestSourceLessHostFiles(t *testing.T) {
	entries := []HostFileEntry{
		{Path: ".config/a.json", Source: "/etc/a.json"},
		{Path: ".config/b.json", HasContent: true},
		{Path: ".config/c.json", Source: "/etc/c.json"},
		{Path: ".config/d.json", Defaults: map[string]any{"k": "v"}},
	}
	got := SourceLessHostFiles(entries)
	if len(got) != 2 {
		t.Fatalf("SourceLessHostFiles kept %d, want 2 (the source-less ones): %+v", len(got), got)
	}
	if got[0].Path != ".config/b.json" || got[1].Path != ".config/d.json" {
		t.Errorf("SourceLessHostFiles kept the wrong entries: %+v", got)
	}
}

// ---- destination staging (docs/design/composed-file-permissions.md §7.5) ----

// TestHostFileStagingCategories pins which destinations need host-side staging to
// be writable. This is the whole reason ~/.npmrc did not work: the jail home is a
// :ro bind, so only a destination under an existing rw bind composes for free.
func TestHostFileStagingCategories(t *testing.T) {
	for _, c := range []struct {
		name  string
		entry HostFileEntry
		want  HostFileStaging
	}{
		{"under .config is already rw", HostFileEntry{Path: ".config/mytool/config.json"}, HostFileStagingNone},
		{"under .cache is already rw", HostFileEntry{Path: ".cache/tool/x.json"}, HostFileStagingNone},
		{"under .local is already rw", HostFileEntry{Path: ".local/share/x"}, HostFileStagingNone},
		{"under an agent overlay dir is rw", HostFileEntry{Path: ".claude/extra.json"}, HostFileStagingNone},
		{"pi overlay dir is rw", HostFileEntry{Path: ".pi/agent/models.json"}, HostFileStagingNone},
		{"home-root dotfile needs the symlink hatch", HostFileEntry{Path: ".npmrc"}, HostFileStagingSymlink},
		{"home-root plain file needs the symlink hatch", HostFileEntry{Path: "gitignore_global"}, HostFileStagingSymlink},
		{"new top-level dir needs a writable subtree", HostFileEntry{Path: "foo/bar.json"}, HostFileStagingWritableDir},
		{"deep new top-level dir needs a writable subtree", HostFileEntry{Path: "foo/bar/baz.conf"}, HostFileStagingWritableDir},
		{"home-root DIR entry needs a writable subtree", HostFileEntry{Path: "mydir", IsDir: true}, HostFileStagingWritableDir},
		{"dir under .config is already rw", HostFileEntry{Path: ".config/nvim", IsDir: true}, HostFileStagingNone},
	} {
		if got := c.entry.StagingFor(); got != c.want {
			t.Errorf("%s: StagingFor(%q, isDir=%v) = %v, want %v",
				c.name, c.entry.Path, c.entry.IsDir, got, c.want)
		}
	}
}

// TestHostFileSymlinkTargetIsInWritableConfig: the symlink hatch must point INTO
// a read-write overlay, or it is a dangling link to another read-only path. It
// must also be slug-keyed so two home-root entries can never share a target.
func TestHostFileSymlinkTargetIsInWritableConfig(t *testing.T) {
	a := HostFileEntry{Path: ".npmrc"}
	b := HostFileEntry{Path: ".gitignore_global"}
	for _, e := range []HostFileEntry{a, b} {
		target := e.SymlinkTarget()
		if !strings.HasPrefix(target, ".config/") {
			t.Errorf("SymlinkTarget(%q) = %q, want a path under .config/ (the rw overlay)", e.Path, target)
		}
		// The target itself must need no further staging, else the hatch is moot.
		if got := (HostFileEntry{Path: target}).StagingFor(); got != HostFileStagingNone {
			t.Errorf("SymlinkTarget(%q) = %q, which itself needs staging %v", e.Path, target, got)
		}
	}
	if a.SymlinkTarget() == b.SymlinkTarget() {
		t.Errorf("two home-root entries share a symlink target %q — captured content would cross over", a.SymlinkTarget())
	}
}

// TestHostFileWritableParent: a file entry stages its PARENT (the dir that must
// exist), a directory entry stages ITSELF (copyTree copies into it).
func TestHostFileWritableParent(t *testing.T) {
	for _, c := range []struct{ path, want string }{
		{"foo/bar.json", "foo"},
		{"foo/bar/baz.conf", "foo/bar"},
	} {
		if got := (HostFileEntry{Path: c.path}).WritableParent(); got != c.want {
			t.Errorf("WritableParent(%q) = %q, want %q", c.path, got, c.want)
		}
	}
	if got := (HostFileEntry{Path: "mydir", IsDir: true}).WritableParent(); got != "mydir" {
		t.Errorf("WritableParent(dir mydir) = %q, want mydir (the tree is copied INTO it)", got)
	}
	// A home-root FILE has no meaningful parent dir to stage — it takes the
	// symlink route instead, but WritableParent must not return "." either way.
	if got := (HostFileEntry{Path: ".npmrc"}).WritableParent(); got == "." {
		t.Errorf("WritableParent(.npmrc) = %q, must never be the home root itself", got)
	}
}

// TestSourceLessHostFilesFromMerged is the pure reader macos-user uses: it must
// return the source-less entries from a merged map and NEVER the source-bearing
// ones, because that half requires the user-config-only read that IS the
// credential boundary.
func TestSourceLessHostFilesFromMerged(t *testing.T) {
	merged := decode(t, `{"host_files": [
		{"path": "~/.config/seed.json", "content": "{}"},
		"~/.config/crosses.json",
		{"path": "~/.config/layered.json", "defaults": {"k": "v"}}
	]}`)
	got := SourceLessHostFilesFrom(merged)
	if len(got) != 2 {
		t.Fatalf("SourceLessHostFilesFrom returned %d entries, want 2: %+v", len(got), got)
	}
	for _, e := range got {
		if e.SourceBearing() {
			t.Errorf("a source-bearing entry leaked through the pure reader: %+v", e)
		}
	}
	if got[0].Path != ".config/layered.json" || got[1].Path != ".config/seed.json" {
		t.Errorf("entries not sorted by path: %+v", got)
	}
}

// TestSourceLessHostFilesFromEmpty: absent key and nil map are both "no entries",
// never a panic (macos-user calls this on every launch).
func TestSourceLessHostFilesFromEmpty(t *testing.T) {
	if got := SourceLessHostFilesFrom(nil); got != nil {
		t.Errorf("nil map returned %+v, want nil", got)
	}
	if got := SourceLessHostFilesFrom(decode(t, `{}`)); got != nil {
		t.Errorf("absent key returned %+v, want nil", got)
	}
}

// ---- inferred `path` (review round 0) ----

// TestHostFilesInfersPathFromSource: mirroring a host file at the same place is
// the common case, so `path` may be omitted when `source` is a "~/…" path. Writing
// the same path twice is noise, and invites the two halves drifting apart.
func TestHostFilesInfersPathFromSource(t *testing.T) {
	e := oneEntry(t, `[{"source": "~/.config/mytool/config.json", "mode": "capture"}]`)
	if e.Path != ".config/mytool/config.json" {
		t.Errorf("inferred Path = %q, want the source's home-relative path", e.Path)
	}
	if e.Codec != "json" {
		t.Errorf("inferred entry lost codec auto-detect: %q", e.Codec)
	}
	if !e.SourceBearing() {
		t.Error("inferred entry must still be source-bearing (it crosses a host file)")
	}
	if e.Mode != HostFileModeCapture {
		t.Errorf("explicit mode lost: %q", e.Mode)
	}
}

// TestHostFilesInferredPathKeepsDirShape: a trailing "/" on the source must survive
// the inference, or a directory entry silently becomes a single-file one.
func TestHostFilesInferredPathKeepsDirShape(t *testing.T) {
	e := oneEntry(t, `[{"source": "~/.pi/agent/themes/"}]`)
	if !e.IsDir {
		t.Errorf("inferred dir entry lost IsDir: %+v", e)
	}
	if e.Path != ".pi/agent/themes" {
		t.Errorf("inferred dir Path = %q, want .pi/agent/themes", e.Path)
	}
	if e.Mode != HostFileModeCopy {
		t.Errorf("dir mode = %q, want copy", e.Mode)
	}
}

// TestHostFilesPathRequiredWithoutSource: a source-less entry has nothing to infer
// from, so `path` stays required — and the error must say when it can be omitted.
func TestHostFilesPathRequiredWithoutSource(t *testing.T) {
	_, problems := checkHostFiles(hostFilesValue(t, `[{"content": "x"}]`), "user", false)
	if len(problems) != 1 {
		t.Fatalf("expected one problem, got %v", problems)
	}
	for _, want := range []string{".path: required", "'source'"} {
		if !strings.Contains(problems[0], want) {
			t.Errorf("error %q missing %q", problems[0], want)
		}
	}
}

// TestHostFilesPathRequiredForNonHomeSource: an absolute source outside $HOME has
// no unambiguous home-relative counterpart (/etc/foo.conf could mean several
// things), so it must name its destination rather than have one guessed.
func TestHostFilesPathRequiredForNonHomeSource(t *testing.T) {
	_, problems := checkHostFiles(hostFilesValue(t, `[{"source": "/etc/foo.conf"}]`), "user", false)
	if len(problems) != 1 {
		t.Fatalf("expected one problem, got %v", problems)
	}
	if !strings.Contains(problems[0], "not under $HOME") {
		t.Errorf("error %q should explain why nothing can be inferred", problems[0])
	}
}

// TestHostFilesInferredPathStillReserved: inference must not become a bypass for
// the reserved-destination guard.
func TestHostFilesInferredPathStillReserved(t *testing.T) {
	_, problems := checkHostFiles(
		hostFilesValue(t, `[{"source": "~/.claude/settings.json"}]`), "user", false)
	if len(problems) != 1 || !strings.Contains(problems[0], "managed by yolo") {
		t.Errorf("inferred path bypassed the reserved-destination guard: %v", problems)
	}
}
