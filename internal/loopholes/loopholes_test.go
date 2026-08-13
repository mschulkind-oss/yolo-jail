package loopholes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
)

// writeManifest writes a manifest.jsonc built from a Go map via
// json.MarshalIndent.
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
	// A config entry keeps the RETIRED transport on purpose: its daemon is a
	// third-party program binding an AF_UNIX socket, and yolo ships nothing that
	// would let such a program publish an endpoint file instead. Retirement here
	// means "no manifest can select it" — pinned by
	// TestValidTransportsIsLoopbackTLSAndNone — not "the socket path is gone".
	for _, m := range loaded {
		if m.Transport != retiredTransportUnixSocket || m.Lifecycle != "spawned" || !m.FromConfig() {
			t.Errorf("synthesized loophole shape wrong: %+v", m)
		}
	}
	if containsStr(validTransports, retiredTransportUnixSocket) {
		t.Error("a MANIFEST can still declare the value a config entry gets internally")
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
// YOLO_REPO_ROOT unset (host mode, no shim), the shared resolver
// (reporoot.Resolve, now the single method BundledLoopholesDir uses) must walk
// up to the real yolo-jail checkout and resolve bundled_loopholes there — NOT
// fall back to an empty in-jail path and drop every bundled loophole. Does NOT
// monkeypatch BundledLoopholesDir (per the audit).
func TestRepoRootHostModeFindsBundled(t *testing.T) {
	t.Setenv("YOLO_REPO_ROOT", "")
	os.Unsetenv("YOLO_REPO_ROOT")
	// cwd during `go test` is the package dir (a descendant of the repo), so the
	// walk should reach the real checkout.
	rr, ok := reporoot.Resolve(os.Getenv)
	if !ok || !fileExists(filepath.Join(rr, "go.mod")) {
		t.Fatalf("reporoot.Resolve=%q,%v is not a yolo-jail checkout (host-mode B3 regression)", rr, ok)
	}
	got := Discover(DiscoverOptions{IncludeDisabled: true, IncludeBundled: true})
	if len(got) == 0 {
		t.Fatal("host-mode discovery found ZERO loopholes — audit §B3 regression")
	}
}

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
// loadFromDir used to `continue` on any load error, so ONE rejected field made the
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
	if len(validTransports) != len(want) {
		t.Fatalf("validTransports = %v, want exactly %v", validTransports, want)
	}
	for _, w := range want {
		if !containsStr(validTransports, w) {
			t.Errorf("validTransports = %v, missing %q", validTransports, w)
		}
	}
	for _, retired := range []string{retiredTransportUnixSocket, retiredTransportTLSIntercept} {
		if containsStr(validTransports, retired) {
			t.Errorf("validTransports still accepts the retired %q", retired)
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
	for _, tc := range []struct{ transport, wantSubstr string }{
		{retiredTransportUnixSocket, "{endpoint}"},
		{retiredTransportTLSIntercept, "intercepts"},
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
			if !contains(msg, tc.wantSubstr) {
				t.Errorf("error does not say what else to change (want %q): %s", tc.wantSubstr, msg)
			}
			// And it really is gone from discovery, warned about rather than silent.
			if got := discoverDir(md, true); len(got) != 0 {
				t.Errorf("rejected manifest still discovered: %v", names(got))
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

	empty := mkdir(t, filepath.Join(t.TempDir(), "none"))
	origB, origU := BundledLoopholesDir, UserLoopholesDir
	BundledLoopholesDir = func() string { return empty }
	UserLoopholesDir = func() string { return md }
	t.Cleanup(func() { BundledLoopholesDir, UserLoopholesDir = origB, origU })

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
