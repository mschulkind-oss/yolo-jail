package run

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// unifyFixture is a workspace with the launching container setup's options bound to its own slice.
type unifyFixture struct {
	ws     string
	o      *Options
	stderr *strings.Builder
}

func newUnifyFixture(t *testing.T, setupName string) *unifyFixture {
	t.Helper()
	packHome(t)
	ws := floortest.ResolvedTemp(t)
	var stderr strings.Builder
	o := peerOptions(ws, setupName, &stderr)
	o.homeState = paths.SetupHomeState(ws, setupName)
	// prepareWsState has made the slice by the time unifyHome runs.
	if err := os.MkdirAll(o.homeState, 0o755); err != nil {
		t.Fatal(err)
	}
	return &unifyFixture{ws: ws, o: o, stderr: &stderr}
}

// put writes content at rel beneath dir, making the parents.
func putUnify(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *unifyFixture) home(name string) string { return paths.SetupHomeState(f.ws, name) }

// unify runs the launch's merge-and-compose for rt over the claude pack, as runContainer does.
func (f *unifyFixture) unify(t *testing.T, rt string, packs ...string) {
	t.Helper()
	if len(packs) == 0 {
		packs = []string{"claude"}
	}
	f.o.unifyHome(jsonx.NewOrderedMap(), f.o.homeDir(), packsFixture(t, packs...), rt)
}

func (f *unifyFixture) unified(rel string) string {
	return filepath.Join(paths.UnifiedHome(f.ws), filepath.FromSlash(rel))
}

// resolveInJail resolves link, an absolute in-jail path, the way the jail does: /workspace is the
// workspace.
func resolveInJail(t *testing.T, ws, link string) string {
	t.Helper()
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("%s is not a link: %v", link, err)
	}
	if !strings.HasPrefix(target, "/workspace/") {
		t.Fatalf("%s -> %s: a link must name the absolute in-jail path", link, target)
	}
	return filepath.Join(ws, strings.TrimPrefix(target, "/workspace/"))
}

// TestALaunchComposesItsSliceAndASwitchKeepsTheSessions (W-UH1's Linux shadow): a Podman launch moves
// a session into the unified home once and leaves a link; an Apple Container launch of the same
// workspace then sees the same file through its own, dotted, slice.
func TestALaunchComposesItsSliceAndASwitchKeepsTheSessions(t *testing.T) {
	f := newUnifyFixture(t, "podman-self")
	podman := f.home("podman-self")
	putUnify(t, podman, "claude/projects/-workspace/w.jsonl", "session")
	putUnify(t, podman, "claude/settings.json", "generated")
	f.unify(t, "podman")

	if got := readFileT(t, f.unified(".claude/projects/-workspace/w.jsonl")); got != "session" {
		t.Fatalf("the session is not in the unified home: %q", got)
	}
	link := filepath.Join(podman, "claude", "projects")
	if want := "/workspace/.yolo/unified-home/.claude/projects"; mustReadlink(t, link) != want {
		t.Errorf("claude/projects -> %s, want %s", mustReadlink(t, link), want)
	}
	if fi, err := os.Lstat(filepath.Join(podman, "claude", "settings.json")); err != nil || !fi.Mode().IsRegular() {
		t.Errorf("a generated file must stay a real file in the slice: %v", err)
	}
	if _, err := os.Lstat(f.unified(".claude/settings.json")); err == nil {
		t.Errorf("a generated file was adopted into the unified home")
	}

	// A switch: the Apple Container setup of the same workspace.
	apple := newUnifyFixtureIn(t, f.ws, "apple-container-self")
	if err := os.MkdirAll(filepath.Join(apple.home("apple-container-self"), ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	apple.unify(t, "container")
	alink := filepath.Join(apple.home("apple-container-self"), ".claude", "projects")
	if got := readFileT(t, filepath.Join(resolveInJail(t, f.ws, alink), "-workspace", "w.jsonl")); got != "session" {
		t.Errorf("the Apple Container setup does not see the Podman setup's session: %q", got)
	}
}

func mustReadlink(t *testing.T, p string) string {
	t.Helper()
	s, err := os.Readlink(p)
	if err != nil {
		t.Fatalf("%s: %v", p, err)
	}
	return s
}

// newUnifyFixtureIn is a fixture for another setup of the same workspace.
func newUnifyFixtureIn(t *testing.T, ws, setupName string) *unifyFixture {
	t.Helper()
	var stderr strings.Builder
	o := peerOptions(ws, setupName, &stderr)
	o.homeState = paths.SetupHomeState(ws, setupName)
	if err := os.MkdirAll(o.homeState, 0o755); err != nil {
		t.Fatal(err)
	}
	return &unifyFixture{ws: ws, o: o, stderr: &stderr}
}

// TestStrandedHomesMergeOnTheFirstQuiescedLaunch (W-UH6's shadow): three sessions stranded in two
// other layouts arrive on the first launch with no jail running, none stays behind, and the launch
// says so in exactly one line. A second launch merges nothing and says nothing.
func TestStrandedHomesMergeOnTheFirstQuiescedLaunch(t *testing.T) {
	f := newUnifyFixture(t, "podman-self")
	podman, apple := f.home("podman-self"), f.home("apple-container-self")
	putUnify(t, podman, "claude/projects/b.jsonl", "b")
	putUnify(t, apple, ".claude/projects/c.jsonl", "c")
	putUnify(t, apple, "claude/projects/a.jsonl", "a")
	putUnify(t, apple, ".claude/settings.json", "generated by apple")
	f.unify(t, "podman")

	for name, want := range map[string]string{"a.jsonl": "a", "b.jsonl": "b", "c.jsonl": "c"} {
		if got := readFileT(t, f.unified(".claude/projects/"+name)); got != want {
			t.Errorf("unified-home projects/%s = %q, want %q", name, got, want)
		}
	}
	for _, gone := range []string{
		filepath.Join(apple, ".claude", "projects", "c.jsonl"), filepath.Join(apple, "claude", "projects", "a.jsonl"),
	} {
		if _, err := os.Lstat(gone); err == nil {
			t.Errorf("%s was left behind in the old home", gone)
		}
	}
	if got := readFileT(t, filepath.Join(apple, ".claude", "settings.json")); got != "generated by apple" {
		t.Errorf("another setup's generated file left its slice: %q", got)
	}
	line := regexp.MustCompile(`(?m)^Merged 3 files from 2 homes into this workspace's shared home\.$`)
	if n := len(line.FindAllString(f.stderr.String(), -1)); n != 1 {
		t.Errorf("the merge's line was printed %d times, want once:\n%s", n, f.stderr.String())
	}
	if n := strings.Count(f.stderr.String(), "Merged "); n != 1 {
		t.Errorf("the launch printed %d merge lines, want exactly one:\n%s", n, f.stderr.String())
	}

	f.stderr.Reset()
	f.unify(t, "podman")
	if strings.Contains(f.stderr.String(), "Merged") {
		t.Errorf("a launch with nothing left to merge said so:\n%s", f.stderr.String())
	}
}

// TestAJailRunningDefersTheMergeOfOtherHomesButNotTheOwnSlice: with a jail of the workspace running
// (here a guest's, which may run beside a container), another home waits, with the line naming
// `yolo stop`; the launching setup's own slice still composes.
func TestAJailRunningDefersTheMergeOfOtherHomesButNotTheOwnSlice(t *testing.T) {
	f := newUnifyFixture(t, "podman-self")
	putUnify(t, f.home("podman-self"), "claude/projects/b.jsonl", "b")
	putUnify(t, f.home("apple-container-self"), ".claude/projects/c.jsonl", "c")
	makePeerSetup(t, f.ws, "seatbelt-self")
	holdKeeperKey(t, keeperKey(yoloruntime.JailName(f.ws, "seatbelt-self"), keeperNotchGuest))
	f.unify(t, "podman")

	if got := readFileT(t, f.unified(".claude/projects/b.jsonl")); got != "b" {
		t.Errorf("the launching slice did not compose while a jail ran: %q", got)
	}
	if _, err := os.Lstat(filepath.Join(f.home("apple-container-self"), ".claude", "projects", "c.jsonl")); err != nil {
		t.Errorf("another setup's home was merged while a jail ran: %v", err)
	}
	if _, err := os.Lstat(f.unified(".claude/projects/c.jsonl")); err == nil {
		t.Errorf("another setup's session arrived while a jail ran")
	}
	for _, want := range []string{"apple-container-self", "`yolo stop`"} {
		if !strings.Contains(f.stderr.String(), want) {
			t.Errorf("the deferral does not say %q:\n%s", want, f.stderr.String())
		}
	}
}

// TestAGuestSourceIsLeftAndNamed (UHP-D3): a guest's slice is HOME and is not composed yet, so
// merging its sessions out would strand them; it stays, and the line says the guest joins later.
func TestAGuestSourceIsLeftAndNamed(t *testing.T) {
	f := newUnifyFixture(t, "podman-self")
	putUnify(t, f.home("seatbelt-self"), ".claude/projects/g.jsonl", "g")
	f.unify(t, "podman")

	if _, err := os.Lstat(filepath.Join(f.home("seatbelt-self"), ".claude", "projects", "g.jsonl")); err != nil {
		t.Errorf("a guest's session left its home: %v", err)
	}
	for _, want := range []string{"seatbelt-self", "later release"} {
		if !strings.Contains(f.stderr.String(), want) {
			t.Errorf("the line does not say %q:\n%s", want, f.stderr.String())
		}
	}
}

// TestUnifyHomeDeclinesOnAReadonlyYolo: a `workspace_readonly` entry covering .yolo means the jail
// could not use a link into it as a writable home, so nothing is composed or merged, one line says
// so, and the launch goes on.
func TestUnifyHomeDeclinesOnAReadonlyYolo(t *testing.T) {
	f := newUnifyFixture(t, "podman-self")
	putUnify(t, f.home("podman-self"), "claude/projects/b.jsonl", "b")
	cfg := jsonx.NewOrderedMap()
	cfg.Set("workspace_readonly", []any{".yolo"})
	f.o.unifyHome(cfg, f.o.homeDir(), packsFixture(t, "claude"), "podman")

	if fi, err := os.Lstat(filepath.Join(f.home("podman-self"), "claude", "projects")); err != nil || !fi.IsDir() {
		t.Errorf("a read-only .yolo's slice was composed anyway: %v", err)
	}
	if _, err := os.Lstat(paths.UnifiedHome(f.ws)); err == nil {
		t.Errorf("the unified home was made under a read-only .yolo")
	}
	if n := strings.Count(f.stderr.String(), "workspace_readonly"); n != 1 {
		t.Errorf("the decline was said %d times, want once:\n%s", n, f.stderr.String())
	}
}

// TestUnifyHomeKeepsAPacksCarveOutsPerSetup: pi's install tree stays in each slice while its
// sessions are shared, and another setup's carve-out never leaves its home.
func TestUnifyHomeKeepsAPacksCarveOutsPerSetup(t *testing.T) {
	f := newUnifyFixture(t, "podman-self")
	podman, apple := f.home("podman-self"), f.home("apple-container-self")
	putUnify(t, podman, "pi/agent/sessions/s.jsonl", "s")
	putUnify(t, podman, "pi/agent/npm/pkg/index.js", "elf")
	putUnify(t, apple, ".pi/agent/npm/pkg/index.js", "macho")
	putUnify(t, apple, ".pi/agent/sessions/t.jsonl", "t")
	f.unify(t, "podman", "pi")

	if got := readFileT(t, f.unified(".pi/agent/sessions/s.jsonl")); got != "s" {
		t.Errorf("a session was not shared: %q", got)
	}
	if got := readFileT(t, f.unified(".pi/agent/sessions/t.jsonl")); got != "t" {
		t.Errorf("another setup's session was not merged: %q", got)
	}
	if got := readFileT(t, filepath.Join(podman, "pi", "agent", "npm", "pkg", "index.js")); got != "elf" {
		t.Errorf("a carve-out left its own slice: %q", got)
	}
	if got := readFileT(t, filepath.Join(apple, ".pi", "agent", "npm", "pkg", "index.js")); got != "macho" {
		t.Errorf("another setup's carve-out was merged away: %q", got)
	}
	if _, err := os.Lstat(f.unified(".pi/agent/npm")); err == nil {
		t.Errorf("a carve-out reached the unified home")
	}
}

// TestUnifyHomeLeavesAHomeThatIsNotTheUsers: a source another user owns stays, with the chown named.
func TestUnifyHomeLeavesAHomeThatIsNotTheUsers(t *testing.T) {
	f := newUnifyFixture(t, "podman-self")
	putUnify(t, f.home("apple-container-self"), ".claude/projects/c.jsonl", "c")
	defer func(old func() int) { unifyHomeUID = old }(unifyHomeUID)
	unifyHomeUID = func() int { return os.Geteuid() + 1 }
	f.unify(t, "podman")

	if _, err := os.Lstat(filepath.Join(f.home("apple-container-self"), ".claude", "projects", "c.jsonl")); err != nil {
		t.Errorf("a home owned by another user was merged: %v", err)
	}
	if !strings.Contains(f.stderr.String(), "chown") {
		t.Errorf("the line does not name the chown:\n%s", f.stderr.String())
	}
}

// TestUnifyHomeLeavesAHomeThatIsItsOwnMount: a rename cannot cross a mount, so such a home stays,
// with the move named.
func TestUnifyHomeLeavesAHomeThatIsItsOwnMount(t *testing.T) {
	f := newUnifyFixture(t, "podman-self")
	other := f.home("apple-container-self")
	putUnify(t, other, ".claude/projects/c.jsonl", "c")
	defer func(old func(string) (uint64, bool)) { unifyHomeDevice = old }(unifyHomeDevice)
	unifyHomeDevice = func(p string) (uint64, bool) {
		if p == other {
			return 2, true
		}
		return 1, true
	}
	f.unify(t, "podman")

	if _, err := os.Lstat(filepath.Join(other, ".claude", "projects", "c.jsonl")); err != nil {
		t.Errorf("a home that is its own mount was merged: %v", err)
	}
	if !strings.Contains(f.stderr.String(), "its own mount") || !strings.Contains(f.stderr.String(), "mv -n") {
		t.Errorf("the line does not name the mount and the move:\n%s", f.stderr.String())
	}
}

// TestUnifyHomeRunsAfterPrepareWsStateAndBeforeDeliverChannel pins the call site no unit harness
// reaches: runContainer calls unifyHome once, after prepareWsState (which makes the slice's
// directories real and copies old overlays into them, so a link made earlier would be replaced)
// and before deliverChannel (the first write the jail reads).
func TestUnifyHomeRunsAfterPrepareWsStateAndBeforeDeliverChannel(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "run.go", nil, 0)
	if err != nil {
		t.Fatalf("parse run.go: %v", err)
	}
	first := map[string]token.Pos{}
	var unify []token.Pos
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "runContainer" {
			continue
		}
		ast.Inspect(fd, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				name := ""
				switch fn := call.Fun.(type) {
				case *ast.SelectorExpr:
					name = fn.Sel.Name
				case *ast.Ident:
					name = fn.Name
				}
				if _, seen := first[name]; !seen {
					first[name] = call.Pos()
				}
				if name == "unifyHome" {
					unify = append(unify, call.Pos())
				}
			}
			return true
		})
	}
	if len(unify) != 1 || first["prepareWsState"] == token.NoPos || first["deliverChannel"] == token.NoPos ||
		unify[0] < first["prepareWsState"] || unify[0] > first["deliverChannel"] {
		t.Errorf("runContainer must call unifyHome once, after prepareWsState and before deliverChannel")
	}
}

// TestADeferredLegacyHomeBindsTheSlice (UHP-D7, superseding SUP-D21): with a jail of the workspace
// running the legacy home cannot move, and the launch binds its own setup's slice instead of the
// legacy home, so its sessions are not split across the two and the legacy home is adopted later.
func TestADeferredLegacyHomeBindsTheSlice(t *testing.T) {
	f := newUnifyFixture(t, "podman-self")
	marker := seedLegacyHome(t, f.ws)
	makePeerSetup(t, f.ws, "seatbelt-self")
	holdKeeperKey(t, keeperKey(yoloruntime.JailName(f.ws, "seatbelt-self"), keeperNotchGuest))
	f.o.homeState = ""
	f.o.selectHome()
	defer f.o.releaseHomeLock()

	if want := paths.SetupHomeState(f.ws, "podman-self"); f.o.homeDir() != want {
		t.Errorf("a deferred launch binds %s, want its slice %s", f.o.homeDir(), want)
	}
	if _, err := os.Stat(filepath.Join(paths.LegacyHomeState(f.ws), marker)); err != nil {
		t.Errorf("the legacy home moved while a jail ran: %v", err)
	}
	for _, want := range []string{"a jail of this project is running", "`yolo stop`"} {
		if !strings.Contains(f.stderr.String(), want) {
			t.Errorf("the deferral does not say %q:\n%s", want, f.stderr.String())
		}
	}
}

// TestLegacyRemainderMergesIntoAPopulatedSlice (UHP-D7): once no jail runs, a legacy home whose
// setup already holds a slice (a deferred launch made it) is not left stranded behind a refusal: its
// remainder is merged into the slice by the union primitive, the slice's own names winning ties
// by keeping both, and the record and capture store move only where the slice has none.
func TestLegacyRemainderMergesIntoAPopulatedSlice(t *testing.T) {
	f := newUnifyFixture(t, "podman-self")
	legacy, slice := paths.LegacyHomeState(f.ws), f.home("podman-self")
	putUnify(t, legacy, "local/bin/installed-by-legacy", "legacy")
	putUnify(t, legacy, "bash_history", "legacy history")
	putUnify(t, slice, "local/bin/installed-by-slice", "slice")
	putUnify(t, slice, "bash_history", "slice history")
	state := paths.WorkspaceStateDir(f.ws)
	putUnify(t, state, "prism/baseline", "legacy baseline")
	putUnify(t, filepath.Dir(slice), "prism/baseline", "slice baseline")

	res, line := adoptLegacyHome(f.o.legacyHomeRequest())
	if res != legacyHomeMoved {
		t.Fatalf("result = %v (%q), want the remainder merged", res, line)
	}
	if got := readFileT(t, filepath.Join(slice, "local", "bin", "installed-by-legacy")); got != "legacy" {
		t.Errorf("the legacy install did not arrive: %q", got)
	}
	if got := readFileT(t, filepath.Join(slice, "local", "bin", "installed-by-slice")); got != "slice" {
		t.Errorf("the slice's own install was disturbed: %q", got)
	}
	if got := readFileT(t, filepath.Join(slice, "bash_history")); got != "slice history" && got != "legacy history" {
		t.Errorf("a clash lost both files: %q", got)
	}
	kept := 0
	for _, name := range []string{"bash_history", "bash_history.from-legacy"} {
		if _, err := os.Lstat(filepath.Join(slice, name)); err == nil {
			kept++
		}
	}
	if kept != 2 {
		t.Errorf("a clash must keep both files, found %d", kept)
	}
	if got := readFileT(t, filepath.Join(filepath.Dir(slice), "prism", "baseline")); got != "slice baseline" {
		t.Errorf("the legacy capture store replaced the slice's: %q", got)
	}
	if _, err := os.Lstat(filepath.Join(legacy, "local", "bin", "installed-by-legacy")); err == nil {
		t.Errorf("the legacy file was copied, not moved")
	}
}
