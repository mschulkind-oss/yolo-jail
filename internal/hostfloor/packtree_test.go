package hostfloor

// packtree_test.go is the floor's arm for a PACK-TREE PROGRAM (packtree.go;
// docs/design/pack-buildable-programs.md §9's `yolo host`, Linux row; the plan's Task 9): a program a
// pack delivers `via: "pack"` is keyed by its source directory's digest (or its prebuilt's sha256),
// never by a fork pin, and its host copy is the store's entry under that key, relocated into the floor
// as a fork's build is. The resolver (Floor.PackTree) is a stand-in here; the CLI's production one is
// driven end to end in internal/cli's hostfloorpacktree_test.go.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

const (
	packTreeDigestOne = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	packTreeDigestTwo = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func packTreeProgram() Program {
	m := &packdecl.Manifest{Contributes: []packdecl.Contribution{{
		Kind: packdecl.KindProgram, Bin: "board", Via: packdecl.ViaPack,
		From: "tools/board-cli", Build: "make", Run: "dist/board",
	}}}
	return Program{Pack: "fx", Install: m.InstallContributions()[0]}
}

// packTreeStore is a fake capture store of pack-tree builds, keyed by digest: each entry the layout
// the placement script leaves (the program's directory and its relative link), its program a shell
// script printing the digest it was built at.
type packTreeStore struct {
	t      *testing.T
	store  *capture.Store
	byKey  map[string]*capture.Entry
	builds []string
}

func newPackTreeStore(t *testing.T) *packTreeStore {
	return &packTreeStore{t: t, store: &capture.Store{Dir: filepath.Join(resolvedTemp(t), "captures")},
		byKey: map[string]*capture.Entry{}}
}

func (s *packTreeStore) add(p Program, digest string) *capture.Entry {
	t := s.t
	t.Helper()
	staged, err := s.store.Stage(p.Bin() + "-" + digest[:8])
	must(t, err)
	tree := capture.TreeDir(staged)
	dir := filepath.Join(tree, filepath.FromSlash(packdecl.PackTreeProgramDir(p.Bin())))
	run := filepath.Join(dir, filepath.FromSlash(p.Install.PackRun))
	must(t, os.MkdirAll(filepath.Dir(run), 0o755))
	must(t, os.WriteFile(run, []byte("#!/bin/sh\necho "+p.Bin()+"-"+digest[:8]+"\n"), 0o755))
	link := filepath.Join(tree, filepath.FromSlash(packdecl.PackTreeProgramLink(p.Bin())))
	must(t, os.MkdirAll(filepath.Dir(link), 0o755))
	must(t, os.Symlink(packdecl.PackTreeLinkTarget(p.Bin(), p.Install.PackRun), link))
	m, err := capture.ManifestForTree(tree, "/home/agent", capture.Platform())
	must(t, err)
	must(t, capture.WriteManifest(staged, m))
	entry, err := s.store.AdmitEntry(staged)
	must(t, err)
	s.byKey[digest] = entry
	return entry
}

// packTreeWorld is a Linux world whose floor resolves p at *digest: a build line, so the act boots a
// jail; Build a stand-in that files the build of the digest it is asked for, or fails with *fail.
// resolves counts the resolver's calls. Every fork hook fails the test when asked.
func packTreeWorld(t *testing.T, digest *string, fail *error) (*world, *packTreeStore, *int) {
	t.Helper()
	w := newLinuxWorld(t)
	ps := newPackTreeStore(t)
	resolves := 0
	w.floor.ForkPin = func(p Program) (string, string) {
		t.Errorf("ForkPin asked for pack-tree program %s", p.Bin())
		return "", ""
	}
	w.floor.ForkPinnable = func(p Program) bool {
		t.Errorf("ForkPinnable asked for pack-tree program %s", p.Bin())
		return true
	}
	w.floor.PinFork = func(p Program, _ func(string)) (string, string) {
		t.Errorf("PinFork asked for pack-tree program %s", p.Bin())
		return "", ""
	}
	w.floor.ResolveBuild = func(p Program, _ string) (*capture.Entry, error) {
		t.Errorf("ResolveBuild (a fork's hit) asked for pack-tree program %s", p.Bin())
		return nil, errors.New("no")
	}
	w.floor.Build = func(p Program, _ string) (*capture.Entry, error) {
		t.Errorf("Build (a fork's act) asked for pack-tree program %s", p.Bin())
		return nil, errors.New("no")
	}
	w.floor.PackTree = func(p Program) PackTreeDelivery {
		resolves++
		d := *digest
		return PackTreeDelivery{
			Revision: d, Recipe: p.Install.PackTreeRecipe(), What: "pack fx's tools/board-cli at tree digest " + d[:12],
			Version: "tree " + d[:12], Jail: true,
			Hit: func() (*capture.Entry, error) {
				if e, ok := ps.byKey[d]; ok {
					return e, nil
				}
				return nil, fmt.Errorf("no build of %s at %s", p.Bin(), d)
			},
			Build: func() (*capture.Entry, error) {
				ps.builds = append(ps.builds, d)
				if fail != nil && *fail != nil {
					return nil, *fail
				}
				return ps.add(p, d), nil
			},
		}
	}
	return w, ps, &resolves
}

// nextLaunch is the same floor as a new process opens it: no resolution kept from the last.
func (w *world) nextLaunch() *Floor {
	f := *w.floor
	f.packTrees = nil
	return &f
}

// THE TASK'S TEST: a pack-tree program needs no pin and resolves by digest. The first install builds
// once at the digest and runs the relocated copy; the next launch at the same digest builds nothing;
// an edit (a new digest) reinstalls the new build once. A status no install preceded (`yolo check`)
// never resolves, since resolving digests the source (§10).
func TestAPackTreeProgramNeedsNoPinAndResolvesByDigest(t *testing.T) {
	digest := packTreeDigestOne
	w, ps, resolves := packTreeWorld(t, &digest, nil)
	p := packTreeProgram()

	if st := w.floor.Status(p); st.Disposition != Missing || *resolves != 0 {
		t.Fatalf("before any install: %s (%s), %d resolutions; want missing and none", st.Disposition, st.Reason, *resolves)
	}
	st, outcome, err := w.floor.Ensure(context.Background(), p)
	if err != nil || outcome != Installed || st.Disposition != Provisioned {
		t.Fatalf("Ensure: %s %s %v\n%s", outcome, st.Disposition, err, w.out.String())
	}
	if strings.Join(ps.builds, ",") != packTreeDigestOne || *resolves != 1 {
		t.Fatalf("builds %v, resolutions %d; want one build at the digest and one resolution", ps.builds, *resolves)
	}
	rec := st.Record
	if rec.Via != packdecl.ViaPack || rec.Declared != "tools/board-cli" || rec.Revision != packTreeDigestOne ||
		rec.Recipe != p.Install.PackTreeRecipe() || rec.Version != "tree "+packTreeDigestOne[:12] ||
		rec.Capture != ps.byKey[packTreeDigestOne].Key {
		t.Errorf("record = %+v", rec)
	}
	cmd := exec.Command(st.Launcher)
	cmd.Env = []string{}
	if got, err := cmd.CombinedOutput(); err != nil || string(got) != "board-aaaaaaaa\n" {
		t.Fatalf("the floor's board ran %q (%v), want the build at the digest", got, err)
	}
	if !strings.Contains(w.out.String(), "no build of board from pack fx's tools/board-cli at tree digest aaaaaaaaaaaa") {
		t.Errorf("the build is not announced before it runs:\n%s", w.out.String())
	}

	next := w.nextLaunch()
	if _, outcome, err := next.Ensure(context.Background(), p); err != nil || outcome != Current || len(ps.builds) != 1 {
		t.Fatalf("the next launch, nothing changed: %s %v, builds %v", outcome, err, ps.builds)
	}

	digest = packTreeDigestTwo
	next = w.nextLaunch()
	st, outcome, err = next.Ensure(context.Background(), p)
	if err != nil || outcome != Installed || st.Record.Revision != packTreeDigestTwo || len(ps.builds) != 2 {
		t.Fatalf("after an edit: %s %+v %v, builds %v\n%s", outcome, st.Record, err, ps.builds, w.out.String())
	}
	if !strings.Contains(w.out.String(), "pack fx's tools/board-cli changed") {
		t.Errorf("the reinstall does not say the tree changed:\n%s", w.out.String())
	}
	cmd = exec.Command(st.Launcher)
	cmd.Env = []string{}
	if got, _ := cmd.CombinedOutput(); string(got) != "board-bbbbbbbb\n" {
		t.Errorf("after the edit the floor runs %q, want the new build", got)
	}
}

// NEVER SERVED PAST ITS KEY (§10, P5): a rebuild at a new digest that fails takes the older build out
// of bin/, and the error is the act's own (its log and retry).
func TestAFailedPackTreeRebuildDoesNotKeepTheOlderBuild(t *testing.T) {
	digest := packTreeDigestOne
	var fail error
	w, _, _ := packTreeWorld(t, &digest, &fail)
	p := packTreeProgram()
	if _, _, err := w.floor.Ensure(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	digest, fail = packTreeDigestTwo, errors.New("building board failed: exit 3; its log: /x.log — retry: `yolo pack build fx --setup host-self`")
	next := w.nextLaunch()
	_, _, err := next.Ensure(context.Background(), p)
	if err == nil || !strings.Contains(err.Error(), "its log: /x.log") || !strings.Contains(err.Error(), "yolo pack build fx --setup host-self") {
		t.Fatalf("Ensure = %v, want the act's failure with its log and retry", err)
	}
	if _, err := os.Stat(next.Launcher("board")); err == nil {
		t.Error("the older build still serves after its rebuild failed")
	}

	// BG-D21: where the pack declares a prebuilt here, the failed build's refusal names the opt-in too.
	optIn := "or, to run pack fx's prebuilt artifact for linux/amd64 instead of building it, set the opt-in"
	resolve := next.PackTree
	next.PackTree = func(p Program) PackTreeDelivery {
		d := resolve(p)
		d.PrebuiltOptInStep = optIn
		return d
	}
	next.packTrees = nil
	if _, _, err := next.Ensure(context.Background(), p); err == nil || !strings.Contains(err.Error(), "its log: /x.log") ||
		!strings.Contains(err.Error(), "; "+optIn) {
		t.Errorf("Ensure with a prebuilt declared = %v, want the act's failure and then the prebuilt opt-in", err)
	}
}

// A BUILD NEEDS A JAIL: with no runtime and no build in the store, the program has no floor entry and
// the reason ends with the runtime step; the act never runs.
func TestAPackTreeBuildWithNoRuntimeIsNoFloorEntry(t *testing.T) {
	digest := packTreeDigestOne
	w, ps, _ := packTreeWorld(t, &digest, nil)
	w.floor.CaptureUnavailable = func() string { return "no container runtime (podman) is on PATH" }
	_, _, err := w.floor.Ensure(context.Background(), packTreeProgram())
	if !errors.Is(err, ErrNoEntry) || !strings.Contains(err.Error(), "no container runtime") ||
		!strings.Contains(err.Error(), "the next `yolo host` launch builds it") {
		t.Fatalf("Ensure = %v, want no floor entry naming the runtime and its step", err)
	}
	if len(ps.builds) != 0 {
		t.Errorf("the act ran with no runtime: %v", ps.builds)
	}

	// BG-D21: where the pack declares a prebuilt here, the no-runtime refusal names the opt-in too.
	optIn := "or, to run pack fx's prebuilt artifact for linux/amd64 instead of building it, set the opt-in"
	resolve := w.floor.PackTree
	w.floor.PackTree = func(p Program) PackTreeDelivery {
		d := resolve(p)
		d.PrebuiltOptInStep = optIn
		return d
	}
	w.floor.packTrees = nil
	if _, _, err := w.floor.Ensure(context.Background(), packTreeProgram()); !errors.Is(err, ErrNoEntry) ||
		!strings.Contains(err.Error(), "; "+optIn) || len(ps.builds) != 0 {
		t.Errorf("Ensure with no runtime and a prebuilt declared = %v (builds %v), want the opt-in named and no build",
			err, ps.builds)
	}
}

// THE WHICH-BYTES LINE: a prebuilt's disclosure is said on every Ensure while it serves, a hit too.
func TestAPrebuiltPackTreeProgramSaysWhichBytesOnEveryLaunch(t *testing.T) {
	digest := packTreeDigestOne
	w, _, _ := packTreeWorld(t, &digest, nil)
	inner := w.floor.PackTree
	const line = "board: prebuilt artifact from pack fx (no tools/board-cli in the pack) — sha256 aaaaaaaaaaaa… verified, https://example.invalid/board"
	w.floor.PackTree = func(p Program) PackTreeDelivery {
		d := inner(p)
		d.Jail, d.Disclosure = false, line
		return d
	}
	p := packTreeProgram()
	if _, _, err := w.floor.Ensure(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	before := strings.Count(w.out.String(), line)
	if _, outcome, err := w.nextLaunch().Ensure(context.Background(), p); err != nil || outcome != Current {
		t.Fatalf("second launch: %s %v", outcome, err)
	}
	if before != 1 || strings.Count(w.out.String(), line) != 2 {
		t.Errorf("a store hit of the prebuilt did not say which bytes run:\n%s", w.out.String())
	}
}

// A PACK THAT SHIPS NO SOURCE AND NO ARTIFACT for the floor's platform is no floor entry, from the
// declaration alone (a stat, never a digest), naming `from`, the platform and what is declared.
func TestAPackTreeProgramWithNoSourceAndNoPrebuiltIsNoFloorEntry(t *testing.T) {
	digest := packTreeDigestOne
	w, _, resolves := packTreeWorld(t, &digest, nil)
	p := packTreeProgram()
	p.Install.PackRoot = resolvedTemp(t) // a pack with no tools/board-cli
	p.Install.Prebuilt = map[string]packdecl.PrebuiltBuild{"darwin/arm64": {URL: "https://example.invalid/b", SHA256: packTreeDigestTwo}}
	st := w.floor.Status(p)
	for _, want := range []string{"ships no tools/board-cli", w.floor.GOOS + "/" + w.floor.GOARCH, "it declares: darwin/arm64"} {
		if st.Disposition != NoEntry || !strings.Contains(st.Reason, want) {
			t.Errorf("Status = %s (%s), want no floor entry naming %q", st.Disposition, st.Reason, want)
		}
	}
	if *resolves != 0 {
		t.Errorf("the status resolved (digested) the program %d times", *resolves)
	}
}

// THE PLATFORM IS A RECORD'S (install-capture.md#record-platform): the store keys by content alone,
// so a script admitted first for a jail's platform keeps that manifest when this floor's platform
// admits the same bytes. The floor's materialize takes its CapturePlatform from a record beside the
// entry, and without one it still refuses. Drop PlatformRecords from materializeBuild and this fails.
func TestAPackTreeEntryFirstAdmittedForAnotherPlatformInstallsByItsRecord(t *testing.T) {
	const floorPlatform = "plan9/mips" // the entries' manifests name capture.Platform()
	for _, tc := range []struct {
		name    string
		records capture.Records
		wantOK  bool
	}{
		{"a record for the floor's platform", func(string) ([]capture.Record, error) {
			return []capture.Record{{Bin: "board", Platform: capture.Platform()}, {Bin: "board", Platform: floorPlatform}}, nil
		}, true},
		{"no record for it", func(string) ([]capture.Record, error) {
			return []capture.Record{{Bin: "board", Platform: capture.Platform()}}, nil
		}, false},
		{"no reader", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			digest := packTreeDigestOne
			w, _, _ := packTreeWorld(t, &digest, nil)
			w.floor.CapturePlatform = floorPlatform
			w.floor.CaptureRecords = tc.records
			_, outcome, err := w.floor.Ensure(context.Background(), packTreeProgram())
			if tc.wantOK && (err != nil || outcome != Installed) {
				t.Fatalf("Ensure = %s %v, want installed by the record\n%s", outcome, err, w.out.String())
			}
			if !tc.wantOK && (err == nil || !errors.Is(err, capture.ErrWrongPlatform)) {
				t.Fatalf("Ensure = %s %v, want the platform refusal\n%s", outcome, err, w.out.String())
			}
		})
	}
}

// ON A MAC (the plan's Task 10) the floor holds a pack-tree program as on Linux: resolved by digest,
// built through the floor's resolver (the CLI's runs the macos-user sealed act), and installed. Where
// the Mac's build act cannot run now, its own blocker (BuildActUnavailable) is the no-entry reason.
// On any other platform there is still no entry, naming a jail and `host_floor`. No fork path is
// reached on any platform.
func TestAPackTreeProgramOnAMacFloorIsBuiltAndInstalled(t *testing.T) {
	digest := packTreeDigestOne
	w, ps, resolves := packTreeWorld(t, &digest, nil)
	f := *w.floor
	f.GOOS = "darwin"
	p := packTreeProgram()
	if st := f.Status(p); st.Disposition == NoEntry {
		t.Fatalf("Status = %s (%s), want a darwin floor to hold a pack-tree program", st.Disposition, st.Reason)
	}
	st, outcome, err := f.Ensure(context.Background(), p)
	if err != nil || outcome != Installed || st.Disposition != Provisioned || *resolves != 1 ||
		len(ps.builds) != 1 {
		t.Fatalf("Ensure on a Mac floor: %s %s %v, %d resolutions, builds %v\n%s", outcome, st.Disposition, err,
			*resolves, ps.builds, w.out.String())
	}

	blocked := *w.floor
	blocked.GOOS, blocked.packTrees = "darwin", nil
	blocked.BuildActUnavailable = func(string, string) string { return "the sandbox account does not exist" }
	digest = packTreeDigestTwo
	if _, _, err := blocked.Ensure(context.Background(), p); !errors.Is(err, ErrNoEntry) ||
		!strings.Contains(err.Error(), "the sandbox account does not exist") {
		t.Errorf("Ensure with the Mac's act blocked = %v, want no entry naming the act's blocker", err)
	}
	// OQ-BG2: the refusal of a build that cannot run names the prebuilt opt-in the resolver handed it
	// (PrebuiltOptInStep), on the status and on the install — and never takes the prebuilt itself.
	optIn := "or, to run pack fx's prebuilt artifact for darwin/arm64 instead of building it, set the opt-in"
	resolve := blocked.PackTree
	blocked.PackTree = func(p Program) PackTreeDelivery {
		d := resolve(p)
		d.PrebuiltOptInStep = optIn
		return d
	}
	blocked.packTrees = nil
	if _, _, err := blocked.Ensure(context.Background(), p); !errors.Is(err, ErrNoEntry) ||
		!strings.Contains(err.Error(), "the sandbox account does not exist; "+optIn) {
		t.Errorf("Ensure with the Mac's act blocked = %v, want the blocker and then the prebuilt opt-in", err)
	}
	if st := blocked.Status(p); st.Disposition != NoEntry || !strings.Contains(st.Reason, "; "+optIn) {
		t.Errorf("Status with the Mac's act blocked = %s (%s), want no entry naming the opt-in", st.Disposition, st.Reason)
	}

	other := *w.floor
	other.GOOS, other.packTrees = "freebsd", nil
	if st := other.Status(p); st.Disposition != NoEntry || !strings.Contains(st.Reason, "`yolo -- board`") ||
		!strings.Contains(st.Reason, "`host_floor`") || strings.Contains(st.Reason, "pin") {
		t.Fatalf("Status on freebsd = %s (%s), want no floor entry naming a jail and `host_floor`", st.Disposition, st.Reason)
	}
	if via(p.Install) != packdecl.ViaPack {
		t.Errorf("via = %q, want pack", via(p.Install))
	}
	if commit, why := f.forkPin(p); commit != "" || !strings.Contains(why, "own tree") {
		t.Errorf("forkPin = %q, %q; want no commit and the pack-tree reason", commit, why)
	}
	if f.awaitsPin(p) {
		t.Error("awaitsPin says a pack-tree program awaits a fork pin")
	}
}
