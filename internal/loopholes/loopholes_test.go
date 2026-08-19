package loopholes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// writeManifest writes a manifest.jsonc built from a Go map via
// json.MarshalIndent.
//
// It SUPPLIES `default_enabled: true` when the caller did not state one, and that
// default is the opposite of the schema's on purpose. After OQ-A9 flipped the
// manifest's default to OFF (docs/design/loophole-activation.md R2), a fixture that
// says nothing about enablement produces a loophole that is not discovered, not
// active, and absent from every report — so every test in this package about
// something ELSE (platforms, requires, doctor_cmd, workspace overrides, the origin
// gate) would go on passing while measuring an empty list. That is a silent
// coverage loss, not a failure, which is why the default is stated here rather than
// left to the schema.
//
// A caller that CARES about enablement writes the key, and its explicit value wins —
// which is what the disabled-loophole tests in this file rely on.
func writeManifest(t *testing.T, dir string, data map[string]any) {
	t.Helper()
	if _, stated := data["default_enabled"]; !stated {
		withDefault := make(map[string]any, len(data)+1)
		for k, v := range data {
			withDefault[k] = v
		}
		withDefault["default_enabled"] = true
		data = withDefault
	}
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

// discoverDir runs discovery over ONE directory of loophole modules, by offering each
// child as a pack-contributed module.
//
// TWO SUBSTITUTIONS DEEP, and the history is the reason the helper exists at all. It
// pointed DiscoverOptions.Root at the directory until OQ-LP10 retired the hand-placed
// user loopholes channel; then it pointed the BUNDLED root at it, chosen over pack
// modules because these tests exercise the FULL manifest vocabulary (`jail_env`,
// absolute bind hosts, `publishes: "endpoint"`) that the pack loader refuses. The
// bundled channel is retired too now (docs/design/broker-as-a-pack.md OQ-BP4), so PACK
// MODULES ARE THE ONLY MODULE SOURCE LEFT and the subset is not avoidable any more.
//
// That is not a loss of coverage, it is a relocation of it: a test whose subject IS the
// wider vocabulary now says so by calling LoadLoophole (the tolerant, unrestricted
// loader, which internal/cli/run's inert-loophole report still uses in production)
// rather than by picking a discovery source that happened to be lenient. What
// discoverDir covers is discovery — ordering, precedence, the config overlay, the
// enabled filter — over manifests a pack could actually ship.
func discoverDir(root string, includeDisabled bool) []*Loophole {
	return discoverWithConfig(root, includeDisabled, nil)
}

// discoverWithConfig is discoverDir plus a `loopholes:` config block.
func discoverWithConfig(root string, includeDisabled bool, cfg *jsonx.OrderedMap) []*Loophole {
	return Discover(DiscoverOptions{
		IncludeDisabled: includeDisabled,
		LoopholesConfig: cfg,
		PackModules:     moduleDirsUnder(root),
	})
}

// validateDir is `yolo check`'s walker over ONE directory, same substitution.
func validateDir(root string) []ValidateEntry {
	defer withModuleDir(root)()
	return ValidateLoopholes()
}

// withModuleDir records root's children as this process's pack modules and returns the
// restore func — for the surfaces that read the RECORD (ValidateLoopholes, NewHostSet)
// rather than taking DiscoverOptions.
func withModuleDir(root string) func() {
	SetPackModules(moduleDirsUnder(root))
	return func() { ResetPackModules() }
}

// moduleDirsUnder lists root's loophole module dirs — every non-hidden child holding a
// manifest.jsonc — in sorted order, each APPROVED to touch the host.
//
// Approved because these fixtures stand in for yolo's own content: an unapproved module
// is still discovered but crosses nothing, which would turn every argv assertion in this
// file into a test of the origin gate instead of its own subject. The gate has its own
// tests (packshipped_test.go, loopholeorigingate_test.go).
func moduleDirsUnder(root string) []PackModule {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	var out []PackModule
	for _, name := range names {
		if strings.HasPrefix(name, ".") {
			continue
		}
		dir := filepath.Join(root, name)
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "manifest.jsonc")); err != nil {
			continue
		}
		out = append(out, PackModule{Dir: dir, HostExecApproved: true})
	}
	return out
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
	if m.Name != "my-mod" || !m.Enabled || m.Transport != TransportLoopbackTLS || m.Lifecycle != "external" {
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
	entries := validateDir(md)
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
	writeManifest(t, mod, map[string]any{"name": "off", "description": "x", "default_enabled": false})
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
	entries := validateDir(md)
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
	cfg := orderedFromPairs(
		"journal", map[string]any{"description": "journalctl bridge"},
		"sockd", map[string]any{
			"description": "third-party socket daemon",
			"command":     []any{"sockd", "--socket", "{socket}"},
		})
	loaded := discoverWithConfig(md, false, cfg)
	byName := map[string]*Loophole{}
	for _, m := range loaded {
		byName[m.Name] = m
		if m.Lifecycle != "spawned" || !m.FromConfig() {
			t.Errorf("synthesized loophole shape wrong: %+v", m)
		}
	}
	// A command-bearing entry is a third-party daemon binding a plain AF_UNIX
	// socket at {socket}. The record says what is TRUE of it — loopback-tls
	// behind yolo's front (publishes "socket") — with the argv unchanged. This
	// is the discover.go flip loophole-packaging.md §2.2 costs at "nothing":
	// the daemon is now WRAPPED rather than expected to publish.
	s := byName["sockd"]
	if s == nil {
		t.Fatalf("sockd not synthesized: %v", names(loaded))
	}
	if s.Transport != TransportLoopbackTLS {
		t.Errorf("sockd Transport = %q, want %q", s.Transport, TransportLoopbackTLS)
	}
	if s.HostDaemon == nil || s.HostDaemon.Publishes != PublishesSocket {
		t.Errorf("sockd HostDaemon = %+v, want Publishes=%q", s.HostDaemon, PublishesSocket)
	}
	if s.HostDaemon != nil && !reflect.DeepEqual(s.HostDaemon.Cmd, []string{"sockd", "--socket", "{socket}"}) {
		t.Errorf("sockd argv changed across synthesis: %v", s.HostDaemon.Cmd)
	}
	// "the argv is unchanged, the daemon's behaviour is unchanged" is the whole
	// promise of the flip, and the connection preamble is the one thing that
	// could break it — sockd's protocol has no room for a frame it never asked
	// for. So a config entry's default is OFF, the opposite of a manifest's.
	if s.HostDaemon != nil && s.HostDaemon.Preamble {
		t.Error("a config-declared daemon defaulted to receiving a preamble; " +
			"it is a third-party program that never saw the key")
	}
	// A command-less entry runs no daemon, and TransportNone means exactly that
	// — not a stub advertising a transport nothing serves.
	j := byName["journal"]
	if j == nil {
		t.Fatalf("journal not synthesized: %v", names(loaded))
	}
	if j.Transport != TransportNone || j.HostDaemon != nil {
		t.Errorf("command-less entry: Transport = %q, HostDaemon = %+v; want %q and nil",
			j.Transport, j.HostDaemon, TransportNone)
	}
	// The retired value is gone from BOTH sides now: no manifest can declare it
	// (pinned by TestValidTransportsIsLoopbackTLSAndNone) and no synthesized
	// record carries it.
	for _, m := range loaded {
		if m.Transport == loopholedecl.RetiredTransportUnixSocket {
			t.Errorf("%s still carries the retired transport", m.Name)
		}
	}
	if containsStr(loopholedecl.ValidTransports(), loopholedecl.RetiredTransportUnixSocket) {
		t.Error("a MANIFEST can declare the retired unix-socket value")
	}
}

// TestConfigLoopholePreambleOptIn: OFF by default is a conservative default, not
// a ceiling. A user who knows their daemon speaks yolo's transport — or who
// wrote it — says so with one key, and gets the same host-asserted jail identity
// a manifest loophole gets.
func TestConfigLoopholePreambleOptIn(t *testing.T) {
	md := modsDir(t)
	cfg := orderedFromPairs("mine", map[string]any{
		"command":  []any{"mine", "--socket", "{socket}"},
		"preamble": true,
	})
	loaded := discoverWithConfig(md, false, cfg)
	if len(loaded) != 1 || loaded[0].HostDaemon == nil {
		t.Fatalf("got %+v", loaded)
	}
	if !loaded[0].HostDaemon.Preamble {
		t.Error("'preamble': true on a config entry did not opt in")
	}
}

func TestWorkspaceOverrideMergesEnabled(t *testing.T) {
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "module-like"))
	writeManifest(t, mod, map[string]any{"name": "module-like", "description": "x", "default_enabled": false})
	cfg := orderedFromPairs("module-like", map[string]any{"enabled": true})
	loaded := discoverWithConfig(md, true, cfg)
	if len(loaded) != 1 || loaded[0].Name != "module-like" || !loaded[0].Enabled || loaded[0].Source != SourcePack {
		t.Errorf("override merge wrong: %+v", loaded)
	}
}

func TestWorkspaceOverrideMergesHostDaemonEnv(t *testing.T) {
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "swaymsg-like"))
	writeManifest(t, mod, map[string]any{
		"name": "swaymsg-like", "description": "x",
		"host_daemon": map[string]any{"cmd": []any{"some-daemon", "--socket", "{socket}"},
			// `publishes: "socket"` is DECLARED rather than defaulted because the fixture is
			// read through the pack loader now: the default is "endpoint", which the
			// pack-shipped subset refuses, and a refused manifest does not fail loudly — the
			// loophole simply vanishes and the assertions below read as an env-merge bug.
			"publishes": "socket",
			"env":       map[string]any{"DEFAULT_KEY": "default"}},
	})
	cfg := orderedFromPairs("swaymsg-like", map[string]any{"env": map[string]any{"SWAYSOCK": "/run/user/1000/sway.sock"}})
	loaded := discoverWithConfig(md, false, cfg)
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
	loaded := discoverWithConfig(md, false, cfg)
	if len(loaded) != 1 || loaded[0].Name != "pure-workspace" || !loaded[0].FromConfig() || loaded[0].Source != SourceConfig {
		t.Errorf("inline synthesis wrong: %+v", loaded)
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

// TestRequiresFileExistsEnvCollapse pins the ${VAR} expansion in `requires.file_exists`,
// through the UNRESTRICTED loader.
//
// LoadLoophole rather than discovery, and the reason is worth stating because it looks
// like a weakening. A pack-shipped loophole may not write `${XDG_RUNTIME_DIR}/...` at all
// — the subset refuses it, since a variable naming an absolute host path one indirection
// later would make the rule about spelling — and packs are the only module source left
// after `bundled_loopholes/` was retired (docs/design/broker-as-a-pack.md OQ-BP4). So
// routing this through discovery would silently stop testing expansion and start testing
// the refusal, which packshipped_test.go already owns.
//
// What is under test is the LOADER's resolution of the key, which is live for every
// manifest yolo reads: internal/cli/run's inert-loophole report calls LoadLoophole in
// production, and `audio` carried exactly this spelling until it became a pack.
func TestRequiresFileExistsEnvCollapse(t *testing.T) {
	unsetJail(t)
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "audio-like"))
	writeManifest(t, mod, map[string]any{"name": "audio-like", "description": "x",
		"requires": map[string]any{"file_exists": "${XDG_RUNTIME_DIR}/pulse/native"}})

	active := func() bool {
		lp, err := LoadLoophole(mod)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		return lp.Active()
	}

	// Unset -> collapses to /pulse/native (empty var), which won't exist.
	t.Setenv("XDG_RUNTIME_DIR", "")
	os.Unsetenv("XDG_RUNTIME_DIR")
	if active() {
		t.Errorf("unset XDG_RUNTIME_DIR should be inactive")
	}

	// Set to a real dir with the socket present -> active.
	runtime := t.TempDir()
	mkdir(t, filepath.Join(runtime, "pulse"))
	if err := os.WriteFile(filepath.Join(runtime, "pulse", "native"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	if !active() {
		t.Errorf("present socket should be active")
	}
	os.Remove(filepath.Join(runtime, "pulse", "native"))
	if active() {
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

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// TestRepoRootHostModeFindsBundled WAS HERE AND IS DELETED, 2026-08-19, and the deletion
// is worth a note because a regression test is otherwise never removed.
//
// Its subject was audit §B3: with YOLO_REPO_ROOT unset (host mode, no shim), the shared
// resolver had to walk up to the real yolo-jail checkout so `BundledLoopholesDir` found
// `bundled_loopholes/` there instead of degrading to an empty in-jail path and dropping
// every bundled loophole. Both halves of that sentence are gone — the function and the
// directory — because the bundled channel is retired
// (docs/design/broker-as-a-pack.md OQ-BP4). Nothing resolves a loophole manifest through
// reporoot any more; a pack's module dir is its STAGED copy under paths.AgentsDir(), which
// no repo walk is involved in finding.
//
// The property it protected did not evaporate, it moved: "the launch sees the loopholes
// yolo ships" is now a question about pack staging, pinned by
// internal/cli/run's TestTheOfficialLoopholePacksAreNotRefusedByTheirOwnReservation and by
// the pack embed's own drift test.

// captureWarnings swaps the package's warn sink for the duration of a test and
// returns the accumulated lines.
func captureWarnings(t *testing.T) *[]string {
	t.Helper()
	var got []string
	prev := warnf
	warnf = func(format string, args ...any) { got = append(got, fmt.Sprintf(format, args...)) }
	t.Cleanup(func() { warnf = prev })
	return &got
}

// TestRejectedManifestIsReportedNotVanished pins the loudness of the worst failure
// mode in the loophole framework.
//
// loadModuleDirs used to `continue` on any load error, so ONE rejected field made the
// entire loophole disappear: no host daemon, no endpoint, no injected env var, no
// entry in `yolo loopholes list`, and no message anywhere saying why. Every
// downstream failure then named something else. bundled_loopholes is go:embed'd, so
// an installed binary fails the same way with no checkout to inspect — and the
// migration to a new transport value is exactly the change that produces it.
func TestRejectedManifestIsReportedNotVanished(t *testing.T) {
	warnings := captureWarnings(t)
	md := modsDir(t)
	good := mkdir(t, filepath.Join(md, "good"))
	writeManifest(t, good, map[string]any{"name": "good", "description": "x"})
	bad := mkdir(t, filepath.Join(md, "bad-transport"))
	writeManifest(t, bad, map[string]any{
		"name": "bad-transport", "description": "x", "transport": "carrier-pigeon",
	})

	loaded := names(discoverDir(md, true))
	if !containsStr(loaded, "good") {
		t.Errorf("discovery = %v, want the valid loophole to survive one bad neighbour", loaded)
	}
	if containsStr(loaded, "bad-transport") {
		t.Errorf("discovery = %v, want the rejected manifest excluded", loaded)
	}

	if len(*warnings) == 0 {
		t.Fatal("a rejected manifest produced NO warning: the loophole vanished silently, " +
			"which is the failure this test exists to prevent")
	}
	var found string
	for _, w := range *warnings {
		if contains(w, bad) {
			found = w
		}
	}
	if found == "" {
		t.Fatalf("no warning named the offending directory %s; got %v", bad, *warnings)
	}
	// The warning has to carry the REASON as well as the path — "something in here
	// failed" sends the reader back to the validator to find out what.
	if !contains(found, "transport=") {
		t.Errorf("warning %q does not carry the load error", found)
	}
	// And it must say the consequence, not just that a file was skipped: the
	// loophole is ABSENT, which is what the reader is about to be confused by.
	if !contains(found, "NOT active") {
		t.Errorf("warning %q does not say the loophole is inactive as a result", found)
	}
}

// TestUnknownManifestKeyDegradesRatherThanVanishes pins the other half of the
// strict/tolerant split, from discovery's side.
//
// Discovery reads TOLERANTLY, so a manifest key only a newer yolo knows must NOT
// take the loophole down with it — that is the `tier` incident's shape, and the
// recovery for a manifest yolo itself ships is "rebuild the image". But it must
// not be silent either, for the same reason the rejection above is not: everything
// the unknown key would have declared is missing, and a degraded loophole whose
// symptom names something else is exactly what costs an afternoon.
func TestUnknownManifestKeyDegradesRatherThanVanishes(t *testing.T) {
	warnings := captureWarnings(t)
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "from-the-future"))
	writeManifest(t, mod, map[string]any{
		"name": "from-the-future", "description": "x",
		"host_teleporter": map[string]any{"cmd": []any{"beam"}},
	})

	loaded := discoverDir(md, true)
	if len(loaded) != 1 || loaded[0].Name != "from-the-future" {
		t.Fatalf("discovery = %v, want the loophole to survive an unknown key", names(loaded))
	}
	if len(loaded[0].SkewNotes) != 1 || !contains(loaded[0].SkewNotes[0], "host_teleporter") {
		t.Errorf("SkewNotes = %v, want one note naming the unknown key", loaded[0].SkewNotes)
	}
	var found string
	for _, w := range *warnings {
		if contains(w, "host_teleporter") {
			found = w
		}
	}
	if found == "" {
		t.Fatalf("no warning named the unknown key; got %v", *warnings)
	}
	if !contains(found, "from-the-future") {
		t.Errorf("warning %q does not name the loophole it degrades", found)
	}

	// The AUTHOR-facing path is the strict one, and it refuses the same manifest —
	// the asymmetry that makes both halves right.
	if _, err := loopholedecl.LoadDir(mod); err == nil {
		t.Error("the strict decoder accepted an unknown key; an author must hear about a typo")
	}
}

// TestWarnSinkIsNotSilentByDefault: the sink above is only meaningful if the
// SHIPPED default actually goes somewhere. warnf was a no-op "for callers that
// install a sink" and nothing in the tree ever installed one, so every warning
// this package emitted was discarded.
func TestWarnSinkIsNotSilentByDefault(t *testing.T) {
	var buf bytes.Buffer
	prevStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	warnf("canary %s", "line")
	_ = w.Close()
	os.Stderr = prevStderr
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	if !contains(buf.String(), "canary line") {
		t.Errorf("the default warn sink discarded its message (captured %q) — a warning "+
			"nobody can read is not a diagnostic", buf.String())
	}
}

// TestLoopbackTLSIsAValidTransport: the manifest validator must accept the unified
// transport BEFORE any manifest declares it. A manifest changed ahead of its
// validator is the R1 failure — the loophole silently disappears.
func TestLoopbackTLSIsAValidTransport(t *testing.T) {
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "tls-svc"))
	writeManifest(t, mod, map[string]any{
		"name": "tls-svc", "description": "x", "transport": "loopback-tls",
	})
	got := discoverDir(md, true)
	if len(got) != 1 || got[0].Name != "tls-svc" {
		t.Fatalf("loaded %v, want [tls-svc]", names(got))
	}
	if got[0].Transport != "loopback-tls" {
		t.Errorf("Transport = %q, want loopback-tls", got[0].Transport)
	}
}

// TestValidTransportsIsLoopbackTLSAndNone pins the retirement itself. "unix-socket"
// and "tls-intercept" are REMOVED, not deprecated — the maintainer's reason being
// that a value which still validates is a value someone will use — so the
// vocabulary is exactly two entries and this test fails the moment a third
// reappears.
func TestValidTransportsIsLoopbackTLSAndNone(t *testing.T) {
	want := []string{TransportLoopbackTLS, TransportNone}
	if len(loopholedecl.ValidTransports()) != len(want) {
		t.Fatalf("loopholedecl.ValidTransports() = %v, want exactly %v", loopholedecl.ValidTransports(), want)
	}
	for _, w := range want {
		if !containsStr(loopholedecl.ValidTransports(), w) {
			t.Errorf("loopholedecl.ValidTransports() = %v, missing %q", loopholedecl.ValidTransports(), w)
		}
	}
	for _, retired := range []string{loopholedecl.RetiredTransportUnixSocket, loopholedecl.RetiredTransportTLSIntercept} {
		if containsStr(loopholedecl.ValidTransports(), retired) {
			t.Errorf("loopholedecl.ValidTransports() still accepts the retired %q", retired)
		}
	}
}

// TestAbsentTransportDefaultsToLoopbackTLS: with one transport, saying nothing
// means it. The old default was "tls-intercept", so a manifest that never
// mentioned transports claimed to intercept TLS.
func TestAbsentTransportDefaultsToLoopbackTLS(t *testing.T) {
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "quiet"))
	writeManifest(t, mod, map[string]any{"name": "quiet", "description": "x"})
	got := discoverDir(md, true)
	if len(got) != 1 {
		t.Fatalf("loaded %v, want [quiet]", names(got))
	}
	if got[0].Transport != TransportLoopbackTLS {
		t.Errorf("default Transport = %q, want %q", got[0].Transport, TransportLoopbackTLS)
	}
}

// TestRetiredTransportRejectedWithMigrationHint: removing a documented value
// breaks third-party manifests that used it, and the consequence of the break is
// that the loophole VANISHES. The rejection therefore has to name the replacement
// — the bare enum error tells a reader what is wrong and not what to do about it.
func TestRetiredTransportRejectedWithMigrationHint(t *testing.T) {
	for _, tc := range []struct {
		transport   string
		wantSubstrs []string
	}{
		// The unix-socket hint must send a migrating author down the EASY path:
		// keep binding the socket at {socket}, declare publishes:"socket", and
		// yolo fronts it — not "publish an endpoint file yourself", which is the
		// harder of the two supported shapes (loophole-packaging.md §2.2).
		{loopholedecl.RetiredTransportUnixSocket, []string{"{socket}", "publishes"}},
		{loopholedecl.RetiredTransportTLSIntercept, []string{"intercepts"}},
	} {
		t.Run(tc.transport, func(t *testing.T) {
			md := modsDir(t)
			mod := mkdir(t, filepath.Join(md, "legacy"))
			writeManifest(t, mod, map[string]any{
				"name": "legacy", "description": "x", "transport": tc.transport,
			})
			_, err := LoadLoophole(mod)
			if err == nil {
				t.Fatalf("transport=%q still loads; it was supposed to be REMOVED", tc.transport)
			}
			msg := err.Error()
			if !contains(msg, TransportLoopbackTLS) {
				t.Errorf("error does not name the replacement transport: %s", msg)
			}
			for _, want := range tc.wantSubstrs {
				if !contains(msg, want) {
					t.Errorf("error does not say what else to change (want %q): %s", want, msg)
				}
			}
			// And it really is gone from discovery, warned about rather than silent.
			if got := discoverDir(md, true); len(got) != 0 {
				t.Errorf("rejected manifest still discovered: %v", names(got))
			}
		})
	}
}

// TestHostDaemonPublishesAndRequestEndParsed: `publishes` and `request_end` are
// the host_daemon's half of the transport contract — publishes:"socket" means
// the daemon binds a plain AF_UNIX socket at {socket} and yolo runs the TLS
// front; request_end:"eof" means the front half-closes upstream when the
// client's request direction ends (loophole-packaging.md §2.1, §2.1b).
func TestHostDaemonPublishesAndRequestEndParsed(t *testing.T) {
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "fronted"))
	writeManifest(t, mod, map[string]any{
		"name": "fronted", "description": "x",
		"host_daemon": map[string]any{
			"cmd":         []any{"some-daemon", "--socket", "{socket}"},
			"publishes":   "socket",
			"request_end": "eof",
		},
	})
	lp, err := LoadLoophole(mod)
	if err != nil {
		t.Fatal(err)
	}
	if lp.HostDaemon == nil || lp.HostDaemon.Publishes != PublishesSocket {
		t.Errorf("Publishes = %+v, want %q", lp.HostDaemon, PublishesSocket)
	}
	if lp.HostDaemon.RequestEnd != RequestEndEOF {
		t.Errorf("RequestEnd = %q, want %q", lp.HostDaemon.RequestEnd, RequestEndEOF)
	}
}

// TestHostDaemonPublishesDefaults: saying nothing keeps today's behaviour
// exactly — the daemon publishes the endpoint file itself, and requests are
// framed (no upstream half-close). The defaults ARE the backward-compat story,
// so they are pinned.
func TestHostDaemonPublishesDefaults(t *testing.T) {
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "quiet-hd"))
	writeManifest(t, mod, map[string]any{
		"name": "quiet-hd", "description": "x",
		"host_daemon": map[string]any{"cmd": []any{"some-daemon", "{endpoint}"}},
	})
	lp, err := LoadLoophole(mod)
	if err != nil {
		t.Fatal(err)
	}
	if lp.HostDaemon.Publishes != PublishesEndpoint {
		t.Errorf("default Publishes = %q, want %q", lp.HostDaemon.Publishes, PublishesEndpoint)
	}
	if lp.HostDaemon.RequestEnd != RequestEndFramed {
		t.Errorf("default RequestEnd = %q, want %q", lp.HostDaemon.RequestEnd, RequestEndFramed)
	}
}

// TestManifestPreambleDefaultSurvivesLoad is the SILENT-DROP tripwire, and it is
// about resolve() rather than about the schema — loopholedecl already pins the
// decoder's default. resolve builds a NEW HostDaemon field by field, so a field
// nobody listed there arrives as its zero value with no error anywhere. For
// `Preamble` that zero value is FALSE, i.e. the opposite of what every manifest
// that says nothing asked for, and the symptom is not a crash: it is a daemon
// quietly served a preamble-free connection whose audit line goes back to
// carrying whatever the client claimed. This is the ca_cert drop (see
// subsetManifest in load.go) one type down, and it is why the field is spelled
// `Preamble` and not `NoPreamble` — with the inversion, the drop would land on
// the SAFE value and no test could see it.
func TestManifestPreambleDefaultSurvivesLoad(t *testing.T) {
	cases := []struct {
		name   string
		daemon map[string]any
		want   bool
	}{
		{"silent-default", map[string]any{"cmd": []any{"d", "{socket}"}, "publishes": "socket"}, true},
		{"declared-off", map[string]any{
			"cmd": []any{"d", "{socket}"}, "publishes": "socket", "preamble": false,
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			md := modsDir(t)
			mod := mkdir(t, filepath.Join(md, tc.name))
			writeManifest(t, mod, map[string]any{
				"name": tc.name, "description": "x", "host_daemon": tc.daemon,
			})
			lp, err := LoadLoophole(mod)
			if err != nil {
				t.Fatal(err)
			}
			if lp.HostDaemon == nil {
				t.Fatal("no host daemon on the record")
			}
			if lp.HostDaemon.Preamble != tc.want {
				t.Errorf("Preamble = %v after load, want %v — resolve() must carry the field",
					lp.HostDaemon.Preamble, tc.want)
			}
		})
	}
}

// TestHostDaemonFieldsSurviveLoad is the GENERAL form of the tripwire above, and
// it exists because the specific form was not enough: the prose in resolve() warns
// about exactly this drop, `Preamble` has a test of its own, and `Scope` was still
// added to the schema and left out of the literal — arriving at the run pipeline as
// "" so a `scope: "host"` manifest got a SECOND daemon spawned per jail, which for
// the broker is two processes racing to burn one single-use refresh token.
//
// So this walks HostDaemon with reflect instead of listing fields. Every exported
// field is declared in a manifest with a NON-ZERO value, and every one must come
// back non-zero after LoadLoophole. A new field added to the struct and forgotten in
// resolve() fails here by construction; a new field added and forgotten in the TABLE
// below fails too, because the table is checked for totality against the type.
//
// It deliberately asserts "not the zero value" rather than an exact value: exact
// values belong in the per-field tests (Cmd is token-substituted at load, so it is
// not equal to what was written), while the failure this catches is always the same
// one — a field that silently became its zero value.
func TestHostDaemonFieldsSurviveLoad(t *testing.T) {
	// One non-zero declaration per exported field of HostDaemon. `preamble` is the
	// odd one: its DEFAULT is true, so the only non-zero value is the default, and
	// declaring it changes nothing — which is fine, the assertion is that it
	// survives.
	declared := map[string]any{
		"Cmd":        []any{"d", "{socket}"},
		"Env":        map[string]any{"K": "V"},
		"Publishes":  "socket",
		"RequestEnd": "eof",
		"Preamble":   true,
		"Scope":      "host",
	}
	typ := reflect.TypeOf(HostDaemon{})
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if !f.IsExported() {
			continue
		}
		if _, ok := declared[f.Name]; !ok {
			t.Fatalf("HostDaemon gained the field %q and this table has no non-zero value "+
				"for it. Add one: resolve() in load.go rebuilds the struct field by field, so "+
				"an unlisted field arrives as its zero value with nothing reporting a problem.",
				f.Name)
		}
	}

	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "survives"))
	writeManifest(t, mod, map[string]any{
		"name": "survives", "description": "x",
		"host_daemon": map[string]any{
			"cmd":         declared["Cmd"],
			"env":         declared["Env"],
			"publishes":   declared["Publishes"],
			"request_end": declared["RequestEnd"],
			"preamble":    declared["Preamble"],
			"scope":       declared["Scope"],
		},
	})
	lp, err := LoadLoophole(mod)
	if err != nil {
		t.Fatal(err)
	}
	if lp.HostDaemon == nil {
		t.Fatal("no host daemon on the record")
	}
	got := reflect.ValueOf(*lp.HostDaemon)
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if !f.IsExported() {
			continue
		}
		v := got.Field(i)
		if v.IsZero() {
			t.Errorf("HostDaemon.%s is the zero value after load, though the manifest declared "+
				"a non-zero one — resolve() in load.go dropped it, and nothing else reports that",
				f.Name)
		}
	}
	// Env is a pointer, so IsZero only catches a nil. Check the CONTENT too, or a
	// resolve that handed back an empty map would pass the loop above.
	if lp.HostDaemon.Env == nil || lp.HostDaemon.Env.Len() == 0 {
		t.Error("HostDaemon.Env lost its entries at load")
	}
}

// TestHostDaemonInvalidPublishesAndRequestEndRejected: an invalid enum value is
// a LOAD error naming the valid set — never a silent fallback to a default,
// which would quietly select the other publication mechanism.
func TestHostDaemonInvalidPublishesAndRequestEndRejected(t *testing.T) {
	cases := []struct {
		name    string
		daemon  map[string]any
		wantSub string
	}{
		{"bad-publishes",
			map[string]any{"cmd": []any{"d", "{socket}"}, "publishes": "carrier-pigeon"},
			"publishes"},
		{"bad-request-end",
			map[string]any{"cmd": []any{"d", "{socket}"}, "request_end": "telepathy"},
			"request_end"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			md := modsDir(t)
			mod := mkdir(t, filepath.Join(md, tc.name))
			writeManifest(t, mod, map[string]any{
				"name": tc.name, "description": "x", "host_daemon": tc.daemon,
			})
			_, err := LoadLoophole(mod)
			if err == nil {
				t.Fatalf("invalid %s loaded", tc.name)
			}
			if !contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q does not name %q", err.Error(), tc.wantSub)
			}
		})
	}
}

// TestPublishesSocketRefusesEndpointToken: under publishes:"socket" the two
// tokens DIVERGE — {socket} is the upstream path the daemon binds, {endpoint}
// is the file yolo publishes in front of it. A manifest naming {endpoint} in
// its argv under that mode would silently publish nothing, so it is refused at
// load with the fix (loophole-packaging.md §2.1).
func TestPublishesSocketRefusesEndpointToken(t *testing.T) {
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "confused"))
	writeManifest(t, mod, map[string]any{
		"name": "confused", "description": "x",
		"host_daemon": map[string]any{
			"cmd":       []any{"some-daemon", "--socket", "{endpoint}"},
			"publishes": "socket",
		},
	})
	_, err := LoadLoophole(mod)
	if err == nil {
		t.Fatal("a publishes:\"socket\" manifest naming {endpoint} in its argv loaded")
	}
	if !contains(err.Error(), "{socket}") {
		t.Errorf("refusal does not name the fix ({socket}): %s", err.Error())
	}
}

// TestLoopholeDirTokenSubstitutedInHostCmds: {loophole_dir} resolves to the
// HOST-side absolute module dir in host_daemon.cmd and doctor_cmd. Before this,
// the token was substituted in exactly one field (host_bind_mounts[].host) and
// a daemon spawn would exec a literal "{loophole_dir}/srv.py"
// (loophole-packaging.md §2.1a).
func TestLoopholeDirTokenSubstitutedInHostCmds(t *testing.T) {
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "toked"))
	writeManifest(t, mod, map[string]any{
		"name": "toked", "description": "x",
		"host_daemon": map[string]any{
			"cmd": []any{"python3", "{loophole_dir}/srv.py", "--socket", "{socket}"},
		},
		"doctor_cmd": []any{"{loophole_dir}/doctor.sh", "--quick"},
	})
	lp, err := LoadLoophole(mod)
	if err != nil {
		t.Fatal(err)
	}
	wantDir := resolvePath(mod)
	if got := lp.HostDaemon.Cmd[1]; got != wantDir+"/srv.py" {
		t.Errorf("host_daemon.cmd[1] = %q, want %q", got, wantDir+"/srv.py")
	}
	if !filepath.IsAbs(lp.HostDaemon.Cmd[1]) {
		t.Errorf("host_daemon.cmd[1] = %q is not absolute", lp.HostDaemon.Cmd[1])
	}
	if got := lp.DoctorCmd[0]; got != wantDir+"/doctor.sh" {
		t.Errorf("doctor_cmd[0] = %q, want %q", got, wantDir+"/doctor.sh")
	}
	// {socket} is NOT load-time vocabulary — the run pipeline owns it.
	if got := lp.HostDaemon.Cmd[3]; got != "{socket}" {
		t.Errorf("host_daemon.cmd[3] = %q; the {socket} token must survive load", got)
	}
}

// TestJailLoopholeDirTokenSubstitutedInJailDaemon: a jail_daemon runs in the
// CONTAINER, where the module dir is bind-mounted at
// /etc/yolo-jail/loopholes/<name> — so its token is {jail_loophole_dir}, a
// different spelling on purpose: one token with two resolutions is the kind of
// asymmetry an author discovers by debugging (loophole-packaging.md §2.1a).
func TestJailLoopholeDirTokenSubstitutedInJailDaemon(t *testing.T) {
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "jailed"))
	writeManifest(t, mod, map[string]any{
		"name": "jailed", "description": "x",
		"jail_daemon": map[string]any{
			"cmd": []any{"{jail_loophole_dir}/agentd", "--config", "{jail_loophole_dir}/cfg.json"},
		},
	})
	lp, err := LoadLoophole(mod)
	if err != nil {
		t.Fatal(err)
	}
	want := JailLoopholeDir("jailed") + "/agentd"
	if got := lp.JailDaemon.Cmd[0]; got != want {
		t.Errorf("jail_daemon.cmd[0] = %q, want %q", got, want)
	}
	if !strings.HasPrefix(want, "/etc/yolo-jail/loopholes/") {
		t.Errorf("JailLoopholeDir = %q; the container mount point moved", want)
	}
}

// TestWrongHalfTokensRefusedAtLoad: using the host token in the jail half (or
// vice versa) is refused at load with a message naming the right one — never
// substituted to a path that is wrong on the side where it runs, and never
// passed through as a literal brace token.
func TestWrongHalfTokensRefusedAtLoad(t *testing.T) {
	cases := []struct {
		name     string
		manifest map[string]any
		wantHint string
	}{
		{"host-daemon-jail-token", map[string]any{
			"host_daemon": map[string]any{"cmd": []any{"{jail_loophole_dir}/srv", "{socket}"}},
		}, "{loophole_dir}"},
		{"doctor-jail-token", map[string]any{
			"doctor_cmd": []any{"{jail_loophole_dir}/doctor.sh"},
		}, "{loophole_dir}"},
		{"jail-daemon-host-token", map[string]any{
			"jail_daemon": map[string]any{"cmd": []any{"{loophole_dir}/agentd"}},
		}, "{jail_loophole_dir}"},
		{"bind-mount-jail-token", map[string]any{
			"host_bind_mounts": []any{
				map[string]any{"host": "{jail_loophole_dir}/data", "container": "/ctx/d"},
			},
		}, "{loophole_dir}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			md := modsDir(t)
			mod := mkdir(t, filepath.Join(md, tc.name))
			manifest := map[string]any{"name": tc.name, "description": "x"}
			for k, v := range tc.manifest {
				manifest[k] = v
			}
			writeManifest(t, mod, manifest)
			_, err := LoadLoophole(mod)
			if err == nil {
				t.Fatal("a wrong-half token loaded")
			}
			if !contains(err.Error(), tc.wantHint) {
				t.Errorf("refusal does not name the right token %q: %s", tc.wantHint, err.Error())
			}
		})
	}
}

// TestListKeysInterceptsOnTheInterceptList: `yolo loopholes list` is the whole
// answer to "is the active transport visible without asking" (loophole-transport
// OQ-T2), so the `transport=` column has to print for every loophole that is not
// intercepting — and the intercept column has to key on `intercepts`, not on a
// transport string, now that no transport implies interception.
func TestListKeysInterceptsOnTheInterceptList(t *testing.T) {
	unsetJail(t)
	md := modsDir(t)
	plain := mkdir(t, filepath.Join(md, "plain"))
	writeManifest(t, plain, map[string]any{
		"name": "plain", "description": "x", "transport": TransportLoopbackTLS,
	})
	icept := mkdir(t, filepath.Join(md, "icept"))
	writeManifest(t, icept, map[string]any{
		"name": "icept", "description": "x", "transport": TransportLoopbackTLS,
		"intercepts": []any{map[string]any{"host": "example.test"}},
	})

	t.Cleanup(withModuleDir(md))

	var out strings.Builder
	nilCfg := func() *jsonx.OrderedMap { return nil }
	rc := List(Deps{
		Out: &out, Err: &out,
		LoadUserConfig:      nilCfg,
		LoadWorkspaceConfig: func(string) *jsonx.OrderedMap { return nil },
	})
	if rc != 0 {
		t.Fatalf("List rc = %d", rc)
	}
	got := out.String()
	if !contains(got, "transport="+TransportLoopbackTLS) {
		t.Errorf("no transport= column for the non-intercepting loophole:\n%s", got)
	}
	if !contains(got, "intercepts=[example.test]") {
		t.Errorf("intercepting loophole did not print its hosts:\n%s", got)
	}
}
