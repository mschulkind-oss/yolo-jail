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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

// patchedFixture is an upstream repository, a fork pack whose series was exported from a branch
// of it, and a HOME whose user config selects the fork and its base.
type patchedFixture struct {
	repo, forkDir, manifest, base, packs, home string
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
	f.packs, f.home = packs, home
	f.writeUserConfig(t, "")
	forkPinHomeSeams(t)
	return f
}

// writeUserConfig writes the HOME's user config selecting the base pack and the fork, with extra
// (a leading-comma JSON member list, "" for none) after "packs".
func (f *patchedFixture) writeUserConfig(t *testing.T, extra string) {
	t.Helper()
	writeFile(t, filepath.Join(f.home, ".config", "yolo-jail", "config.jsonc"), `{"packs":[`+
		`{"source":"file://`+filepath.Join(f.packs, "basepack")+`","name":"basepack"},`+
		`{"source":"file://`+f.forkDir+`","name":"forkpack"}]`+extra+`}`)
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

// UPDATE UNDER A GIT TOO OLD FOR THE REPLAY names updating git as the step (PF-D58), not a retry of
// itself that meets the same git, and fails.
func TestPackUpdateUnderAnOldGitNamesUpdatingGit(t *testing.T) {
	f := newPatchedFixture(t, "")
	f.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	bin := t.TempDir()
	writeFile(t, filepath.Join(bin, "git"), "#!/bin/sh\nif [ \"$1\" = version ]; then echo \"git version 2.39.5\"; exit 0; fi\n"+
		"exec "+shquote.Quote(realGit)+" \"$@\"\n")
	if err := os.Chmod(filepath.Join(bin, "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	prev := patchedForkStore
	patchedForkStore = func() *packsrc.Store { s := prev(); s.Git = filepath.Join(bin, "git"); return s }
	t.Cleanup(func() { patchedForkStore = prev })
	rc, out, errw := packVerb(t, "update")
	if rc == 0 {
		t.Errorf("update under git 2.39.5 succeeded\n%s\n%s", out, errw)
	}
	if !strings.Contains(out, "needs git 2.40 or newer (`git merge-tree --merge-base`), and this host's git is 2.39.5 — "+
		"update git, then run `yolo pack update` again") || strings.Contains(out, "`yolo pack update` retries") {
		t.Errorf("update does not name updating git as the step:\n%s", out)
	}
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
		") takes the series (2 patches") || !strings.Contains(out, "applies — "+patchedNotBuilt) {
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
		"applies — " + patchedNotBuilt} {
		if !strings.Contains(out, w) {
			t.Errorf("status lacks %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, "no pin yet") {
		t.Errorf("status reads a patched fork as an unpinned plain fork:\n%s", out)
	}
}

func explicitGoodAndConflict(t *testing.T) (*patchedAdvanceFixture, string, string) {
	t.Helper()
	fx, oldKey := patchedActorLaunch(t)
	v12 := fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	fx.later(2 * time.Hour)
	return fx, oldKey, v12
}

func usePatchedForkGit(t *testing.T, git string) {
	t.Helper()
	prev := patchedForkStore
	patchedForkStore = func() *packsrc.Store {
		s := prev()
		s.Git = git
		return s
	}
	t.Cleanup(func() { patchedForkStore = prev })
}

// INSTALL MUST READ CACHED AUTHORITY BEFORE ITS GOOD-BUILD EARLY SKIP. It consumes a typed failure
// offline, without changing Good or invoking git; literal bypass may skip only this intact Good.
func TestPackInstallConsumesTypedFailureBeforeGoodSkip(t *testing.T) {
	for _, tc := range []struct {
		name, bypass string
		wantRC       int
	}{
		{name: "unset", wantRC: 1},
		{name: "zero", bypass: "0", wantRC: 1},
		{name: "true", bypass: "true", wantRC: 1},
		{name: "literal-one", bypass: "1", wantRC: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
			fx, oldKey, v12 := explicitGoodAndConflict(t)
			before := fx.record(t).Good
			if rc, out, errw := packVerb(t, "update"); rc == 0 ||
				!strings.Contains(errw, "ERROR: forkpack/tool: patch application failed") {
				t.Fatalf("forced update did not record the conflict: rc=%d\n%s\n%s", rc, out, errw)
			}
			t.Setenv("YOLO_ALLOW_PATCH_FAILURES", tc.bypass)
			git := filepath.Join(t.TempDir(), "git")
			writeFile(t, git, "#!/bin/sh\necho git-was-called >&2\nexit 99\n")
			if err := os.Chmod(git, 0o755); err != nil {
				t.Fatal(err)
			}
			usePatchedForkGit(t, git)
			rc, out, errw := packVerb(t, "install")
			if rc != tc.wantRC || strings.Contains(out+errw, "git-was-called") {
				t.Fatalf("install rc=%d want %d or called git after cached authority: out=%s err=%s", rc, tc.wantRC, out, errw)
			}
			if tc.bypass == "1" {
				if !strings.Contains(out+errw, "CONTINUING: using intact admitted build") ||
					!strings.Contains(out+errw, "skips this subject's advance") {
					t.Fatalf("literal bypass did not identify the skipped subject:\n%s\n%s", out, errw)
				}
			} else if !strings.Contains(errw, "ERROR: forkpack/tool: patch application failed at upstream v1.2.0 ("+v12+")") {
				t.Fatalf("install Good shortcut hid current failure:\n%s\n%s", out, errw)
			}
			current := fx.record(t)
			if current.Good == nil || current.Good.Entry != before.Entry || current.Good.Entry != oldKey {
				t.Fatalf("install failure/bypass changed the current Good build: before=%+v after=%+v", before, current.Good)
			}
			series, err := fx.fork(t).ReadSeries()
			if err != nil {
				t.Fatal(err)
			}
			if failure := current.CurrentPatchFailure(current.Read, series.Digest); failure == nil || failure.Target.Commit != v12 {
				t.Fatalf("install cleared or lost persistent failure authority: %+v", current)
			}
		})
	}
}

// A forced explicit retry can turn an unchanged transient application-command failure into a
// clean replay of the same candidate. The second actual command must clear only that target.
func TestPackUpdateForcedRetryRepairsTheSameCandidate(t *testing.T) {
	fx := newPatchedAdvanceFixture(t, "")
	fx.writeUserConfig(t, `,"agent_updates":{"forkpack":false}`)
	v11 := fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	bin := t.TempDir()
	count := filepath.Join(bin, "merge-tree-count")
	wrapper := filepath.Join(bin, "git")
	writeFile(t, wrapper, "#!/bin/sh\n"+
		"for a in \"$@\"; do if [ \"$a\" = merge-tree ]; then n=$(cat "+shellQuote(count)+" 2>/dev/null || echo 0); n=$((n+1)); echo $n > "+shellQuote(count)+"; if [ $n -eq 1 ]; then echo transient application failure >&2; echo second diagnostic line >&2; exit 2; fi; fi; done\n"+
		"exec "+shellQuote(realGit)+" \"$@\"\n")
	if err := os.Chmod(wrapper, 0o755); err != nil {
		t.Fatal(err)
	}
	usePatchedForkGit(t, wrapper)
	if rc, out, errw := packVerb(t, "update"); rc == 0 || !strings.Contains(errw, "Application command: ") ||
		!strings.Contains(errw, "transient application failure") || !strings.Contains(errw, "second diagnostic line") {
		t.Fatalf("first forced application-command error was not retained: rc=%d\n%s\n%s", rc, out, errw)
	}
	before := patchedRecord(t)
	if before.PatchFailure == nil || before.PatchFailure.Target.Commit != v11 || before.PatchFailure.Kind != "application-command" {
		t.Fatalf("first explicit attempt did not persist the exact candidate failure: %+v", before)
	}
	// The wrapper's one-shot rejection is spent. Update forces a fresh check and a new guarded replay.
	if rc, out, errw := packVerb(t, "update"); rc != 0 {
		t.Fatalf("clean forced retry failed: rc=%d\n%s\n%s", rc, out, errw)
	} else if !strings.Contains(out, "`agent_updates` holds pack forkpack: no launch checks it") {
		t.Fatalf("explicit retry lost the existing hold disclosure:\n%s\n%s", out, errw)
	}
	after := patchedRecord(t)
	series, err := fx.fork(t).ReadSeries()
	if err != nil {
		t.Fatal(err)
	}
	var applied bool
	for _, outcome := range after.Outcomes {
		if outcome.Commit == v11 && outcome.Series == series.Digest && outcome.Yolo == patchedYoloVersion() &&
			outcome.Kind == packsrc.OutcomeApplies {
			applied = true
		}
	}
	if failure := after.CurrentPatchFailure(after.Read, series.Digest); failure != nil || !applied {
		t.Fatalf("clean retry did not settle only the exact failed candidate: failure=%+v record=%+v", failure, after)
	}
}

// A failed explicit subject does not suppress a separate patched subject that is clean. Both
// actual check callers run, the failed one remains fatal, and the clean one's replay is recorded.
func TestPackUpdateAggregatesExplicitFailuresAcrossSubjects(t *testing.T) {
	fx, _, v12 := explicitGoodAndConflict(t)
	baseManifest := `{"name":"basepack","contributes":[{"kind":"program","bin":"tool","via":"npm","package":"tool"},{"kind":"program","bin":"tool2","via":"npm","package":"tool2"}]}`
	writeFile(t, filepath.Join(fx.packs, "basepack", "pack.json"), baseManifest)
	secondRepoRoot := t.TempDir()
	secondRepo := filepath.Join(secondRepoRoot, "repo")
	upstreamGit(t, secondRepoRoot, "clone", "-q", fx.repo, secondRepo)
	upstreamGit(t, secondRepo, "checkout", "-q", "--detach", fx.base)
	upstreamGit(t, secondRepo, "checkout", "-q", "-b", "second-series")
	writeFile(t, filepath.Join(secondRepo, "f.txt"), lines30(map[int]string{20: "twenty"}))
	upstreamGit(t, secondRepo, "add", "-A")
	upstreamGit(t, secondRepo, "commit", "-qm", "second series")
	secondDir := filepath.Join(fx.packs, "secondpack")
	patches := filepath.Join(secondDir, "patches")
	upstreamGit(t, secondRepo, "format-patch", "-q", "--base="+fx.base, "-o", patches, fx.base+"..HEAD")
	writeFile(t, filepath.Join(secondDir, "pack.json"), `{"name":"secondpack","contributes":[{"kind":"program","bin":"tool2","via":"source","fork_of":"basepack","source":"git+file://`+
		fx.repo+`?ref=main","patches":"patches","build":"sh build.sh","produces":[".local/bin/tool2"]}]}`)
	cfgPath := filepath.Join(fx.home, ".config", "yolo-jail", "config.jsonc")
	cfg, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg = bytes.Replace(cfg, []byte("]}"), []byte(`,{"source":"file://`+secondDir+`","name":"secondpack"}]}`), 1)
	if err := os.WriteFile(cfgPath, cfg, 0o600); err != nil {
		t.Fatal(err)
	}
	rc, out, errw := packVerb(t, "update")
	if rc == 0 || !strings.Contains(errw, "ERROR: forkpack/tool: patch application failed at upstream v1.2.0 ("+v12+")") {
		t.Fatalf("aggregate update lost its failed subject or exit: rc=%d\n%s\n%s", rc, out, errw)
	}
	if !strings.Contains(out, "secondpack/tool2:") || !strings.Contains(out, "applies — "+patchedNotBuilt) {
		t.Fatalf("failed subject suppressed the unrelated clean subject:\n%s\n%s", out, errw)
	}
	second, err := (&packsrc.Store{Dir: paths.PacksDir()}).LoadCheckRecord("secondpack/tool2")
	if err != nil || second == nil || len(second.Outcomes) == 0 {
		t.Fatalf("the unrelated clean replay was not recorded: record=%+v err=%v", second, err)
	}
	series, err := packsrc.ReadSeries(secondDir, "patches")
	if err != nil {
		t.Fatal(err)
	}
	if failure := second.CurrentPatchFailure(second.Read, series.Digest); failure != nil {
		t.Fatalf("the clean independent subject acquired a patch failure: %+v", failure)
	}
}

// Literal bypass in an explicit update is a foreground subject skip: it leaves the current
// compatible Good untouched and does not turn the older fit into a claimed result.
func TestPackUpdateLiteralBypassSkipsOnlyTheCurrentGoodSubject(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "1")
	fx, oldKey, v12 := explicitGoodAndConflict(t)
	before := fx.record(t).Good
	rc, out, errw := packVerb(t, "update")
	if rc != 0 || !strings.Contains(out, "skipped this explicit subject; no build or fallback was accepted") ||
		!strings.Contains(errw, "patch application failed at upstream v1.2.0 ("+v12+")") ||
		!strings.Contains(errw, "CONTINUING: using intact admitted build") {
		t.Fatalf("literal bypass did not skip only this explicit subject: rc=%d\n%s\n%s", rc, out, errw)
	}
	if len(fx.builds) != 1 || fx.record(t).Good == nil || fx.record(t).Good.Entry != oldKey ||
		fx.record(t).Good.Entry != before.Entry {
		t.Fatalf("bypass built or moved Good: builds=%d before=%+v after=%+v", len(fx.builds), before, fx.record(t).Good)
	}
}

// No Good means the literal bypass has no compatible subject to skip; it cannot accept an older fit.
func TestPackUpdateLiteralBypassWithoutGoodStillFails(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "1")
	f := newPatchedFixture(t, "")
	f.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	v12 := f.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	rc, out, errw := packVerb(t, "update")
	if rc == 0 || strings.Contains(out+errw, "CONTINUING: using intact admitted build") ||
		!strings.Contains(errw, "patch application failed at upstream v1.2.0 ("+v12+")") {
		t.Fatalf("bypass accepted a subject without Good: rc=%d\n%s\n%s", rc, out, errw)
	}
}

// A cached opaque diagnosis is not classified from its text by install's Good shortcut. This
// unchanged subject remains a no-Git install skip until an explicit update actually checks it.
func TestPackInstallOpaqueApplyErrorIsNotTextClassified(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx, oldKey := patchedActorLaunch(t)
	store := &packsrc.Store{Dir: paths.PacksDir()}
	if err := store.WithCheckRecord("forkpack/tool", nil, func(r *packsrc.CheckRecord, readErr error, _ func() error) (bool, error) {
		if readErr != nil {
			return false, readErr
		}
		r.ApplyErr = &packsrc.ApplyError{Seq: r.Check.Seq, Error: "merge conflict in a commit message"}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	git := filepath.Join(t.TempDir(), "git")
	writeFile(t, git, "#!/bin/sh\necho git-was-called >&2\nexit 99\n")
	if err := os.Chmod(git, 0o755); err != nil {
		t.Fatal(err)
	}
	usePatchedForkGit(t, git)
	rc, out, errw := packVerb(t, "install")
	if rc != 0 || strings.Contains(out+errw, "git-was-called") || strings.Contains(errw, "patch application failed") {
		t.Fatalf("opaque text was inferred as current failure or install ran git: rc=%d\n%s\n%s", rc, out, errw)
	}
	after := fx.record(t)
	if after.Good == nil || after.Good.Entry != oldKey || after.ApplyErr == nil || after.ApplyErr.Error != "merge conflict in a commit message" {
		t.Fatalf("opaque diagnosis or Good changed during install: %+v", after)
	}
}

// RecordReplay write failure cannot erase the command's already classified failure. The obstruction
// is injected after WalkSeries releases the mirror lock, at the actual check-record lock path.
func TestPackUpdateConflictSurvivesReplayRecordFailure(t *testing.T) {
	f := newPatchedFixture(t, "")
	f.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	v12 := f.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	lock := patchedRecordLockPath("forkpack/tool")
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	wrapper := filepath.Join(t.TempDir(), "git")
	writeFile(t, wrapper, "#!/bin/sh\nfor a in \"$@\"; do if [ \"$a\" = merge-tree ]; then rm -rf "+shellQuote(lock)+"; mkdir -p "+
		shellQuote(lock)+"; break; fi; done\nexec "+shellQuote(realGit)+" \"$@\"\n")
	if err := os.Chmod(wrapper, 0o755); err != nil {
		t.Fatal(err)
	}
	usePatchedForkGit(t, wrapper)
	rc, out, errw := packVerb(t, "update")
	if rc == 0 || !strings.Contains(errw, "recording the replay:") || !strings.Contains(errw, "is a directory") ||
		!strings.Contains(errw, "patch application failed at upstream v1.2.0 ("+v12+")") ||
		!strings.Contains(errw, "Conflict: f.txt") {
		t.Fatalf("record failure erased operation failure or exit status: rc=%d\n%s\n%s", rc, out, errw)
	}
	if strings.Contains(out+errw, "applies — "+patchedNotBuilt) {
		t.Fatalf("explicit record failure allowed a fit to be accepted:\n%s\n%s", out, errw)
	}
	record, err := (&packsrc.Store{Dir: paths.PacksDir()}).LoadCheckRecord("forkpack/tool")
	if err != nil || record.PatchFailure != nil || len(record.Outcomes) != 0 {
		t.Fatalf("failed RecordReplay unexpectedly changed the record: %+v err=%v", record, err)
	}
}

func TestExplicitPostRetryGuardRejectsChangedAuthority(t *testing.T) {
	inputs := packsrc.CheckInputs{Repo: "git+file:///repo?ref=main", Ref: "main", Follow: "head", Base: "base"}
	selected := &packsrc.CheckFound{Seq: 7, Tip: "tip", List: []packsrc.ListEntry{{Commit: "newer", Tag: "v1.3.0"}, {Commit: "target", Tag: "v1.2.0"}}}
	record := &packsrc.CheckRecord{Schema: 1, Owner: "forkpack/tool", Seq: 7, Read: inputs, Check: selected}
	snapshot, err := record.ReplaySnapshot(inputs, "series-digest")
	if err != nil {
		t.Fatal(err)
	}
	if !explicitSelectedCheckStillCurrent(record, snapshot, selected) {
		t.Fatal("unchanged original finished check was refused")
	}
	for _, tc := range []struct {
		name   string
		mutate func(*packsrc.CheckRecord)
	}{
		{name: "owner", mutate: func(r *packsrc.CheckRecord) { r.Owner = "other/tool" }},
		{name: "read", mutate: func(r *packsrc.CheckRecord) { r.Read.Ref = "other" }},
		{name: "record-seq", mutate: func(r *packsrc.CheckRecord) { r.Seq++ }},
		{name: "unfinished-check", mutate: func(r *packsrc.CheckRecord) { r.Check = nil }},
		{name: "finished-check-seq", mutate: func(r *packsrc.CheckRecord) { r.Check.Seq++ }},
		{name: "finished-check-candidates", mutate: func(r *packsrc.CheckRecord) { r.Check.List[0].Commit = "later-candidate" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := *record
			check := *record.Check
			check.List = append([]packsrc.ListEntry(nil), record.Check.List...)
			copy.Check = &check
			tc.mutate(&copy)
			if explicitSelectedCheckStillCurrent(&copy, snapshot, selected) {
				t.Fatalf("accepted changed selected-check authority: %+v", copy)
			}
		})
	}
}

// check, even when that later check has the same inputs and contains the same newer candidate. The
// pass-through load seam mutates the actual record immediately before the caller's actual reread.
func TestPackUpdateStopsAfterLaterCheckArrivesBetweenRetryAndReread(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx, _ := patchedActorLaunch(t)
	v12 := fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 20: "twenty"})
	fx.later(2 * time.Hour)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	bin := t.TempDir()
	count := filepath.Join(bin, "merge-tree-count")
	wrapper := filepath.Join(bin, "git")
	writeFile(t, wrapper, "#!/bin/sh\n"+
		"for a in \"$@\"; do if [ \"$a\" = merge-tree ]; then n=$(cat "+shellQuote(count)+" 2>/dev/null || echo 0); n=$((n+1)); echo $n > "+shellQuote(count)+"; if [ $n -eq 1 ]; then echo transient application failure >&2; exit 2; fi; fi; done\n"+
		"exec "+shellQuote(realGit)+" \"$@\"\n")
	if err := os.Chmod(wrapper, 0o755); err != nil {
		t.Fatal(err)
	}
	usePatchedForkGit(t, wrapper)
	if rc, _, errw := packVerb(t, "update"); rc == 0 || !strings.Contains(errw, "patch application failed at upstream v1.2.0 ("+v12+")") {
		t.Fatalf("fixture did not persist its exact transient target failure: rc=%d\n%s", rc, errw)
	}
	v13 := fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 20: "twenty", 21: "twenty-one"})
	f := fx.fork(t)
	series, err := f.ReadSeries()
	if err != nil {
		t.Fatal(err)
	}
	store := &packsrc.Store{Dir: paths.PacksDir()}
	var selectedSeq int64
	injected := false
	previousLoad := explicitPostRetryRecordLoad
	explicitPostRetryRecordLoad = func(actual *packsrc.Store, fork packload.Fork, s *packsrc.Series) (*packsrc.CheckRecord, error) {
		record, err := actual.LoadCheckRecord(fork.Key())
		if err != nil {
			return nil, err
		}
		if record.Check == nil {
			return nil, fmt.Errorf("post-retry record has no finished check")
		}
		selectedSeq = record.Seq
		record.Seq++
		check := *record.Check
		check.Seq = record.Seq
		check.At++
		record.Check = &check
		data, err := json.Marshal(record)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(actual.CheckRecordPath(fork.Key()), data, 0o600); err != nil {
			return nil, err
		}
		injected = true
		return previousLoad(actual, fork, s)
	}
	t.Cleanup(func() { explicitPostRetryRecordLoad = previousLoad })
	if store.NoWait {
		t.Fatal("fixture store unexpectedly uses NoWait")
	}
	rc, out, errw := packVerb(t, "update")
	if !injected || selectedSeq == 0 || rc == 0 || !strings.Contains(errw, "replay authority changed after its successful retry") {
		t.Fatalf("the old update adopted a later check rather than stopping: injected=%v selectedSeq=%d rc=%d\n%s\n%s",
			injected, selectedSeq, rc, out, errw)
	}
	current, err := store.LoadCheckRecord(f.Key())
	if err != nil || current.Check == nil || current.Check.Seq != current.Seq || current.Seq != selectedSeq+1 {
		t.Fatalf("later finished check was not preserved: record=%+v err=%v", current, err)
	}
	for _, outcome := range current.Outcomes {
		if outcome.Commit == v13 && outcome.Series == series.Digest {
			t.Fatalf("the old explicit act replayed shared newer candidate %s after the later check: %+v", v13, outcome)
		}
	}
}

func TestPackUpdateDefersHostApplyAfterExplicitFailure(t *testing.T) {
	f := newPatchedFixture(t, "")
	f.writeUserConfig(t, `,"host_management":"own"`)
	f.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	v12 := f.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	called := false
	prevApply := hostApplyFromPackUpdate
	hostApplyFromPackUpdate = func(args []string, _ io.Writer, _ io.Writer, _ bool, _ io.Reader) int {
		called = true
		if len(args) != 1 || args[0] != "--assert" {
			t.Errorf("deferred host apply args = %v, want [--assert]", args)
		}
		return 0
	}
	t.Cleanup(func() { hostApplyFromPackUpdate = prevApply })
	rc, out, errw := packVerb(t, "update")
	if rc == 0 || !called || !strings.Contains(errw, "patch application failed at upstream v1.2.0 ("+v12+")") {
		t.Fatalf("pack update lost its actor failure or skipped its deferred apply: rc=%d called=%v\n%s\n%s",
			rc, called, out, errw)
	}
}

// A check record that changes after the explicit check was captured is never adopted by the old
// walk. Stale replay persistence stops, and only the freshly readable authority is reported.
func TestPackUpdateDoesNotAdoptANewerCheckSnapshot(t *testing.T) {
	f := newPatchedFixture(t, "")
	f.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	if rc, out, errw := packVerb(t, "update"); rc != 0 {
		t.Fatalf("clean initial update: rc=%d\n%s\n%s", rc, out, errw)
	}
	v12 := f.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	store := &packsrc.Store{Dir: paths.PacksDir()}
	newer := patchedRecord(t)
	newer.Seq += 10
	check := *newer.Check
	check.Seq = newer.Seq
	check.List = append([]packsrc.ListEntry{{Commit: v12, Tag: "v1.2.0", Version: "1.2.0"}}, check.List...)
	newer.Check = &check
	series, err := packsrc.ReadSeries(f.forkDir, "patches")
	if err != nil {
		t.Fatal(err)
	}
	newer.PatchFailure = &packsrc.PatchFailure{Owner: newer.Owner, Inputs: newer.Read, Series: series.Digest,
		Target: check.List[0], Kind: "conflict", Member: "newer-authority.patch", Paths: []string{"newer.txt"}, Seq: newer.Seq}
	data, err := json.Marshal(newer)
	if err != nil {
		t.Fatal(err)
	}
	prepared := filepath.Join(t.TempDir(), "newer-check.json")
	if err := os.WriteFile(prepared, data, 0o600); err != nil {
		t.Fatal(err)
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	wrapper := filepath.Join(t.TempDir(), "git")
	writeFile(t, wrapper, "#!/bin/sh\nfor a in \"$@\"; do if [ \"$a\" = merge-tree ]; then cp "+shellQuote(prepared)+" "+
		shellQuote(store.CheckRecordPath(newer.Owner))+"; break; fi; done\nexec "+shellQuote(realGit)+" \"$@\"\n")
	if err := os.Chmod(wrapper, 0o755); err != nil {
		t.Fatal(err)
	}
	usePatchedForkGit(t, wrapper)
	rc, out, errw := packVerb(t, "update")
	if rc == 0 || !strings.Contains(errw, "newer-authority.patch") || !strings.Contains(errw, "0001-ten.patch") ||
		!strings.Contains(errw, "recording the replay:") || strings.Contains(out+errw, "applies — "+patchedNotBuilt) {
		t.Fatalf("stale operation evidence or current later authority was lost, or an older fit was accepted: rc=%d\n%s\n%s", rc, out, errw)
	}
	current, err := store.LoadCheckRecord(newer.Owner)
	if err != nil || current.Seq != newer.Seq || current.PatchFailure == nil ||
		current.PatchFailure.Member != "newer-authority.patch" || current.PatchFailure.Paths[0] != "newer.txt" {
		t.Fatalf("newer explicit authority was overwritten: record=%+v err=%v", current, err)
	}
}

// A pure-core current failure found by the post-retry reread is consumed as that new authority;
// the older operation's clean retry must not overwrite it or turn it into a fit.
func TestPackUpdateConsumesCurrentFailureFromLaterCheckAfterRetry(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx, _ := patchedActorLaunch(t)
	v12 := fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 20: "twenty"})
	fx.later(2 * time.Hour)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	count := filepath.Join(t.TempDir(), "merge-tree-count")
	wrapper := filepath.Join(t.TempDir(), "git")
	writeFile(t, wrapper, "#!/bin/sh\n"+
		"for a in \"$@\"; do if [ \"$a\" = merge-tree ]; then n=$(cat "+shellQuote(count)+" 2>/dev/null || echo 0); n=$((n+1)); echo $n > "+shellQuote(count)+"; if [ $n -eq 1 ]; then echo transient application failure >&2; exit 2; fi; fi; done\n"+
		"exec "+shellQuote(realGit)+" \"$@\"\n")
	if err := os.Chmod(wrapper, 0o755); err != nil {
		t.Fatal(err)
	}
	usePatchedForkGit(t, wrapper)
	if rc, _, errw := packVerb(t, "update"); rc == 0 || !strings.Contains(errw, "patch application failed at upstream v1.2.0 ("+v12+")") {
		t.Fatalf("fixture did not persist its transient target failure: rc=%d\n%s", rc, errw)
	}
	v13 := fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 20: "twenty", 21: "twenty-one"})
	f, series := fx.fork(t), mustSeries(t, fx)
	store := &packsrc.Store{Dir: paths.PacksDir()}
	previousLoad := explicitPostRetryRecordLoad
	injected := false
	explicitPostRetryRecordLoad = func(actual *packsrc.Store, fork packload.Fork, s *packsrc.Series) (*packsrc.CheckRecord, error) {
		record, err := actual.LoadCheckRecord(fork.Key())
		if err != nil {
			return nil, err
		}
		record.Seq++
		check := *record.Check
		check.Seq, check.At = record.Seq, check.At+1
		record.Check = &check
		if len(check.List) == 0 || check.List[0].Commit != v13 {
			return nil, fmt.Errorf("later finished check lost shared newer candidate %s", v13)
		}
		record.PatchFailure = &packsrc.PatchFailure{Owner: fork.Key(), Inputs: record.Read, Series: s.Digest,
			Target: check.List[0], Kind: "conflict", Member: "later-authority.patch", Paths: []string{"later.txt"}, Seq: record.Seq}
		data, err := json.Marshal(record)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(actual.CheckRecordPath(fork.Key()), data, 0o600); err != nil {
			return nil, err
		}
		injected = true
		return previousLoad(actual, fork, s)
	}
	t.Cleanup(func() { explicitPostRetryRecordLoad = previousLoad })
	before := fx.record(t).Good
	rc, out, errw := packVerb(t, "update")
	if !injected || rc == 0 || !strings.Contains(errw, "Patch: later-authority.patch") ||
		strings.Contains(errw, "Patch: 0001-ten.patch") || strings.Contains(out, "applies — "+patchedNotBuilt) {
		t.Fatalf("later current failure was not consumed as new authority: injected=%v rc=%d\n%s\n%s", injected, rc, out, errw)
	}
	current, err := store.LoadCheckRecord(f.Key())
	if err != nil || current.PatchFailure == nil || current.PatchFailure.Member != "later-authority.patch" ||
		current.PatchFailure.Target.Commit != v13 || current.Good == nil || current.Good.Entry != before.Entry {
		t.Fatalf("later authority was overwritten or older failure promoted: record=%+v err=%v", current, err)
	}
	if failure := current.CurrentPatchFailure(current.Read, series.Digest); failure == nil || failure.Member != "later-authority.patch" {
		t.Fatalf("core no longer recognizes the newer failure as current: %+v", failure)
	}
}

type errorSignalWriter struct {
	mu         sync.Mutex
	buf        bytes.Buffer
	signal     chan struct{}
	signalOnce sync.Once
}

func (w *errorSignalWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	n, err := w.buf.Write(p)
	w.mu.Unlock()
	if bytes.Contains(p, []byte("patch application failed")) {
		w.signalOnce.Do(func() { close(w.signal) })
	}
	return n, err
}

func (w *errorSignalWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// The actual typed operation error must reach errw while this owned record lock is still held,
// before RecordReplay can block. FIFO handshakes order replay and lock acquisition without sleeps.
func TestPackUpdateReportsTypedFailureBeforeBlockingRecordReplay(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx, _ := patchedActorLaunch(t)
	v12 := fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	f := fx.fork(t)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	ipc := t.TempDir()
	readyFIFO, resumeFIFO := filepath.Join(ipc, "ready"), filepath.Join(ipc, "resume")
	if err := exec.Command("mkfifo", readyFIFO, resumeFIFO).Run(); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	wrapper := filepath.Join(ipc, "git")
	writeFile(t, wrapper, "#!/bin/sh\n"+
		"is_merge=0; for a in \"$@\"; do [ \"$a\" = merge-tree ] && is_merge=1; done\n"+
		"if [ $is_merge -eq 1 ]; then "+shellQuote(realGit)+" \"$@\"; rc=$?; printf 'go\\n' > "+shellQuote(readyFIFO)+"; IFS= read -r token < "+shellQuote(resumeFIFO)+"; exit $rc; fi\n"+
		"exec "+shellQuote(realGit)+" \"$@\"\n")
	if err := os.Chmod(wrapper, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	previousStore := patchedForkStore
	patchedForkStore = func() *packsrc.Store {
		s := previousStore()
		s.Git, s.Ctx = wrapper, ctx
		return s
	}
	defer func() { patchedForkStore = previousStore }()
	store := patchedForkStore()
	if store.NoWait {
		t.Fatal("record-lock regression must keep NoWait unchanged")
	}
	ready := make(chan error, 1)
	go func() {
		fifo, err := os.Open(readyFIFO)
		if err != nil {
			ready <- err
			return
		}
		defer fifo.Close()
		var token [3]byte
		_, err = io.ReadFull(fifo, token[:])
		ready <- err
	}()
	var out bytes.Buffer
	errw := &errorSignalWriter{signal: make(chan struct{})}
	type commandResult struct{ rc int }
	done := make(chan commandResult, 1)
	commandFinished := false
	go func() { done <- commandResult{rc: packMain([]string{"update"}, &out, errw, false)} }()
	lockEntered, releaseLock := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unlock := func() { releaseOnce.Do(func() { close(releaseLock) }) }
	wrapperPaused := false
	wrapperResumed := false
	resumeWrapper := func() {
		if !wrapperPaused || wrapperResumed {
			return
		}
		fifo, err := os.OpenFile(resumeFIFO, os.O_WRONLY, 0)
		if err == nil {
			_, _ = fifo.Write([]byte("go\\n"))
			_ = fifo.Close()
			wrapperResumed = true
		}
	}
	defer func() {
		resumeWrapper()
		unlock()
		cancel()
		if !commandFinished {
			select {
			case <-done:
			case <-time.After(2 * time.Second):
			}
		}
	}()
	waitCtx, waitCancel := context.WithTimeout(ctx, 6*time.Second)
	defer waitCancel()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatal(err)
		}
		wrapperPaused = true
	case <-waitCtx.Done():
		t.Fatal("replay command did not reach its FIFO checkpoint")
	}
	lockDone := make(chan error, 1)
	go func() {
		lockDone <- store.WithCheckRecord(f.Key(), nil, func(_ *packsrc.CheckRecord, readErr error, _ func() error) (bool, error) {
			if readErr != nil {
				return false, readErr
			}
			close(lockEntered)
			<-releaseLock
			return false, nil
		})
	}()
	select {
	case <-lockEntered:
	case <-waitCtx.Done():
		unlock()
		t.Fatal("test did not acquire the owned check-record lock")
	}
	resumeWrapper()
	reportedBeforeUnlock := false
	reportCtx, reportCancel := context.WithTimeout(ctx, testsupport.ReadinessBudget(t))
	select {
	case <-errw.signal:
		reportedBeforeUnlock = true
	case <-reportCtx.Done():
	}
	reportCancel()
	unlock()
	doneCtx, doneCancel := context.WithTimeout(ctx, 6*time.Second)
	defer doneCancel()
	select {
	case err := <-lockDone:
		if err != nil {
			t.Errorf("release held check record: %v", err)
		}
	case <-doneCtx.Done():
		t.Error("owned check-record lock did not release")
	}
	var result commandResult
	select {
	case result = <-done:
		commandFinished = true
	case <-doneCtx.Done():
		t.Fatal("explicit update did not finish after record lock release")
	}
	if !reportedBeforeUnlock || result.rc == 0 ||
		strings.Count(errw.String(), "ERROR: forkpack/tool: patch application failed at upstream v1.2.0 ("+v12+")") != 1 {
		t.Fatalf("typed operation error did not precede persistence exactly once: before-unlock=%v rc=%d\n%s\n%s",
			reportedBeforeUnlock, result.rc, out.String(), errw.String())
	}
}

// A compatible foreground literal-1 bypass makes capture a deliberate skip, not a fake build or
// a failed capture. The original pre-3P1 assertion that required nonzero is retained in the
// evidence directory as a contract mismatch, not treated as a valid behavior test.
func TestCaptureLiteralBypassDeclinesConflictingSubject(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "1")
	fx, oldKey, v12 := explicitGoodAndConflict(t)
	beforeBuilds := len(fx.builds)
	var out, errw bytes.Buffer
	rc := captureHost([]string{"tool"}, &out, &errw, false)
	if rc != 0 || !strings.Contains(out.String(), "capture skipped: explicit patch-failure bypass") ||
		!strings.Contains(errw.String(), "patch application failed at upstream v1.2.0 ("+v12+")") {
		t.Fatalf("capture did not report a deliberate non-build skip: rc=%d\nout=%s\nerr=%s", rc, out.String(), errw.String())
	}
	good := fx.record(t).Good
	series, err := fx.fork(t).ReadSeries()
	if err != nil {
		t.Fatal(err)
	}
	record := fx.record(t)
	if len(fx.builds) != beforeBuilds || good == nil || good.Entry != oldKey ||
		record.CurrentPatchFailure(record.Read, series.Digest) == nil {
		t.Fatalf("capture skip built, moved Good, or erased failure: builds=%d/%d good=%+v\n%s\n%s",
			len(fx.builds), beforeBuilds, good, out.String(), errw.String())
	}
}

// Capture accepts the explicit skip only for literal one; unset, 0 and true remain refusals.
func TestCapturePatchFailureBypassNeedsLiteralOne(t *testing.T) {
	for _, tc := range []struct{ name, bypass string }{{"unset", ""}, {"zero", "0"}, {"true", "true"}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("YOLO_ALLOW_PATCH_FAILURES", tc.bypass)
			fx, oldKey, v12 := explicitGoodAndConflict(t)
			beforeBuilds := len(fx.builds)
			var out, errw bytes.Buffer
			rc := captureHost([]string{"tool"}, &out, &errw, false)
			if rc == 0 || strings.Contains(out.String(), "capture skipped: explicit patch-failure bypass") ||
				!strings.Contains(errw.String(), "patch application failed at upstream v1.2.0 ("+v12+")") {
				t.Fatalf("bypass %q was accepted: rc=%d\nout=%s\nerr=%s", tc.bypass, rc, out.String(), errw.String())
			}
			if len(fx.builds) != beforeBuilds || fx.record(t).Good == nil || fx.record(t).Good.Entry != oldKey {
				t.Fatalf("refused capture built or moved Good: builds=%d/%d record=%+v", len(fx.builds), beforeBuilds, fx.record(t))
			}
		})
	}
}

// Literal-one cannot turn capture into success without a serving Good, or after Good was reaped or
// its recipe changed. Ordinary check failures remain refusals too.
func TestCapturePatchFailureBypassRequiresCurrentGoodAndSuccessfulCheck(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(*testing.T, *patchedAdvanceFixture)
	}{
		{name: "reaped", prepare: func(t *testing.T, fx *patchedAdvanceFixture) {
			good := fx.record(t).Good
			if good == nil {
				t.Fatal("fixture has no Good")
			}
			if err := os.RemoveAll(filepath.Join(paths.CapturesDir(), "entries", good.Entry)); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "changed-recipe", prepare: func(t *testing.T, fx *patchedAdvanceFixture) {
			data, err := os.ReadFile(fx.manifest)
			if err != nil {
				t.Fatal(err)
			}
			changed := strings.Replace(string(data), `"build":"sh build.sh"`, `"build":"sh build.sh --changed"`, 1)
			if changed == string(data) {
				t.Fatal("fixture build recipe was not found")
			}
			writeFile(t, fx.manifest, changed)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "1")
			fx, _, v12 := explicitGoodAndConflict(t)
			tc.prepare(t, fx)
			beforeBuilds := len(fx.builds)
			var out, errw bytes.Buffer
			rc := captureHost([]string{"tool"}, &out, &errw, false)
			if rc == 0 || strings.Contains(out.String(), "capture skipped: explicit patch-failure bypass") ||
				!strings.Contains(errw.String(), "patch application failed at upstream v1.2.0 ("+v12+")") {
				t.Fatalf("literal bypass accepted %s Good: rc=%d\nout=%s\nerr=%s", tc.name, rc, out.String(), errw.String())
			}
			if len(fx.builds) != beforeBuilds {
				t.Fatalf("capture built after %s Good stopped resolving", tc.name)
			}
		})
	}
}

func TestCapturePatchFailureBypassWithoutGoodFails(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "1")
	f := newPatchedFixture(t, "")
	f.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	v12 := f.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	var out, errw bytes.Buffer
	if rc := captureHost([]string{"tool"}, &out, &errw, false); rc == 0 ||
		strings.Contains(out.String(), "capture skipped: explicit patch-failure bypass") ||
		!strings.Contains(errw.String(), "patch application failed at upstream v1.2.0 ("+v12+")") {
		t.Fatalf("literal bypass accepted capture without Good: rc=%d\nout=%s\nerr=%s", rc, out.String(), errw.String())
	}
}

func TestCapturePatchFailureBypassDoesNotHideFetchFailure(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "1")
	fx, oldKey := patchedActorLaunch(t)
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	wrapper := filepath.Join(t.TempDir(), "git")
	writeFile(t, wrapper, "#!/bin/sh\nfor a in \"$@\"; do if [ \"$a\" = fetch ]; then echo fetch-failure >&2; exit 41; fi; done\nexec "+shellQuote(realGit)+" \"$@\"\n")
	if err := os.Chmod(wrapper, 0o755); err != nil {
		t.Fatal(err)
	}
	usePatchedForkGit(t, wrapper)
	var out, errw bytes.Buffer
	if rc := captureHost([]string{"tool"}, &out, &errw, false); rc == 0 ||
		strings.Contains(out.String(), "capture skipped: explicit patch-failure bypass") ||
		!strings.Contains(errw.String(), "fetch-failure") {
		t.Fatalf("literal bypass hid an ordinary fetch failure: rc=%d\nout=%s\nerr=%s", rc, out.String(), errw.String())
	}
	if fx.record(t).Good == nil || fx.record(t).Good.Entry != oldKey {
		t.Fatalf("fetch failure moved Good: %+v", fx.record(t).Good)
	}
}

func TestCapturePatchFailureBypassDoesNotHideCachedFetchFailureProgram(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx, oldKey, v12 := explicitGoodAndConflict(t)
	if rc, out, errw := packVerb(t, "update"); rc == 0 ||
		!strings.Contains(errw, "patch application failed at upstream v1.2.0 ("+v12+")") {
		t.Fatalf("explicit update did not record the cached conflict: rc=%d\n%s\n%s", rc, out, errw)
	}
	before := fx.record(t)
	series, err := fx.fork(t).ReadSeries()
	if err != nil {
		t.Fatal(err)
	}
	if before.Good == nil || before.Good.Entry != oldKey || before.Check == nil || before.Check.FetchErr != "" ||
		len(before.Check.List) == 0 || before.CurrentPatchFailure(before.Read, series.Digest) == nil ||
		before.CurrentPatchFailure(before.Read, series.Digest).Target.Commit != v12 {
		t.Fatalf("explicit update did not leave a cached failed candidate and compatible Good: %+v", before)
	}
	mirrors, err := filepath.Glob(filepath.Join(paths.PacksDir(), "mirrors", "*"))
	if err != nil || len(mirrors) == 0 {
		t.Fatalf("explicit update did not populate the source mirror: %v err=%v", mirrors, err)
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	wrapper := filepath.Join(t.TempDir(), "git")
	writeFile(t, wrapper, "#!/bin/sh\nfor a in \"$@\"; do if [ \"$a\" = fetch ]; then echo fetch-failure >&2; exit 41; fi; done\nexec "+shellQuote(realGit)+" \"$@\"\n")
	if err := os.Chmod(wrapper, 0o755); err != nil {
		t.Fatal(err)
	}
	usePatchedForkGit(t, wrapper)
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "1")
	builds := len(fx.builds)
	var out, errw bytes.Buffer
	rc := captureHost([]string{"tool"}, &out, &errw, false)
	if rc == 0 || strings.Contains(out.String(), "capture skipped: explicit patch-failure bypass") ||
		!strings.Contains(errw.String(), "fetch-failure") {
		t.Fatalf("cached conflict bypass concealed the failed forced fetch: rc=%d\nout=%s\nerr=%s", rc, out.String(), errw.String())
	}
	after := fx.record(t)
	series, err = fx.fork(t).ReadSeries()
	if err != nil {
		t.Fatal(err)
	}
	if len(fx.builds) != builds || after.Good == nil || after.Good.Entry != oldKey || after.Check == nil ||
		!strings.Contains(after.Check.FetchErr, "fetch-failure") ||
		after.CurrentPatchFailure(after.Read, series.Digest) == nil ||
		after.CurrentPatchFailure(after.Read, series.Digest).Target.Commit != v12 {
		t.Fatalf("failed-fetch capture built, moved Good, or erased typed failure: builds=%d/%d record=%+v\n%s\n%s",
			len(fx.builds), builds, after, out.String(), errw.String())
	}
}

func TestCapturePatchFailureBypassDoesNotHideCachedFetchFailureTree(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx := newTreesFixture(t, 1)
	key := fx.trees[0].Key()
	initial, _ := fx.slot(t, "podman", 1, nil)
	if initial[key].Dir == "" {
		t.Fatalf("fixture did not admit an initial tree build: %+v", initial[key])
	}
	store := patchedForkStore()
	before, err := store.LoadCheckRecord(key)
	if err != nil || before.Good == nil {
		t.Fatalf("initial tree build did not record Good: %+v err=%v", before, err)
	}
	v12 := fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	if rc, out, errw := packVerb(t, "update"); rc == 0 ||
		!strings.Contains(errw, "patch application failed at upstream v1.2.0 ("+v12+")") {
		t.Fatalf("explicit update did not record the tree conflict: rc=%d\n%s\n%s", rc, out, errw)
	}
	before, err = store.LoadCheckRecord(key)
	series, seriesErr := fx.trees[0].ReadSeries()
	if err != nil || seriesErr != nil || before.Good == nil || before.Good.Entry == "" || before.Check == nil ||
		before.Check.FetchErr != "" || len(before.Check.List) == 0 ||
		before.CurrentPatchFailure(before.Read, series.Digest) == nil ||
		before.CurrentPatchFailure(before.Read, series.Digest).Target.Commit != v12 {
		t.Fatalf("explicit tree update did not leave cached conflict and compatible Good: record=%+v err=%v series=%v", before, err, seriesErr)
	}
	mirrors, err := filepath.Glob(filepath.Join(paths.PacksDir(), "mirrors", "*"))
	if err != nil || len(mirrors) == 0 {
		t.Fatalf("explicit update did not populate the tree source mirror: %v err=%v", mirrors, err)
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	wrapper := filepath.Join(t.TempDir(), "git")
	writeFile(t, wrapper, "#!/bin/sh\nfor a in \"$@\"; do if [ \"$a\" = fetch ]; then echo fetch-failure >&2; exit 41; fi; done\nexec "+shellQuote(realGit)+" \"$@\"\n")
	if err := os.Chmod(wrapper, 0o755); err != nil {
		t.Fatal(err)
	}
	usePatchedForkGit(t, wrapper)
	previousRun := captureRunPipeline
	var runs int
	captureRunPipeline = func(opts run.Options) int {
		runs++
		return previousRun(opts)
	}
	t.Cleanup(func() { captureRunPipeline = previousRun })
	beforeRuns := runs
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "1")
	var out, errw bytes.Buffer
	rc := captureHost([]string{"treespack/ext-1"}, &out, &errw, false)
	if rc == 0 || strings.Contains(out.String(), "capture skipped: explicit patch-failure bypass") ||
		!strings.Contains(errw.String(), "fetch-failure") {
		t.Fatalf("cached tree conflict bypass concealed the failed forced fetch: rc=%d\nout=%s\nerr=%s", rc, out.String(), errw.String())
	}
	after, err := store.LoadCheckRecord(key)
	series, seriesErr = fx.trees[0].ReadSeries()
	if err != nil || seriesErr != nil || runs != beforeRuns || after.Good == nil ||
		after.Good.Entry != before.Good.Entry || after.Check == nil ||
		!strings.Contains(after.Check.FetchErr, "fetch-failure") ||
		after.CurrentPatchFailure(after.Read, series.Digest) == nil ||
		after.CurrentPatchFailure(after.Read, series.Digest).Target.Commit != v12 {
		t.Fatalf("failed-fetch tree capture built, moved Good, or erased typed failure: before=%+v after=%+v runs=%d/%d err=%v series=%v\n%s\n%s",
			before.Good, after.Good, runs, beforeRuns, err, seriesErr, out.String(), errw.String())
	}
}

// The real tree-key route through captureHost has the same explicit skip disposition as a program.
func TestCapturePatchedExtensionBypassSkipsWithoutBuilding(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	fx := newTreesFixture(t, 1)
	key := fx.trees[0].Key()
	initial, _ := fx.slot(t, "podman", 1, nil)
	if initial[key].Dir == "" {
		t.Fatalf("fixture did not admit an initial tree build: %+v", initial[key])
	}
	store := &packsrc.Store{Dir: paths.PacksDir()}
	before, err := store.LoadCheckRecord(key)
	if err != nil || before.Good == nil {
		t.Fatalf("initial tree build did not record Good: %+v err=%v", before, err)
	}
	v12 := fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	previousRun := captureRunPipeline
	var runs int
	captureRunPipeline = func(opts run.Options) int {
		runs++
		return previousRun(opts)
	}
	t.Cleanup(func() { captureRunPipeline = previousRun })
	beforeRuns := runs
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "1")
	var out, errw bytes.Buffer
	rc := captureHost([]string{"treespack/ext-1"}, &out, &errw, false)
	if rc != 0 || !strings.Contains(out.String(), "capture skipped: explicit patch-failure bypass") ||
		!strings.Contains(errw.String(), "patch application failed at upstream v1.2.0 ("+v12+")") {
		t.Fatalf("tree-key capture did not report a deliberate non-build skip: rc=%d\nout=%s\nerr=%s", rc, out.String(), errw.String())
	}
	after, err := store.LoadCheckRecord(key)
	if err != nil || after.Good == nil || after.Good.Entry != before.Good.Entry || runs != beforeRuns {
		t.Fatalf("tree capture skip built, admitted, or moved Good: before=%+v after=%+v runs=%d/%d err=%v",
			before.Good, after.Good, runs, beforeRuns, err)
	}
	series, err := fx.trees[0].ReadSeries()
	if err != nil {
		t.Fatal(err)
	}
	if after.CurrentPatchFailure(after.Read, series.Digest) == nil {
		t.Fatalf("tree capture bypass erased the current typed failure: %+v", after)
	}
}

// A CONFLICT AT THE NEWEST VERSION is fatal to this explicit check: it is recorded against the
// original check snapshot, and no older fit is treated as accepted after application failed.
func TestPackUpdateStopsOnAConflictBeforeOlderFit(t *testing.T) {
	f := newPatchedFixture(t, "")
	v11 := f.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	v12 := f.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	rc, out, errw := packVerb(t, "update")
	if rc == 0 {
		t.Fatalf("update accepted an older fit after the newest candidate failed:\n%s\n%s", out, errw)
	}
	for _, w := range []string{
		"ERROR: forkpack/tool: patch application failed at upstream v1.2.0 (" + v12 + ")",
		"Patch: 0001-ten.patch", "Conflict: f.txt", "Operation stopped; no older fit or base will be built.",
	} {
		if !strings.Contains(errw, w) {
			t.Errorf("update error lacks %q:\n%s\n%s", w, out, errw)
		}
	}
	if strings.Contains(out+errw, "newest fit, upstream v1.1.0 ("+shortSHA(v11)+") takes the series") ||
		strings.Contains(out+errw, "applies — "+patchedNotBuilt) {
		t.Errorf("update reported an older fit as accepted after the conflict:\n%s\n%s", out, errw)
	}
	series, err := packsrc.ReadSeries(f.forkDir, "patches")
	if err != nil {
		t.Fatal(err)
	}
	record := patchedRecord(t)
	failure := record.CurrentPatchFailure(record.Read, series.Digest)
	if failure == nil || failure.Target.Commit != v12 || failure.Member != "0001-ten.patch" ||
		len(failure.Paths) != 1 || failure.Paths[0] != "f.txt" {
		t.Fatalf("explicit update did not persist the original candidate's typed failure: %+v", record)
	}
	_, sout, serr := packVerb(t, "status")
	if !strings.Contains(sout+serr, "does not take") {
		t.Fatalf("status lost the recorded conflict:\n%s\n%s", sout, serr)
	}
}

// A conflict is fatal even when the series' base itself is clean: explicit update must not continue
// down the list or relabel the application failure as a no-fit/base fallback.
func TestPackUpdateConflictDoesNotFallBackToBase(t *testing.T) {
	f := newPatchedFixture(t, "")
	upstreamGit(t, f.repo, "tag", "-d", "v1.0.0")
	v11 := f.commit(t, "v1.1.0", map[int]string{11: "eleven"})
	rc, out, errw := packVerb(t, "update")
	if rc == 0 || !strings.Contains(errw, "patch application failed at upstream v1.1.0 ("+v11+")") {
		t.Fatalf("update did not stop at the actual application failure: rc=%d\n%s\n%s", rc, out, errw)
	}
	if strings.Contains(out+errw, "the series' base "+shortSHA(f.base)+" takes the series") ||
		strings.Contains(out+errw, "no upstream version on the list takes the series") {
		t.Fatalf("update presented the base as fallback after an actual conflict:\n%s\n%s", out, errw)
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

// AN EDIT TO WHAT THE FORK FOLLOWS, since its last check, leaves status naming no stale candidate:
// the record's list answers the old rule (§4.2), so status says what changed and names the act that
// checks it now.
func TestPackStatusAfterAnEditNamesNoStaleCandidate(t *testing.T) {
	f := newPatchedFixture(t, "")
	f.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	if rc, out, errw := packVerb(t, "update"); rc != 0 {
		t.Fatalf("update rc=%d\n%s\n%s", rc, out, errw)
	}
	f.writeManifest(t, "main", "head")
	_, out, _ := packVerb(t, "status")
	if !strings.Contains(out, "follow rule changed since the last check") ||
		!strings.Contains(out, "`yolo pack update` checks it now") {
		t.Errorf("status after an edited follow does not say the record is the old rule's:\n%s", out)
	}
	if strings.Contains(out, "candidate: v1.1.0") {
		t.Errorf("status shows the release rule's candidate under head:\n%s", out)
	}
}

// A TAGLESS UPSTREAM UNDER THE DEFAULT RULE builds the series' base (PF-D60, amending PF-D27), as a
// branch whose versions all predate the base does, and never silently: update replays the base and
// says the series stays there until a tag appears or `follow` changes, naming `follow: "head"`, and
// status says the same whenever it is asked.
func TestPackUpdateOnATaglessUpstreamReplaysTheBaseAndSaysWhy(t *testing.T) {
	f := newPatchedFixture(t, "")
	upstreamGit(t, f.repo, "tag", "-d", "v1.0.0")
	f.commitMsg(t, "untagged one", "", map[int]string{14: "fourteen"})
	f.commitMsg(t, "untagged two", "", map[int]string{14: "fourteen", 20: "twenty"})
	rc, out, errw := packVerb(t, "update")
	if rc != 0 {
		t.Errorf("update rc=%d on a tagless branch, whose base builds:\n%s\n%s", rc, out, errw)
	}
	stays := "so what runs stays at the series' base " + shortSHA(f.base) + " until a tag appears or `follow` changes"
	for _, w := range []string{"fork forkpack/tool: ?ref=main of ", "carries no version tag that `follow: \"release\"` reads",
		stays, "`follow: \"head\"` follows the branch's commits",
		"the series' base " + shortSHA(f.base) + " (no version of the branch contains it) takes the series"} {
		if !strings.Contains(out, w) {
			t.Errorf("update lacks %q:\n%s\n%s", w, out, errw)
		}
	}
	_, out, _ = packVerb(t, "status")
	for _, w := range []string{"carries no version tag that `follow: \"release\"` reads", stays,
		"`follow: \"head\"` follows the branch's commits"} {
		if !strings.Contains(out, w) {
			t.Errorf("status lacks %q:\n%s", w, out)
		}
	}
}

// VERSIONS THAT ALL PREDATE THE BASE are an empty list, not a ref problem: the first advance's
// fallback is the series' base, which update names as the base and not as an upstream version.
func TestPackUpdateNamesTheBaseFallbackAsTheBase(t *testing.T) {
	f := newPatchedFixture(t, "")
	upstreamGit(t, f.repo, "tag", "-d", "v1.0.0")
	// A version on a history the base is not on, merged into main: merged, and older than the base.
	upstreamGit(t, f.repo, "checkout", "-q", "--orphan", "old")
	upstreamGit(t, f.repo, "rm", "-rqf", ".")
	writeFile(t, filepath.Join(f.repo, "old.txt"), "old\n")
	upstreamGit(t, f.repo, "add", "-A")
	upstreamGit(t, f.repo, "commit", "-qm", "old line")
	upstreamGit(t, f.repo, "tag", "v0.9.0")
	upstreamGit(t, f.repo, "checkout", "-q", "main")
	upstreamGit(t, f.repo, "merge", "-q", "--allow-unrelated-histories", "-m", "merge old", "old")
	rc, out, errw := packVerb(t, "update")
	if rc != 0 {
		t.Fatalf("update rc=%d\n%s\n%s", rc, out, errw)
	}
	if want := "the series' base " + shortSHA(f.base) + " (no version of the branch contains it) takes the series"; !strings.Contains(out, want) {
		t.Errorf("update lacks %q:\n%s", want, out)
	}
	if strings.Contains(out, "upstream "+shortSHA(f.base)) {
		t.Errorf("update labels the series' base as an upstream version:\n%s", out)
	}
}

// EVERY VERB A PATCHED FORK'S LINES NAME EXISTS: update's and status's lines, a conflict's included,
// name only `yolo pack` verbs this yolo dispatches — a conflict's `yolo pack rebase` among them —
// since following a step that names a verb this yolo lacks would only fail.
func TestPatchedForkLinesNameOnlyVerbsThatExist(t *testing.T) {
	f := newPatchedFixture(t, "")
	f.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	f.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	_, out, errw := packVerb(t, "update")
	_, sout, serr := packVerb(t, "status")
	all := out + errw + sout + serr
	if !strings.Contains(errw, "patch application failed") || !strings.Contains(sout, "does not take") {
		t.Fatalf("the explicit conflict or status line was missing:\n%s\n%s\n%s", all, sout, serr)
	}
	if !strings.Contains(all, "yolo pack rebase forkpack/tool") {
		t.Errorf("the conflict's next step does not name `yolo pack rebase`:\n%s", all)
	}
	for _, m := range regexp.MustCompile("yolo pack ([a-z-]+)").FindAllStringSubmatch(all, -1) {
		switch m[1] {
		case "install", "update", "status":
			continue
		}
		var o, e bytes.Buffer
		packMain([]string{m[1]}, &o, &e, false)
		if strings.Contains(e.String(), "unknown verb") {
			t.Errorf("a patched fork's line names `yolo pack %s`, which this yolo does not have:\n%s", m[1], all)
		}
	}
}

// A TAG OR A COMMIT ?ref= IS A HOLD (§3.3, §3.4): status says the fork is held there and follows
// nothing, never "following release".
func TestPackStatusSaysARefHolds(t *testing.T) {
	for _, tc := range []struct{ name, kind string }{{"tag", "tag"}, {"commit", "commit"}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPatchedFixture(t, "")
			ref := "v1.0.0"
			if tc.kind == "commit" {
				ref = f.base
			}
			f.writeManifest(t, ref, "")
			if rc, out, errw := packVerb(t, "update"); rc != 0 {
				t.Fatalf("update rc=%d\n%s\n%s", rc, out, errw)
			}
			_, out, _ := packVerb(t, "status")
			for _, w := range []string{"held at that " + tc.kind, "its ?ref= names a " + tc.kind + ", so it follows nothing"} {
				if !strings.Contains(out, w) {
					t.Errorf("status lacks %q:\n%s", w, out)
				}
			}
			if strings.Contains(out, "following release") {
				t.Errorf("status says a %s hold follows release:\n%s", tc.kind, out)
			}
		})
	}
}

// AGENT_UPDATES HOLDS A PATCHED FORK, through its own pack or its base (PF-D19): update still checks
// it, as a human asked, and says the hold; status names it.
func TestAgentUpdatesHoldIsSaidAtUpdateAndStatus(t *testing.T) {
	for _, pack := range []string{"forkpack", "basepack"} {
		t.Run(pack, func(t *testing.T) {
			f := newPatchedFixture(t, "")
			f.writeUserConfig(t, `,"agent_updates":{"`+pack+`":false}`)
			rc, out, errw := packVerb(t, "update")
			if rc != 0 {
				t.Fatalf("update rc=%d\n%s\n%s", rc, out, errw)
			}
			if want := "`agent_updates` holds pack " + pack + ": no launch checks it"; !strings.Contains(out, want) {
				t.Errorf("update lacks %q:\n%s", want, out)
			}
			_, out, _ = packVerb(t, "status")
			if want := "held: `agent_updates` holds pack " + pack + ", so no launch checks it"; !strings.Contains(out, want) {
				t.Errorf("status lacks %q:\n%s", want, out)
			}
		})
	}
}
