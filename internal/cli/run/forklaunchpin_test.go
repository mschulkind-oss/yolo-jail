package run

// forklaunchpin_test.go pins THE LAUNCH'S PIN at its call site, Run's own entry point
// (docs/design/forked-programs-as-packs.md FP-D18, applying the maintainer's OQ-PF1: "yolo pack
// install and update still exist, but neither is required"). A launch that carries a fork the fork
// lock does not pin for its declared source pins it, once, says so, and builds that commit; a
// STANDING pin never moves at launch, however far its branch has moved; a pin a launch cannot make
// is that fork's reason, with the next step; a dry run pins nothing. Each fork's "remote" is a real
// local git repository (git+file://), so nothing here reaches the network. A standing pin whose
// commit this machine never fetched is the build act's to fetch (cli's forkpin_test.go).

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

// launchForkRepo is a real git repository with one commit on main, and a func that commits again
// and returns the new HEAD.
func launchForkRepo(t *testing.T) (dir string, commit func(msg string) string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir = t.TempDir()
	git := func(args ...string) string {
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
	git("init", "-q", "-b", "main")
	commit = func(msg string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "build.sh"), []byte("# "+msg+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		git("add", "-A")
		git("commit", "-qm", msg)
		return git("rev-parse", "HEAD")
	}
	commit("first")
	return dir, commit
}

// forkLockEntry is what the fork lock holds for forkpack/tool, and whether it holds anything.
func forkLockEntry(t *testing.T) (packsrc.ForkLockEntry, bool) {
	t.Helper()
	l, err := packsrc.LoadForkLock(packsrc.ForkLockPath(paths.UserConfigPath()))
	if err != nil {
		t.Fatal(err)
	}
	return l.Get("forkpack/tool")
}

// recordingBuild is a build act that records the pins it is handed and answers with key k1.
func recordingBuild(got *[]packload.ForkPin) func(*Options) {
	return func(o *Options) {
		o.BuildForks = func(req ForkBuildRequest) map[string]entrypoint.ForkDelivery {
			pins := req.Pins
			*got = append(*got, pins...)
			return map[string]entrypoint.ForkDelivery{"tool": {Key: "k1"}}
		}
	}
}

// pinnedLine is the disclosure a launch prints for a pin it made.
func pinnedLine(commit, source string) string {
	return "pinned fork forkpack/tool at " + commit[:8] + " (" + source + "); `yolo pack update` moves it"
}

// THE MOTIVATING CASE: a fork selected and never pinned. The launch pins it at what its ref names,
// writes the fork lock, says so in one line, and builds that commit — with no `yolo pack install`
// anywhere. Before FP-D18 this launch built nothing and told the user to run `yolo pack install`.
func TestALaunchPinsAnUnpinnedForkAndBuildsIt(t *testing.T) {
	repo, _ := launchForkRepo(t)
	head := strings.TrimSpace(mustGit(t, repo, "rev-parse", "HEAD"))
	source := "git+file://" + repo + "?ref=main"
	forkLaunchHome(t, source)
	var built []packload.ForkPin
	argv, printed := fakePodmanLaunch(t, recordingBuild(&built))

	e, ok := forkLockEntry(t)
	if !ok || e.Commit != head || e.Source != source || e.Ref != "main" {
		t.Fatalf("the fork lock holds %+v (%v), want forkpack/tool pinned at %s for %s\n%s", e, ok, head, source, printed)
	}
	if len(built) != 1 || built[0].Commit != head {
		t.Errorf("the build act was handed %+v, want the commit the launch pinned (%s)\n%s", built, head, printed)
	}
	if !strings.Contains(printed, pinnedLine(head, source)) {
		t.Errorf("the launch does not disclose the pin it made:\nwant %q\n%s", pinnedLine(head, source), printed)
	}
	if strings.Contains(printed, "yolo pack install") {
		t.Errorf("a launch that pinned its fork still names `yolo pack install`:\n%s", printed)
	}
	if d := forkBuildsInArgv(t, argv); d["tool"].Key != "k1" {
		t.Errorf("the jail is handed %+v, want tool's key k1", d)
	}
}

// A STANDING PIN NEVER MOVES AT LAUNCH: the branch moves on after the first launch pinned it, and
// the next launch builds the pinned commit, prints no pin line, and does not fetch — the mirror's
// branch still names the first commit. Moving it is `yolo pack update`'s act alone.
func TestALaunchNeverMovesAStandingForkPin(t *testing.T) {
	repo, commit := launchForkRepo(t)
	head1 := strings.TrimSpace(mustGit(t, repo, "rev-parse", "HEAD"))
	source := "git+file://" + repo + "?ref=main"
	forkLaunchHome(t, source)
	var built []packload.ForkPin
	if _, printed := fakePodmanLaunch(t, recordingBuild(&built)); !strings.Contains(printed, pinnedLine(head1, source)) {
		t.Fatalf("the first launch did not pin the fork:\n%s", printed)
	}
	lock := packsrc.ForkLockPath(paths.UserConfigPath())
	before, err := os.ReadFile(lock)
	if err != nil {
		t.Fatal(err)
	}

	head2 := commit("second")
	built = nil
	_, printed := fakePodmanLaunch(t, recordingBuild(&built))
	if e, _ := forkLockEntry(t); e.Commit != head1 {
		t.Errorf("a launch moved a standing pin to %s (want it left at %s)", e.Commit, head1)
	}
	if after, _ := os.ReadFile(lock); !bytes.Equal(before, after) {
		t.Errorf("a launch rewrote the fork lock of a standing pin:\n%s", after)
	}
	if len(built) != 1 || built[0].Commit != head1 {
		t.Errorf("the build act was handed %+v, want the standing pin %s, never the moved head %s", built, head1, head2)
	}
	if strings.Contains(printed, "pinned fork") {
		t.Errorf("a launch with a standing pin printed a pin line:\n%s", printed)
	}
	mirrors, _ := filepath.Glob(filepath.Join(paths.PacksDir(), "mirrors", "*"))
	if len(mirrors) != 1 {
		t.Fatalf("mirrors = %v, want the one the first launch fetched", mirrors)
	}
	if got := strings.TrimSpace(mustGit(t, mirrors[0], "rev-parse", "refs/heads/main")); got != head1 {
		t.Errorf("the second launch fetched the fork's source (its mirror's main is %s, want %s)", got, head1)
	}
}

// A STANDING PIN OUTLIVES THE HOURLY REFRESH: an hour has passed since the pin (its branch stamp is
// gone), and a fetch for something else sharing the fork's repository has moved the mirror's branch to
// the new head. A launch that re-resolved the ref by the launch's ref rule would now fetch, or read
// the moved mirror, and pin the new head; this one builds the pinned commit and leaves the lock alone.
func TestAStandingForkPinOutlivesTheHourlyRefresh(t *testing.T) {
	repo, commit := launchForkRepo(t)
	head1 := strings.TrimSpace(mustGit(t, repo, "rev-parse", "HEAD"))
	source := "git+file://" + repo + "?ref=main"
	forkLaunchHome(t, source)
	var built []packload.ForkPin
	if _, printed := fakePodmanLaunch(t, recordingBuild(&built)); !strings.Contains(printed, pinnedLine(head1, source)) {
		t.Fatalf("the first launch did not pin the fork:\n%s", printed)
	}
	lock := packsrc.ForkLockPath(paths.UserConfigPath())
	before, err := os.ReadFile(lock)
	if err != nil {
		t.Fatal(err)
	}
	head2 := commit("second")
	if err := os.RemoveAll(filepath.Join(paths.PacksDir(), "stamps")); err != nil {
		t.Fatal(err)
	}
	mirrors, _ := filepath.Glob(filepath.Join(paths.PacksDir(), "mirrors", "*"))
	if len(mirrors) != 1 {
		t.Fatalf("mirrors = %v, want the one the first launch fetched", mirrors)
	}
	mustGit(t, mirrors[0], "fetch", "-q", "origin", "+refs/heads/*:refs/heads/*")
	if got := strings.TrimSpace(mustGit(t, mirrors[0], "rev-parse", "refs/heads/main")); got != head2 {
		t.Fatalf("the mirror's main is %s after the fetch, want %s", got, head2)
	}

	built = nil
	_, printed := fakePodmanLaunch(t, recordingBuild(&built))
	if after, _ := os.ReadFile(lock); !bytes.Equal(before, after) {
		t.Errorf("a launch rewrote a standing pin after the hourly refresh:\n%s", after)
	}
	if len(built) != 1 || built[0].Commit != head1 {
		t.Errorf("the build act was handed %+v, want the standing pin %s, never the moved head %s", built, head1, head2)
	}
	if strings.Contains(printed, "pinned fork") {
		t.Errorf("a launch with a standing pin printed a pin line:\n%s", printed)
	}
}

// A PIN MADE FOR ANOTHER SOURCE IS NO PIN: an edited `source` is a new address, and the launch pins
// the address the manifest names now, in place of the old entry.
func TestALaunchPinsAForkWhoseSourceChanged(t *testing.T) {
	repo, _ := launchForkRepo(t)
	head := strings.TrimSpace(mustGit(t, repo, "rev-parse", "HEAD"))
	source := "git+file://" + repo + "?ref=main"
	forkLaunchHome(t, source)
	l := &packsrc.ForkLock{}
	l.Set(packsrc.ForkLockEntry{Key: "forkpack/tool", Source: "git+file:///gone/old?ref=main", Ref: "main",
		Commit: strings.Repeat("a", 40)})
	if err := l.Save(packsrc.ForkLockPath(paths.UserConfigPath())); err != nil {
		t.Fatal(err)
	}
	var built []packload.ForkPin
	_, printed := fakePodmanLaunch(t, recordingBuild(&built))
	if e, _ := forkLockEntry(t); e.Commit != head || e.Source != source {
		t.Errorf("the fork lock holds %+v, want the new source pinned at %s\n%s", e, head, printed)
	}
	if len(built) != 1 || built[0].Commit != head {
		t.Errorf("the build act was handed %+v, want %s", built, head)
	}
}

// A PIN THE LAUNCH CANNOT MAKE is that fork's reason, never a refused launch (§9: a broken fork is
// one missing tool): nothing is built, nothing is written to the fork lock, and the reason the jail
// receives names the failure and the next step.
func TestALaunchThatCannotPinAForkNamesTheNextStep(t *testing.T) {
	source := "git+file://" + filepath.Join(t.TempDir(), "absent") + "?ref=main"
	forkLaunchHome(t, source)
	var built []packload.ForkPin
	argv, printed := fakePodmanLaunch(t, recordingBuild(&built))
	if len(built) != 0 {
		t.Errorf("a fork the launch could not pin was handed to the build act: %+v", built)
	}
	if _, ok := forkLockEntry(t); ok {
		t.Error("a pin that failed was recorded in the fork lock")
	}
	reason := forkBuildsInArgv(t, argv)["tool"].Reason
	for _, want := range []string{"pinning it failed", "fix what that names and launch again"} {
		if !strings.Contains(reason, want) {
			t.Errorf("the jail's reason lacks %q: %q\n%s", want, reason, printed)
		}
	}
}

// A DRY RUN PINS NOTHING: it materializes nothing (which is why it skips the pack refresh too), so
// it reads the fork lock and names the unpinned fork with what pins it.
func TestADryRunLaunchPinsNoFork(t *testing.T) {
	repo, _ := launchForkRepo(t)
	forkLaunchHome(t, "git+file://"+repo+"?ref=main")
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, t.TempDir(), "macos-user", &stdout, &stderr, nil)
	o.DryRun = true
	o.MacosUserRun = func(*jsonx.OrderedMap, string, []string, []string, string, string, macosuser.HomeOverlay,
		macosuser.HostContext, bool, *jsonx.OrderedMap, []packload.BlockedTool, macosuser.JailDaemons) int {
		return 0
	}
	Run(*o)
	printed := stdout.String() + stderr.String()
	if _, ok := forkLockEntry(t); ok {
		t.Errorf("a dry run pinned a fork:\n%s", printed)
	}
	if _, err := os.Stat(filepath.Join(paths.PacksDir(), "mirrors")); !os.IsNotExist(err) {
		t.Errorf("a dry run fetched a fork's source (err %v)", err)
	}
	if !strings.Contains(printed, "it has no pin yet — the next launch pins it") {
		t.Errorf("a dry run does not say what pins the fork:\n%s", printed)
	}
}

// mustGit runs git in dir and returns its stdout.
func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = testsupport.HermeticGitEnv(packsrc.CleanGitEnv(os.Environ()))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v in %s: %v", args, dir, err)
	}
	return string(out)
}
