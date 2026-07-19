package loopholes

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// writeManifest writes a manifest.jsonc built from a Go map (json.Marshal, like
// the Python fixtures' json.dumps(data, indent=2)).
func writeManifest(t *testing.T, dir string, data map[string]any) {
	t.Helper()
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.jsonc"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func mkdir(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// modsDir matches the `mods_dir` fixture: a fresh loopholes root.
func modsDir(t *testing.T) string {
	t.Helper()
	return mkdir(t, filepath.Join(t.TempDir(), "loopholes"))
}

func discoverDir(root string, includeDisabled bool) []*Loophole {
	return Discover(DiscoverOptions{Root: root, RootSet: true, IncludeDisabled: includeDisabled, IncludeBundled: false})
}

func names(loaded []*Loophole) []string {
	out := make([]string, len(loaded))
	for i, m := range loaded {
		out[i] = m.Name
	}
	return out
}

func TestDiscoverEmptyAndNonexistent(t *testing.T) {
	md := modsDir(t)
	if got := discoverDir(md, false); len(got) != 0 {
		t.Errorf("empty dir: got %v", names(got))
	}
	if got := discoverDir(filepath.Join(md, "does-not-exist"), false); len(got) != 0 {
		t.Errorf("nonexistent dir: got %v", names(got))
	}
}

func TestLoadsMinimalManifest(t *testing.T) {
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "my-mod"))
	writeManifest(t, mod, map[string]any{"name": "my-mod", "description": "test"})
	loaded := discoverDir(md, false)
	if len(loaded) != 1 {
		t.Fatalf("expected 1, got %d", len(loaded))
	}
	m := loaded[0]
	if m.Name != "my-mod" || !m.Enabled || m.Transport != "tls-intercept" || m.Lifecycle != "external" {
		t.Errorf("defaults wrong: %+v", m)
	}
	if len(m.Intercepts) != 0 || m.CACertSet {
		t.Errorf("intercepts/ca defaults wrong: %+v", m)
	}
}

func TestNameMustMatchDirectory(t *testing.T) {
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "dir-name"))
	writeManifest(t, mod, map[string]any{"name": "different-name", "description": "x"})
	if got := discoverDir(md, false); len(got) != 0 {
		t.Errorf("should skip mismatched name: %v", names(got))
	}
	entries := ValidateLoopholes(md, true, false)
	if len(entries) != 1 || entries[0].Loophole != nil {
		t.Fatalf("expected 1 error entry, got %+v", entries)
	}
	if !contains(entries[0].Err, "disagrees with directory") {
		t.Errorf("err = %q", entries[0].Err)
	}
}

func TestDisabledSkippedByDefault(t *testing.T) {
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "off"))
	writeManifest(t, mod, map[string]any{"name": "off", "description": "x", "enabled": false})
	if got := discoverDir(md, false); len(got) != 0 {
		t.Errorf("disabled should be skipped: %v", names(got))
	}
	got := discoverDir(md, true)
	if len(got) != 1 || got[0].Name != "off" {
		t.Errorf("include_disabled: got %v", names(got))
	}
}

func TestInvalidTransportAndLifecycleRejected(t *testing.T) {
	md := modsDir(t)
	bt := mkdir(t, filepath.Join(md, "bad-transport"))
	writeManifest(t, bt, map[string]any{"name": "bad-transport", "description": "x", "transport": "carrier-pigeon"})
	bl := mkdir(t, filepath.Join(md, "bad-lifecycle"))
	writeManifest(t, bl, map[string]any{"name": "bad-lifecycle", "description": "x", "lifecycle": "orbiting"})
	entries := ValidateLoopholes(md, true, false)
	byName := map[string]ValidateEntry{}
	for _, e := range entries {
		byName[filepath.Base(e.Path)] = e
	}
	if e := byName["bad-transport"]; e.Loophole != nil || !contains(e.Err, "transport=") {
		t.Errorf("bad-transport err = %q", e.Err)
	}
	if e := byName["bad-lifecycle"]; e.Loophole != nil || !contains(e.Err, "lifecycle=") {
		t.Errorf("bad-lifecycle err = %q", e.Err)
	}
}

func TestInvalidManifestDoesNotBreakOthers(t *testing.T) {
	md := modsDir(t)
	good := mkdir(t, filepath.Join(md, "good"))
	writeManifest(t, good, map[string]any{"name": "good", "description": "x"})
	bad := mkdir(t, filepath.Join(md, "bad"))
	if err := os.WriteFile(filepath.Join(bad, "manifest.jsonc"), []byte("{not: json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := names(discoverDir(md, false)); !reflect.DeepEqual(got, []string{"good"}) {
		t.Errorf("got %v", got)
	}
}

func TestHiddenDirsSkipped(t *testing.T) {
	md := modsDir(t)
	hidden := mkdir(t, filepath.Join(md, ".git"))
	writeManifest(t, hidden, map[string]any{"name": ".git", "description": "x"})
	if got := discoverDir(md, false); len(got) != 0 {
		t.Errorf("hidden dir should be skipped: %v", names(got))
	}
}

func TestConfigSynthesizedAsLoopholes(t *testing.T) {
	md := modsDir(t)
	cfg := orderedFromPairs("journal", map[string]any{"description": "journalctl bridge"},
		"cgroup-delegate", map[string]any{"description": "cgroup v2 delegate"})
	loaded := Discover(DiscoverOptions{Root: md, RootSet: true, LoopholesConfig: cfg})
	got := names(loaded)
	if !containsStr(got, "journal") || !containsStr(got, "cgroup-delegate") {
		t.Fatalf("got %v", got)
	}
	for _, m := range loaded {
		if m.Transport != "unix-socket" || m.Lifecycle != "spawned" || !m.FromConfig() {
			t.Errorf("synthesized loophole shape wrong: %+v", m)
		}
	}
}

func TestWorkspaceOverrideMergesEnabled(t *testing.T) {
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "bundled-like"))
	writeManifest(t, mod, map[string]any{"name": "bundled-like", "description": "x", "enabled": false})
	cfg := orderedFromPairs("bundled-like", map[string]any{"enabled": true})
	loaded := Discover(DiscoverOptions{Root: md, RootSet: true, IncludeDisabled: true, LoopholesConfig: cfg})
	if len(loaded) != 1 || loaded[0].Name != "bundled-like" || !loaded[0].Enabled || loaded[0].Source != SourceUser {
		t.Errorf("override merge wrong: %+v", loaded)
	}
}

func TestWorkspaceOverrideMergesHostDaemonEnv(t *testing.T) {
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "swaymsg-like"))
	writeManifest(t, mod, map[string]any{
		"name": "swaymsg-like", "description": "x",
		"host_daemon": map[string]any{"cmd": []any{"some-daemon", "--socket", "{socket}"}, "env": map[string]any{"DEFAULT_KEY": "default"}},
	})
	cfg := orderedFromPairs("swaymsg-like", map[string]any{"env": map[string]any{"SWAYSOCK": "/run/user/1000/sway.sock"}})
	loaded := Discover(DiscoverOptions{Root: md, RootSet: true, LoopholesConfig: cfg})
	if len(loaded) != 1 || loaded[0].HostDaemon == nil {
		t.Fatalf("got %+v", loaded)
	}
	env := loaded[0].HostDaemon.Env
	if v, _ := env.Get("DEFAULT_KEY"); v != "default" {
		t.Errorf("DEFAULT_KEY = %q", v)
	}
	if v, _ := env.Get("SWAYSOCK"); v != "/run/user/1000/sway.sock" {
		t.Errorf("SWAYSOCK = %q", v)
	}
}

func TestWorkspaceInlineWhenNoMatchingManifest(t *testing.T) {
	md := modsDir(t)
	cfg := orderedFromPairs("pure-workspace", map[string]any{"description": "new inline"})
	loaded := Discover(DiscoverOptions{Root: md, RootSet: true, LoopholesConfig: cfg})
	if len(loaded) != 1 || loaded[0].Name != "pure-workspace" || !loaded[0].FromConfig() || loaded[0].Source != SourceConfig {
		t.Errorf("inline synthesis wrong: %+v", loaded)
	}
}

func TestSetEnabledRoundtrip(t *testing.T) {
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "togg"))
	writeManifest(t, mod, map[string]any{"name": "togg", "description": "x", "enabled": true})
	if err := SetEnabled(mod, false); err != nil {
		t.Fatal(err)
	}
	if got := discoverDir(md, false); len(got) != 0 {
		t.Errorf("after disable: %v", names(got))
	}
	if got := discoverDir(md, true); len(got) != 1 {
		t.Errorf("include_disabled after disable: %v", names(got))
	}
	if err := SetEnabled(mod, true); err != nil {
		t.Fatal(err)
	}
	got := discoverDir(md, false)
	if len(got) != 1 || !got[0].Enabled {
		t.Errorf("after re-enable: %+v", got)
	}
}

func TestSetEnabledDropsComments(t *testing.T) {
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "commented"))
	body := "// a leading comment\n{\n  \"name\": \"commented\", // inline\n  \"description\": \"x\",\n  \"enabled\": true\n}\n"
	if err := os.WriteFile(filepath.Join(mod, "manifest.jsonc"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled(mod, false); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(mod, "manifest.jsonc"))
	if contains(string(out), "leading comment") || contains(string(out), "inline") {
		t.Errorf("comments should be dropped, got:\n%s", out)
	}
	if !contains(string(out), "// yolo-jail loophole manifest.") {
		t.Errorf("header missing:\n%s", out)
	}
}

// unsetJail clears YOLO_VERSION for the duration of a host-side test (this
// suite may run inside a jail where it is set). t.Setenv registers restoration.
func unsetJail(t *testing.T) {
	t.Helper()
	t.Setenv("YOLO_VERSION", "")
	os.Unsetenv("YOLO_VERSION")
}

func TestRequiresCommandOnPath(t *testing.T) {
	unsetJail(t)
	md := modsDir(t)
	miss := mkdir(t, filepath.Join(md, "needs-xyz"))
	writeManifest(t, miss, map[string]any{"name": "needs-xyz", "description": "x",
		"requires": map[string]any{"command_on_path": "xyz-never-exists-abc"}})
	loaded := discoverDir(md, false)
	if len(loaded) != 1 || loaded[0].RequirementsMet() || loaded[0].Active() {
		t.Fatalf("missing cmd should be inactive: %+v", loaded)
	}
	reason, ok := loaded[0].InactiveReason()
	if !ok || !contains(reason, "xyz-never-exists-abc") {
		t.Errorf("reason = %q", reason)
	}

	md2 := modsDir(t)
	present := mkdir(t, filepath.Join(md2, "needs-sh"))
	writeManifest(t, present, map[string]any{"name": "needs-sh", "description": "x",
		"requires": map[string]any{"command_on_path": "sh"}})
	loaded2 := discoverDir(md2, false)
	if !loaded2[0].RequirementsMet() || !loaded2[0].Active() {
		t.Errorf("sh should be active")
	}
	if r, ok := loaded2[0].InactiveReason(); ok {
		t.Errorf("expected no reason, got %q", r)
	}
}

func TestRequiresFileExistsEnvCollapse(t *testing.T) {
	unsetJail(t)
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "audio-like"))
	writeManifest(t, mod, map[string]any{"name": "audio-like", "description": "x",
		"requires": map[string]any{"file_exists": "${XDG_RUNTIME_DIR}/pulse/native"}})

	// Unset -> collapses to /pulse/native (empty var), which won't exist.
	t.Setenv("XDG_RUNTIME_DIR", "")
	os.Unsetenv("XDG_RUNTIME_DIR")
	if discoverDir(md, false)[0].Active() {
		t.Errorf("unset XDG_RUNTIME_DIR should be inactive")
	}

	// Set to a real dir with the socket present -> active.
	runtime := t.TempDir()
	mkdir(t, filepath.Join(runtime, "pulse"))
	if err := os.WriteFile(filepath.Join(runtime, "pulse", "native"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	if !discoverDir(md, false)[0].Active() {
		t.Errorf("present socket should be active")
	}
	os.Remove(filepath.Join(runtime, "pulse", "native"))
	if discoverDir(md, false)[0].Active() {
		t.Errorf("removed socket should be inactive")
	}
}

func TestExpandEnvUnit(t *testing.T) {
	t.Setenv("FOO", "bar")
	os.Unsetenv("MISSING_VAR_XYZ")
	cases := map[string]string{
		"${FOO}/x":             "bar/x",
		"$FOO-$FOO":            "bar-bar",
		"${MISSING_VAR_XYZ}/y": "/y", // unresolved collapses to empty
		"$MISSING_VAR_XYZ/z":   "/z", // ditto
		"no refs here":         "no refs here",
		"literal $ sign":       "literal $ sign", // lone $ not a ref
	}
	for in, want := range cases {
		if got := expandEnv(in); got != want {
			t.Errorf("expandEnv(%q) = %q, want %q", in, got, want)
		}
	}
}

// helpers ------------------------------------------------------------------

func contains(s, sub string) bool { return indexOf(s, sub) >= 0 }

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// TestRepoRootHostModeFindsBundled is the audit §B3 regression: with
// YOLO_REPO_ROOT unset (host mode, no shim), repoRoot() must walk up to the real
// yolo-jail checkout and resolve bundled_loopholes there — NOT fall back to the
// in-jail /opt/yolo-jail (which is empty on a host) and drop every bundled
// loophole. Does NOT monkeypatch BundledLoopholesDir (per the audit).
func TestRepoRootHostModeFindsBundled(t *testing.T) {
	t.Setenv("YOLO_REPO_ROOT", "")
	os.Unsetenv("YOLO_REPO_ROOT")
	// cwd during `go test` is the package dir (a descendant of the repo), so the
	// walk should reach the real checkout.
	rr := repoRoot()
	if !fileExists(filepath.Join(rr, "go.mod")) {
		t.Fatalf("repoRoot()=%q is not a yolo-jail checkout (host-mode B3 regression)", rr)
	}
	got := Discover(DiscoverOptions{IncludeDisabled: true, IncludeBundled: true})
	if len(got) == 0 {
		t.Fatal("host-mode discovery found ZERO loopholes — audit §B3 regression")
	}
}
