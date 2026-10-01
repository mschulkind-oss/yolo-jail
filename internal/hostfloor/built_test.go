package hostfloor

// built_test.go is the floor's third recipe, a FORK's build (built.go;
// docs/design/forked-programs-as-packs.md FP-D4, the plan's step 7): the capture store's build of
// the fork's pinned commit, relocated into the floor, over floortest's fake Node and a fake store
// whose entries are shaped like the 2026-10-01 stand-in's (a Node package under ~/.npm-global, its
// bin a RELATIVE link into it). No build runs anywhere: Build is a stand-in that files an entry.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

const (
	forkCommitOne = "1111111111111111111111111111111111111111"
	forkCommitTwo = "2222222222222222222222222222222222222222"
)

// forkProgram is a base pack's forkcli after the selection's fork rewrite: delivered by fork pack
// forkpack's build, its program the npm-shaped bin under ~/.npm-global.
func forkProgram() Program {
	return Program{Pack: "basepack", Install: packdecl.Install{
		Kind: packdecl.InstallKindSource, Bin: "forkcli", ForkedBy: "forkpack",
		Source: "git+https://example.invalid/forkcli-fork?ref=main",
		Build:  `npm ci && npm install -g "$(npm pack --silent)"`,
		Produces: []string{".npm-global/bin/forkcli",
			".npm-global/lib/node_modules/forkcli"},
	}}
}

// buildStore is a fake capture store of fork builds, one per (bin, commit, recipe) — what
// ResolveBuild answers with the hit check: the pin's commit and the fork's current recipe, nothing
// else.
type buildStore struct {
	t      *testing.T
	store  *capture.Store
	byPin  map[string]*capture.Entry
	builds []string
}

func newBuildStore(t *testing.T) *buildStore {
	return &buildStore{t: t, store: &capture.Store{Dir: filepath.Join(resolvedTemp(t), "captures")},
		byPin: map[string]*capture.Entry{}}
}

// add admits a build of bin at commit, made under the jail home /home/agent: a Node package whose
// bin is a relative link to its entry script, and a text file naming the build home absolutely —
// the reference a relocation rewrites. relocatable false records it the way a build embedding the
// home in a binary is recorded.
func (b *buildStore) add(bin, commit string, relocatable bool) *capture.Entry {
	b.t.Helper()
	p := forkProgram()
	p.Install.Bin = bin
	return b.addFor(p, commit, relocatable)
}

// addFor is add for p's recipe: the build p's declaration asks for at commit.
func (b *buildStore) addFor(p Program, commit string, relocatable bool) *capture.Entry {
	b.t.Helper()
	bin := p.Bin()
	return b.addShapedFor(p, commit, func(m *capture.Manifest) {
		if !relocatable {
			m.Relocatable = false
			m.NotRelocatable = []string{".npm-global/lib/node_modules/" + bin + "/addon.node is not text and embeds /home/agent"}
		}
	})
}

// addShaped is add with the manifest handed to shape before it is written: a build whose own
// account of itself says something add's never does.
func (b *buildStore) addShaped(bin, commit string, shape func(*capture.Manifest)) *capture.Entry {
	b.t.Helper()
	p := forkProgram()
	p.Install.Bin = bin
	return b.addShapedFor(p, commit, shape)
}

// keyOf is the fake store's key for p's build at commit: the bin, the commit and p's recipe.
func (b *buildStore) keyOf(p Program, commit string) string {
	return p.Bin() + "@" + commit + "@" + p.Install.SourceRecipe()
}

func (b *buildStore) addShapedFor(p Program, commit string, shape func(*capture.Manifest)) *capture.Entry {
	t := b.t
	t.Helper()
	bin := p.Bin()
	staged, err := b.store.Stage(bin + "-" + commit[:8])
	must(t, err)
	tree := capture.TreeDir(staged)
	pkg := ".npm-global/lib/node_modules/" + bin
	script := "#!/usr/bin/env node\nconsole.log('" + bin + " " + commit[:8] + "')\n"
	config := `{"root":"/home/agent/` + pkg + `"}` + "\n"
	must(t, os.MkdirAll(filepath.Join(tree, filepath.FromSlash(pkg)), 0o755))
	must(t, os.MkdirAll(filepath.Join(tree, ".npm-global", "bin"), 0o755))
	must(t, os.WriteFile(filepath.Join(tree, filepath.FromSlash(pkg), "cli.js"), []byte(script), 0o755))
	must(t, os.WriteFile(filepath.Join(tree, filepath.FromSlash(pkg), "config.json"), []byte(config), 0o644))
	target := "../lib/node_modules/" + bin + "/cli.js"
	must(t, os.Symlink(target, filepath.Join(tree, ".npm-global", "bin", bin)))
	m := &capture.Manifest{
		Schema: capture.ManifestSchema, Home: "/home/agent", Platform: capture.Platform(),
		Surfaces: []string{".npm-global", ".local", "go"}, Excluded: []string{},
		Entries: []capture.ManifestEntry{
			{Path: ".npm-global", Kind: capture.KindDir, Mode: "0755"},
			{Path: ".npm-global/bin", Kind: capture.KindDir, Mode: "0755"},
			{Path: ".npm-global/bin/" + bin, Kind: capture.KindSymlink, Target: target},
			{Path: ".npm-global/lib", Kind: capture.KindDir, Mode: "0755"},
			{Path: ".npm-global/lib/node_modules", Kind: capture.KindDir, Mode: "0755"},
			{Path: pkg, Kind: capture.KindDir, Mode: "0755"},
			{Path: pkg + "/cli.js", Kind: capture.KindFile, Mode: "0755", Size: int64(len(script))},
			{Path: pkg + "/config.json", Kind: capture.KindFile, Mode: "0644", Size: int64(len(config))},
		},
		AbsoluteRefs: []capture.AbsoluteRef{{Path: pkg + "/config.json", Kind: capture.RefFileContent, Value: "/home/agent"}},
		RefScan:      capture.RefScanFull, Relocatable: true,
	}
	shape(m)
	must(t, capture.WriteManifest(staged, m))
	entry, err := b.store.AdmitEntry(staged)
	must(t, err)
	b.byPin[b.keyOf(p, commit)] = entry
	return entry
}

func (b *buildStore) resolve(p Program, commit string) (*capture.Entry, error) {
	if e, ok := b.byPin[b.keyOf(p, commit)]; ok {
		return e, nil
	}
	return nil, fmt.Errorf("nothing in %s records a build of %s at %s", b.store.Dir, p.Bin(), commit)
}

// forkWorld is a world whose floor holds forkProgram: Linux (where the floor holds a build), the
// fork pinned at *pin, the store's builds answering ResolveBuild, and Build a stand-in that files a
// relocatable build of the commit it is asked for.
func forkWorld(t *testing.T, pin *string) (*world, *buildStore) {
	t.Helper()
	w := newWorld(t)
	w.floor.GOOS = "linux"
	bs := newBuildStore(t)
	w.floor.ForkPin = func(p Program) (string, string) {
		if p.Install.ForkedBy != "forkpack" {
			t.Errorf("ForkPin asked about %s, built by %q", p.Bin(), p.Install.ForkedBy)
		}
		return *pin, ""
	}
	w.floor.ResolveBuild = bs.resolve
	w.floor.Build = func(p Program, commit string) (*capture.Entry, error) {
		bs.builds = append(bs.builds, commit)
		return bs.addFor(p, commit, true), nil
	}
	return w, bs
}

// A fork's build in the store is the floor's copy: materialized and RELOCATED out of the jail's
// home, and started by the floor's own Node by path, from a launcher that needs no environment.
// The store already held the pinned build, so nothing was built.
func TestAForkBuildIsMaterializedIntoTheFloorAndRunsOnItsNode(t *testing.T) {
	pin := forkCommitOne
	w, bs := forkWorld(t, &pin)
	entry := bs.add("forkcli", forkCommitOne, true)
	p := forkProgram()

	if st := w.floor.Status(p); st.Disposition != Missing {
		t.Fatalf("before any install: %s (%s), want missing", st.Disposition, st.Reason)
	}
	st, outcome, err := w.floor.Ensure(context.Background(), p)
	if err != nil {
		t.Fatalf("Ensure: %v\n%s", err, w.out.String())
	}
	if outcome != Installed || st.Disposition != Provisioned || len(bs.builds) != 0 {
		t.Fatalf("outcome %s, disposition %s, builds %v", outcome, st.Disposition, bs.builds)
	}
	rec := st.Record
	if rec.Via != "source" || rec.Declared != p.Install.Source || rec.Revision != forkCommitOne ||
		rec.Recipe != p.Install.SourceRecipe() || rec.Capture != entry.Key ||
		rec.Version != "commit 111111111111" || rec.Node != w.version {
		t.Errorf("record = %+v", rec)
	}
	node := filepath.Join(w.floor.Dir, "node", "v"+w.version, "bin", "node")
	if len(rec.Exec) != 2 || rec.Exec[0] != node || rec.Exec[1] != rec.Entry ||
		!strings.HasSuffix(rec.Entry, filepath.FromSlash("/home/.npm-global/bin/forkcli")) {
		t.Fatalf("Exec = %q, want [%s <the build's bin>]: a Node fork starts on the floor's Node", rec.Exec, node)
	}
	cmd := exec.Command(st.Launcher, "--version")
	cmd.Env = []string{}
	got, err := cmd.CombinedOutput()
	if err != nil || string(got) != "node:"+rec.Entry+" --version\n" {
		t.Fatalf("running the floor's forkcli with no environment: %q %v", got, err)
	}
	// RELOCATED: the reference to the build's home now names the floor's install.
	home := filepath.Dir(filepath.Dir(filepath.Dir(rec.Entry)))
	cfg, err := os.ReadFile(filepath.Join(home, ".npm-global", "lib", "node_modules", "forkcli", "config.json"))
	if err != nil || strings.Contains(string(cfg), "/home/agent") || !strings.Contains(string(cfg), home) {
		t.Errorf("config.json after the materialize = %q (%v), want its /home/agent reference rewritten to %s",
			cfg, err, home)
	}
	if out := w.out.String(); !strings.Contains(out, "fork pack forkpack's build of "+p.Install.Source+
		" at commit "+forkCommitOne) || !strings.Contains(out, "materialized forkcli from fork build "+entry.Key) {
		t.Errorf("the install does not say what it put in place:\n%s", out)
	}
}

// A store with no build at the pin: the floor runs the build act once, at the pinned commit, then
// materializes what it filed; the next launch finds the floor's copy current and builds nothing.
func TestAForkBuildTheStoreLacksIsBuiltOnceAtThePin(t *testing.T) {
	pin := forkCommitOne
	w, bs := forkWorld(t, &pin)
	p := forkProgram()
	if st := w.floor.Status(p); st.Disposition != Missing {
		t.Fatalf("with no build and a build act: %s (%s), want missing", st.Disposition, st.Reason)
	}
	st, outcome, err := w.floor.Ensure(context.Background(), p)
	if err != nil || outcome != Installed || st.Record.Revision != forkCommitOne {
		t.Fatalf("Ensure: %s %+v %v\n%s", outcome, st.Record, err, w.out.String())
	}
	if strings.Join(bs.builds, ",") != forkCommitOne {
		t.Fatalf("builds = %v, want one, at the pin", bs.builds)
	}
	if _, outcome, err := w.floor.Ensure(context.Background(), p); err != nil || outcome != Current ||
		len(bs.builds) != 1 {
		t.Errorf("the second launch: %s %v, builds %v", outcome, err, bs.builds)
	}
	if !strings.Contains(w.out.String(), "no build of forkcli at commit 111111111111 on this machine yet") {
		t.Errorf("the build is not announced before it runs:\n%s", w.out.String())
	}
}

// A build that cannot move out of the jail's home is NO FLOOR ENTRY, naming that home — and it is
// never rebuilt for the floor, since the same commit and recipe build the same bytes.
func TestAForkBuildThatCannotLeaveTheJailHomeIsNoFloorEntry(t *testing.T) {
	pin := forkCommitOne
	w, bs := forkWorld(t, &pin)
	bs.add("forkcli", forkCommitOne, false)
	w.floor.Build = func(Program, string) (*capture.Entry, error) {
		t.Fatal("the floor rebuilt a build that cannot move")
		return nil, nil
	}
	p := forkProgram()
	st := w.floor.Status(p)
	for _, want := range []string{"fork pack forkpack's build of forkcli at commit 111111111111",
		"built for the jail's home, /home/agent", "runs in a jail only", "addon.node is not text"} {
		if st.Disposition != NoEntry || !strings.Contains(st.Reason, want) {
			t.Errorf("Status = %s (%s), want no floor entry naming %q", st.Disposition, st.Reason, want)
		}
	}
	if _, _, err := w.floor.Ensure(context.Background(), p); !errors.Is(err, ErrNoEntry) {
		t.Errorf("Ensure = %v, want ErrNoEntry", err)
	}
	if _, err := os.Stat(w.floor.Dir); err == nil {
		t.Errorf("asking about a build that cannot move created %s", w.floor.Dir)
	}
	// The same refusal when it is the install's own build that turns out unmovable.
	fresh, fbs := forkWorld(t, &pin)
	fresh.floor.Build = func(p Program, commit string) (*capture.Entry, error) {
		return fbs.addFor(p, commit, false), nil
	}
	st, _, err := fresh.floor.Ensure(context.Background(), p)
	if !errors.Is(err, ErrNoEntry) || st.Disposition != NoEntry || !strings.Contains(st.Reason, "/home/agent") {
		t.Errorf("when the install builds it: err %v, %s (%s)", err, st.Disposition, st.Reason)
	}
	if dirs, _ := os.ReadDir(fresh.floor.programsDir("forkcli")); len(dirs) != 0 {
		t.Errorf("an unmovable build left %d install directories", len(dirs))
	}
}

// A MOVED PIN IS PENDING, and the next Ensure builds and installs the new commit. A changed build
// recipe is pending too: an entry built from the old one is another build.
func TestAMovedForkPinIsPendingAndReinstallsTheNewCommit(t *testing.T) {
	pin := forkCommitOne
	w, bs := forkWorld(t, &pin)
	p := forkProgram()
	if _, _, err := w.floor.Ensure(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	pin = forkCommitTwo
	st := w.floor.Status(p)
	if st.Disposition != Provisioned || !strings.Contains(st.Pending,
		"fork pack forkpack now pins it at commit 222222222222 (installed: commit 111111111111)") {
		t.Fatalf("a moved pin: %s pending %q", st.Disposition, st.Pending)
	}
	st, outcome, err := w.floor.Ensure(context.Background(), p)
	if err != nil || outcome != Installed || st.Record.Revision != forkCommitTwo || st.Pending != "" {
		t.Fatalf("a moved pin reinstalls: %s %+v %v\n%s", outcome, st.Record, err, w.out.String())
	}
	if strings.Join(bs.builds, ",") != forkCommitOne+","+forkCommitTwo {
		t.Errorf("builds = %v, want one per pin", bs.builds)
	}

	edited := p
	edited.Install.Build = "make install"
	if st := w.floor.Status(edited); !strings.Contains(st.Pending, "build recipe changed since commit 222222222222") {
		t.Errorf("an edited build line: pending %q", st.Pending)
	}
}

// A failed reinstall at a moved pin does NOT keep the old build serving, as a failed npm update
// keeps its version: the installed build is of another commit, a near-miss (§9).
func TestAFailedBuildAtAMovedPinDoesNotKeepTheOldBuildServing(t *testing.T) {
	pin := forkCommitOne
	w, _ := forkWorld(t, &pin)
	p := forkProgram()
	if _, _, err := w.floor.Ensure(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	pin = forkCommitTwo
	w.floor.Build = func(Program, string) (*capture.Entry, error) {
		return nil, errors.New("npm ERR! the build failed")
	}
	st, outcome, err := w.floor.Ensure(context.Background(), p)
	if err == nil || outcome == Kept || outcome == Current {
		t.Fatalf("Ensure = %s, %v: a build of the old pin served after the new one failed", outcome, err)
	}
	if !strings.Contains(err.Error(), "the build failed") ||
		!strings.Contains(w.out.String(), "the installed commit 111111111111 is not the build the pack now asks for, so the floor no longer runs it") {
		t.Errorf("err %v\n%s", err, w.out.String())
	}
	// NOT SERVED BY ANY ROUTE: the launcher leaves bin/, which ends every host agent's PATH, so an
	// agent's own `forkcli` does not find the old commit there either; and the floor says missing.
	if _, lerr := os.Lstat(w.floor.Launcher("forkcli")); !os.IsNotExist(lerr) {
		t.Errorf("the old build's launcher is still in bin/ (%v): every host agent's PATH still runs it", lerr)
	}
	if st.Disposition == Provisioned || st.Record != nil {
		t.Errorf("Ensure's status after the failure = %s %+v, want the old build not reported as held", st.Disposition, st.Record)
	}
	if st := w.floor.Status(p); st.Disposition != Missing {
		t.Errorf("Status after the failure = %s (%s), want missing", st.Disposition, st.Reason)
	}
}

// THE NEAR-MISS RULE IS ABOUT WHICH BUILD, both ways round. A fork's build at its pin that is pending
// only for a raised node_floor IS the build the lock names, so a failed reinstall keeps it serving,
// as an npm program keeps its version. And a fork's build left in the floor once the program is the
// base's again (the fork pack dropped) is not what the pack asks for, so a failed install of the
// base's own package does not leave it running under the base's name.
func TestAFailedReinstallKeepsAForkBuildOnlyWhenItIsThePinsBuild(t *testing.T) {
	pin := forkCommitOne
	w, _ := forkWorld(t, &pin)
	p := forkProgram()
	if _, _, err := w.floor.Ensure(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	raised := p
	raised.Install.NodeFloor = "99.1"
	w.floor.NodeFloor = "99.1"
	w.floor.Node.BaseURL = "http://127.0.0.1:1" // nothing listens: the new Node cannot be fetched
	st, outcome, err := w.floor.Ensure(context.Background(), raised)
	if err != nil || outcome != Kept || st.Record == nil || st.Record.Revision != forkCommitOne {
		t.Fatalf("a raised node_floor whose reinstall failed: %s %v %+v, want the pin's build kept\n%s",
			outcome, err, st.Record, w.out.String())
	}
	if _, lerr := os.Stat(w.floor.Launcher("forkcli")); lerr != nil {
		t.Errorf("the pin's build lost its launcher: %v", lerr)
	}

	dropped, _ := forkWorld(t, &pin)
	if _, _, err := dropped.floor.Ensure(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	base := npmProgram("basepack", "forkcli", "forkcli-unpublished") // the registry has no such package
	st, outcome, err = dropped.floor.Ensure(context.Background(), base)
	if err == nil || outcome == Kept {
		t.Fatalf("the base's install failed over a fork's build: %s %v — the fork's build kept serving", outcome, err)
	}
	if _, lerr := os.Lstat(dropped.floor.Launcher("forkcli")); !os.IsNotExist(lerr) {
		t.Errorf("the dropped fork's build is still in bin/: %v", lerr)
	}
	if st.Disposition == Provisioned {
		t.Errorf("status after the failure = %s, want the fork's build not reported as held", st.Disposition)
	}

	// And the fork's own two near-misses: a build from an edited recipe, and the base's upstream
	// program installed before the fork was selected — never run under the fork's name.
	failing := func(Program, string) (*capture.Entry, error) { return nil, errors.New("npm ERR! the build failed") }
	edited, _ := forkWorld(t, &pin)
	if _, _, err := edited.floor.Ensure(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	edited.floor.Build = failing
	recipe := p
	recipe.Install.Build = "make install"
	if _, outcome, err := edited.floor.Ensure(context.Background(), recipe); err == nil || outcome == Kept {
		t.Errorf("an edited recipe whose build failed: %s %v, want the old recipe's build not kept", outcome, err)
	}
	upstream, _ := forkWorld(t, &pin)
	upstream.publish("forkcli-pkg", "1.0.0", "bin=forkcli")
	if _, _, err := upstream.floor.Ensure(context.Background(), npmProgram("basepack", "forkcli", "forkcli-pkg")); err != nil {
		t.Fatalf("installing the base's own package: %v\n%s", err, upstream.out.String())
	}
	upstream.floor.Build = failing
	if _, outcome, err := upstream.floor.Ensure(context.Background(), p); err == nil || outcome == Kept {
		t.Errorf("a fork whose build failed over the base's package: %s %v, want the base's package not run as the fork", outcome, err)
	}
	if _, lerr := os.Lstat(upstream.floor.Launcher("forkcli")); !os.IsNotExist(lerr) {
		t.Errorf("the base's package is still in bin/ under the fork's program: %v", lerr)
	}
}

// A MOVED PIN ON A MACHINE THAT CANNOT BUILD is what a fork the floor never held is there: no floor
// entry, with the reason, so `yolo host` runs the PATH copy — never a build act started with no
// runtime, and never the old commit.
func TestAMovedForkPinOnAMachineThatCannotBuildIsNoFloorEntry(t *testing.T) {
	pin := forkCommitOne
	w, bs := forkWorld(t, &pin)
	p := forkProgram()
	if _, _, err := w.floor.Ensure(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	pin = forkCommitTwo
	w.floor.CaptureUnavailable = func() string { return "no container runtime (podman) is on PATH" }
	st, _, err := w.floor.Ensure(context.Background(), p)
	if !errors.Is(err, ErrNoEntry) || st.Disposition != NoEntry ||
		!strings.Contains(st.Reason, "there is no build of forkcli at commit 222222222222 on this machine, and no container runtime") {
		t.Fatalf("Ensure = %s (%s), %v; want no floor entry naming the missing runtime", st.Disposition, st.Reason, err)
	}
	if len(bs.builds) != 1 {
		t.Errorf("builds = %v: a build act ran on a machine that cannot build", bs.builds)
	}
	if _, lerr := os.Lstat(w.floor.Launcher("forkcli")); !os.IsNotExist(lerr) {
		t.Errorf("the old build's launcher is still in bin/: %v", lerr)
	}
}

// A fork's node_floor RAISES THE FLOOR'S NODE, as an npm program's does: its Node script starts on
// the floor's interpreter (HighestNodeFloor counts it). Above the Node the floor runs on, the
// install refuses rather than starting it on an older one, and a provisioned build whose
// node_floor rose is pending.
func TestAForksNodeFloorIsTheFloorsToMeet(t *testing.T) {
	pin := forkCommitOne
	w, _ := forkWorld(t, &pin)
	p := forkProgram()
	p.Install.NodeFloor = "99.1"
	w.floor.NodeFloor = HighestNodeFloor([]Program{p, npmProgram("x", "x", "x")})
	if got := w.floor.NodeVersion(); got != "99.1.0" {
		t.Fatalf("NodeVersion = %s, want 99.1.0: a fork's node_floor is the floor's too", got)
	}
	st, _, err := w.floor.Ensure(context.Background(), p)
	if err != nil || st.Record.Node != "99.1.0" {
		t.Fatalf("Ensure: %+v %v\n%s", st.Record, err, w.out.String())
	}

	below, _ := forkWorld(t, &pin)
	if _, _, err := below.floor.Ensure(context.Background(), p); err == nil ||
		!strings.Contains(err.Error(), "below the pack's node_floor 99.1") {
		t.Errorf("a floor whose Node is below the fork's node_floor: %v", err)
	}

	raised, _ := forkWorld(t, &pin)
	plain := forkProgram()
	if _, _, err := raised.floor.Ensure(context.Background(), plain); err != nil {
		t.Fatal(err)
	}
	if st := raised.floor.Status(p); st.Disposition != Provisioned ||
		!strings.Contains(st.Pending, "below the pack's node_floor 99.1") {
		t.Errorf("a node_floor raised past the installed build's Node: %s pending %q", st.Disposition, st.Pending)
	}
}

// What the build's own manifest says can keep it off the floor, read before anything is
// materialized: no runnable program at the fork's program path, and a claim to be relocatable that
// the relocating materialize does not honor (a scan that was not the full one), which is the same
// "built for the jail's home" answer as a build that says it cannot move.
func TestAForkBuildsManifestCanKeepItOffTheFloor(t *testing.T) {
	pin := forkCommitOne
	w, bs := forkWorld(t, &pin)
	w.floor.Build = func(Program, string) (*capture.Entry, error) {
		t.Fatal("the floor rebuilt a build the store already holds at the pin")
		return nil, nil
	}
	bs.addShaped("forkcli", forkCommitOne, func(m *capture.Manifest) {
		for i, e := range m.Entries {
			if e.Path == ".npm-global/bin/forkcli" {
				m.Entries[i].Target = "../lib/node_modules/forkcli/gone.js"
			}
		}
	})
	p := forkProgram()
	if st := w.floor.Status(p); st.Disposition != NoEntry ||
		!strings.Contains(st.Reason, "cannot run outside a jail") || !strings.Contains(st.Reason, "gone.js") {
		t.Errorf("a build whose bin links to nothing it recorded: %s (%s)", st.Disposition, st.Reason)
	}

	claims, cbs := forkWorld(t, &pin)
	claims.floor.Build = w.floor.Build
	cbs.addShaped("forkcli", forkCommitOne, func(m *capture.Manifest) { m.RefScan = capture.RefScanSymlinks })
	st, _, err := claims.floor.Ensure(context.Background(), p)
	if !errors.Is(err, ErrNoEntry) || st.Disposition != NoEntry ||
		!strings.Contains(st.Reason, "built for the jail's home, /home/agent") {
		t.Errorf("a relocatable claim the materialize refuses: %s (%s), %v", st.Disposition, st.Reason, err)
	}
	if _, lerr := os.Lstat(claims.floor.Launcher("forkcli")); !os.IsNotExist(lerr) {
		t.Errorf("a build the materialize refused has a launcher: %v", lerr)
	}
}

// A FORK IS NEVER POLLED: past the update interval a provisioned build is current, and the floor
// neither reads the store, builds, nor stamps the record.
func TestAForkBuildIsNeverPolled(t *testing.T) {
	pin := forkCommitOne
	w, _ := forkWorld(t, &pin)
	clk := &clock{t: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	w.floor.Now = clk.now
	p := forkProgram()
	st, _, err := w.floor.Ensure(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	checked := st.Record.Checked
	w.floor.ResolveBuild = func(Program, string) (*capture.Entry, error) {
		t.Fatal("the refresh read the store for a fork")
		return nil, nil
	}
	w.floor.Build = func(Program, string) (*capture.Entry, error) {
		t.Fatal("the refresh built a fork")
		return nil, nil
	}
	clk.t = clk.t.Add(5 * time.Hour)
	st, outcome, err := w.floor.Ensure(context.Background(), p)
	if err != nil || outcome != Current || !st.Record.Checked.Equal(checked) {
		t.Errorf("past the interval: %s %v, checked %s (was %s)", outcome, err, st.Record.Checked, checked)
	}
}

// The ways the floor holds no build of a fork: no usable pin, a Mac, and no build in the store with
// no runtime to make one. Each says why and names the fork; none touches the prefix.
func TestNoFloorEntryForAFork(t *testing.T) {
	cases := []struct {
		name string
		mut  func(f *Floor, pin *string)
		want []string
	}{
		{"no pin", func(f *Floor, _ *string) {
			f.ForkPin = func(Program) (string, string) {
				return "", "it has no pin yet — run `yolo pack install` to pin git+https://example.invalid/forkcli-fork?ref=main"
			}
		}, []string{"built from source by fork pack forkpack", "run `yolo pack install`"}},
		{"no pin reader", func(f *Floor, _ *string) { f.ForkPin = nil }, []string{"fork pack forkpack", "reads no fork pin"}},
		{"a Mac", func(f *Floor, _ *string) { f.GOOS = "darwin" }, []string{"in a Linux capture jail", "darwin/"}},
		{"no build and no runtime", func(f *Floor, _ *string) {
			f.CaptureUnavailable = func() string { return "no container runtime (podman) is on PATH" }
		}, []string{"there is no build of forkcli at commit 111111111111", "podman"}},
		{"no build act", func(f *Floor, _ *string) { f.Build = nil }, []string{"cannot run a fork's build"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pin := forkCommitOne
			w, _ := forkWorld(t, &pin)
			c.mut(w.floor, &pin)
			p := forkProgram()
			st := w.floor.Status(p)
			for _, want := range c.want {
				if st.Disposition != NoEntry || !strings.Contains(st.Reason, want) {
					t.Errorf("Status = %s (%s), want no floor entry naming %q", st.Disposition, st.Reason, want)
				}
			}
			if _, _, err := w.floor.Ensure(context.Background(), p); !errors.Is(err, ErrNoEntry) {
				t.Errorf("Ensure = %v, want ErrNoEntry", err)
			}
			if _, err := os.Stat(w.floor.Dir); err == nil {
				t.Errorf("asking about a fork with no floor entry created %s", w.floor.Dir)
			}
		})
	}
}

// A provisioned build whose pin goes away (the lock lost it, the fork's source changed) is no
// floor entry at once — the floor never serves a build the lock does not name — and the next
// `yolo host apply --assert` removes it.
func TestAProvisionedForkWhosePinIsGoneIsNoFloorEntry(t *testing.T) {
	pin := forkCommitOne
	w, _ := forkWorld(t, &pin)
	p := forkProgram()
	if _, _, err := w.floor.Ensure(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	pin = ""
	w.floor.ForkPin = func(Program) (string, string) {
		return "", "its source changed since it was pinned — run `yolo pack install`"
	}
	if st := w.floor.Status(p); st.Disposition != NoEntry || !strings.Contains(st.Reason, "source changed") {
		t.Fatalf("Status = %s (%s)", st.Disposition, st.Reason)
	}
	removed := w.floor.Reconcile([]Program{p}, true)
	if len(removed) != 1 || removed[0].Bin != "forkcli" {
		t.Fatalf("Reconcile = %+v, want the unpinned fork's entry removed", removed)
	}
	if _, err := os.Stat(w.floor.Launcher("forkcli")); !os.IsNotExist(err) {
		t.Errorf("the launcher is still there: %v", err)
	}
}

// A build whose program is not a Node script is started as itself, on no Node.
func TestAForkBuildThatIsNotANodeScriptIsStartedAsItself(t *testing.T) {
	pin := forkCommitOne
	w, bs := forkWorld(t, &pin)
	p := forkProgram()
	p.Install.Produces = []string{".local/bin/forkcli"}
	w.floor.Build = func(p Program, commit string) (*capture.Entry, error) {
		staged, err := bs.store.Stage("native-" + commit[:8])
		must(t, err)
		body := "#!/bin/sh\necho forkcli native \"$@\"\n"
		bin := filepath.Join(capture.TreeDir(staged), ".local", "bin")
		must(t, os.MkdirAll(bin, 0o755))
		must(t, os.WriteFile(filepath.Join(bin, "forkcli"), []byte(body), 0o755))
		must(t, capture.WriteManifest(staged, &capture.Manifest{
			Schema: capture.ManifestSchema, Home: "/home/agent", Platform: capture.Platform(),
			Surfaces: []string{".local"}, Excluded: []string{},
			Entries: []capture.ManifestEntry{
				{Path: ".local", Kind: capture.KindDir, Mode: "0755"},
				{Path: ".local/bin", Kind: capture.KindDir, Mode: "0755"},
				{Path: ".local/bin/forkcli", Kind: capture.KindFile, Mode: "0755", Size: int64(len(body))},
			},
			AbsoluteRefs: []capture.AbsoluteRef{}, RefScan: capture.RefScanFull, Relocatable: true,
		}))
		entry, err := bs.store.AdmitEntry(staged)
		must(t, err)
		bs.byPin[bs.keyOf(p, commit)] = entry
		return entry, nil
	}
	st, _, err := w.floor.Ensure(context.Background(), p)
	if err != nil {
		t.Fatalf("Ensure: %v\n%s", err, w.out.String())
	}
	if st.Record.Node != "" || len(st.Record.Exec) != 1 || st.Record.Exec[0] != st.Record.Entry {
		t.Fatalf("record = %+v, want the program started as itself", st.Record)
	}
	cmd := exec.Command(st.Launcher, "x")
	cmd.Env = []string{}
	if got, err := cmd.CombinedOutput(); err != nil || string(got) != "forkcli native x\n" {
		t.Errorf("running it: %q %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(w.floor.Dir, "node")); err == nil {
		t.Error("a program that is not a Node script fetched the floor's Node")
	}
}
