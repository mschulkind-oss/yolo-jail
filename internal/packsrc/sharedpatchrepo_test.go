package packsrc

// sharedpatchrepo_test.go pins that FORKS WHOSE PATCH BRANCHES SHARE ONE REPOSITORY do not queue behind each
// other's upstream probe at a launch (docs/design/local-pack-clones.md §4.2: concurrent across mirrors, one
// deadline covering only the upstream's lock wait, probe and fetch). A launch with several patched forks whose
// patch branches all live in one dotfiles repository once timed every check but the first out on "waiting for
// the mirror lock", because each check held the shared patch-branch mirror's lock across its own upstream's
// probe and fetch.

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// gatedProbeGit is a git shim whose ls-remote of one repository (match, a path inside its URL) announces
// itself on started and blocks until a line is written to release; every other run is the real git.
func gatedProbeGit(t *testing.T, match string) (git string, started *bufio.Reader, release *os.File) {
	t.Helper()
	dir := t.TempDir()
	fifo := func(name string) (string, *os.File) {
		p := filepath.Join(dir, name)
		if err := syscall.Mkfifo(p, 0o600); err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(p, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { f.Close() })
		return p, f
	}
	startedPath, startedF := fifo("started")
	releasePath, releaseF := fifo("release")
	script := "#!/bin/sh\nprobe=; hit=\nfor a in \"$@\"; do\n" +
		"  [ \"$a\" = ls-remote ] && probe=1\n" +
		"  case \"$a\" in *" + shquote.Quote(match) + "*) hit=1;; esac\ndone\n" +
		"if [ -n \"$probe\" ] && [ -n \"$hit\" ]; then echo started >> " + shquote.Quote(startedPath) +
		"; read _ < " + shquote.Quote(releasePath) + "; fi\n" +
		"exec " + shquote.Quote(storeGit()) + " \"$@\"\n"
	git = filepath.Join(dir, "git")
	if err := os.WriteFile(git, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return git, bufio.NewReader(startedF), releaseF
}

func TestAForkDoesNotWaitOnAnotherForksUpstreamProbeThroughASharedPatchRepository(t *testing.T) {
	a := newPatchedUpstream(t)
	b := newPatchedUpstream(t)
	b.store = a.store
	// ONE PATCH REPOSITORY carrying a branch for each fork, each started from its own upstream's base.
	patches := filepath.Join(t.TempDir(), "dotfiles")
	gitIn(t, filepath.Dir(patches), "clone", "-q", a.repo, patches)
	gitIn(t, patches, "checkout", "-q", "-b", "patch/a", a.base)
	commitFile(t, patches, "f.txt", thirtyLines(map[int]string{10: "a ten"}))
	gitIn(t, patches, "remote", "add", "b", b.repo)
	gitIn(t, patches, "fetch", "-q", "b")
	gitIn(t, patches, "checkout", "-q", "-b", "patch/b", b.base)
	commitFile(t, patches, "f.txt", thirtyLines(map[int]string{10: "b ten"}))
	// ITS OWN ORIGIN, as a dotfiles checkout has: neither upstream is a remote of it.
	origin := filepath.Join(t.TempDir(), "dotfiles-origin.git")
	gitIn(t, filepath.Dir(origin), "init", "-q", "--bare", origin)
	gitIn(t, patches, "remote", "remove", "b")
	gitIn(t, patches, "remote", "set-url", "origin", origin)
	gitIn(t, patches, "push", "-q", "origin", "patch/a", "patch/b")
	gitIn(t, patches, "fetch", "-q", "origin")
	wa := PatchedWant{Owner: "forkpack/a", Source: a.source("main"), PatchBranch: "git+file://" + patches + "?ref=patch/a"}
	wb := PatchedWant{Owner: "forkpack/b", Source: b.source("main"), PatchBranch: "git+file://" + patches + "?ref=patch/b"}
	for _, w := range []PatchedWant{wa, wb} {
		if p := a.check(t, w, true).Record.Check.Problem; p != "" {
			t.Fatalf("priming %s: %s", w.Owner, p)
		}
	}

	git, started, release := gatedProbeGit(t, a.repo)
	a.store.Git = git
	later := func() time.Time { return a.now.Add(time.Minute) }
	done := make(chan CheckResult, 1)
	go func() {
		done <- a.store.CheckPatched(wa, CheckOptions{Check: checkOf(context.Background()), Now: later})
	}()
	got := make(chan error, 1)
	go func() { _, err := started.ReadString('\n'); got <- err }()
	select {
	case err := <-got:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Minute):
		t.Fatal("fork a's upstream probe never started")
	}

	// FORK B'S DEADLINE FIRES THE MOMENT IT HAS TO WAIT FOR A LOCK: with fork a's probe in flight, any wait
	// at all is a wait behind fork a's upstream.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var waits []string
	res := a.store.CheckPatched(wb, CheckOptions{Check: checkOf(ctx), Now: later,
		Begin: func() (func(string), func()) {
			return func(line string) { waits = append(waits, line); cancel() }, func() {}
		}})
	if _, err := release.WriteString("go\n"); err != nil {
		t.Fatal(err)
	}
	ra := <-done
	if res.Err != nil || !res.Ran || res.Unchecked != "" || len(waits) != 0 {
		t.Fatalf("fork b's check = ran %v err %v unchecked %q waits %q, want a check that never waited on fork a",
			res.Ran, res.Err, res.Unchecked, waits)
	}
	if p := res.Record.Check.Problem; p != "" || res.Record.Check.FetchErr != "" {
		t.Errorf("fork b's check problem %q fetch error %q", p, res.Record.Check.FetchErr)
	}
	if ra.Err != nil || !ra.Ran || ra.Unchecked != "" || ra.Record.Check.Problem != "" {
		t.Errorf("fork a's check = ran %v err %v unchecked %q problem %q", ra.Ran, ra.Err, ra.Unchecked,
			ra.Record.Check.Problem)
	}
	if strings.Contains(strings.Join(waits, "\n"), "another yolo process") {
		t.Errorf("a wait inside this process was blamed on another process: %q", waits)
	}
}

// A WAIT SAYS WHO HOLDS THE LOCK: a sibling in this process is named as such, and a holder this process did not
// take through the store's flocks (another process, as far as it can tell) as another yolo process.
func TestALockWaitNamesWhetherTheHolderIsThisProcess(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	waitFor := func() string {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		c := *s
		c.Ctx = ctx
		var line string
		if _, err := c.lockMirror("file:///dotfiles", func(l string) { line = l; cancel() }); err == nil {
			t.Fatal("the second lock was granted while the first was held")
		}
		return line
	}
	unlock, err := s.lockMirror("file:///dotfiles", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := waitFor(); !strings.Contains(got, "other work in this same yolo process") {
		t.Errorf("a wait on a sibling here said %q", got)
	}
	unlock()

	f, err := os.OpenFile(filepath.Join(s.Dir, "locks", mirrorSlug("file:///dotfiles")+".lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	if got := waitFor(); !strings.Contains(got, "another yolo process") {
		t.Errorf("a wait on an outside holder said %q", got)
	}
}
