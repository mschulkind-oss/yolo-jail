package macosuser

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
)

// capturestore_test.go pins the macos-user half of install-capture.md hand-off H4, answered (b):
// the launch stages a ROOT-OWNED COPY of each selected install-capture entry under the state dir,
// and names that store to the bootstrap, which bakes it into every generated launcher.
//
// The plan is a pure value, so everything that DECIDES what a Mac will run is checked here. The
// stage commands are also RUN, against temp dirs standing in for the state dir and the user's
// store, because their behaviour — copy once, never a link, prune what the store no longer
// selects — lives in two short shell scripts, and a test of their words alone would pass for a
// script that does none of it. Not as root, so ownership is the one property this cannot see:
// root runs them on a Mac, which is what makes every staged byte root's.

const (
	// captureKeyA and its kin are store keys' shape: lowercase hex (internal/capture's Key).
	captureKeyA    = "0123456789abcdef"
	captureKeyKept = "1111111111111111"
	captureKeyOld  = "2222222222222222"
	captureSource  = "/Users/matt/.local/share/yolo-jail/captures/entries/" + captureKeyA
)

// stagedCaptureCtx is a host context carrying one picked entry and one kept key.
func stagedCaptureCtx() HostContext {
	return HostContext{
		Captures:     []CaptureEntry{{Bin: "probetool", Key: captureKeyA, Source: captureSource}},
		CapturesKept: []string{captureKeyKept},
	}
}

// The end-to-end shape at the plan level: a launch handed an entry stages it, under the state dir,
// and tells the bootstrap where — the one variable entrypoint's capturesDir bakes into a launcher.
func TestRunPlanStagesTheSelectedCaptureAndNamesTheStore(t *testing.T) {
	plan := planWithCtx(t, stagedCaptureCtx())

	root := StagedCapturesRoot("")
	if plan.CapturesDir != root || root != stateDir+"/"+capturesLeaf {
		t.Fatalf("plan.CapturesDir = %q, want %q under the state dir", plan.CapturesDir, root)
	}
	if !containsArg(plan.BootstrapArgv, entrypoint.CapturesDirEnv+"="+root) {
		t.Errorf("the bootstrap is not told the staged store (%s=%s), so every launcher bakes an "+
			"empty one and downloads: %v", entrypoint.CapturesDirEnv, root, plan.BootstrapArgv)
	}
	if !containsCommand(plan.CaptureStageCommands, stageCaptureArgv(root, stagedCaptureCtx().Captures[0])) {
		t.Errorf("nothing stages the picked entry: %v", plan.CaptureStageCommands)
	}
	// Never among the stage commands a launch refuses without: the store is optional.
	for _, c := range plan.StageCommands {
		if strings.Contains(strings.Join(c, " "), root) {
			t.Errorf("a capture-store command is among the fatal stage commands: %v", c)
		}
	}
	if len(plan.Captures) != 1 || plan.Captures[0].Key != captureKeyA {
		t.Errorf("plan.Captures = %+v, want the one entry the host context carried", plan.Captures)
	}
	if problems := captureStoreInvariants(plan); len(problems) != 0 {
		t.Errorf("a plan that stages its capture fails its own invariants: %v", problems)
	}
	// The ENV FILE does not carry it: the launcher bakes the store at generation time, and the
	// agent's own environment has no reader for it.
	if strings.Contains(plan.EnvFileContent, entrypoint.CapturesDirEnv) {
		t.Errorf("the session env file names the capture store; only the bootstrap reads it")
	}
}

// A launch handed no entry names no store and runs no capture command: an absent variable is how
// a launcher learns there is nothing to materialize, and it then downloads as before H4.
func TestRunPlanWithNoCapturesNamesNoStore(t *testing.T) {
	plan := planWithCtx(t, HostContext{CapturesKept: []string{captureKeyKept}})
	if _, named := argvEnvValue(plan.BootstrapArgv, entrypoint.CapturesDirEnv); named {
		t.Errorf("a launch that staged no capture names a store to the bootstrap: %v", plan.BootstrapArgv)
	}
	if plan.CapturesDir != "" || len(plan.Captures) != 0 {
		t.Errorf("plan.CapturesDir = %q, Captures = %v, want none", plan.CapturesDir, plan.Captures)
	}
	for _, c := range append(append([][]string(nil), plan.StageCommands...), plan.CaptureStageCommands...) {
		if strings.Contains(strings.Join(c, " "), StagedCapturesRoot("")) {
			t.Errorf("a launch that stages no capture touches the staged store: %v", c)
		}
	}
	if got := StageCaptureCommands(nil, []string{captureKeyKept}, ""); got != nil {
		t.Errorf("StageCaptureCommands with no entries = %v, want none", got)
	}
}

// An install capture's own bootstrap is never told a store, or the launcher it runs as the
// installer would materialize the previous capture and file it as a fresh install.
func TestACapturePlanNamesNoCaptureStore(t *testing.T) {
	plan := BuildCapturePlan(testCaptureOptions())
	if _, named := argvEnvValue(plan.BootstrapArgv, entrypoint.CapturesDirEnv); named {
		t.Errorf("the capture's bootstrap names a capture store, so its installer could "+
			"materialize instead of installing: %v", plan.BootstrapArgv)
	}
}

// A key or a source a root script may not be handed is dropped, not spliced into a path root
// writes under; dropping the only entry leaves a plan that names no store.
func TestStageCaptureCommandsDropWhatARootScriptMayNotBeHanded(t *testing.T) {
	bad := []CaptureEntry{
		{Bin: "a", Key: "../../etc", Source: captureSource},
		{Bin: "b", Key: "ABCDEF0123456789", Source: captureSource},
		{Bin: "c", Key: captureKeyA, Source: "relative/entries/" + captureKeyA},
	}
	if got := StageCaptureCommands(bad, nil, ""); got != nil {
		t.Errorf("StageCaptureCommands staged entries a root script may not be handed: %v", got)
	}
	plan := planWithCtx(t, HostContext{Captures: bad})
	if plan.CapturesDir != "" {
		t.Errorf("a plan whose every entry was dropped still names a store: %q", plan.CapturesDir)
	}
}

// PlanInvariants refuses each way the store could be named wrongly. Each case starts from a
// clean plan and changes one thing, so a refusal is about that thing.
func TestPlanInvariantsRefuseAWrongCaptureStore(t *testing.T) {
	withStore := func(t *testing.T, v string) RunPlan {
		plan := planWithCtx(t, stagedCaptureCtx())
		argv := append([]string(nil), plan.BootstrapArgv...)
		for i, a := range argv {
			if strings.HasPrefix(a, entrypoint.CapturesDirEnv+"=") {
				argv[i] = entrypoint.CapturesDirEnv + "=" + v
			}
		}
		plan.BootstrapArgv = argv
		return plan
	}
	cases := []struct {
		name string
		plan func(t *testing.T) RunPlan
		want string
	}{
		{"outside the state dir", func(t *testing.T) RunPlan { return withStore(t, "/private/tmp/captures") },
			"not under the root-owned state dir"},
		{"under /Users", func(t *testing.T) RunPlan { return withStore(t, CaptureRootDefault()) },
			"is under /Users"},
		{"under a firmlinked /Users", func(t *testing.T) RunPlan {
			return withStore(t, "/System/Volumes/Data/Users/matt/.local/share/yolo-jail/captures")
		}, "is under /Users"},
		{"not staged", func(t *testing.T) RunPlan {
			plan := planWithCtx(t, stagedCaptureCtx())
			var kept [][]string
			for _, c := range plan.CaptureStageCommands {
				if !containsArg(c, stageCaptureScriptName) {
					kept = append(kept, c)
				}
			}
			plan.CaptureStageCommands = kept
			return plan
		}, "nothing stages the capture of probetool"},
		{"copied by a fatal stage command", func(t *testing.T) RunPlan {
			plan := planWithCtx(t, stagedCaptureCtx())
			plan.StageCommands = append(plan.StageCommands, plan.CaptureStageCommands...)
			return plan
		}, "a capture-store script is among the stage commands a launch refuses without"},
		{"named with nothing staged", func(t *testing.T) RunPlan {
			plan := planWithCtx(t, stagedCaptureCtx())
			plan.Captures = nil
			return plan
		}, "stages no install capture"},
		{"staged and not named", func(t *testing.T) RunPlan {
			plan := planWithCtx(t, stagedCaptureCtx())
			var argv []string
			for _, a := range plan.BootstrapArgv {
				if !strings.HasPrefix(a, entrypoint.CapturesDirEnv+"=") {
					argv = append(argv, a)
				}
			}
			plan.BootstrapArgv = argv
			return plan
		}, "is not baked into the bootstrap env"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			problems := PlanInvariants(c.plan(t))
			if !strings.Contains(strings.Join(problems, "\n"), c.want) {
				t.Errorf("PlanInvariants did not refuse with %q:\n%s", c.want, strings.Join(problems, "\n"))
			}
		})
	}
}

// THE SESSION PROFILE IS UNCHANGED, which is the property (b) was chosen for: the staged store is
// under /var/yolo-jail, which the profile's `(allow default)` already lets the sandbox read, so
// staging it adds no rule — not a read allow for the user's store under /Users, which (a) would.
func TestTheSessionProfileIsTheSameWithAndWithoutCaptures(t *testing.T) {
	without := planWithCtx(t, HostContext{})
	with := planWithCtx(t, stagedCaptureCtx())
	if with.Seatbelt != without.Seatbelt {
		t.Errorf("staging a capture changed the session Seatbelt profile; (b) changes no byte of it")
	}
	if strings.Contains(with.Seatbelt, capturesLeaf) || strings.Contains(with.Seatbelt, "yolo-jail/captures") {
		t.Errorf("the session profile names the capture store")
	}
}

// The dry run says which store the launchers read and what is in it, and says so when it is
// nothing, so "nothing captured yet" and "this backend cannot materialize" stay distinguishable.
func TestPrintPlanNamesTheStagedCaptures(t *testing.T) {
	var buf bytes.Buffer
	PrintPlan(&buf, planWithCtx(t, stagedCaptureCtx()), nil)
	line := lineWith(buf.String(), "captures:")
	for _, want := range []string{StagedCapturesRoot(""), "probetool " + captureKeyA, "root-owned"} {
		if !strings.Contains(line, want) {
			t.Errorf("the dry run's captures line does not name %q:\n%s", want, line)
		}
	}
	// Its copies are listed with the privileged commands, marked as the launch runs them: a
	// failure of one warns and the launch goes on.
	if line := lineWith(buf.String(), stageCaptureScriptName); !strings.Contains(line, "(best-effort)") {
		t.Errorf("the dry run does not list the capture copy as best-effort: %q", line)
	}
	buf.Reset()
	PrintPlan(&buf, planWithCtx(t, HostContext{}), nil)
	if line := lineWith(buf.String(), "captures:"); !strings.Contains(line, "none staged") {
		t.Errorf("a dry run that stages no capture does not say so: %q", line)
	}
}

// lineWith returns the first line of s containing sub, or "".
func lineWith(s, sub string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, sub) {
			return l
		}
	}
	return ""
}

// The commands' WORDS: a copy (`cp -R`) renamed into place, readable by every account and
// writable by none but its owner, and no link of the store's inodes anywhere — a hardlink of an
// entry a capture made would hand the sandbox account bytes it owns, which every workspace runs.
func TestStageCaptureCommandsCopyAndNeverLink(t *testing.T) {
	cmds := StageCaptureCommands(stagedCaptureCtx().Captures, nil, "")
	joined := ""
	for _, c := range cmds {
		joined += strings.Join(c, " ") + "\n"
	}
	for _, want := range []string{cpBin + ` -R "$3" "$tmp"`, chmodBin + ` -R a+rX,go-w "$tmp"`,
		mvBin + ` "$tmp" "$dst"`, `tmp="$1/` + capturesStagingLeaf + `/$2"`} {
		if !strings.Contains(stageCaptureScript, want) {
			t.Errorf("the stage script does not %q:\n%s", want, stageCaptureScript)
		}
	}
	for _, bad := range []string{"/bin/ln", " ln ", "pax", "cp -l", "cp -al", "--link", "ditto"} {
		if strings.Contains(stageCaptureScript, bad) || strings.Contains(pruneCapturesScript, bad) {
			t.Errorf("a capture stage script uses %q, which would link the store's inodes or, run "+
				"by root, keep the sandbox account as their owner", bad)
		}
	}
	// The copy lands under staging/ and only the rename reaches entries/, so no reader scanning
	// entries/ meets a half-copied entry.
	if strings.Contains(stageCaptureScript, `entries/$2.new`) {
		t.Errorf("the stage script copies beside the entries, where a reader would scan the copy")
	}
	if !strings.Contains(joined, StagedCapturesRoot("")) {
		t.Errorf("the commands do not stage into %s:\n%s", StagedCapturesRoot(""), joined)
	}
}

// THE COMMANDS RUN, as the invoking user rather than root, against temp dirs: the picked entry
// is COPIED (its files are not the source's inodes), its links copied as links, group and other
// write bits gone; an entry already staged is not copied again; a staged entry the store no longer
// selects is removed, a kept one stays, and a copy a killed launch left under staging/ goes.
func TestStageCaptureCommandsCopyOncePruneAndNeverLink(t *testing.T) {
	for _, bin := range []string{"/bin/sh", cpBin, chmodBin, mvBin, rmBin, mkdirBin} {
		if _, err := os.Stat(bin); err != nil {
			t.Skipf("%s is not on this machine, so the stage commands cannot run here: %v", bin, err)
		}
	}
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sd := filepath.Join(tmp, "state")
	src := filepath.Join(tmp, "userstore", "entries", captureKeyA)
	bin := filepath.Join(src, "tree", ".local", "bin")
	mustMkdir(t, bin, 0o775)
	mustWrite(t, filepath.Join(bin, "probetool"), "#!/bin/sh\necho probe\n", 0o555)
	mustWrite(t, filepath.Join(bin, "loose"), "group-writable", 0o664)
	mustWrite(t, filepath.Join(src, ".yolo-capture-complete"), "", 0o444)
	if err := os.Symlink("/nonexistent/versions/1.0", filepath.Join(bin, "link")); err != nil {
		t.Fatal(err)
	}
	root := StagedCapturesRoot(sd)
	mustMkdir(t, filepath.Join(root, capturesEntriesLeaf, captureKeyOld, "tree"), 0o755)
	mustMkdir(t, filepath.Join(root, capturesEntriesLeaf, captureKeyKept, "tree"), 0o755)
	mustMkdir(t, filepath.Join(root, capturesStagingLeaf, captureKeyOld), 0o755)

	cmds := StageCaptureCommands([]CaptureEntry{{Bin: "probetool", Key: captureKeyA, Source: src}},
		[]string{captureKeyKept}, sd)
	runAll := func() {
		t.Helper()
		for _, c := range cmds {
			if out, err := exec.Command(c[0], c[1:]...).CombinedOutput(); err != nil {
				t.Fatalf("%v: %v\n%s", c, err, out)
			}
		}
	}
	runAll()

	dst := filepath.Join(root, capturesEntriesLeaf, captureKeyA)
	staged := filepath.Join(dst, "tree", ".local", "bin", "probetool")
	body, err := os.ReadFile(staged)
	if err != nil || string(body) != "#!/bin/sh\necho probe\n" {
		t.Fatalf("the entry was not copied whole (%v): %q", err, body)
	}
	srcInfo, _ := os.Stat(filepath.Join(bin, "probetool"))
	dstInfo, _ := os.Stat(staged)
	if os.SameFile(srcInfo, dstInfo) {
		t.Errorf("the staged file IS the user store's inode — a link, not a copy")
	}
	if dstInfo.Mode().Perm()&0o022 != 0 {
		t.Errorf("staged file mode %v leaves group or other write", dstInfo.Mode().Perm())
	}
	if fi, err := os.Stat(filepath.Join(dst, "tree", ".local", "bin", "loose")); err != nil || fi.Mode().Perm()&0o022 != 0 {
		t.Errorf("a group-writable source file is staged %v (%v), want no group or other write", fi.Mode(), err)
	}
	if fi, err := os.Stat(filepath.Join(dst, "tree", ".local", "bin")); err != nil || fi.Mode().Perm()&0o022 != 0 ||
		fi.Mode().Perm()&0o005 != 0o005 {
		t.Errorf("a staged directory is %v (%v), want readable and searchable by all, writable by none but its owner",
			fi.Mode(), err)
	}
	if target, err := os.Readlink(filepath.Join(dst, "tree", ".local", "bin", "link")); err != nil ||
		target != "/nonexistent/versions/1.0" {
		t.Errorf("a link in the entry was not copied as the same link: %q (%v)", target, err)
	}
	for _, d := range []string{root, filepath.Join(root, capturesEntriesLeaf)} {
		if fi, err := os.Stat(d); err != nil || fi.Mode().Perm() != 0o755 {
			t.Errorf("%s is %v (%v), want 0755 so the sandbox can search it", d, fi.Mode(), err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, capturesEntriesLeaf, captureKeyOld)); !os.IsNotExist(err) {
		t.Errorf("a staged entry the store no longer selects survived the prune (%v)", err)
	}
	if _, err := os.Stat(filepath.Join(root, capturesEntriesLeaf, captureKeyKept)); err != nil {
		t.Errorf("a kept entry was pruned: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, capturesStagingLeaf, captureKeyOld)); !os.IsNotExist(err) {
		t.Errorf("a copy a killed launch left under staging/ survived (%v)", err)
	}

	// SECOND LAUNCH: the key is a content address, so an entry staged under it is not copied
	// again. A file only the staged copy has proves the tree was left alone.
	sentinel := filepath.Join(dst, "sentinel")
	mustWrite(t, sentinel, "", 0o644)
	runAll()
	if _, err := os.Stat(sentinel); err != nil {
		t.Errorf("a second launch re-copied an entry already staged under its key (%v)", err)
	}
}

func mustMkdir(t *testing.T, dir string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, mode); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

// A CAPTURE THAT CANNOT BE STAGED DOES NOT REFUSE THE LAUNCH. The store is an optimization — a
// miss is the launcher's fallback to its download (internal/cli's capturematerialize.go) — so a
// copy that fails (a full disk during a 1.2 GB plain copy, an I/O error, the user's entry reaped
// by a concurrent `yolo prune --apply` after the host picked it) costs this launch the copy and
// nothing else: the launch goes on to its bootstrap and its session, and says which program
// installs the ordinary way. Red before the copy was best-effort: every stage command was fatal,
// and the launch refused with "Could not stage entrypoint" and no next step.
func TestACaptureThatCannotBeStagedDoesNotRefuseTheLaunch(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	var buf bytes.Buffer
	d.Out = &buf
	run := d.Run
	d.Run = func(argv []string) int {
		rc := run(argv)
		if containsArg(argv, stageCaptureScriptName) {
			return 1
		}
		return rc
	}
	opts := newOpts("/Users/Shared/yolo/proj")
	opts.HostCtx = stagedCaptureCtx()
	if rc := RunMacosUser(d, opts); rc != 42 {
		t.Fatalf("a launch whose capture copy failed returned %d, want the session's 42:\n%s", rc, buf.String())
	}
	joined := strings.Join(rec, "\n")
	if !strings.Contains(joined, stageCaptureScriptName) {
		t.Fatalf("the launch never ran the capture's copy, so this test proves nothing:\n%s", joined)
	}
	if !strings.Contains(joined, "darwin-bootstrap") || !strings.Contains(joined, "proxy:") {
		t.Errorf("the launch did not reach its bootstrap and session after the copy failed:\n%s", joined)
	}
	out := buf.String()
	for _, want := range []string{"could not copy the install capture of probetool", captureKeyA,
		"probetool installs the ordinary way this launch", "the next launch tries the copy again"} {
		if !strings.Contains(out, want) {
			t.Errorf("the launch did not say %q about the copy it could not make:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Could not stage entrypoint") {
		t.Errorf("a failed capture copy was reported as a failed entrypoint stage:\n%s", out)
	}
}

// The store-wide commands (make the store, open it, prune it) are best-effort on the same rule,
// and when one fails no entry is copied into a store that may not exist: one line, then the
// launch, every launcher missing and downloading.
func TestACaptureStoreThatCannotBePreparedDoesNotRefuseTheLaunch(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	var buf bytes.Buffer
	d.Out = &buf
	run := d.Run
	d.Run = func(argv []string) int {
		rc := run(argv)
		if len(argv) > 1 && argv[1] == mkdirBin && strings.Contains(strings.Join(argv, " "), StagedCapturesRoot("")) {
			return 1
		}
		return rc
	}
	opts := newOpts("/Users/Shared/yolo/proj")
	opts.HostCtx = stagedCaptureCtx()
	if rc := RunMacosUser(d, opts); rc != 42 {
		t.Fatalf("a launch whose capture store could not be made returned %d, want the session's 42:\n%s", rc, buf.String())
	}
	joined := strings.Join(rec, "\n")
	if strings.Contains(joined, stageCaptureScriptName) {
		t.Errorf("an entry was copied into a store that could not be made:\n%s", joined)
	}
	if !strings.Contains(buf.String(), "could not prepare the install-capture store at "+StagedCapturesRoot("")) {
		t.Errorf("the launch did not say it could not prepare the store:\n%s", buf.String())
	}
}

// A signal during a capture copy still ends the launch with the signal's status: best-effort is
// about a copy that failed, not about a launch the user is ending.
func TestASignalDuringACaptureCopyEndsTheLaunch(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	d.Out = io.Discard
	signalled := false
	d.Ending = func() (int, bool) { return 130, signalled }
	run := d.Run
	d.Run = func(argv []string) int {
		rc := run(argv)
		if containsArg(argv, stageCaptureScriptName) {
			signalled = true
			return 1
		}
		return rc
	}
	opts := newOpts("/Users/Shared/yolo/proj")
	opts.HostCtx = stagedCaptureCtx()
	if rc := RunMacosUser(d, opts); rc != 130 {
		t.Fatalf("a signal during the capture copy returned %d, want its status 130", rc)
	}
	if strings.Contains(strings.Join(rec, "\n"), "proxy:") {
		t.Errorf("the session started after a signal ended the launch")
	}
}

// THE COPY CLEANS UP AFTER ITSELF when any step of it fails, so a launch on a full disk does not
// keep a half-made copy of a 1.2 GB entry for the rest of its run, and it exits non-zero so the
// launch can say so. Run for real: entries/ is a file here, so the copy succeeds and the rename
// into entries/ fails, which works the same as the invoking user and as root.
func TestAFailedCaptureCopyLeavesNoPartialCopy(t *testing.T) {
	for _, bin := range []string{"/bin/sh", cpBin, chmodBin, mvBin, rmBin} {
		if _, err := os.Stat(bin); err != nil {
			t.Skipf("%s is not on this machine, so the stage script cannot run here: %v", bin, err)
		}
	}
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(tmp, "userstore", "entries", captureKeyA)
	mustMkdir(t, filepath.Join(src, "tree"), 0o755)
	mustWrite(t, filepath.Join(src, "tree", "f"), "bytes", 0o444)
	root := filepath.Join(tmp, "captures")
	mustMkdir(t, filepath.Join(root, capturesStagingLeaf), 0o755)
	mustWrite(t, filepath.Join(root, capturesEntriesLeaf), "not a directory", 0o644)

	argv := stageCaptureArgv(root, CaptureEntry{Bin: "probetool", Key: captureKeyA, Source: src})
	if out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err == nil {
		t.Fatalf("the stage script exited 0 although its rename failed:\n%s", out)
	}
	if _, err := os.Lstat(filepath.Join(root, capturesStagingLeaf, captureKeyA)); !os.IsNotExist(err) {
		t.Errorf("a failed copy left its partial tree under staging/ (%v)", err)
	}

	// And a source gone from the user's store (a concurrent reap) fails the same way.
	if err := os.Remove(filepath.Join(root, capturesEntriesLeaf)); err != nil {
		t.Fatal(err)
	}
	mustMkdir(t, filepath.Join(root, capturesEntriesLeaf), 0o755)
	argv = stageCaptureArgv(root, CaptureEntry{Bin: "probetool", Key: captureKeyA, Source: src + "-gone"})
	if out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err == nil {
		t.Fatalf("the stage script exited 0 for a source that does not exist:\n%s", out)
	}
	for _, p := range []string{filepath.Join(root, capturesStagingLeaf, captureKeyA),
		filepath.Join(root, capturesEntriesLeaf, captureKeyA)} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("a copy of a missing source left %s (%v)", p, err)
		}
	}
}

// PreflightLaunch ASKS WHAT THE LAUNCH ASKS, of the same workspace spelling and under the same
// hold name: a caller deciding "will the backend refuse this launch?" before the launch does
// (internal/cli/run's auto-capture) must get the launch's answer, or it acts for launches the
// backend then refuses, or skips ones it admits.
func TestPreflightLaunchAsksWhatTheLaunchAsks(t *testing.T) {
	type holdCall struct{ ws, cname string }
	recordHold := func(calls *[]holdCall, refusal string) func(string, string, string) (func(), string) {
		return func(ws, cname, _ string) (func(), string) {
			*calls = append(*calls, holdCall{ws, cname})
			if refusal != "" {
				return nil, refusal
			}
			return func() {}, ""
		}
	}
	const ws = "/Users/Shared/yolo/proj"

	// Admitted: both take the hold, under one name for one workspace.
	var pre, launch []holdCall
	d := mockDeps(nil)
	release, ok := PreflightLaunch(d.launchProbes(), recordHold(&pre, ""), ws)
	if !ok || release == nil {
		t.Fatalf("PreflightLaunch refused a launch every precondition admits")
	}
	release()
	d.HoldAccountHome = recordHold(&launch, "")
	if rc := RunMacosUser(d, newOpts(ws)); rc != 42 {
		t.Fatalf("the launch itself was refused (rc %d)", rc)
	}
	if len(pre) != 1 || len(launch) != 1 || pre[0] != launch[0] {
		t.Errorf("PreflightLaunch held %+v, the launch %+v; they must be one hold", pre, launch)
	}

	// Refused by a precondition: the in-home rule, a pure fact about the path, and no hold taken.
	pre = nil
	if _, ok := PreflightLaunch(d.launchProbes(), recordHold(&pre, ""), "/Users/matt/proj"); ok || len(pre) != 0 {
		t.Errorf("PreflightLaunch admitted a workspace under a home (ok %v, holds %v)", ok, pre)
	}
	nd := mockDeps(nil)
	nd.SandboxUserExists = func() bool { return false }
	if _, ok := PreflightLaunch(nd.launchProbes(), recordHold(&pre, ""), ws); ok {
		t.Errorf("PreflightLaunch admitted a Mac with no sandbox account")
	}

	// Refused by the hold, as the launch is.
	if _, ok := PreflightLaunch(d.launchProbes(), recordHold(&pre, "another session is live"), ws); ok {
		t.Errorf("PreflightLaunch admitted a launch the account-home hold refuses")
	}
}
