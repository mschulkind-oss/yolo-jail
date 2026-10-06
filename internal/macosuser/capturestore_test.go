package macosuser

import (
	"bytes"
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
	if !containsCommand(plan.StageCommands, stageCaptureArgv(root, stagedCaptureCtx().Captures[0])) {
		t.Errorf("nothing stages the picked entry: %v", plan.StageCommands)
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
	for _, c := range plan.StageCommands {
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
			for _, c := range plan.StageCommands {
				if !containsArg(c, stageCaptureScriptName) {
					kept = append(kept, c)
				}
			}
			plan.StageCommands = kept
			return plan
		}, "nothing stages the capture of probetool"},
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
