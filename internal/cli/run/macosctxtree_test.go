package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// DP-L1: the host bytes a `/ctx` mount carries on every other backend now cross on
// macos-user too, by COPY into a tree the launch stages root-owned.
//
// ⚠ EVERY TEST HERE DRIVES A REAL Run(), and that is the point rather than thoroughness.
// The composer could be unit-tested directly and would pass with the call deleted from the
// macos-user arm — the shape AGENTS.md says this repo has shipped five times, and the shape
// this backend has been bitten by three separate times (pack staging, launch flags, the
// profile channel, each a B-0). The arm returns above runContainer, so anything the
// container path does implicitly has to be re-pinned here explicitly.

// ctxLaunchHome sets up a fake host home with the claude pack selected and returns it.
func ctxLaunchHome(t *testing.T, extraConfigKeys string) string {
	t.Helper()
	home := packHome(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"packs": ["claude"]` + extraConfigKeys + `}`
	if err := os.WriteFile(filepath.Join(dir, "config.jsonc"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

// writeHostFileAt writes a host-side file, creating parents.
func writeHostFileAt(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

// runMacosUserCapturingCtx drives Run() on the macos-user arm with a stub backend and
// returns the HostContext the handler was handed, plus the launch's whole output.
func runMacosUserCapturingCtx(t *testing.T, ws string, tweak func(*Options)) (macosuser.HostContext, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	if tweak != nil {
		tweak(o)
	}
	var got macosuser.HostContext
	reached := false
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string, _ macosuser.HomeOverlay,
		hostCtx macosuser.HostContext, _ bool, _ *jsonx.OrderedMap, _ []packload.BlockedTool, _ macosuser.JailDaemons) int {
		got, reached = hostCtx, true
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}
	if !reached {
		t.Fatalf("Run() never reached the macos-user handler\nstderr:\n%s", stderr.String())
	}
	return got, stdout.String() + stderr.String()
}

// THE CELL ITSELF: a pack `reads-host` grant. The claude pack's settings surface declares
// `readsHost`, so the human's ~/.claude/settings.json is what the agent's own settings.json
// must be composed from — and on this backend it was composed from DEFAULTS instead,
// silently, for as long as the backend has existed (DP-B row in §5.1).
//
// It asserts the BYTES, not a path: a tree computed and never written is exactly as empty
// to the bootstrap as no tree at all, which is the lesson
// TestPacksAreStagedBeforeBackendDispatch was written from.
func TestMacosUserLaunchDeliversAPackReadsHostGrant(t *testing.T) {
	home := ctxLaunchHome(t, "")
	writeHostFileAt(t, filepath.Join(home, ".claude", "settings.json"),
		`{"theme":"the human's own"}`, 0o644)

	ctx, _ := runMacosUserCapturingCtx(t, t.TempDir(), nil)

	if ctx.Tree == "" {
		t.Fatalf("the macos-user arm composed no context tree, so the claude pack's " +
			"reads-host grant did not cross and the agent's settings.json is a default")
	}
	// The layout IS the manifest: the jail opens $YOLO_CTX_ROOT/<the /ctx path minus /ctx>.
	staged := filepath.Join(ctx.Tree, "host-claude", "settings.json")
	body, err := os.ReadFile(staged)
	if err != nil {
		t.Fatalf("the grant's bytes are not at %s: %v", staged, err)
	}
	if !strings.Contains(string(body), "the human's own") {
		t.Errorf("staged file holds %q, not the human's file", string(body))
	}
	// And the launcher RECORDED it, which is the half the jail's fail-closed read needs:
	// without the record a wrong-path delivery composes from defaults in silence, which is
	// the 2026-09-05 bug OQ-CO10 closed.
	want := packload.CtxRoot + "/host-claude/settings.json"
	if !containsString(ctx.Delivered, want) {
		t.Errorf("Delivered = %v, want it to name %s — the jail cannot tell a file that "+
			"was never delivered from one that did not arrive", ctx.Delivered, want)
	}
}

// THE OTHER CELL: a source-bearing `host_files` entry. It used to be FILTERED OUT of the
// wire entirely (macosuser.sourceLessHostFilesWire), so a user who pointed at a host file
// by path got no file at that path at all.
//
// Both halves are asserted because either alone is silently useless: bytes in the tree the
// wire never names are never read, and an entry in the wire whose bytes are absent renders
// the user's declared file from its defaults layer.
func TestMacosUserLaunchDeliversASourceBearingHostFilesEntry(t *testing.T) {
	home := ctxLaunchHome(t, `, "host_files": [{"path": ".npmrc", "source": "~/.npmrc"}]`)
	writeHostFileAt(t, filepath.Join(home, ".npmrc"), "registry=https://example.invalid\n", 0o644)
	ws := t.TempDir()

	ctx, _ := runMacosUserCapturingCtx(t, ws, nil)

	if len(ctx.HostFiles) != 1 || ctx.HostFiles[0].Path != ".npmrc" {
		t.Fatalf("HostFiles = %+v, want the one source-bearing entry — without it the "+
			"bootstrap is never told the file exists", ctx.HostFiles)
	}
	staged := filepath.Join(ctx.Tree, "host-user", ctx.HostFiles[0].Slug())
	body, err := os.ReadFile(staged)
	if err != nil {
		t.Fatalf("the entry's bytes are not at %s: %v", staged, err)
	}
	if !strings.Contains(string(body), "example.invalid") {
		t.Errorf("staged file holds %q, not the user's ~/.npmrc", string(body))
	}
}

// A SOURCE THAT IS NOT THERE IS NOT A FAILURE, and must not be RECORDED as delivered.
// Most users have never written a ~/.claude/settings.json, the surface correctly composes
// from its lower layers, and this is the one case where nothing arriving is right. Claiming
// it would refuse the launch, because the jail's read fails closed on a path the launcher
// said it delivered.
func TestMacosUserLaunchClaimsNothingForAnAbsentHostFile(t *testing.T) {
	ctxLaunchHome(t, "") // no ~/.claude/settings.json written
	ctx, _ := runMacosUserCapturingCtx(t, t.TempDir(), nil)

	if len(ctx.Delivered) != 0 {
		t.Errorf("Delivered = %v for a home with no settings.json; the jail would refuse "+
			"the launch, because a delivered path it cannot read is the ONE disposition "+
			"that fails closed", ctx.Delivered)
	}
	if ctx.Tree != "" {
		t.Errorf("composed a tree at %s with nothing to put in it — a launch that carried "+
			"no host bytes must say so by absence, or the host-layer report claims a "+
			"delivery mechanism it did not use", ctx.Tree)
	}
}

// RENDER-MARK PARITY, the launcher half (docs/design/notch-scoped-config-contributions.md
// §4.3, NS-D3). Two launches of one home with the same delivered file, differing only in
// whether yolo has rendered claude/settings into that home: the first is the user's own file
// and is NOT labelled, the second is yolo's render and IS — read off the provenance mark with
// the container launcher's own call (hostLayerIsRender), so a managed home is a baseline in a
// sandbox exactly as it is in a container.
//
// Through a real Run(), for this file's standing reason: delete the call from
// buildMacosCtxTree and the arm still reaches the handler, with nothing labelled. That was
// the macos-user leak: a host-only entry `yolo host apply` wrote came back into the sandbox
// as the user's layer.
func TestMacosUserLaunchLabelsAHostFileYoloHasRendered(t *testing.T) {
	home := ctxLaunchHome(t, "")
	writeHostFileAt(t, filepath.Join(home, ".claude", "settings.json"), `{"theme":"mine"}`, 0o644)
	want := packload.CtxRoot + "/host-claude/settings.json"

	ctx, _ := runMacosUserCapturingCtx(t, t.TempDir(), nil)
	if !containsString(ctx.Delivered, want) {
		t.Fatalf("fixture: %s was not delivered at all (Delivered = %v)", want, ctx.Delivered)
	}
	if len(ctx.Rendered) != 0 {
		t.Errorf("Rendered = %v in a home yolo has never rendered into — the sandbox would "+
			"stop composing the settings file its user already has", ctx.Rendered)
	}

	// The one trace a writing host apply leaves, written through the Target that decides
	// where it goes rather than a hand-joined path.
	mark := render.Host(home, nil, render.OwnershipUnstated).ProvenancePath("claude", "settings")
	writeHostFileAt(t, mark, "{}", 0o600)

	ctx, _ = runMacosUserCapturingCtx(t, t.TempDir(), nil)
	if !containsString(ctx.Rendered, want) {
		t.Errorf("Rendered = %v, want it to name %s — yolo has rendered claude/settings into "+
			"this home, and the sandbox would fold that render back in as the user's layer",
			ctx.Rendered, want)
	}
	// A labelled path is still DELIVERED: the label says what arrived, not whether anything
	// did, so the jail's fail-closed witness keeps its subject.
	if !containsString(ctx.Delivered, want) {
		t.Errorf("Delivered = %v lost %s once it was labelled", ctx.Delivered, want)
	}
}

// A DIRECTORY host_files ENTRY CROSSES BY COPY, CONFINED TO ITS SOURCE. It used to be left
// undelivered and named ("a copy does not scale to an arbitrary tree"), but a container's
// directory host_files is a full copy at boot too, so DP-D15's size reason never applied to it;
// the copy is capped instead (TestMacosUserRefusesADirectoryHostFileOverTheCap).
//
// The trailing slashes are what DECLARE it a directory entry — config.checkHostFiles reads the
// shape off the declaration, never off a stat, so a dir entry is a thing the user wrote rather
// than a thing yolo inferred.
//
// THE WHOLE CHAIN, with nothing hand-written between the steps: Run's macos-user arm composes
// the tree, the plan builder bakes the wire and the context root, PlanInvariants passes, and the
// entrypoint's own host_files step, handed exactly that wire and that tree as its YOLO_CTX_ROOT,
// lands the files in the home. Delete the copy from buildMacosCtxTree and the files are not in
// the tree; drop the entry from HostFiles and the wire does not name it.
func TestMacosUserLaunchCopiesADirectoryHostFileEntry(t *testing.T) {
	home := ctxLaunchHome(t, `, "host_files": [{"path": ".config/themes/", "source": "~/themes/"}]`)
	src := filepath.Join(home, "themes")
	writeHostFileAt(t, filepath.Join(src, "inner.txt"), "INNER\n", 0o644)
	writeHostFileAt(t, filepath.Join(src, "sub", "deep.txt"), "DEEP\n", 0o644)
	writeHostFileAt(t, filepath.Join(src, "run.sh"), "#!/bin/sh\nexit 0\n", 0o755)
	// Links: one that stays inside the source (followed, as a bind resolves it), and three that
	// a container bind would show the jail as dangling — absolute, `../` out of the source, and
	// dangling — none of which may carry a byte of the rest of the home.
	writeHostFileAt(t, filepath.Join(home, "outside-secret"), "SECRET\n", 0o600)
	for link, target := range map[string]string{
		"in-root-link": "sub/deep.txt",
		"abs-link":     filepath.Join(home, "outside-secret"),
		"escape-link":  "../outside-secret",
		"dangling":     "nope",
	} {
		if err := os.Symlink(target, filepath.Join(src, link)); err != nil {
			t.Fatal(err)
		}
	}

	ctx, out := runMacosUserCapturingCtx(t, t.TempDir(), nil)

	if len(ctx.HostFiles) != 1 || !ctx.HostFiles[0].IsDir || ctx.HostFiles[0].Path != ".config/themes" {
		t.Fatalf("HostFiles = %+v, want the one directory entry — without it the bootstrap is "+
			"never told the tree exists", ctx.HostFiles)
	}
	staged := filepath.Join(ctx.Tree, "host-user", ctx.HostFiles[0].Slug())
	for rel, want := range map[string]string{
		"inner.txt":    "INNER\n",
		"sub/deep.txt": "DEEP\n",
		"in-root-link": "DEEP\n",
		"run.sh":       "#!/bin/sh\nexit 0\n",
	} {
		if got := readOrAbsentAt(t, filepath.Join(staged, rel)); got != want {
			t.Errorf("staged %s = %q, want %q", rel, got, want)
		}
	}
	if fi, err := os.Stat(filepath.Join(staged, "run.sh")); err != nil || fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("the staged run.sh lost its exec bit (%v, %v): the boot would render it "+
			"non-executable", fi, err)
	}
	for _, rel := range []string{"abs-link", "escape-link", "dangling"} {
		if _, err := os.Lstat(filepath.Join(staged, rel)); err == nil {
			t.Errorf("%s was staged: a link that leads out of the source carried host bytes "+
				"nobody declared", rel)
		}
	}
	if err := filepath.Walk(ctx.Tree, func(p string, info os.FileInfo, err error) error {
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			t.Errorf("the composed tree holds a link at %s; it must hold files and folders only", p)
		}
		if err == nil && info.Mode().IsRegular() {
			if b, _ := os.ReadFile(p); strings.Contains(string(b), "SECRET") {
				t.Errorf("%s carries the bytes of a file outside the source", p)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "does not cross") {
		t.Errorf("the launch still says a directory entry does not cross:\n%s", out)
	}

	// The plan and the boot, handed what this launch produced.
	plan := macosuser.BuildRunPlan("/Users/Shared/yolo/proj", jsonx.NewOrderedMap(),
		[]string{"claude"}, []string{"/bin/zsh", "-l"}, "/usr/local/bin/yolo", "",
		macosuser.HomeOverlay{}, ctx, jsonx.NewOrderedMap(), nil, nil)
	if probs := macosuser.PlanInvariants(plan); len(probs) != 0 {
		t.Fatalf("the plan for a directory entry fails its invariants: %v", probs)
	}
	wire := ""
	for _, a := range plan.BootstrapArgv {
		if v, ok := strings.CutPrefix(a, "YOLO_HOST_FILES="); ok {
			wire = v
		}
	}
	if !strings.Contains(wire, ".config/themes") {
		t.Fatalf("YOLO_HOST_FILES does not name the directory entry: %q", wire)
	}
	t.Setenv("YOLO_CTX_ROOT", ctx.Tree)
	jailHome := t.TempDir()
	var errw bytes.Buffer
	e := &entrypoint.Env{Home: jailHome, Workspace: t.TempDir(),
		Vars: map[string]string{"YOLO_HOST_FILES": wire}, Stderr: &errw, LogOnly: &errw}
	if err := entrypoint.ConfigureHostFiles(e); err != nil {
		t.Fatalf("the boot's host_files step failed: %v\n%s", err, errw.String())
	}
	for rel, want := range map[string]string{"inner.txt": "INNER\n", "sub/deep.txt": "DEEP\n"} {
		if got := readOrAbsentAt(t, filepath.Join(jailHome, ".config", "themes", rel)); got != want {
			t.Errorf("the jail home's ~/.config/themes/%s = %q, want %q", rel, got, want)
		}
	}
}

// An ABSENT directory source crosses on the wire and fails nothing, on the file entry's rule:
// the entrypoint finds nothing at host-user/<slug> and writes nothing, as on a container whose
// bind was skipped.
func TestMacosUserLaunchWiresADirectoryEntryWhoseSourceIsAbsent(t *testing.T) {
	ctxLaunchHome(t, `, "host_files": [{"path": ".config/themes/", "source": "~/themes/"}]`)

	ctx, _ := runMacosUserCapturingCtx(t, t.TempDir(), nil)

	if len(ctx.HostFiles) != 1 || !ctx.HostFiles[0].IsDir {
		t.Fatalf("HostFiles = %+v, want the directory entry on the wire", ctx.HostFiles)
	}
	if ctx.Tree != "" {
		t.Errorf("composed a tree at %s with nothing to put in it", ctx.Tree)
	}
}

// THE CAP: a directory larger than this backend copies at launch ENDS the launch, naming the
// entry and what to do instead — a fatal refusal rather than a partial tree that looks like the
// user's. Crossed through a test-sized cap, per entry and then across the launch.
func TestMacosUserRefusesADirectoryHostFileOverTheCap(t *testing.T) {
	for _, tc := range []struct {
		name              string
		entryCap, launchC dirCopyCap
		want              string
	}{
		{"one entry over its own cap", dirCopyCap{bytes: 6, entries: 100},
			dirCopyCap{bytes: 1 << 20, entries: 100}, "copying one.txt would pass what macos-user copies for a directory host_files entry (at most 6 bytes in 100"},
		{"two entries over the launch's", dirCopyCap{bytes: 1 << 20, entries: 100},
			dirCopyCap{bytes: 1 << 20, entries: 2}, "for every directory host_files entry of one launch together"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			restoreE, restoreL := macosDirHostFileEntryCap, macosDirHostFileLaunchCap
			macosDirHostFileEntryCap, macosDirHostFileLaunchCap = tc.entryCap, tc.launchC
			t.Cleanup(func() { macosDirHostFileEntryCap, macosDirHostFileLaunchCap = restoreE, restoreL })
			home := ctxLaunchHome(t, `, "host_files": [{"path": ".config/a/", "source": "~/a/"}, `+
				`{"path": ".config/b/", "source": "~/b/"}]`)
			writeHostFileAt(t, filepath.Join(home, "a", "one.txt"), "0123456789\n", 0o644)
			writeHostFileAt(t, filepath.Join(home, "b", "two.txt"), "x\n", 0o644)
			writeHostFileAt(t, filepath.Join(home, "b", "three.txt"), "y\n", 0o644)

			out := runMacosUserExpectingRefusal(t, t.TempDir(), nil)
			for _, want := range []string{tc.want, "Split the entry into FILE entries",
				`runtime: "podman"`, "Apple Container " + acROBindsFloor} {
				if !strings.Contains(out, want) {
					t.Errorf("the refusal does not say %q:\n%s", want, out)
				}
			}
			if !strings.Contains(out, "host_files ~/.config/") {
				t.Errorf("the refusal does not name the entry:\n%s", out)
			}
		})
	}
}

// ⚠ THE INVERSION. Two warnings said pack `reads-host` grants "do not cross on macos-user"
// and that source-bearing `host_files` entries "are dropped". Both gaps are CLOSED, and a
// warning describing a closed gap teaches the reader to distrust the ones still true —
// the rule loopholeinert.go is already written under, applied to itself.
//
// It replaces TestMacosUserNotesHostByteGaps, which asserted the opposite. This is the
// NEGATIVE half only; the positive halves are the two delivery tests above.
func TestMacosUserLaunchNoLongerWarnsThatHostBytesCannotCross(t *testing.T) {
	home := ctxLaunchHome(t, `, "host_files": [{"path": ".npmrc", "source": "~/.npmrc"}]`)
	writeHostFileAt(t, filepath.Join(home, ".claude", "settings.json"), `{"a":1}`, 0o644)
	writeHostFileAt(t, filepath.Join(home, ".npmrc"), "registry=https://example.invalid\n", 0o644)

	_, out := runMacosUserCapturingCtx(t, t.TempDir(), nil)

	for _, retired := range []string{
		"reads-host grants do not cross",
		"are dropped on macos-user",
		"renders from its DEFAULTS layer",
	} {
		if strings.Contains(out, retired) {
			t.Errorf("the launch still says %q, but the bytes now cross by copy into the "+
				"staged context tree:\n%s", retired, out)
		}
	}
}

// THE DISCLOSURE MUST TRAVEL WITH THE BYTES. A pack now reads the human's home on this
// backend exactly as it does on every other one, and the banner is the entire trust
// boundary — packhostgrants.go: "the boundary today is DISCLOSURE, not consent", and
// OQ-TP9 deleted the approval gate only because the disclosure survives it. The container
// path prints this inside runContainer, below the macos-user return, so the arm prints its
// own; deleting that call ships a host-byte read nothing announces.
func TestMacosUserLaunchDisclosesWhatThePackReadsFromTheHost(t *testing.T) {
	home := ctxLaunchHome(t, "")
	writeHostFileAt(t, filepath.Join(home, ".claude", "settings.json"), `{"a":1}`, 0o644)

	_, out := runMacosUserCapturingCtx(t, t.TempDir(), nil)

	for _, want := range []string{"claude", "settings.json"} {
		if !strings.Contains(out, want) {
			t.Errorf("the launch did not disclose that the claude pack reads the human's "+
				"settings.json (missing %q). The disclosure is the whole trust boundary for "+
				"a host-file grant:\n%s", want, out)
		}
	}
}

// REBUILT, NEVER ACCUMULATED. A grant the user REVOKED — a pack dropped from `packs`, an
// entry deleted from their config — must stop being delivered, and a tree that only ever
// grew would keep composing a host file into a surface whose declaration is gone.
func TestMacosUserCtxTreeIsRebuiltFromScratchEachLaunch(t *testing.T) {
	home := ctxLaunchHome(t, "")
	writeHostFileAt(t, filepath.Join(home, ".claude", "settings.json"), `{"a":1}`, 0o644)
	ws := t.TempDir()

	ctx, _ := runMacosUserCapturingCtx(t, ws, nil)
	if ctx.Tree == "" {
		t.Fatal("no tree composed on the first launch")
	}
	stale := filepath.Join(ctx.Tree, "host-gone", "settings.json")
	writeHostFileAt(t, stale, `{"revoked":true}`, 0o644)

	ctx2, _ := runMacosUserCapturingCtx(t, ws, nil)
	if ctx2.Tree != ctx.Tree {
		t.Fatalf("the second launch composed a different tree (%s vs %s)", ctx2.Tree, ctx.Tree)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Errorf("a previous launch's file survived at %s; a revoked grant would keep "+
			"composing into the agent's config forever", stale)
	}
}

// The exec bit has a READER downstream: entrypoint.hostSourceIsExecutable stats the staged
// copy to decide the rendered mode, so a host script arriving without its bit renders
// non-executable and the agent told to run it gets EACCES. `host_files` gained executable
// delivery deliberately; dropping it here would take it away on this backend alone.
func TestMacosUserCtxTreeCarriesTheExecBit(t *testing.T) {
	home := ctxLaunchHome(t, `, "host_files": [{"path": ".local/bin/hook", "source": "~/hook.sh"}]`)
	writeHostFileAt(t, filepath.Join(home, "hook.sh"), "#!/bin/sh\nexit 0\n", 0o755)

	ctx, _ := runMacosUserCapturingCtx(t, t.TempDir(), nil)
	if len(ctx.HostFiles) != 1 {
		t.Fatalf("HostFiles = %+v, want the one entry", ctx.HostFiles)
	}
	staged := filepath.Join(ctx.Tree, "host-user", ctx.HostFiles[0].Slug())
	info, err := os.Stat(staged)
	if err != nil {
		t.Fatalf("stat %s: %v", staged, err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("staged copy is %v, not executable; the rendered file would be "+
			"non-executable and running it gives EACCES", info.Mode().Perm())
	}
}

// --dry-run composes too, for buildMacosHomeOverlay's reason: the plan's job is to
// describe the launch, and a plan whose stage list omitted the context tree would show a
// launch nobody runs. Composing writes only into the host-side staging dir; RunMacosUser
// still returns before executing a single staged command.
func TestMacosUserDryRunStillComposesTheContextTree(t *testing.T) {
	home := ctxLaunchHome(t, "")
	writeHostFileAt(t, filepath.Join(home, ".claude", "settings.json"), `{"a":1}`, 0o644)

	ctx, _ := runMacosUserCapturingCtx(t, t.TempDir(), func(o *Options) { o.DryRun = true })
	if ctx.Tree == "" {
		t.Error("--dry-run composed no context tree, so the plan it prints omits the " +
			"staging a real launch performs")
	}
}

// THE CONTEXT MOUNTS ON THIS ARM (docs/design/context-mounts.md §4 steps 3-5). A declared
// config `mounts` element or pack `mount` grant is DELIVERED as a root-owned link plus
// Seatbelt rules where the siting rules admit its source, and REFUSES the launch, named, with
// its reason, everywhere else — DP-D15's "a fatal error … rather than having it be surprisingly
// not there", narrowed by OQ-CX5 to the sources this backend cannot serve.
//
// ⚠ Run(), for macosctxtree_test.go's own reason and one sharper: the decision is a function of
// the config and a pack list, so a unit test of it passes with the call deleted — and a missing
// call is EXACTLY the defect, since every other reader of these two keys is below the arm's
// return.
//
// ⚠ THE SITING'S macOS FACTS ARE STATED BY EVERY TEST (o.macosCtxSiting), never inherited:
// whether a t.TempDir() fixture sits in "a place the sandbox may write" is a fact about where
// this machine keeps its temp dir (/tmp here, /var/folders on a Mac, anything under TMPDIR), so
// a test that let the default decide would pass or fail by machine.

// sitingWritable is a default macOS install's siting with the sandbox's writable places
// replaced by `writable`, resolved, and the shared root by sharedRoot when it is not "".
func sitingWritable(t *testing.T, sharedRoot string, writable ...string) *macosuser.ContextSiting {
	t.Helper()
	s := macosuser.DarwinContextSiting()
	s.WritableRoots = nil
	for _, w := range writable {
		resolved, err := filepath.EvalSymlinks(w)
		if err != nil {
			t.Fatal(err)
		}
		s.WritableRoots = append(s.WritableRoots, resolved)
	}
	if sharedRoot != "" {
		s.SharedRoot = sharedRoot
	}
	return &s
}

// refusingSiting puts every temp dir in the sandbox's writable set, so any fixture source the
// test made refuses as "which the sandbox may write" — the state every source was in before
// delivery existed, now for a stated reason.
func refusingSiting(t *testing.T) func(*Options) {
	return func(o *Options) { o.macosCtxSiting = sitingWritable(t, "", os.TempDir()) }
}

// deliveringSiting puts nothing in the writable set and the shared root at sharedRoot, so a
// fixture source outside the workspace and the home is one this backend delivers.
func deliveringSiting(t *testing.T, sharedRoot string) func(*Options) {
	return func(o *Options) { o.macosCtxSiting = sitingWritable(t, sharedRoot) }
}

// runMacosUserExpectingRefusal drives Run() on the macos-user arm and returns its output,
// failing unless the launch refused WITHOUT reaching the backend handler.
func runMacosUserExpectingRefusal(t *testing.T, ws string, tweak func(*Options)) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	if tweak != nil {
		tweak(o)
	}
	reached := false
	o.MacosUserRun = func(*jsonx.OrderedMap, string, []string, []string, string, string, macosuser.HomeOverlay,
		macosuser.HostContext, bool, *jsonx.OrderedMap, []packload.BlockedTool, macosuser.JailDaemons) int {
		reached = true
		return 0
	}
	rc := Run(*o)
	out := stdout.String() + stderr.String()
	if rc == 0 || reached {
		t.Fatalf("Run() = %d, handler reached = %v: a declared context mount this backend cannot "+
			"deliver must refuse the launch before the sandbox starts\n%s", rc, reached, out)
	}
	return out
}

func TestMacosUserRefusesADeclaredConfigMountItCannotDeliver(t *testing.T) {
	logs := t.TempDir()
	home := ctxLaunchHome(t, `, "mounts": ["~/code/ref-repo", "`+logs+`:/ctx/logs"]`)
	// The source EXISTS: an absent one is skipped, not refused (the test below).
	if err := os.MkdirAll(filepath.Join(home, "code", "ref-repo"), 0o755); err != nil {
		t.Fatal(err)
	}

	out := runMacosUserExpectingRefusal(t, t.TempDir(), refusingSiting(t))

	for _, want := range []string{
		"Refusing the macos-user launch",
		"/ctx/ref-repo",               // the bare-path entry, at the destination it would have taken
		"/ctx/logs",                   // the host:container entry, at the one it named
		"which the sandbox may write", // the REASON, per entry — not one sentence for all
		"container runtime",
		macosuser.SharedRootDefault(), // where to move it
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal never mentioned %q:\n%s", want, out)
		}
	}
	// Not the container path's sentence borrowed: Apple Container refuses a `:ro` bind it
	// would otherwise make, and this backend makes no bind at all.
	if strings.Contains(out, "read-only (:ro)") {
		t.Errorf("the macos-user refusal borrowed Apple Container's `:ro` reason:\n%s", out)
	}
}

// A read-write element outside the shared root refuses, and a --dry-run refuses too, since its
// plan would describe a launch that cannot run.
func TestMacosUserRefusesAReadWriteMountOutsideTheSharedRootAndADryRunToo(t *testing.T) {
	data := floortest.ResolvedTemp(t)
	ctxLaunchHome(t, `, "mounts": [{"host": "`+data+`", "mode": "rw", "at": "/ctx/data"}]`)

	out := runMacosUserExpectingRefusal(t, t.TempDir(), func(o *Options) {
		o.DryRun = true
		deliveringSiting(t, "/Users/Shared/yolo")(o)
	})
	for _, want := range []string{"/ctx/data (read-write)", "a read-write source must sit under /Users/Shared/yolo"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal did not say %q:\n%s", want, out)
		}
	}
}

// A SOURCE INSIDE A REAL HOME REFUSES (OQ-CX7: v1 delivers only sources outside every home),
// with the home named — the common case on a Mac, so the one most worth a precise reason.
func TestMacosUserRefusesASourceInsideAHome(t *testing.T) {
	users := floortest.ResolvedTemp(t)
	notes := filepath.Join(users, "alice", "notes")
	if err := os.MkdirAll(notes, 0o755); err != nil {
		t.Fatal(err)
	}
	ctxLaunchHome(t, `, "mounts": ["`+notes+`"]`)

	out := runMacosUserExpectingRefusal(t, t.TempDir(), func(o *Options) {
		s := sitingWritable(t, "")
		s.UsersRoot, s.UsersRootAliases = users, nil
		o.macosCtxSiting = s
	})
	if want := "is inside the home folder " + filepath.Join(users, "alice"); !strings.Contains(out, want) {
		t.Errorf("the refusal did not say %q:\n%s", want, out)
	}
}

// A PACK `mount` GRANT IS THE SAME DECLARATION, and refuses the same way. No pack yolo ships
// declares one, so this drives a configured (file://) pack.
func TestMacosUserRefusesAPackMountGrantItCannotDeliver(t *testing.T) {
	home := acmeMountPack(t, true)

	out := runMacosUserExpectingRefusal(t, t.TempDir(), refusingSiting(t))

	for _, want := range []string{"Refusing the macos-user launch", "pack acme", "~/datasets/acme", "/ctx/acme"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal never mentioned %q:\n%s", want, out)
		}
	}
	_ = home
}

// A LINK AROUND A PACK'S `reads-host` DESTINATION REFUSES at the top of the arm, against what
// the selected packs DECLARE rather than what this machine's home happens to hold: the claude
// pack's grant lands at /ctx/host-claude/settings.json, and a link at /ctx/host-claude would be
// staged inside the directory the composed tree makes there. The source is otherwise one this
// backend delivers, and ~/.claude/settings.json is deliberately absent.
func TestMacosUserRefusesAMountAroundAPackHostFileDestination(t *testing.T) {
	lib := filepath.Join(floortest.ResolvedTemp(t), "lib")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	ctxLaunchHome(t, `, "mounts": ["`+lib+`:/ctx/host-claude"]`)

	out := runMacosUserExpectingRefusal(t, t.TempDir(), deliveringSiting(t, ""))
	if want := "contains /ctx/host-claude/settings.json, which yolo's own staging uses"; !strings.Contains(out, want) {
		t.Errorf("the refusal did not say %q:\n%s", want, out)
	}
}

// acmeMountPack selects a configured pack `acme` whose `mount` grant is ~/datasets/acme →
// /ctx/acme, plus an env claim the banner always prints, and makes the source when withSource.
func acmeMountPack(t *testing.T, withSource bool) string {
	t.Helper()
	home := packHome(t)
	src := filepath.Join(t.TempDir(), "acme")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	writeHostFileAt(t, filepath.Join(src, "pack.json"),
		`{"name":"acme","contributes":[{"kind":"mount","host":"datasets/acme","into":"acme"},`+
			`{"kind":"env","vars":{"ACME_MARKER":"1"}}]}`, 0o644)
	writeUserPacks(t, home, `["file://`+src+`"]`)
	if withSource {
		if err := os.MkdirAll(filepath.Join(home, "datasets", "acme"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

// KEYED ON A DELIVERABLE DECLARATION, never on a default (DP-D15's own warning): a source
// that does not exist would be absent on every backend, so it is skipped with the container
// backends' line and the launch goes on (CX-D9).
func TestMacosUserSkipsADeclaredMountWhoseSourceIsAbsent(t *testing.T) {
	ctxLaunchHome(t, `, "mounts": ["~/no/such/dir"]`)

	ctx, out := runMacosUserCapturingCtx(t, t.TempDir(), refusingSiting(t))
	if !strings.Contains(out, "mount path does not exist, skipping") {
		t.Errorf("an absent mount source was not named as skipped:\n%s", out)
	}
	if strings.Contains(out, "Refusing the macos-user launch") {
		t.Errorf("an absent source refused the launch:\n%s", out)
	}
	if len(ctx.Links) != 0 {
		t.Errorf("an absent source crossed as a link: %+v", ctx.Links)
	}
}

// THE DELIVERY ITSELF (§4 step 4): a read-only config element whose source the siting admits
// reaches the backend as a link — at the container's /ctx path, from the RESOLVED source, read-
// only and a directory — and the launch neither refuses nor discloses a write. Fails if the arm
// stops handing the links over (ctxDelivery.ctx.Links) or stops deciding them.
func TestMacosUserDeliversAReadOnlyConfigMountAsALink(t *testing.T) {
	root := floortest.ResolvedTemp(t)
	lib := filepath.Join(root, "lib")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	// Through a symlink, so the RESOLVED source is what must cross: a rule on the link's own
	// spelling would match nothing (declaration-parity.md §6.1 probe 2).
	alias := filepath.Join(floortest.ResolvedTemp(t), "lib-alias")
	if err := os.Symlink(lib, alias); err != nil {
		t.Fatal(err)
	}
	ctxLaunchHome(t, `, "mounts": ["`+alias+`:/ctx/lib"]`)

	ctx, out := runMacosUserCapturingCtx(t, t.TempDir(), deliveringSiting(t, ""))

	want := macosuser.ContextLink{Dest: "/ctx/lib", Source: lib, Dir: true}
	if len(ctx.Links) != 1 || ctx.Links[0] != want {
		t.Fatalf("Links = %+v, want exactly %+v — the backend was not handed the mount to link", ctx.Links, want)
	}
	if strings.Contains(out, "Refusing the macos-user launch") || strings.Contains(out, "Read-write mount:") {
		t.Errorf("a deliverable read-only mount refused or was disclosed as writable:\n%s", out)
	}
}

// STEP 5: a read-write element under the shared root crosses read-write, and §2.4's
// disclosure is on the launch stream WITH YOLO_NO_BANNER=1 set, naming the path the agent
// opens here — under the context dir, not /ctx.
func TestMacosUserDeliversAReadWriteMountAndDisclosesIt(t *testing.T) {
	shared := floortest.ResolvedTemp(t)
	data := filepath.Join(shared, "datasets")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	ctxLaunchHome(t, `, "mounts": [{"host": "`+data+`", "mode": "rw", "at": "/ctx/data"}]`)
	t.Setenv("YOLO_NO_BANNER", "1")
	ws := t.TempDir()

	ctx, out := runMacosUserCapturingCtx(t, ws, func(o *Options) {
		deliveringSiting(t, shared)(o)
		base := o.Getenv
		o.Getenv = func(k string) string {
			if k == "YOLO_NO_BANNER" {
				return "1"
			}
			return base(k)
		}
	})

	if len(ctx.Links) != 1 || !ctx.Links[0].RW || ctx.Links[0].Source != data || ctx.Links[0].Dest != "/ctx/data" {
		t.Fatalf("Links = %+v, want one read-write link to %s at /ctx/data", ctx.Links, data)
	}
	staged := macosuser.StagedCtxRoot(cnameFromWorkspace(t, ws), "") + "/data"
	for _, want := range []string{"Read-write mount:", data + " → " + staged, "including a symlink the jail planted"} {
		if !strings.Contains(out, want) {
			t.Errorf("the launch stream lacks %q:\n%s", want, out)
		}
	}
}

// THE BANNER DISCLOSES A PACK `mount` THIS BACKEND DELIVERS, as every container arm does, and
// the grant crosses as a link from the home path the pack names, resolved. DP-B2's banner half
// left the claim out while nothing delivered it; delivery brought it back.
func TestMacosUserBannerDisclosesAPackMountItDelivers(t *testing.T) {
	home := acmeMountPack(t, true)

	ctx, out := runMacosUserCapturingCtx(t, t.TempDir(), deliveringSiting(t, ""))

	if !strings.Contains(out, "ACME_MARKER=1") {
		t.Fatalf("fixture: the pack's env claim is not on the banner:\n%s", out)
	}
	disclosed := false
	for _, line := range strings.Split(out, "\n") {
		disclosed = disclosed || (strings.Contains(line, "acme:") && strings.Contains(line, "[mount]"))
	}
	if !disclosed {
		t.Errorf("the macos-user banner does not disclose the pack mount it delivers:\n%s", out)
	}
	want := macosuser.ContextLink{Dest: "/ctx/acme", Source: filepath.Join(home, "datasets", "acme"),
		Named: "~/datasets/acme", Dir: true, Pack: "acme"}
	if len(ctx.Links) != 1 || ctx.Links[0] != want {
		t.Errorf("Links = %+v, want %+v", ctx.Links, want)
	}
}

// THE CONTROL, and it is the difference between a refusal and one nobody can get past: a
// launch that declared no context mount says nothing about one and reaches the handler.
func TestMacosUserSaysNothingAboutContextMountsNobodyDeclared(t *testing.T) {
	ctxLaunchHome(t, "")

	ctx, out := runMacosUserCapturingCtx(t, t.TempDir(), nil)

	for _, unwanted := range []string{"Refusing the macos-user launch", "context mount"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("a launch with no `mounts` and no pack grant still printed %q:\n%s",
				unwanted, out)
		}
	}
	if len(ctx.Links) != 0 {
		t.Errorf("a launch that declared nothing handed the backend links: %+v", ctx.Links)
	}
}

// cnameFromWorkspace is the container name the run pipeline derives for ws.
func cnameFromWorkspace(t *testing.T, ws string) string {
	t.Helper()
	return runtime.FromWorkspace(resolvePath(ws))
}

// containsString is a local membership test — the run package has no generic helper and
// one comparison does not earn an import.
func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// PARITY IN THE STATE NOBODY LOOKS AT: a source-bearing entry whose host file does not
// EXIST yet still reaches the wire, so the destination renders from its `defaults`/
// `content` layers. That is what the container path does — hostUserFileArgs emits the
// entry and skips only the bind — and gating the wire on the copy would leave this backend
// with the pre-DP-L1 answer (no file at that path at all) in exactly the case a reader
// would assume was covered by the delivery test above.
func TestMacosUserLaunchStillWiresAnEntryWhoseSourceIsAbsent(t *testing.T) {
	// `defaults`, not `content`: the two are mutually exclusive with `source` on one side
	// and not the other, and `defaults` is the layer a missing host file falls back to.
	ctxLaunchHome(t, `, "host_files": [{"path": ".npmrc.json", "source": "~/.npmrc.json", `+
		`"defaults": {"registry": "https://default.invalid"}}]`)
	// ~/.npmrc deliberately NOT written.

	ctx, _ := runMacosUserCapturingCtx(t, t.TempDir(), nil)

	if len(ctx.HostFiles) != 1 || ctx.HostFiles[0].Path != ".npmrc.json" {
		t.Fatalf("HostFiles = %+v; an entry whose source does not exist yet must still "+
			"cross, or nothing renders at ~/.npmrc.json and the user's declared file "+
			"is not there", ctx.HostFiles)
	}
	if ctx.Tree != "" {
		t.Errorf("composed a tree at %s with no bytes to put in it", ctx.Tree)
	}
}

// readOrAbsentAt is a file's contents, or "<absent>".
func readOrAbsentAt(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "<absent>"
	}
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
