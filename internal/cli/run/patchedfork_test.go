package run

// patchedfork_test.go pins the launch's half of a PATCHED fork (docs/design/patched-forks.md §6.3,
// §6.5, §7, PF-D20, PF-D25): the fresh-launch slot hands a patched fork to the build act's advance
// rather than giving it the no-pin reason, and never in a jail; the delivery record each fresh
// launch leaves beside its pack tree, read by a move's reap and gone with the tree; the fork block's
// line naming the series and the good build with no git; an attach's line naming the build its
// running jail was handed; and the interrupt scope a Ctrl-C ends the advance through.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

const (
	patchedBase   = "0123456789abcdef0123456789abcdef01234567"
	patchedSource = "git+file:///nonexistent/yolo-test/tool-upstream?ref=main"
)

// patchedLaunchHome selects a base pack and a PATCHED fork of its program, whose series is one
// format-patch member naming patchedBase, and returns the fork pack's directory.
func patchedLaunchHome(t *testing.T) string {
	t.Helper()
	home := packHome(t)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	packs := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(packs, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("basepack/pack.json", `{"contributes":[{"kind":"program","bin":"tool","via":"npm","package":"tool"}]}`)
	write("forkpack/pack.json", `{"contributes":[{"kind":"program","bin":"tool","via":"source","fork_of":"basepack",`+
		`"source":"`+patchedSource+`","patches":"patches","build":"make install","produces":[".local/bin/tool"]}]}`)
	write("forkpack/patches/0001-ten.patch", "From "+strings.Repeat("a", 40)+" Mon Sep 17 00:00:00 2001\n"+
		"From: t <t@e>\nSubject: [PATCH] ten\n\n---\ndiff --git a/f.txt b/f.txt\n--- a/f.txt\n+++ b/f.txt\n"+
		"@@ -1 +1 @@\n-1\n+ten\n-- \nbase-commit: "+patchedBase+"\n")
	writeUserPacks(t, home, `[{"source":"file://`+filepath.Join(packs, "basepack")+`","name":"basepack"},`+
		`{"source":"file://`+filepath.Join(packs, "forkpack")+`","name":"forkpack"}]`)
	return filepath.Join(packs, "forkpack")
}

// THE ADVANCE'S CALL SITE: a launch carrying a patched fork hands it to the build act — never the
// no-pin reason a plain fork with no pin gets — and the jail gets the act's answer. Red when the
// fresh-launch slot stops calling the build act, or routes a patched fork to its pin's reason.
func TestALaunchHandsAPatchedForkToItsAdvance(t *testing.T) {
	patchedLaunchHome(t)
	var gotPins []packload.ForkPin
	var hand func(string, HandedFork) error
	var handedFile, ws string
	var gotReq ForkBuildRequest
	argv, printed := fakePodmanLaunch(t, func(o *Options) {
		ws = o.Workspace
		o.BuildForks = func(req ForkBuildRequest) map[string]entrypoint.ForkDelivery {
			gotPins, hand, gotReq = req.Pins, req.Hand, req
			// THE LAUNCH'S OWN WRITERS, teed into its launch.log, which a failed build's line names.
			if req.Stdout != nil && req.Stderr != nil {
				fmt.Fprintln(req.Stdout, "advance-stdout-marker")
				fmt.Fprintln(req.Stderr, "advance-stderr-marker")
			}
			if hand != nil {
				if err := hand("tool", HandedFork{Key: "k-patched", Fork: "forkpack/tool", Commit: patchedBase, Patches: 1}); err != nil {
					t.Errorf("Hand: %v", err)
				}
				// Beside the launch's own tree (Run took a copy of these options, so find it by name).
				found, _ := filepath.Glob(filepath.Join(paths.PackTreeRoot(yoloruntime.FromWorkspace(o.Workspace)),
					"*"+handedForksSuffix))
				if len(found) != 1 {
					t.Errorf("Hand wrote %d delivery records beside the pack trees, want 1: %v", len(found), found)
				} else {
					handedFile = found[0]
					if _, err := os.Stat(strings.TrimSuffix(handedFile, handedForksSuffix)); err != nil {
						t.Errorf("the delivery record %s is beside no pack tree: %v", handedFile, err)
					}
				}
			}
			return map[string]entrypoint.ForkDelivery{"tool": {Key: "k-patched"}}
		}
	})
	if len(gotPins) != 1 || !gotPins[0].Fork.Patched() || gotPins[0].Fork.Key() != "forkpack/tool" {
		t.Fatalf("the build act was handed %+v, want the patched fork\n%s", gotPins, printed)
	}
	if hand == nil {
		t.Error("the request carries no Hand, so a move could reap the build this launch hands")
	}
	// THE RUNTIME AND THE WORKSPACE, which the advance's lines name (§9: Apple Container's capture
	// jail; the launch.log a failed build's output is in).
	if gotReq.Runtime != "podman" || gotReq.Workspace != ws {
		t.Errorf("the request names runtime %q and workspace %q, want podman and %s", gotReq.Runtime, gotReq.Workspace, ws)
	}
	logged, err := os.ReadFile(filepath.Join(ws, ".yolo", LaunchLogName))
	if err != nil {
		t.Fatalf("the launch log: %v", err)
	}
	for _, m := range []string{"advance-stdout-marker", "advance-stderr-marker"} {
		if !strings.Contains(string(logged), m) {
			t.Errorf("what the advance writes through the request does not reach the launch log (%s missing):\n%s", m, logged)
		}
	}
	if d := forkBuildsInArgv(t, argv); d["tool"].Key != "k-patched" {
		t.Errorf("the jail is handed %+v, want the advance's key", d)
	}
	// THE LAUNCH'S PINNER SKIPS IT (PF-D16): no fork-lock entry, no lock file at all.
	if _, err := os.Stat(packsrc.ForkLockPath(paths.UserConfigPath())); !os.IsNotExist(err) {
		t.Errorf("the launch wrote the fork lock for a patched fork (err %v)", err)
	}
	// THE RECORD GOES WITH THE TREE: this launch's container never started, so both are gone.
	if handedFile != "" {
		if _, err := os.Stat(handedFile); !os.IsNotExist(err) {
			t.Errorf("the delivery record outlived its discarded pack tree (err %v)", err)
		}
	}
}

// NEVER IN A JAIL: a patched fork is checked, replayed and built on the host (§4.1), so a launch
// inside a jail hands it a reason naming the host, and asks the build act nothing.
func TestALaunchInAJailBuildsNoPatchedFork(t *testing.T) {
	patchedLaunchHome(t)
	t.Setenv("YOLO_VERSION", "9.9.9-test")
	o := goldenOptions("/ws", t.TempDir())
	o.CapturesDir = func() string { return "/store" }
	called := false
	o.BuildForks = func(ForkBuildRequest) map[string]entrypoint.ForkDelivery { called = true; return nil }
	o.forkPinned = []packload.ForkPin{{Fork: packload.Fork{Pack: "forkpack", Base: "basepack", Bin: "tool",
		Patches: "patches"}, Reason: packload.PatchedForkPinReason}}
	got := o.forkDeliveriesFor("podman")
	if called {
		t.Error("a launch inside a jail asked the build act for a patched fork")
	}
	if d := got["tool"]; d.Key != "" || !strings.Contains(d.Reason, "built on the host") {
		t.Errorf("in a jail the patched fork is handed %+v, want a reason naming the host", d)
	}
}

// THE FORK BLOCK'S LINE names the series and the good build, from the record alone — no git — and
// carries the held suffix while a conflict holds the newest candidate back.
func TestTheForkBlockNamesAPatchedForksGoodBuildAndItsHold(t *testing.T) {
	forkDir := patchedLaunchHome(t)
	f := packload.Fork{Pack: "forkpack", Base: "basepack", Bin: "tool", Source: patchedSource, Build: "make install",
		Produces: []string{".local/bin/tool"}, Root: forkDir, Patches: "patches"}
	t.Setenv("PATH", t.TempDir()) // no git on PATH: the line must not need one
	line, warn := patchedForkLine(packload.ForkPin{Fork: f})
	if !strings.Contains(line, "is a patched fork of "+patchedSource+" + 1 patch (series ") ||
		!strings.Contains(line, "no build of it on this machine yet") || !warn {
		t.Errorf("with no good build the line is %q (warn %v)", line, warn)
	}
	series, err := f.ReadSeries()
	if err != nil {
		t.Fatal(err)
	}
	in, _, _, _ := f.CheckWant(series).Inputs()
	recipe := forkPatchedRecipe(f, series)
	newer := strings.Repeat("b", 40)
	store := patchedPacksStore()
	if err := store.WithCheckRecord(f.Key(), nil, func(r *packsrc.CheckRecord, _ error, _ func() error) (bool, error) {
		r.CheckedAt, r.Read = time.Now().Unix(), in
		r.Check = &packsrc.CheckFound{Seq: 1, RefKind: "branch", List: []packsrc.ListEntry{
			{Commit: newer, Tag: "v1.1.0", Version: "1.1.0"}, {Commit: patchedBase, Tag: "v1.0.0", Version: "1.0.0"}}}
		r.Good = &packsrc.GoodBuild{Commit: patchedBase, Tag: "v1.0.0", Version: "1.0.0", Series: series.Digest,
			Recipe: recipe, Patches: 1, Entry: "k1"}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	line, warn = patchedForkLine(packload.ForkPin{Fork: f})
	if !strings.Contains(line, ", at v1.0.0 (01234567)") || warn || strings.Contains(line, "held") {
		t.Errorf("with a good build the line is %q (warn %v)", line, warn)
	}
	if err := store.WithCheckRecord(f.Key(), nil, func(r *packsrc.CheckRecord, _ error, _ func() error) (bool, error) {
		r.SetOutcome(packsrc.EntryOutcome{Commit: newer, Kind: packsrc.OutcomeConflict, Series: series.Digest,
			Member: "0001-ten.patch", Paths: []string{"f.txt"}})
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	line, warn = patchedForkLine(packload.ForkPin{Fork: f})
	if !strings.Contains(line, "held at v1.0.0 (01234567): upstream v1.1.0 (bbbbbbbb) does not take 0001-ten.patch") ||
		!strings.Contains(line, "`yolo pack update` shows the conflict") || !warn {
		t.Errorf("with a conflict recorded the line is %q (warn %v)", line, warn)
	}
}

// forkPatchedRecipe is a patched fork's recipe under its series, as the advance computes it.
func forkPatchedRecipe(f packload.Fork, s *packsrc.Series) string {
	return packdecl.ForkSourcePatchedRecipe(f.Source, f.Build, f.Produces, s.Digest)
}

// AN ATTACH SAYS WHAT ITS JAIL RUNS (§7), from what the jail was handed, and that the next fresh
// launch runs a good build that moved since. Red when the attach stops calling noteAttachForkBuilds.
func TestAnAttachSaysWhichPatchedBuildItsJailRuns(t *testing.T) {
	patchedLaunchHome(t)
	var cname string
	_, printed := fakePodmanLaunch(t, func(o *Options) {
		cname = yoloruntime.FromWorkspace(o.Workspace)
		tree, err := newPackTree(cname)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeLivePackTree(cname, tree); err != nil {
			t.Fatal(err)
		}
		if err := recordHandedFork(tree, "tool", HandedFork{Key: "k-old", Fork: "forkpack/tool", Commit: patchedBase,
			Tag: "v1.0.0", Patches: 1}); err != nil {
			t.Fatal(err)
		}
		if err := patchedPacksStore().WithCheckRecord("forkpack/tool", nil, func(r *packsrc.CheckRecord, _ error, _ func() error) (bool, error) {
			r.Good = &packsrc.GoodBuild{Commit: strings.Repeat("b", 40), Tag: "v1.1.0", Patches: 1, Entry: "k-new"}
			return true, nil
		}); err != nil {
			t.Fatal(err)
		}
		o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
			joined := strings.Join(argv, " ")
			switch {
			case len(argv) >= 2 && argv[1] == "ps" && strings.Contains(joined, "name=^/"+cname+"$"):
				return ExecResult{Ran: true, RC: 0, Stdout: "abc123\n"}
			case len(argv) >= 2 && argv[1] == "inspect":
				return ExecResult{Ran: true, RC: 0, Stdout: "YOLO_VERSION=9.9.9-test\n" + entrypointContractTagsLine() + "\n"}
			}
			return ExecResult{Ran: true, RC: 0}
		}
		o.BuildForks = func(ForkBuildRequest) map[string]entrypoint.ForkDelivery {
			t.Error("an attach ran the advance")
			return nil
		}
	})
	if !strings.Contains(printed, "Attaching to existing jail") {
		t.Fatalf("the fixture did not attach:\n%s", printed)
	}
	want := "this jail runs fork forkpack/tool at v1.0.0 (01234567) + 1 patch; v1.1.0 (bbbbbbbb) + 1 patch is built, " +
		"and the next fresh launch, once this jail stops, runs it"
	if !strings.Contains(printed, want) {
		t.Errorf("the attach does not name the build its jail runs and the one built since:\n%s", printed)
	}
}

// THE DELIVERY RECORDS a move's reap reads: every key a tree's record names, across workspaces; a
// record that cannot be read is an error, so the reaper keeps every build.
func TestHandedForkKeysReadsEveryTreesRecord(t *testing.T) {
	packHome(t)
	for i, cname := range []string{"yolo-a", "yolo-b"} {
		tree, err := newPackTree(cname)
		if err != nil {
			t.Fatal(err)
		}
		if err := recordHandedFork(tree, "tool", HandedFork{Key: []string{"k1", "k2"}[i], Fork: "f/tool"}); err != nil {
			t.Fatal(err)
		}
	}
	keys, err := HandedForkKeys()
	if err != nil || !keys["k1"] || !keys["k2"] || len(keys) != 2 {
		t.Fatalf("handed keys = %v, %v", keys, err)
	}
	if err := os.MkdirAll(paths.PackTreeRoot("yolo-c"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.PackTreeRoot("yolo-c"), "x"+handedForksSuffix), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := HandedForkKeys(); err == nil {
		t.Error("an unreadable delivery record read as none, which would let a move reap a running jail's build")
	}
}

// discardPackTree takes the tree's delivery record with it.
func TestDiscardingAPackTreeTakesItsDeliveryRecord(t *testing.T) {
	packHome(t)
	tree, err := newPackTree("yolo-x")
	if err != nil {
		t.Fatal(err)
	}
	if err := recordHandedFork(tree, "tool", HandedFork{Key: "k"}); err != nil {
		t.Fatal(err)
	}
	discardPackTree("yolo-x", tree)
	if _, err := os.Stat(handedForksPath(tree)); !os.IsNotExist(err) {
		t.Errorf("the delivery record outlived its tree (err %v)", err)
	}
}

// THE INTERRUPT SCOPE (PF-D25): a SIGINT while it runs cancels its work and is returned, and the
// launch's own arm under it never acts; a SIGTERM is raised again for that arm once the scope ends.
func TestTheInterruptScopeEndsItsWorkNotTheLaunch(t *testing.T) {
	exited := make(chan int, 1)
	outer := armLaunchSignalsWith(func() {}, func(code int) { exited <- code })
	defer outer.disarm()
	sig := InterruptScope(func(ctx context.Context) {
		_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
		select {
		case <-ctx.Done():
		case <-time.After(10 * time.Second):
			t.Error("the scope's context was not cancelled by a SIGINT")
		}
	})
	if sig != syscall.SIGINT {
		t.Errorf("the scope returned %v, want the SIGINT", sig)
	}
	select {
	case code := <-exited:
		t.Errorf("the launch's arm acted on the scope's SIGINT (exit %d)", code)
	case <-time.After(200 * time.Millisecond):
	}

	var raised []string
	prev := reraiseSignal
	reraiseSignal = func(s os.Signal) { raised = append(raised, s.String()) }
	t.Cleanup(func() { reraiseSignal = prev })
	InterruptScope(func(ctx context.Context) {
		_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
		<-ctx.Done()
	})
	if len(raised) != 1 || raised[0] != syscall.SIGTERM.String() {
		t.Errorf("a SIGTERM in the scope was raised again as %v, want the SIGTERM once", raised)
	}
}

// BELOW APPLE CONTAINER'S READ-ONLY FLOOR a patched fork is a plain fork's case (§9): no store is
// mounted, so no advance runs and the fork is told why.
func TestNoPatchedForkAdvancesBelowTheAppleContainerFloor(t *testing.T) {
	o := goldenOptions("/ws", t.TempDir())
	o.CapturesDir = func() string { return "/store" }
	called := false
	o.BuildForks = func(ForkBuildRequest) map[string]entrypoint.ForkDelivery { called = true; return nil }
	o.forkPinned = []packload.ForkPin{{Fork: packload.Fork{Pack: "forkpack", Bin: "tool", Patches: "patches"},
		Reason: packload.PatchedForkPinReason}}
	got := o.forkDeliveriesFor("container")
	if called || !strings.Contains(got["tool"].Reason, "mounts no capture store") {
		t.Errorf("below the floor: called %v, answered %+v", called, got)
	}
}

// A DELIVERY RECORD THAT CANNOT BE WRITTEN says what that costs — an attach cannot name the build,
// and nothing else changes, since no move reaps a plain fork's build and a patched fork's advance
// records its own bin — and the step that lets the next launch write it.
func TestADeliveryRecordThatCannotBeWrittenSaysWhatItCosts(t *testing.T) {
	o := goldenOptions("/ws", t.TempDir())
	var errw strings.Builder
	o.Stderr = &errw
	o.packTree = filepath.Join(t.TempDir(), "missing", "tree")
	o.recordHandedForks(map[string]entrypoint.ForkDelivery{"tool": {Key: "k1"}})
	for _, w := range []string{"could not record what this launch hands its jail for tool",
		"an attach to this jail will not say which build it runs", "the jail itself is unaffected",
		"making " + filepath.Dir(o.packTree) + " writable"} {
		if !strings.Contains(errw.String(), w) {
			t.Errorf("the warning lacks %q:\n%s", w, errw.String())
		}
	}
	if strings.Contains(errw.String(), "keeps every build") {
		t.Errorf("the warning claims what a failed write does not do:\n%s", errw.String())
	}
}
