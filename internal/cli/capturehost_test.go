package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// capturehost_test.go drives the WHOLE `yolo capture` host act with the run pipeline
// substituted — resolve the declaration, stage inside the store, emit the jail argv, admit
// the proto-entry, write the receipt, sweep the workspace.
//
// Substituting the pipeline rather than calling the pieces is the point. install-capture.md
// and AGENTS.md both name the same failure shape — a test that pins the callee while the
// call site is unpinned — and every assertion below is downstream of
// `captureRunPipeline(opts)` actually being called with options this act composed: delete
// that line and the fake never runs, no proto-entry exists, and the manifest read fails.

// captureFixtureHome sets HOME to a temp tree holding a user config that selects one LOCAL
// pack declaring `probetool` via an installer URL, and returns the home.
//
// A local (file://) pack because that is the origin whose installerUrl HonoredInstalls
// grants — the same reason integration/installmechanism_test.go uses one.
func captureFixtureHome(t *testing.T, contributes string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	pack := filepath.Join(home, "packs", "fixture")
	if err := os.MkdirAll(pack, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"fixture","description":"capture fixture","contributes":[` + contributes + `]}`
	if err := os.WriteFile(filepath.Join(pack, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"packs": [{"source": "file://` + pack + `", "name": "fixture"}]}`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.jsonc"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

const captureFixtureInstaller = `{"kind":"program","bin":"probetool","via":"installer",` +
	`"url":"file:///ctx/packs/fixture/install.sh"}`

// fakeCaptureJail replaces the run pipeline with a driver simulation: it records the options
// it was handed and fills the out dir the way capture.Run would.
//
// It writes the proto-entry SHAPE (tree/ plus a manifest beside it) rather than calling
// capture.Run, because what this test is about is the host half — the driver's own behaviour
// is measured in internal/capture against real installers.
func fakeCaptureJail(t *testing.T, seen *run.Options, entries []capture.ManifestEntry) func(run.Options) int {
	t.Helper()
	return func(o run.Options) int {
		*seen = o
		out := filepath.Join(o.Workspace, captureOutLeaf)
		tree := capture.TreeDir(out)
		if err := os.MkdirAll(tree, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			p := filepath.Join(tree, filepath.FromSlash(e.Path))
			if e.Kind == capture.KindDir {
				if err := os.MkdirAll(p, 0o755); err != nil {
					t.Fatal(err)
				}
				continue
			}
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, bytes.Repeat([]byte("x"), int(e.Size)), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		m := &capture.Manifest{
			Schema: capture.ManifestSchema, Home: "/home/agent", Platform: "linux/arm64",
			Surfaces: []string{".npm-global", ".local", "go"},
			Excluded: capture.DefaultExcludes(), Entries: entries,
		}
		if err := capture.WriteManifest(out, m); err != nil {
			t.Fatal(err)
		}
		return 0
	}
}

func withFakeCaptureJail(t *testing.T, fn func(run.Options) int) {
	t.Helper()
	prev := captureRunPipeline
	captureRunPipeline = fn
	t.Cleanup(func() { captureRunPipeline = prev })
}

// One capture, end to end on the host side.
func TestCaptureAdmitsTheEntryAndWritesTheReceipt(t *testing.T) {
	home := captureFixtureHome(t, captureFixtureInstaller)
	var seen run.Options
	entries := []capture.ManifestEntry{
		{Path: ".local", Kind: capture.KindDir, Mode: "0755"},
		{Path: ".local/bin", Kind: capture.KindDir, Mode: "0755"},
		{Path: ".local/bin/probetool", Kind: capture.KindFile, Mode: "0755", Size: 64},
	}
	withFakeCaptureJail(t, fakeCaptureJail(t, &seen, entries))

	var out, errw bytes.Buffer
	if rc := captureHost([]string{"probetool"}, &out, &errw, false); rc != 0 {
		t.Fatalf("rc = %d\nstdout: %s\nstderr: %s", rc, out.String(), errw.String())
	}

	// 1. THE JAIL RAN AGAINST A SCRATCH WORKSPACE INSIDE THE STORE. That siting is not
	//    cosmetic: Store.Admit refuses a staged tree from anywhere else, because admission
	//    is an os.Rename and a scratch dir on another mount would silently copy the
	//    gigabytes the store exists to stop copying.
	store := &capture.Store{Dir: paths.CapturesDirUnder(home)}
	if want := store.StagingDir("probetool"); seen.Workspace != want {
		t.Errorf("capture workspace = %q, want %q", seen.Workspace, want)
	}
	if seen.Args == nil || !equalArgs(seen.Args, captureJailArgv("probetool")) {
		t.Errorf("jail argv = %v, want %v", seen.Args, captureJailArgv("probetool"))
	}
	if !seen.New {
		t.Error("a capture must not attach to an existing container for its workspace")
	}
	if !seen.AcceptConfigChanges {
		t.Error("the scratch workspace has no config a human wrote; a capture must not " +
			"turn a user-config edit into a prompt about a directory nobody has seen")
	}

	// 2. The entry is admitted, complete, and content-addressed.
	key := captureKeyOf(t, store)
	entry, err := store.Resolve(key)
	if err != nil {
		t.Fatalf("the admitted entry does not resolve: %v", err)
	}
	if _, err := os.Stat(filepath.Join(entry.Tree, ".local", "bin", "probetool")); err != nil {
		t.Errorf("the captured binary is missing from the entry: %v", err)
	}
	// The manifest came WITH the tree — AdmitEntry moves the whole proto-entry, so there
	// is no window in which the marker says complete and the manifest is not there.
	m, err := capture.ReadManifest(entry.Root)
	if err != nil {
		t.Fatalf("the entry has no manifest beside its tree: %v", err)
	}
	if len(m.Entries) != len(entries) {
		t.Errorf("manifest has %d entries, want %d", len(m.Entries), len(entries))
	}

	// 3. The receipt is beside the entry, in the schema the boot's reader parses.
	rec := readOneCaptureReceipt(t, capture.ReceiptsPath(entry.Root))
	for _, c := range []struct{ name, got, want string }{
		{"kind", str(rec["kind"]), entrypoint.ReceiptKindCapture},
		{"act", str(rec["act"]), entrypoint.ReceiptActRecord},
		{"bin", str(rec["bin"]), "probetool"},
		{"declared", str(rec["declared"]), "file:///ctx/packs/fixture/install.sh"},
		{"resolved", str(rec["resolved"]), key},
		{"path", str(rec["path"]), entry.Root},
		{"platform", str(rec["platform"]), "linux/arm64"},
	} {
		if c.got != c.want {
			t.Errorf("receipt %s = %q, want %q", c.name, c.got, c.want)
		}
	}
	if got, want := rec["bytes"], float64(64); got != want {
		t.Errorf("receipt bytes = %v, want %v (the manifest's file sizes)", got, want)
	}
	if d := str(rec["sha256"]); !strings.HasPrefix(d, key) || len(d) != 64 {
		t.Errorf("receipt sha256 = %q, want a 64-char digest starting with the key %q", d, key)
	}

	// 4. The scratch workspace is swept. A capture boots a whole jail, so leaving it would
	//    keep one provisioned home per captured program forever.
	if _, err := os.Stat(seen.Workspace); !os.IsNotExist(err) {
		t.Errorf("the capture workspace survived: %v", err)
	}
}

// A second capture of the SAME bytes is idempotent: one entry, and the receipt log beside it
// grows by a line rather than being replaced.
func TestCaptureIsIdempotentAndAppendsASecondReceipt(t *testing.T) {
	home := captureFixtureHome(t, captureFixtureInstaller)
	entries := []capture.ManifestEntry{{Path: ".local/bin/probetool", Kind: capture.KindFile, Mode: "0755", Size: 8}}
	var seen run.Options
	withFakeCaptureJail(t, fakeCaptureJail(t, &seen, entries))

	var out, errw bytes.Buffer
	for i := 0; i < 2; i++ {
		if rc := captureHost([]string{"probetool"}, &out, &errw, false); rc != 0 {
			t.Fatalf("capture %d: rc = %d\n%s", i+1, rc, errw.String())
		}
	}

	store := &capture.Store{Dir: paths.CapturesDirUnder(home)}
	keys, err := os.ReadDir(filepath.Join(store.Dir, "entries"))
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 {
		t.Errorf("got %d entries, want 1 — identical bytes are one capture", len(keys))
	}
	entry, err := store.Resolve(keys[0].Name())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(capture.ReceiptsPath(entry.Root))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(strings.TrimRight(string(data), "\n"), "\n") + 1; n != 2 {
		t.Errorf("got %d receipt lines, want 2 — the log is append-only:\n%s", n, data)
	}
}

// An installer that left nothing in the capture surfaces is a FAILURE, not an empty package.
// An empty entry would resolve for every later materialize and deliver nothing.
func TestCaptureRefusesAnEmptyDelta(t *testing.T) {
	home := captureFixtureHome(t, captureFixtureInstaller)
	var seen run.Options
	withFakeCaptureJail(t, fakeCaptureJail(t, &seen, nil))

	var out, errw bytes.Buffer
	if rc := captureHost([]string{"probetool"}, &out, &errw, false); rc == 0 {
		t.Fatal("an empty delta must fail the capture")
	}
	if !strings.Contains(errw.String(), "left nothing") {
		t.Errorf("the refusal should say what was missing:\n%s", errw.String())
	}
	if _, err := os.Stat(filepath.Join(paths.CapturesDirUnder(home), "entries")); !os.IsNotExist(err) {
		t.Error("nothing may be admitted for an empty delta")
	}
}

// A jail that exited non-zero stores nothing. Half an install filed as a package is worse
// than no package — the store's whole torn-write discipline exists to keep one off disk and
// cannot help if the act admits a tree from a run that died.
func TestCaptureStoresNothingWhenTheJailFails(t *testing.T) {
	home := captureFixtureHome(t, captureFixtureInstaller)
	withFakeCaptureJail(t, func(o run.Options) int { return 7 })

	var out, errw bytes.Buffer
	if rc := captureHost([]string{"probetool"}, &out, &errw, false); rc != 7 {
		t.Errorf("rc = %d, want the jail's own 7", rc)
	}
	if _, err := os.Stat(filepath.Join(paths.CapturesDirUnder(home), "entries")); !os.IsNotExist(err) {
		t.Error("a failed capture must admit nothing")
	}
}

// An npm-declared program has a registry version to name, so it needs no capture — and
// saying so beats launching a jail to discover it.
func TestCaptureRefusesAnNpmProgram(t *testing.T) {
	captureFixtureHome(t, `{"kind":"program","bin":"probetool","via":"npm","package":"probetool@1.0.0"}`)
	ran := false
	withFakeCaptureJail(t, func(o run.Options) int { ran = true; return 0 })

	var out, errw bytes.Buffer
	if rc := captureHost([]string{"probetool"}, &out, &errw, false); rc == 0 {
		t.Fatal("an npm program must not be captured")
	}
	if ran {
		t.Error("the refusal must land before a jail is launched")
	}
	if !strings.Contains(errw.String(), "npm") {
		t.Errorf("the refusal should name the resolver:\n%s", errw.String())
	}
}

// A bin no selected pack declares is refused before anything is staged.
func TestCaptureRefusesAnUndeclaredProgram(t *testing.T) {
	home := captureFixtureHome(t, captureFixtureInstaller)
	withFakeCaptureJail(t, func(o run.Options) int { t.Error("no jail may launch"); return 0 })

	var out, errw bytes.Buffer
	if rc := captureHost([]string{"nosuchtool"}, &out, &errw, false); rc == 0 {
		t.Fatal("an undeclared program must be refused")
	}
	if _, err := os.Stat(filepath.Join(paths.CapturesDirUnder(home), "staging")); !os.IsNotExist(err) {
		t.Error("nothing may be staged for a program that is not declared")
	}
}

// A SECOND capture of the same program, while the first holds the lock, refuses instead of
// racing it into the store or parking behind its download.
func TestCaptureRefusesWhileAnotherCaptureHoldsTheLock(t *testing.T) {
	captureFixtureHome(t, captureFixtureInstaller)
	held := tryFlockAt(captureLockPath("probetool"))
	if held == nil {
		t.Skip("flock is a no-op on this filesystem")
	}
	defer held.Close()
	// A second flock from the SAME process succeeds (flock is per open file description,
	// and this one is a different fd only if the OS says so), so hold the lock the way a
	// second process would see it: the production path opens its own fd, which on Linux
	// conflicts with ours. Guard the test on that actually being true here.
	if probe := tryFlockAt(captureLockPath("probetool")); probe != nil {
		probe.Close()
		t.Skip("this filesystem does not make a second flock on the same file conflict")
	}
	withFakeCaptureJail(t, func(o run.Options) int { t.Error("no jail may launch"); return 0 })

	var out, errw bytes.Buffer
	if rc := captureHost([]string{"probetool"}, &out, &errw, false); rc == 0 {
		t.Fatal("a concurrent capture of one program must be refused")
	}
	if !strings.Contains(errw.String(), "already running") {
		t.Errorf("the refusal should name the other capture:\n%s", errw.String())
	}
}

// The DISPATCH entry drops the subcommand token, which dispatchNative hands every handler
// as args[0].
//
// It has its own test because the tests above call captureHost directly and are all green
// with the token left in: the first real `yolo capture <bin>` then dies with "one program
// at a time (got \"capture\" and …)", which is the pinned-callee/unpinned-call-site shape
// AGENTS.md names — and it shipped once here before this test existed. rc 1 (the program is
// not declared) rather than rc 2 (two programs) is the whole discriminator.
func TestRunCaptureDropsTheSubcommandToken(t *testing.T) {
	captureFixtureHome(t, captureFixtureInstaller)
	withFakeCaptureJail(t, func(o run.Options) int { t.Error("no jail may launch"); return 0 })

	if rc := runCapture([]string{"capture", "nosuchtool"}); rc != 1 {
		t.Errorf("rc = %d, want 1 (an undeclared program); rc 2 means the subcommand token "+
			"was read as a second program name", rc)
	}
}

// The jail argv is where three facts have to agree, and none of them is checkable at run
// time: the workspace bind's container path, the surface root's position UNDER it, and the
// env var that stops the launcher exec'ing the tool.
//
// Derived here rather than re-typed, so the assertion fails when the SOURCE of a fact moves
// — paths.WorkspaceHomeState is the same function the run pipeline's prepareWsState uses to
// decide where those directories actually are.
func TestCaptureJailArgvReachesTheSurfacesThroughTheWorkspaceBind(t *testing.T) {
	argv := captureJailArgv("probetool")
	joined := strings.Join(argv, " ")

	wantOut := "--out=" + path.Join(containerWorkspace, captureOutLeaf)
	wantRoot := "--surface-root=" + paths.WorkspaceHomeState(containerWorkspace)
	for _, want := range []string{
		"yolo", "internal", "capture-run", wantOut, wantRoot,
		"--", "env", entrypoint.InstallOnlyEnv + "=1", "probetool",
	} {
		if !containsArg(argv, want) {
			t.Errorf("argv is missing %q:\n%s", want, joined)
		}
	}
	// THE MOUNT IS THE PREDICATE. rename(2) compares the mount, not the device, so the
	// scratch dir and the surfaces must be reachable under ONE bind — the workspace's —
	// or the driver copies the whole delta instead of moving it.
	if !strings.HasPrefix(paths.WorkspaceHomeState(containerWorkspace), containerWorkspace+"/") {
		t.Errorf("the surface root %q is not under the workspace bind %q; the delta would "+
			"take the copy path", paths.WorkspaceHomeState(containerWorkspace), containerWorkspace)
	}
	if !strings.HasPrefix(path.Join(containerWorkspace, captureOutLeaf), containerWorkspace+"/") {
		t.Error("the out dir must be inside the workspace bind for the same reason")
	}
	// And it must not be inside a surface, or the capture would capture itself.
	for _, s := range paths.HomeSurfaces() {
		surface := path.Join(paths.WorkspaceHomeState(containerWorkspace), s.Subtree)
		if strings.HasPrefix(path.Join(containerWorkspace, captureOutLeaf)+"/", surface+"/") {
			t.Errorf("the out dir is inside the capture surface %s", surface)
		}
	}
}

// captureKeyOf returns the single admitted entry's key.
func captureKeyOf(t *testing.T, s *capture.Store) string {
	t.Helper()
	ents, err := os.ReadDir(filepath.Join(s.Dir, "entries"))
	if err != nil {
		t.Fatalf("no entries dir: %v", err)
	}
	if len(ents) != 1 {
		t.Fatalf("got %d entries, want 1", len(ents))
	}
	return ents[0].Name()
}

// readOneCaptureReceipt reads the single receipt line as a generic JSON object, so the
// assertions are about the WIRE FORMAT a later yolo will parse rather than about the struct
// this yolo happens to marshal.
func readOneCaptureReceipt(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no receipt beside the entry: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("got %d receipt lines, want 1:\n%s", len(lines), data)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &m); err != nil {
		t.Fatalf("the receipt is not JSON: %v\n%s", err, lines[0])
	}
	return m
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func containsArg(argv []string, want string) bool {
	for _, a := range argv {
		if a == want {
			return true
		}
	}
	return false
}

func equalArgs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
