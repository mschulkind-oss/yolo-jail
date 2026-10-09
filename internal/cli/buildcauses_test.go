package cli

// buildcauses_test.go pins how a build jail that refused at its boot is said (PPX-D42): its cause in
// plain words from the boot's record, never the hold offer or a generator's step name, each on a
// line of its own; one refusal of a jail's own config stops every later build sealed the same way,
// each saying it was skipped; and a yolo bug — the seal mounting read-only what a pack writes — is
// said as yolo's, with where to report it, where anything else asks the user to fix what it names.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// The transcript's refusal, as the build jail's pid 1 printed it.
const (
	bootErrorLine   = "Error: configure_pi_automode: mkdir /home/agent/.pi/agent/extensions/pi-automode: read-only file system"
	bootRefusalLine = "yolo-entrypoint: refusing to start the jail: 1 config generator(s) failed:"
	bootItemLine    = "  - configure_pi_automode: mkdir /home/agent/.pi/agent/extensions/pi-automode: read-only file system"
)

// automodeFailure is the transcript's failed generator, as the boot records it.
func automodeFailure() entrypoint.GenFailure {
	return entrypoint.GenFailure{Step: "configure_pi_automode", Pack: "treepack",
		Doing: "writing pi's automode file (~/.pi/agent/extensions/pi-automode/settings.json)",
		Path:  "~/.pi/agent/extensions/pi-automode", ReadOnly: true, InWritableDir: true,
		Error: "mkdir /home/agent/.pi/agent/extensions/pi-automode: read-only file system"}
}

// bootRefusingJail is a build jail whose boot refused as the transcript's did: the record beside
// its boot.log (none when record is false, an entrypoint older than the record), and pid 1's lines,
// the hold offer last but one, on the jail's own stderr. It counts its runs.
func bootRefusingJail(t *testing.T, runs *int, record bool, failures ...entrypoint.GenFailure) func(run.Options) int {
	return func(o run.Options) int {
		*runs++
		if record {
			data, err := json.Marshal(entrypoint.BootRefusal{Failures: failures})
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(o.Workspace, ".yolo", entrypoint.BootRefusalName), string(data))
		}
		js := jailStderr(o)
		fmt.Fprintln(js, bootErrorLine)
		fmt.Fprintln(js, "  cgroup delegate: not available (no host daemon socket)")
		fmt.Fprintln(js, "\nyolo-entrypoint: this container is about to be removed WITH the failed state inside it (--rm).")
		fmt.Fprintln(js, "  Re-run with YOLO_HOLD_ON_REFUSAL=1 to hold it open instead and `exec` in to look around.")
		fmt.Fprintln(js, bootRefusalLine)
		fmt.Fprintln(js, bootItemLine)
		return 1
	}
}

// THE CAUSE IN PLAIN WORDS (1): the boot's record names the pack and the file, each line its own,
// and nothing the launch is handed quotes the hold offer, a step's internal name or a joined line.
// Red with the record's read (buildForkUnderLock's ReadBootRefusal) deleted.
func TestABuildJailsBootRefusalIsRelayedInPlainWords(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	runs := 0
	withFakeCaptureJail(t, bootRefusingJail(t, &runs, true, automodeFailure()))
	d, out := fx.deliver(t, true)
	want := []string{"writing pi's automode file (~/.pi/agent/extensions/pi-automode/settings.json), which pack treepack declares, failed:",
		"  ~/.pi/agent/extensions/pi-automode is mounted read-only in that jail"}
	if d.Cause == nil || !slices.Equal(d.Cause.Lines, want) || !d.Cause.YoloBug || !slices.Equal(d.Cause.Packs, []string{"treepack"}) {
		t.Fatalf("the launch is handed the cause %+v, want %q, a yolo bug of a jail sealed to treepack\n%s", d.Cause, want, out)
	}
	all := out + d.Reason + strings.Join(d.Cause.Lines, "\n")
	for _, gone := range []string{"HOLD_ON_REFUSAL", "configure_pi_automode", " / ", "cgroup delegate"} {
		if strings.Contains(all, gone) {
			t.Errorf("what is said quotes %q:\n%s", gone, all)
		}
	}
	if !strings.Contains(out, "Building extension "+treeKeyCLI+": its build jail refused to start (") {
		t.Errorf("the build's result does not say its jail refused:\n%s", out)
	}
}

// WITH NO RECORD (an entrypoint older than it), the jail's own last lines are relayed, the hold
// offer left out. Red with jailTail's hold-offer filter deleted.
func TestABuildJailsRefusalWithNoRecordLeavesOutTheHoldOffer(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	runs := 0
	withFakeCaptureJail(t, bootRefusingJail(t, &runs, false))
	d, out := fx.deliver(t, true)
	want := []string{"cgroup delegate: not available (no host daemon socket)", bootRefusalLine, strings.TrimSpace(bootItemLine)}
	if d.Cause == nil || !slices.Equal(d.Cause.Lines, want) || d.Cause.YoloBug {
		t.Errorf("the launch is handed %+v, want the jail's last lines %q, not yolo's fault\n%s", d.Cause, want, out)
	}
}

// threeTreeFixture is newTreeFixture with two more patched extensions of the same pack and upstream,
// so every build jail is sealed to treepack alone.
func threeTreeFixture(t *testing.T) (*treeFixture, []packload.Fork) {
	t.Helper()
	fx := newTreeFixture(t, `"f.txt"`)
	var trees []string
	for _, name := range []string{"tool-ext", "two-ext", "three-ext"} {
		trees = append(trees, `{"kind":"files","into":".tool/ext/`+name+`","source":"git+file://`+fx.repo+
			`?ref=main","patches":"patches","build":"true","produces":["f.txt"]}`)
	}
	writeFile(t, filepath.Join(fx.treeDir, "pack.json"), `{"name":"treepack","contributes":[`+strings.Join(trees, ",")+`]}`)
	all := packload.PatchedTrees(selectConfiguredHostPacks().packs)
	if len(all) != 3 {
		t.Fatalf("the fixture carries %d patched extensions, want 3", len(all))
	}
	return fx, all
}

// deliverAll runs one launch's tree arm over trees.
func deliverAll(t *testing.T, trees []packload.Fork) (map[string]run.TreeDelivery, string) {
	t.Helper()
	return deliverAllOn(t, trees, "podman")
}

// deliverAllOn is deliverAll on runtime rt.
func deliverAllOn(t *testing.T, trees []packload.Fork, rt string) (map[string]run.TreeDelivery, string) {
	t.Helper()
	var out, errw syncBuffer
	req := run.TreeBuildRequest{Trees: trees, Platform: patchedTestPlatform, Runtime: rt, Workspace: "/ws", Build: true,
		CopyRoot: filepath.Join(t.TempDir(), "tree.patched")}
	return deliverTreesForLaunch(req, &out, &errw, false), out.String() + errw.String()
}

// deliverOneAtATime is deliverAll in a pool of one build slot, as on Apple Container, whose keys
// therefore build in turn.
func deliverOneAtATime(t *testing.T, trees []packload.Fork) (map[string]run.TreeDelivery, string) {
	t.Helper()
	var errw syncBuffer
	req := run.TreeBuildRequest{Trees: trees, Platform: patchedTestPlatform, Runtime: "podman", Workspace: "/ws", Build: true,
		CopyRoot: filepath.Join(t.TempDir(), "tree.patched")}
	_, got := runBuildSlot(run.BuildSlotRequest{Trees: &req, MaxBuilds: 1}, &errw, false)
	return got, errw.String()
}

// ONE CAUSE, ONCE (2): a build jail whose own config was refused stops every build sealed the same
// way that takes a build slot after it from starting, each saying it was skipped and why, and every
// one is handed the first's cause, so the launch says it once. In a pool of one slot that is every
// later key. Red with the advance's refusedSeal check deleted.
func TestABuildJailsConfigRefusalSkipsEveryBuildSealedTheSameWay(t *testing.T) {
	_, trees := threeTreeFixture(t)
	runs := 0
	withFakeCaptureJail(t, bootRefusingJail(t, &runs, true, automodeFailure()))
	got, out := deliverOneAtATime(t, trees)
	if runs != 1 {
		t.Errorf("%d build jails ran, want the first alone:\n%s", runs, out)
	}
	// The key whose check ends first takes the one slot and is the one that runs.
	var first string
	for _, f := range trees {
		if strings.Contains(out, "Building "+f.Label()+": its build jail refused to start") {
			first = f.Label()
		}
	}
	for _, f := range trees {
		if f.Label() == first {
			continue
		}
		want := "skipped " + f.Label() + ": not started — its build jail is sealed to pack treepack, as " + first +
			"'s was, and would refuse to start the same way"
		if first == "" || !strings.Contains(out, want) {
			t.Errorf("the act does not say %s was skipped and why (%q):\n%s", f.Label(), want, out)
		}
	}
	for _, f := range trees {
		if d := got[f.Key()]; d.Dir != "" || !d.Cause.Same(got[trees[0].Key()].Cause) || d.Cause == nil {
			t.Errorf("%s is handed %+v, want no build and the first's cause", f.Key(), d)
		}
	}
	if n := strings.Count(out, "read-only"); n != 0 {
		t.Errorf("the act said the cause %d times, which the launch says once:\n%s", n, out)
	}
}

// A REFUSAL AT THE TREE'S OWN DIRECTORY depends on the tree, so the next build is started. Red if
// configRefused stops reading the failures' paths.
func TestARefusalAtTheTreesOwnDirectorySkipsNothing(t *testing.T) {
	_, trees := threeTreeFixture(t)
	runs := 0
	withFakeCaptureJail(t, func(o run.Options) int {
		own := automodeFailure()
		own.Path = "~/.tool/ext/" + o.SealedTree + "/settings.json" // each build's own directory
		return bootRefusingJail(t, &runs, true, own)(o)
	})
	if _, out := deliverAll(t, trees); runs != 3 || strings.Contains(out, "skipped ") {
		t.Errorf("%d build jails ran, want all three, none skipped:\n%s", runs, out)
	}
}

// WHO CAN FIX IT (3): a `yolo capture` of a build whose jail mounted read-only what a pack writes
// says it is yolo's bug, not the pack's, and where to report it; a cause the user can fix keeps
// "Fix what it names", as does a read-only path no selected pack makes writable, which the user's
// own jail refuses too. Red with buildCause's yolo-bug test deleted, or its writable-dir test.
func TestWhoCanFixABuildJailsRefusal(t *testing.T) {
	for _, tc := range []struct {
		failure entrypoint.GenFailure
		bug     bool
	}{
		{automodeFailure(), true},
		// A READ-ONLY PATH NO PACK MAKES WRITABLE is refused in the user's own jail too: the pack's.
		{func() entrypoint.GenFailure { g := automodeFailure(); g.InWritableDir = false; return g }(), false},
		{entrypoint.GenFailure{Step: "pack_treepack_surfaces", Pack: "treepack", Doing: "reading the config files",
			Error: "pack treepack: surface pi/automode: codec \"yaml\" is not one of json, toml, lines, raw"}, false},
	} {
		newTreeFixture(t, `"f.txt"`)
		runs := 0
		withFakeCaptureJail(t, bootRefusingJail(t, &runs, true, tc.failure))
		var out, errw bytes.Buffer
		if rc, handled := captureTree(treeKeyCLI, &out, &errw, false); !handled || rc == 0 {
			t.Fatalf("captureTree = %d, %v\n%s%s", rc, handled, out.String(), errw.String())
		}
		said := errw.String()
		bug := strings.Contains(said, "This is a bug in yolo, not in pack treepack: the build jail refused a config your own "+
			"launch accepts.") && strings.Contains(said, "Report it at "+entrypoint.IssuesURL)
		fix := strings.Contains(said, "Fix what it names, then `yolo capture "+treeKeyCLI+"` builds it")
		if bug != tc.bug || fix == tc.bug {
			t.Errorf("%s: said as yolo's bug %v and as the user's to fix %v, want the bug %v:\n%s", tc.failure.Step, bug,
				fix, tc.bug, said)
		}
		for _, l := range tc.failure.Lines() {
			if !strings.Contains(said, "\n    "+l+"\n") {
				t.Errorf("%s: the cause's line %q is not on a line of its own:\n%s", tc.failure.Step, l, said)
			}
		}
	}
}

// A NEWER BUILD'S REFUSAL WHILE THE GOOD BUILD SERVES stays a warning (PPX-D40's reading): every
// extension still runs its good build, and the cause is said once for all of them when the act's
// builds are done. Red with deliverTreesForLaunch's flush deleted.
func TestANewerBuildsRefusalWhileTheGoodBuildServesIsSaidOnce(t *testing.T) {
	fx, trees := threeTreeFixture(t)
	if got, out := deliverAll(t, trees); len(got) != 3 || got[trees[2].Key()].Dir == "" {
		t.Fatalf("the first launch delivered %+v\n%s", got, out)
	}
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	fx.now = fx.now.Add(2 * time.Hour)
	runs := 0
	withFakeCaptureJail(t, bootRefusingJail(t, &runs, true, automodeFailure()))
	got, out := deliverOneAtATime(t, trees)
	for _, f := range trees {
		if got[f.Key()].Dir == "" {
			t.Errorf("%s lost its good build to a newer build's refusal: %+v", f.Key(), got[f.Key()])
		}
	}
	if n := strings.Count(out, "is mounted read-only in that jail"); runs != 1 || n != 1 {
		t.Errorf("%d build jails ran and the cause was said %d times, want one of each:\n%s", runs, n, out)
	}
	want := "⚠ extension treepack/tool-ext, extension treepack/two-ext and extension treepack/three-ext: a newer build's " +
		"jail refused to start, so each is still running its good build"
	if !strings.Contains(out, want) || !strings.Contains(out, "This is a bug in yolo, not in pack treepack") {
		t.Errorf("the held builds are not said once, with whose bug it is (%q):\n%s", want, out)
	}
}

// ON APPLE CONTAINER a newer build held at its good build because its jail did not start names that
// runtime's limit (PF-D21), the likeliest cause there, with the capture that builds it once the other
// jails stop; on podman it does not. Red with flush leaving out the runtime's line.
func TestAHeldBuildOnAppleContainerNamesItsLimit(t *testing.T) {
	for _, rt := range []string{"container", "podman"} {
		fx, trees := threeTreeFixture(t)
		trees = trees[:1]
		if got, out := deliverAllOn(t, trees, rt); got[trees[0].Key()].Dir == "" {
			t.Fatalf("%s: the first launch delivered %+v\n%s", rt, got, out)
		}
		fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
		fx.now = fx.now.Add(2 * time.Hour)
		runs := 0
		withFakeCaptureJail(t, bootRefusingJail(t, &runs, true, automodeFailure()))
		got, out := deliverAllOn(t, trees, rt)
		if got[trees[0].Key()].Dir == "" {
			t.Fatalf("%s: the held build lost its good build: %+v\n%s", rt, got, out)
		}
		want := "On Apple Container a build jail cannot start beside a running jail: if that is what stopped it, " +
			"`yolo capture " + trees[0].CaptureArg() + "` builds it once the other jails stop."
		if said := strings.Contains(out, want); said != (rt == "container") {
			t.Errorf("%s: Apple Container's limit said %v (%q):\n%s", rt, said, want, out)
		}
	}
}

// A BUILD THAT LEAVES NOTHING SERVING AT A JAIL LAUNCH IS SAID ONCE: a failed first build leaves its
// reason to the launch's refusal (internal/cli/run's missingbuilds.go), which says it in full, so the
// act prints no line of its own for it. A series the held version does not take is a patch
// application failure, which since PF-D81 the act says itself, once, in its prominent error with the
// rebase as its repair, before the launch's refusal, and hands the launch the typed failure as said.
// Red with buildFailedLines' or noFit's warning printed at a jail launch with nothing serving, or with
// the patch failure's error block missing or repeated.
func TestABuildThatLeavesNothingAtALaunchIsSaidOnceByTheLaunch(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx := newPatchedAdvanceFixture(t, "")
	fx.rc = 2
	r, term := fx.launchReported(t)
	if r.delivery.Key != "" || !r.delivery.Unsaid || !strings.Contains(r.delivery.Reason, "failed on the host") {
		t.Fatalf("the failed first build handed %+v, want its reason left to the launch\n%s", r.delivery, term)
	}
	if strings.Contains(term, ": the build of ") {
		t.Errorf("the act said the failed build's cause, which the launch's refusal says:\n%s", term)
	}

	fx = newPatchedAdvanceFixture(t, "")
	v12 := fx.commit(t, "v1.2.0", map[int]string{11: "eleven"})
	fx.writeManifest(t, "v1.2.0", "")
	r, term = fx.launchReported(t)
	if r.delivery.Key != "" || r.delivery.Unsaid || r.delivery.PatchFailure == nil ||
		r.delivery.PatchFailure.Target.Commit != v12 || len(fx.builds) != 0 {
		t.Fatalf("the hold no version fits handed %+v after %d builds, want no build and the typed failure, said by the act\n%s",
			r.delivery, len(fx.builds), term)
	}
	for _, w := range []string{"ERROR: forkpack/tool: patch application failed at upstream v1.2.0 (" + v12 + ")\n",
		"  Repair: yolo pack rebase forkpack/tool --onto " + v12 + "\n", "  Bypass: YOLO_ALLOW_PATCH_FAILURES=1 yolo\n"} {
		if n := strings.Count(term, w); n != 1 {
			t.Errorf("the act said %q %d times, want once:\n%s", w, n, term)
		}
	}
	if strings.Contains(term, "nothing to build —") {
		t.Errorf("the act said there is nothing to build, which the launch's refusal says:\n%s", term)
	}
}

// ONE CAUSE, ONCE, IN A POOL THAT BUILDS AT ONCE: keys sealed the same way whose build jails start
// together each meet the refusal, since none has refused when the others start. Each says only its
// one result line — no cause, no "skipped" — and each is handed the same cause, which the launch's
// refusal groups and says once (run's missingbuilds.go). Red with the act printing a not-started
// build's cause at a jail launch, or with the causes stopping being Same.
func TestBuildsSealedAlikeThatStartTogetherAreEachHandedTheOneCause(t *testing.T) {
	_, trees := threeTreeFixture(t)
	runs := 0
	withFakeCaptureJail(t, bootRefusingJail(t, &runs, true, automodeFailure()))
	got, out := deliverAll(t, trees) // run.SlotBuildJails slots: 3 at once on any host with 6 CPUs or more
	for _, f := range trees {
		d := got[f.Key()]
		if d.Dir != "" || d.Cause == nil || !d.Cause.Same(got[trees[0].Key()].Cause) {
			t.Errorf("%s is handed %+v, want no build and the one cause", f.Key(), d)
		}
		// One line each: its result, or — a key whose check ended after a sibling's refusal was
		// known, as on a loaded machine — its skip.
		if n := strings.Count(out, "Building "+f.Label()+": ") + strings.Count(out, "skipped "+f.Label()+": "); n != 1 {
			t.Errorf("%s has %d result or skip lines, want one:\n%s", f.Label(), n, out)
		}
	}
	if strings.Contains(out, "read-only") {
		t.Errorf("the act said the cause, which the launch says once:\n%s", out)
	}
	if runs+strings.Count(out, "skipped ") != len(trees) {
		t.Errorf("%d build jails ran and %d were skipped, want every key one or the other:\n%s", runs,
			strings.Count(out, "skipped "), out)
	}
}
