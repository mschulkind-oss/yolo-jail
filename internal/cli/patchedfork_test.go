package cli

// patchedfork_test.go pins the explicit acts on a PATCHED fork (patchedfork.go;
// docs/design/patched-forks.md §8.3, PF-D12, PF-D16) through the verbs themselves, against a real
// local upstream repository: `yolo pack update` checks the upstream and reports whether the series
// replays at the candidate, with the conflict message and the newest fit when it does not; it
// records each outcome, writes no fork-lock entry and drops a plain fork's entry left under the
// key; `yolo pack install` leaves a patched fork with a good build alone; and `yolo pack status`
// reports all of it offline.

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

// patchedFixture is an upstream repository, a fork pack whose series was exported from a branch
// of it, and a HOME whose user config selects the fork and its base.
type patchedFixture struct {
	repo, forkDir, manifest, base string
}

// upstreamGit runs git in dir with a hermetic environment and a fixed identity.
func upstreamGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(testsupport.HermeticGitEnv(packsrc.CleanGitEnv(os.Environ())),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// lines30 is the upstream's one file with edits by line number.
func lines30(edits map[int]string) string {
	var b strings.Builder
	for i := 1; i <= 30; i++ {
		if e, ok := edits[i]; ok {
			b.WriteString(e + "\n")
		} else {
			fmt.Fprintf(&b, "%d\n", i)
		}
	}
	return b.String()
}

func (f *patchedFixture) commit(t *testing.T, tag string, edits map[int]string) string {
	t.Helper()
	return f.commitMsg(t, "upstream "+tag, tag, edits)
}

func (f *patchedFixture) commitMsg(t *testing.T, msg, tag string, edits map[int]string) string {
	t.Helper()
	writeFile(t, filepath.Join(f.repo, "f.txt"), lines30(edits))
	upstreamGit(t, f.repo, "add", "-A")
	upstreamGit(t, f.repo, "commit", "-qm", msg)
	if tag != "" {
		upstreamGit(t, f.repo, "tag", tag)
	}
	return upstreamGit(t, f.repo, "rev-parse", "HEAD")
}

// newPatchedFixture makes the upstream at v1.0.0, a two-member series over it (lines 10 and 12),
// and the HOME selecting a base pack and the patched fork of its program.
func newPatchedFixture(t *testing.T, follow string) *patchedFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	f := &patchedFixture{repo: t.TempDir()}
	upstreamGit(t, f.repo, "init", "-q", "-b", "main")
	f.base = f.commit(t, "v1.0.0", nil)
	upstreamGit(t, f.repo, "checkout", "-q", "-b", "fork")
	f.commitMsg(t, "ten", "", map[int]string{10: "ten"})
	f.commitMsg(t, "twelve", "", map[int]string{10: "ten", 12: "twelve"})
	packs := t.TempDir()
	f.forkDir = filepath.Join(packs, "forkpack")
	upstreamGit(t, f.repo, "format-patch", "-q", "--base="+f.base, "-o", filepath.Join(f.forkDir, "patches"), "main..fork")
	upstreamGit(t, f.repo, "checkout", "-q", "main")
	upstreamGit(t, f.repo, "branch", "-q", "-D", "fork")

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_VERSION", "")
	writeFile(t, filepath.Join(packs, "basepack", "pack.json"),
		`{"name":"basepack","contributes":[{"kind":"program","bin":"tool","via":"npm","package":"tool"}]}`)
	f.manifest = filepath.Join(f.forkDir, "pack.json")
	f.writeManifest(t, "main", follow)
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), `{"packs":[`+
		`{"source":"file://`+filepath.Join(packs, "basepack")+`","name":"basepack"},`+
		`{"source":"file://`+f.forkDir+`","name":"forkpack"}]}`)
	forkPinHomeSeams(t)
	return f
}

func (f *patchedFixture) writeManifest(t *testing.T, ref, follow string) {
	t.Helper()
	extra := ""
	if follow != "" {
		extra = `,"follow":"` + follow + `"`
	}
	writeFile(t, f.manifest, `{"name":"forkpack","contributes":[{"kind":"program","bin":"tool","via":"source",`+
		`"fork_of":"basepack","source":"git+file://`+f.repo+`?ref=`+ref+`","patches":"patches"`+extra+`,`+
		`"build":"sh build.sh","produces":[".local/bin/tool"]}]}`)
}

// forkPinHomeSeams stubs the update verb's in-jail refresh and host apply, as forkPinHome does.
func forkPinHomeSeams(t *testing.T) {
	t.Helper()
	origRefresh, origApply := programRefresh, hostApplyFromPackUpdate
	programRefresh = func(richtext.Printer, io.Writer) int { return 0 }
	hostApplyFromPackUpdate = func([]string, io.Writer, io.Writer, bool, io.Reader) int { return 0 }
	t.Cleanup(func() { programRefresh, hostApplyFromPackUpdate = origRefresh, origApply })
}

func packVerb(t *testing.T, verb string) (int, string, string) {
	t.Helper()
	var out, errw bytes.Buffer
	rc := packMain([]string{verb}, &out, &errw, false)
	return rc, out.String(), errw.String()
}

func patchedRecord(t *testing.T) *packsrc.CheckRecord {
	t.Helper()
	r, err := (&packsrc.Store{Dir: paths.PacksDir()}).LoadCheckRecord("forkpack/tool")
	if err != nil {
		t.Fatalf("the check record: %v", err)
	}
	return r
}

// UPDATE CHECKS AND REPLAYS: the candidate is the newest version, the series takes it, the outcome
// is recorded, and nothing is pinned — forks.lock.json holds no entry for a patched fork.
func TestPackUpdateReportsThatThePatchedSeriesApplies(t *testing.T) {
	f := newPatchedFixture(t, "")
	v11 := f.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	rc, out, errw := packVerb(t, "update")
	if rc != 0 {
		t.Fatalf("update rc=%d\n%s\n%s", rc, out, errw)
	}
	if !strings.Contains(out, "fork forkpack/tool") || !strings.Contains(out, "upstream v1.1.0 ("+shortSHA(v11)+
		") takes the series (2 patches") || !strings.Contains(out, "applies — the next launch builds it") {
		t.Errorf("update does not say the series applies at v1.1.0:\n%s", out)
	}
	if got := pinnedCommit(t); got != "" {
		t.Errorf("update pinned a patched fork at %s", got)
	}
	rec := patchedRecord(t)
	if rec.Check == nil || len(rec.Check.List) != 2 || rec.Applies(v11, rec.Outcomes[0].Series,
		patchedYoloVersion(), rec.Outcomes[0].Git) == nil {
		t.Errorf("the record = %+v, want the list and the clean replay at v1.1.0", rec)
	}

	// STATUS, OFFLINE: the remote gone, it still names the candidate, the outcome, the series.
	if err := os.Rename(f.repo, f.repo+".gone"); err != nil {
		t.Fatal(err)
	}
	rc, out, errw = packVerb(t, "status")
	if rc != 0 {
		t.Fatalf("status rc=%d\n%s\n%s", rc, out, errw)
	}
	for _, w := range []string{"patched fork of basepack's tool", "2 patches in patches (series ",
		"good build: none on this machine yet", "candidate: v1.1.0 (" + shortSHA(v11) + ")",
		"applies — the next launch builds it", "next check: in "} {
		if !strings.Contains(out, w) {
			t.Errorf("status lacks %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, "no pin yet") {
		t.Errorf("status reads a patched fork as an unpinned plain fork:\n%s", out)
	}
}

// A CONFLICT AT THE NEWEST VERSION: the conflict message names the member, its paths and the next
// step; the walk goes on to the newest fit; and status shows both.
func TestPackUpdateReportsAConflictAndTheNewestFit(t *testing.T) {
	f := newPatchedFixture(t, "")
	v11 := f.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	v12 := f.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	rc, out, errw := packVerb(t, "update")
	if rc != 0 {
		t.Fatalf("update rc=%d (a fit exists)\n%s\n%s", rc, out, errw)
	}
	for _, w := range []string{
		"fork forkpack/tool: upstream v1.2.0 (" + shortSHA(v12) + ") does not take the patch series —",
		"0001-ten.patch conflicts in f.txt",
		"nothing runs yet: the first build is the newest fit, v1.1.0 (" + shortSHA(v11) + ")",
		"rebase the series: yolo pack rebase forkpack/tool",
		"the newest fit, upstream v1.1.0 (" + shortSHA(v11) + "), takes the series",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("update lacks %q:\n%s", w, out)
		}
	}
	_, out, _ = packVerb(t, "status")
	for _, w := range []string{"candidate: v1.2.0", "does not take 0001-ten.patch (conflicts in f.txt)",
		"below it: v1.1.0", "applies"} {
		if !strings.Contains(out, w) {
			t.Errorf("status lacks %q:\n%s", w, out)
		}
	}
}

// NOTHING FITS: the conflict at every version, said with what runs — on a first advance the
// series' base — and a failed exit.
func TestPackUpdateWhenNothingFitsFails(t *testing.T) {
	f := newPatchedFixture(t, "")
	upstreamGit(t, f.repo, "tag", "-d", "v1.0.0")
	f.commit(t, "v1.1.0", map[int]string{11: "eleven"})
	rc, out, _ := packVerb(t, "update")
	if rc == 0 {
		t.Errorf("update exited 0 with no version taking the series:\n%s", out)
	}
	for _, w := range []string{"does not take the patch series", "no upstream version on the list takes the series",
		"the first build is the series' base " + shortSHA(f.base)} {
		if !strings.Contains(out, w) {
			t.Errorf("update lacks %q:\n%s", w, out)
		}
	}
}

// A PLAIN FORK'S PIN LEFT UNDER THE KEY from before a migration is dropped, and said.
func TestPackUpdateDropsAPlainForkPinLeftUnderAPatchedForksKey(t *testing.T) {
	f := newPatchedFixture(t, "")
	l := &packsrc.ForkLock{}
	l.Set(packsrc.ForkLockEntry{Key: "forkpack/tool", Source: "git+file:///old/fork?ref=main", Ref: "main",
		Commit: strings.Repeat("a", 40)})
	if err := l.Save(forkLockPath()); err != nil {
		t.Fatal(err)
	}
	_ = f
	rc, out, errw := packVerb(t, "update")
	if rc != 0 {
		t.Fatalf("update rc=%d\n%s\n%s", rc, out, errw)
	}
	if got := pinnedCommit(t); got != "" {
		t.Errorf("the plain-fork pin under a patched fork's key survived: %s", got)
	}
	if !strings.Contains(out, "fork forkpack/tool is a patched fork now; its plain-fork pin is dropped from forks.lock.json") {
		t.Errorf("update does not say it dropped the plain-fork pin:\n%s", out)
	}
}

// INSTALL LEAVES A PATCHED FORK WITH A GOOD BUILD ALONE, as it leaves a pinned plain fork: no check
// runs, so the record's sequence does not move.
func TestPackInstallLeavesAPatchedForkWithAGoodBuildAlone(t *testing.T) {
	newPatchedFixture(t, "")
	if rc, out, errw := packVerb(t, "install"); rc != 0 {
		t.Fatalf("install rc=%d\n%s\n%s", rc, out, errw)
	}
	rec := patchedRecord(t)
	if rec.Seq != 1 {
		t.Fatalf("install with no good build did not check it: seq %d", rec.Seq)
	}
	store := &packsrc.Store{Dir: paths.PacksDir()}
	if err := store.WithCheckRecord("forkpack/tool", nil, func(r *packsrc.CheckRecord, _ error, _ func() error) (bool, error) {
		r.Good = &packsrc.GoodBuild{Commit: rec.Check.Tip, Tag: "v1.0.0", Series: "s", Recipe: "r", Patches: 2}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	rc, out, errw := packVerb(t, "install")
	if rc != 0 {
		t.Fatalf("second install rc=%d\n%s\n%s", rc, out, errw)
	}
	if got := patchedRecord(t).Seq; got != 1 {
		t.Errorf("install checked a patched fork that has a good build (seq %d)", got)
	}
	if !strings.Contains(out, "forkpack/tool unchanged (good build v1.0.0") {
		t.Errorf("install does not say it left the patched fork alone:\n%s", out)
	}
}

// A SERIES THAT CANNOT BE READ is the fork's reason, at update and at status alike.
func TestABrokenSeriesIsThePatchedForksReason(t *testing.T) {
	f := newPatchedFixture(t, "")
	if err := os.RemoveAll(filepath.Join(f.forkDir, "patches")); err != nil {
		t.Fatal(err)
	}
	rc, _, errw := packVerb(t, "update")
	if rc == 0 || !strings.Contains(errw, "patch series patches: the directory does not exist") {
		t.Errorf("update over a missing series: rc=%d\n%s", rc, errw)
	}
	_, out, _ := packVerb(t, "status")
	if !strings.Contains(out, "patch series patches: the directory does not exist") {
		t.Errorf("status does not give the series' reason:\n%s", out)
	}
}

// STATUS BEFORE ANY CHECK says so and names what checks.
func TestPackStatusBeforeAnyCheck(t *testing.T) {
	newPatchedFixture(t, "head")
	_, out, _ := packVerb(t, "status")
	for _, w := range []string{"following head", "not checked on this machine yet", "yolo pack update"} {
		if !strings.Contains(out, w) {
			t.Errorf("status lacks %q:\n%s", w, out)
		}
	}
}

// THE HOST FLOOR'S PINNER NEVER PINS A PATCHED FORK: the floor builds its fork from the base's
// rewritten program (floorForkBuild), and that fork carries `patches`, so the one pinner every act
// reaches (packload.PinForks) gives its reason and writes no fork-lock entry. Without the field
// the floor's install would pin the upstream's branch head and build it unpatched.
func TestTheHostFloorNeverPinsAPatchedFork(t *testing.T) {
	newPatchedFixture(t, "")
	prog := hostfloor.Program{Pack: "basepack", Install: packdecl.Install{Kind: packdecl.InstallKindSource,
		Bin: "tool", Source: "git+file:///nonexistent/upstream?ref=main", Build: "sh build.sh",
		Produces: []string{".local/bin/tool"}, ForkedBy: "forkpack", Patches: "patches"}}
	if !floorForkBuild(prog, "").Fork.Patched() {
		t.Fatal("the floor's fork of a patched-fork program is not a patched fork")
	}
	f := productionHostFloor(io.Discard, []hostfloor.Program{prog})
	if f.ForkPinnable(prog) {
		t.Error("the floor reads a patched fork as pinnable")
	}
	commit, reason := f.PinFork(prog, func(string) {})
	if commit != "" || reason != packload.PatchedForkPinReason {
		t.Errorf("the floor's pin of a patched fork = %q, %q; want no pin and its reason", commit, reason)
	}
	if _, err := os.Stat(forkLockPath()); !os.IsNotExist(err) {
		t.Errorf("the floor's pin wrote the fork lock for a patched fork (err %v)", err)
	}
}
