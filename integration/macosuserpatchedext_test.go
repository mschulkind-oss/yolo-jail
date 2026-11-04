package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// macosuserpatchedext_test.go is the macos-user cell for a PATCHED EXTENSION
// (docs/reference/pi-extensions.md §4 and §8): a macos-user launch builds the extension's tree for
// darwin in the sealed build act (the optional build line run in the checkout, then the checkout
// copied out), admits it to the capture store under darwin/<arch>, and delivers a per-launch copy
// through the macos-user home overlay at `~/<into>` in the sandbox home, write-protected by
// Seatbelt. A needed tree with no build refuses the launch, naming the hatches;
// YOLO_SKIP_EXTENSIONS starts the session without it.
//
// THE FIXTURE is patchedextension_test.go's shape — an owning agent pack whose settings surface's
// list names the tree (that list entry is what makes the tree NEEDED, §8.2), and a contributing
// pack with the `files` contribution and the `config-list` entry — over patchedfork_test.go's
// upstream (patchFixtureUpstream: the one-member series patching line ten of f.txt). HERMETIC: the
// upstream is a local git repository (git+file://), the owning agent's installer points nowhere and
// the readiness act is off (withoutFixtureProgramInstall), so nothing is installed and no agent
// starts: every session runs bash only.
//
// ⚠ Mac-only, behind requireMacosUser: the sandbox account, the run-as rule and nix. Every
// Linux runner, and a nested jail, cannot reach it; .github/workflows/macos-user.yml selects these
// with `-run '^TestMacosUser'`. Each test removes the capture entries it added and its
// extension's check records.

const (
	macExtAgentPack = "mptree-agent"
	macExtAgentBin  = "mptreeagent"
	macExtPack      = "mptree-ext"
	macExtName      = "mptree"
	macExtInto      = ".mptreeagent/ext/" + macExtName
	macExtSettings  = ".mptreeagent/settings.json"
)

// macExtFixture is one patched-extension fixture selected for macos-user launches.
type macExtFixture struct {
	up     patchFixtureUpstream
	owner  string
	state  string
	store  string
	before map[string]bool
	ws     string
	v11    string
}

// jsonString is s as a JSON string literal, quotes included.
func jsonString(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// newMacExtFixture builds the upstream (v1.0.0, the series' base, then v1.1.0 with line ten set to
// ten11), the series exported against v1.0.0, the owning agent pack, the contributing pack (with
// build as its build line, none when "", and produces when non-empty), the isolated user config
// selecting both and a workspace under the shared root. With ten11 "ten" the series fits v1.1.0;
// with anything else it conflicts there from the start, and there is no good build.
func newMacExtFixture(t *testing.T, ten11, build string, produces []string) *macExtFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	up := patchFixtureUpstream{t: t, dir: resolvedTempDir(t)}
	up.git("init", "-q", "-b", "main")
	base := up.release("1.0.0", "ten", "v1.0.0")

	ext := resolvedTempDir(t)
	up.git("checkout", "-q", "-b", "series")
	body, err := os.ReadFile(filepath.Join(up.dir, "f.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(up.dir, "f.txt"), []byte(strings.Replace(string(body), "ten\n", "patched\n", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	up.git("commit", "-qam", "patch line ten")
	up.git("format-patch", "-q", "--base="+base, "-o", filepath.Join(ext, "patches"), "main..series")
	up.git("checkout", "-q", "main")
	up.git("branch", "-q", "-D", "series")
	v11 := up.release("1.1.0", ten11, "v1.1.0")

	writeManifest := func(dir, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// THE OWNING AGENT PACK: a program that is never installed or run, its home directory as its
	// state (as pi's `~/.pi` is), and the settings surface whose list names the tree.
	agent := resolvedTempDir(t)
	writeManifest(agent, `{"name":"`+macExtAgentPack+`","contributes":[`+
		`{"kind":"program","bin":"`+macExtAgentBin+`","via":"installer","url":"file:///nonexistent/`+macExtAgentBin+`-install.sh"},`+
		`{"kind":"state","at":".mptreeagent","scope":"workspace"},`+
		`{"kind":"config","config":[{"agent":"`+macExtAgentBin+`","name":"settings","codec":"json",`+
		`"path":"~/`+macExtSettings+`"}]}]}`)
	// THE CONTRIBUTING PACK: the patched extension and the list entry that makes the agent load it.
	files := `{"kind":"files","into":"` + macExtInto + `","source":"git+file://` + up.dir + `?ref=main","patches":"patches"`
	if build != "" {
		files += `,"build":` + jsonString(t, build)
	}
	if len(produces) > 0 {
		p, err := json.Marshal(produces)
		if err != nil {
			t.Fatal(err)
		}
		files += `,"produces":` + string(p)
	}
	files += `}`
	writeManifest(ext, `{"name":"`+macExtPack+`","contributes":[`+files+`,`+
		`{"kind":"config-list","surface":"`+macExtAgentBin+`/settings","path":"/packages",`+
		`"add":["~/`+macExtInto+`"]}]}`)
	packHome(t, `{"packs": [{"source": "file://`+agent+`", "name": "`+macExtAgentPack+`"}, `+
		`{"source": "file://`+ext+`", "name": "`+macExtPack+`"}]}`)

	home := os.Getenv("HOME")
	f := &macExtFixture{up: up, owner: macExtPack + "/" + macExtName,
		state: paths.GlobalStorageUnder(home), store: paths.CapturesDirUnder(home), v11: v11}
	f.before = captureEntryNames(t, f.store)
	// The record, and any record beside it under the same owner (a per-platform one), is removed
	// before and after: the pack store may be the machine's.
	removeRecords := func() {
		for _, m := range f.records() {
			_ = os.Remove(m)
		}
	}
	removeRecords()
	t.Cleanup(func() {
		removeNewCaptureEntries(t, f.store, f.before, macExtName)
		removeRecords()
	})
	f.ws = macosUserWorkspace(t, `{}`)
	return f
}

// records is every check record file of the extension: its own and any per-platform sibling.
func (f *macExtFixture) records() []string {
	record := (&packsrc.Store{Dir: filepath.Join(f.state, "packs")}).CheckRecordPath(f.owner)
	matches, _ := filepath.Glob(strings.TrimSuffix(record, ".json") + "*")
	return matches
}

// macExtProbe is the session's probe of the delivered tree: whether it is there, the patched line,
// the build's output, whether the sandbox account can read it and whether it can write it, what the
// session was handed, and the agent's settings as rendered.
const macExtProbe = `d="$HOME/` + macExtInto + `"
if [ -d "$d" ]; then echo TREE_PRESENT; else echo TREE_ABSENT; fi
echo "LINE10=$(sed -n 10p "$d/f.txt" 2>/dev/null)"
echo "VER=$(cat "$d/ver.txt" 2>/dev/null)"
if [ -r "$d/built.txt" ]; then echo BUILT_READABLE; else echo BUILT_UNREADABLE; fi
echo "BUILT=$(cat "$d/built.txt" 2>/dev/null)"
if [ -L "$d/bin/built-link" ]; then echo "LINK_KEPT=$(cat "$d/bin/built-link" 2>/dev/null)"; else echo LINK_LOST; fi
if touch "$d/.probe" 2>/dev/null; then echo TREE_WRITABLE; else echo TREE_READONLY; fi
if [ -e "$d/f.txt" ] && (echo x >> "$d/f.txt") 2>/dev/null; then echo FILE_WRITABLE; else echo FILE_READONLY; fi
echo "TREES=$YOLO_PATCHED_TREES"
echo "SETTINGS=$(tr -d ' \n' < "$HOME/` + macExtSettings + `" 2>/dev/null)"
echo PROBE_DONE`

// launch runs the probe in a macos-user sandbox session of the fixture's workspace.
func (f *macExtFixture) launch(t *testing.T, opts ...runOption) result {
	t.Helper()
	return runMacosUser(t, f.ws, macExtProbe,
		append([]runOption{withoutFixtureProgramInstall(), withHostSemantics()}, opts...)...)
}

// live is the fixture's complete store entries of the extension.
func (f *macExtFixture) live(t *testing.T) []string {
	t.Helper()
	return liveCaptureEntries(t, f.store, newCaptureEntries(t, f.store, f.before, macExtName))
}

// probeLine is the value of the probe's NAME= line in out, and whether it printed one.
func probeLine(out, name string) (string, bool) {
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(line, name+"="); ok {
			return v, true
		}
	}
	return "", false
}

// assertDarwinEntry checks the launch added exactly one complete store entry of the extension,
// built for darwin/<arch> from v1.1.0 with the series, and that a check record holds it as the
// good build. It returns the entry's name.
func (f *macExtFixture) assertDarwinEntry(t *testing.T, r result) string {
	t.Helper()
	live := f.live(t)
	if len(live) != 1 {
		t.Fatalf("the launch added %d complete store entries of %s, want 1 (the darwin build of the extension): %v\nstderr:\n%s",
			len(live), macExtName, live, r.stderr)
	}
	entry := filepath.Join(f.store, "entries", live[0])
	want := "darwin/" + runtime.GOARCH
	if m, err := capture.ReadManifest(entry); err != nil || m.Platform != want {
		t.Errorf("the entry %s's manifest is %+v (%v), want platform %s", entry, m, err, want)
	}
	builds, err := entrypoint.ReadBuildReceipts(capture.ReceiptsPath(entry))
	if err != nil || len(builds) != 1 {
		t.Fatalf("the entry %s's build receipts are %+v (%v), want exactly one", entry, builds, err)
	}
	if b := builds[0]; b.Platform != want || b.Fork != f.owner || b.Bin != macExtName || b.Revision != f.v11 || b.Series == "" {
		t.Errorf("the build receipt is %+v, want a %s build of extension %s (bin %s) at %s with its series digest",
			b, want, f.owner, macExtName, f.v11)
	}
	// The check record: the owner's own, or a per-platform sibling, names a good build of v1.1.0.
	var seen []string
	good := false
	for _, p := range f.records() {
		data, err := os.ReadFile(p)
		if err != nil {
			seen = append(seen, p+": "+err.Error())
			continue
		}
		var rec struct {
			Good *packsrc.GoodBuild `json:"good"`
		}
		if err := json.Unmarshal(data, &rec); err != nil {
			seen = append(seen, p+": "+err.Error())
			continue
		}
		seen = append(seen, p+": "+string(data))
		if rec.Good != nil && rec.Good.Commit == f.v11 {
			good = true
		}
	}
	if !good {
		t.Errorf("no check record of %s holds a good build at v1.1.0 (%s); records found:\n%s",
			f.owner, f.v11, strings.Join(seen, "\n"))
	}
	return live[0]
}

// TestMacosUserPatchedExtensionWithABuildIsBuiltAndDelivered: the first macos-user launch checks
// the upstream, replays the series onto v1.1.0, runs the build line in the checkout under the
// sealed darwin build act, admits the tree under darwin/<arch> with a good build on record, and the
// sandbox session finds it at `~/<into>`: the build's output readable and carrying the upstream
// version and the patched line, the tree and its files not writable, the session handed the build
// in YOLO_PATCHED_TREES and the agent's list naming it.
func TestMacosUserPatchedExtensionWithABuildIsBuiltAndDelivered(t *testing.T) {
	requireMacosUser(t)
	build := `printf '%s_%s\n' "$(cat ver.txt)" "$(sed -n 10p f.txt)" > built.txt && mkdir -p bin && ln -s ../built.txt bin/built-link`
	f := newMacExtFixture(t, "ten", build, []string{"built.txt"})

	r := f.launch(t)
	out := r.combined()
	if r.rc != 0 || !strings.Contains(r.stdout, "PROBE_DONE") {
		t.Fatalf("the first macos-user launch: rc %d, want the session to run the probe\nstdout:\n%s\nstderr:\n%s",
			r.rc, r.stdout, r.stderr)
	}
	for _, w := range []string{
		"TREE_PRESENT",
		"LINE10=patched",
		"VER=1.1.0",
		"BUILT_READABLE",
		"BUILT=1.1.0_patched",
		"LINK_KEPT=1.1.0_patched",
		"TREE_READONLY",
		"FILE_READONLY",
	} {
		if !strings.Contains(r.stdout, w) {
			t.Errorf("the session's probe lacks %q (the tree at ~/%s, built from v1.1.0 with the series, "+
				"readable and write-protected):\n%s", w, macExtInto, r.stdout)
		}
	}
	if trees, _ := probeLine(r.stdout, "TREES"); !strings.Contains(trees, f.owner) {
		t.Errorf("the session was not handed the extension's build in YOLO_PATCHED_TREES (got %q):\n%s", trees, r.stdout)
	}
	if settings, _ := probeLine(r.stdout, "SETTINGS"); !strings.Contains(settings, `"~/`+macExtInto+`"`) {
		t.Errorf("the agent's settings (~/%s) do not list ~/%s (got %q)", macExtSettings, macExtInto, settings)
	}
	if want := "built extension " + f.owner + ": v1.1.0 (" + f.v11[:8] + ") + 1 patch"; !strings.Contains(out, want) {
		t.Errorf("the launch lacks %q:\n%s", want, out)
	}
	f.assertDarwinEntry(t, r)
}

// TestMacosUserPatchedExtensionWithNoBuildLineIsDelivered: a patched extension with no build line
// still passes through the sealed act and the admit, and the session finds the patched tree at
// `~/<into>`, write-protected.
func TestMacosUserPatchedExtensionWithNoBuildLineIsDelivered(t *testing.T) {
	requireMacosUser(t)
	f := newMacExtFixture(t, "ten", "", nil)

	r := f.launch(t)
	if r.rc != 0 || !strings.Contains(r.stdout, "PROBE_DONE") {
		t.Fatalf("the macos-user launch: rc %d, want the session to run the probe\nstdout:\n%s\nstderr:\n%s",
			r.rc, r.stdout, r.stderr)
	}
	for _, w := range []string{"TREE_PRESENT", "LINE10=patched", "VER=1.1.0", "TREE_READONLY", "FILE_READONLY"} {
		if !strings.Contains(r.stdout, w) {
			t.Errorf("the session's probe lacks %q (the patched tree, with no build line, at ~/%s, "+
				"write-protected):\n%s", w, macExtInto, r.stdout)
		}
	}
	if trees, _ := probeLine(r.stdout, "TREES"); !strings.Contains(trees, f.owner) {
		t.Errorf("the session was not handed the extension's build in YOLO_PATCHED_TREES (got %q):\n%s", trees, r.stdout)
	}
	f.assertDarwinEntry(t, r)
}

// TestMacosUserPatchedExtensionConflictRefusesAndSkipStartsWithoutIt: (a) a FIRST launch whose
// series conflicts at v1.1.0, with no good build, refuses before the session runs anything, naming
// the extension and the ways out — YOLO_SKIP_EXTENSIONS and a container backend among them — and
// admits nothing; (b) YOLO_SKIP_EXTENSIONS=<pack>/<name> starts the session without it: no tree
// at `~/<into>`, the agent's list not naming it, and the launch's line saying it was skipped.
func TestMacosUserPatchedExtensionConflictRefusesAndSkipStartsWithoutIt(t *testing.T) {
	requireMacosUser(t)
	f := newMacExtFixture(t, "upstream-ten", "true", nil)

	// (a) The refusal.
	r := f.launch(t)
	out := r.combined()
	if r.rc == 0 || strings.Contains(r.stdout, "PROBE_DONE") {
		t.Fatalf("a first launch whose series conflicts at v1.1.0: rc %d, want a refusal before the session "+
			"runs anything\nstdout:\n%s\nstderr:\n%s", r.rc, r.stdout, r.stderr)
	}
	for _, w := range []string{f.owner, "YOLO_SKIP_EXTENSIONS="} {
		if !strings.Contains(out, w) {
			t.Errorf("the refusal lacks %q:\n%s", w, out)
		}
	}
	// A series that does not apply fails on every backend, so the refusal offers the patch hatches,
	// never a runtime switch (that belongs to the no-darwin-build refusal alone).
	if !strings.Contains(out, "YOLO_SKIP_PATCHES=") {
		t.Errorf("the refusal lacks YOLO_SKIP_PATCHES:\n%s", out)
	}
	if strings.Contains(out, "YOLO_RUNTIME=") || strings.Contains(out, "YOLO_SETUP=podman-self") || strings.Contains(out, "YOLO_SETUP=apple-container-self") {
		t.Errorf("a conflicting series' refusal offers a runtime switch, which cannot help:\n%s", out)
	}
	// §8.7: YOLO_ALLOW_PATCH_FAILURES is listed only when a build of the series serves.
	if strings.Contains(out, "YOLO_ALLOW_PATCH_FAILURES=") {
		t.Errorf("the refusal offers YOLO_ALLOW_PATCH_FAILURES with no build to run:\n%s", out)
	}
	if live := f.live(t); len(live) != 0 {
		t.Errorf("the refused launch left complete store entries %v; nothing fits, so nothing is admitted", live)
	}

	// (b) The skip.
	r = f.launch(t, withEnv("YOLO_SKIP_EXTENSIONS="+f.owner))
	out = r.combined()
	if r.rc != 0 || !strings.Contains(r.stdout, "PROBE_DONE") {
		t.Fatalf("YOLO_SKIP_EXTENSIONS=%s: rc %d, want the session to start without the extension\n"+
			"stdout:\n%s\nstderr:\n%s", f.owner, r.rc, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stdout, "TREE_ABSENT") {
		t.Errorf("YOLO_SKIP_EXTENSIONS: the session has a tree at ~/%s, want none:\n%s", macExtInto, r.stdout)
	}
	if settings, _ := probeLine(r.stdout, "SETTINGS"); strings.Contains(settings, macExtInto) {
		t.Errorf("YOLO_SKIP_EXTENSIONS: the agent's settings still list the skipped tree (got %q)", settings)
	}
	if want := "extension " + f.owner + ": skipped (YOLO_SKIP_EXTENSIONS)"; !strings.Contains(out, want) {
		t.Errorf("YOLO_SKIP_EXTENSIONS: the launch lacks %q:\n%s", want, out)
	}
	if strings.Contains(out, "YOLO_SKIP_EXTENSIONS: no effect") {
		t.Errorf("YOLO_SKIP_EXTENSIONS: the launch says the hatch has no effect, but macos-user delivers trees:\n%s", out)
	}
	if live := f.live(t); len(live) != 0 {
		t.Errorf("the skipped launch admitted store entries %v, want none", live)
	}
}
