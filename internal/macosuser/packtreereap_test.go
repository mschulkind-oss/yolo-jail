package macosuser

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// handedReaper is what a fake StartPackTreeReaper saw: the argv, and its own descriptor of the
// claim, duplicated as exec would duplicate it into the child's fd 3.
type handedReaper struct {
	argv  []string
	claim *os.File
}

func fakeReaperStart(t *testing.T, got *handedReaper) func([]string, *os.File) error {
	return func(argv []string, claim *os.File) error {
		fd, err := syscall.Dup(int(claim.Fd()))
		if err != nil {
			t.Fatalf("dup the claim: %v", err)
		}
		got.argv, got.claim = argv, os.NewFile(uintptr(fd), "claim")
		t.Cleanup(func() { _ = got.claim.Close() })
		return nil
	}
}

// execRun runs argv as it is, for the reaper's removal of a fixture tree.
func execRun(argv []string) int {
	if err := exec.Command(argv[0], argv[1:]...).Run(); err != nil {
		return 1
	}
	return 0
}

// claimHeldElsewhere reports whether a fresh descriptor of path is refused its lock.
func claimHeldElsewhere(t *testing.T, path string) bool {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open the claim %s: %v", path, err)
	}
	defer f.Close()
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return false
	}
	return errors.Is(err, syscall.EWOULDBLOCK)
}

// THE BUG (the maintainer's Mac, `yolo -- pi --version`): the session's guest left two
// sandbox-account processes running past the launch's wait, and the launch kept its guest pack
// tree for `yolo prune --apply` and said so. A process the session started in a session of its
// own is out of the launch's reach and is not its to kill, so the launch now hands the tree and
// its claim to the reaper, which removes it once those processes exit. Through the real call site:
// RunMacosUser hands off, and the reaper run on the descriptor it was handed removes the tree.
func TestALingeringGuestHandsTheTreeToTheReaperWhichRemovesItOnceItExits(t *testing.T) {
	escaped := GuestProc{PID: 71286, PPID: 1, Sandbox: true, Start: time.Now(), Name: "sleep"}
	var handed handedReaper
	tree, claims, out, rc := runPackTreeLaunch(t, 0, func() ([]GuestProc, error) {
		return append(hostTable(), escaped), nil
	}, func(d *Deps, _ *Options) {
		d.StartPackTreeReaper = fakeReaperStart(t, &handed)
	})
	if rc != 0 {
		t.Fatalf("RunMacosUser = %d\n%s", rc, out)
	}
	if strings.Contains(out.String(), "Kept guest pack tree") {
		t.Errorf("a launch that handed its tree off still kept it:\n%s", out)
	}
	if !strings.Contains(out.String(), "removed in the background once sandbox process 71286 (sleep) has exited") {
		t.Errorf("the hand-off was not said, naming the holder:\n%s", out)
	}
	if len(handed.argv) < 7 || handed.argv[1] != "internal" || handed.argv[2] != PackTreeReapVerb ||
		handed.argv[3] != "--root" || filepath.Base(handed.argv[4]) != filepath.Base(tree) || handed.argv[5] != "--anchor" {
		t.Fatalf("the reaper's argv = %q", handed.argv)
	}
	if _, err := os.Stat(tree); err != nil {
		t.Fatalf("the tree went while its holder still ran: %v", err)
	}
	claimPath := filepath.Join(claims, filepath.Base(tree)+sessionRecordSuffix)
	if !claimHeldElsewhere(t, claimPath) {
		t.Fatalf("the claim %s is not held once the launch returned: the reaper was not handed it", claimPath)
	}

	// The reaper, on the descriptor it was handed: the holder is there once, then gone.
	_, anchor, _, err := parsePackTreeReapArgs(handed.argv[3:])
	if err != nil {
		t.Fatal(err)
	}
	reads := 0
	var log bytes.Buffer
	rc = reapHandedPackTree(packTreeReaper{
		root: tree, anchor: anchor, claim: handed.claim,
		packsDir: filepath.Dir(tree), claimDir: claims,
		procs: func() ([]GuestProc, error) {
			reads++
			if reads == 1 {
				return append(hostTable(), escaped), nil
			}
			return hostTable(), nil
		},
		run: execRun, out: &log, now: time.Now, sleep: func(time.Duration) {},
	})
	if rc != 0 || reads < 2 {
		t.Fatalf("reaper = %d after %d read(s):\n%s", rc, reads, log.String())
	}
	if _, err := os.Lstat(tree); !os.IsNotExist(err) {
		t.Errorf("the reaper kept the tree once its holder exited (%v):\n%s", err, log.String())
	}
	if !strings.Contains(log.String(), "removed") {
		t.Errorf("the reaper's log does not say it removed the tree:\n%s", log.String())
	}
	assertNoClaims(t, claims)
}

// A reaper that cannot start leaves the launch where it was: the one Kept line, saying why.
func TestAReaperThatCannotStartKeepsTheTreeInOneLine(t *testing.T) {
	tree, claims, out, _ := runPackTreeLaunch(t, 0, func() ([]GuestProc, error) {
		return append(hostTable(), GuestProc{PID: 9003, PPID: 1, Sandbox: true, Start: time.Now()}), nil
	}, func(d *Deps, _ *Options) {
		d.StartPackTreeReaper = func([]string, *os.File) error { return errors.New("no fork for you") }
	})
	if _, err := os.Stat(tree); err != nil {
		t.Errorf("a tree a live guest may read was removed: %v", err)
	}
	assertOneKeptLine(t, out.String(), "sandbox process 9003")
	if !strings.Contains(out.String(), "no fork for you") {
		t.Errorf("the Kept line does not say why the reaper did not start:\n%s", out)
	}
	assertNoClaims(t, claims)
}

// reaperFixture makes a sealed per-launch tree in a packs dir and a held claim on it, as a
// launch leaves them, and returns the tree, the claims dir and the claim's descriptor.
func reaperFixture(t *testing.T) (tree, claims string, claim *os.File) {
	t.Helper()
	packs := filepath.Join(t.TempDir(), packsLeaf)
	unsealOnCleanup(t, packs)
	tree = filepath.Join(packs, "yolo-ws-1234abcd.20261017T222944Z-1")
	if err := os.MkdirAll(filepath.Join(tree, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "bin", "x"), []byte("x"), 0o555); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(tree, "bin"), 0o555); err != nil {
		t.Fatal(err)
	}
	claims = t.TempDir()
	rec, err := openPackTreeClaim(claims, tree, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rec.close)
	return tree, claims, rec.lock
}

func TestTheReaperGivesUpAfterItsBoundAndLetsTheClaimGo(t *testing.T) {
	tree, claims, claim := reaperFixture(t)
	start := time.Now()
	clock := start
	var log bytes.Buffer
	rc := reapHandedPackTree(packTreeReaper{
		root: tree, anchor: start.Add(-time.Second), claim: claim,
		packsDir: filepath.Dir(tree), claimDir: claims,
		procs: func() ([]GuestProc, error) {
			return append(hostTable(), GuestProc{PID: 812, PPID: 1, Sandbox: true, Start: start}), nil
		},
		run: execRun, out: &log, now: func() time.Time { return clock },
		sleep: func(d time.Duration) { clock = clock.Add(d) },
	})
	if rc != 0 {
		t.Fatalf("reaper = %d:\n%s", rc, log.String())
	}
	if _, err := os.Stat(tree); err != nil {
		t.Errorf("the reaper removed a tree its holder still may read: %v", err)
	}
	if !strings.Contains(log.String(), "sandbox process 812") || !strings.Contains(log.String(), "`yolo prune --apply`") {
		t.Errorf("the give-up line does not name the holder and prune:\n%s", log.String())
	}
	if clock.Sub(start) < packTreeReapBound {
		t.Errorf("the reaper gave up after %s, before its bound %s", clock.Sub(start), packTreeReapBound)
	}
	assertNoClaims(t, claims)
}

// The reaper's argv is its whole authority over what it removes, so it refuses any path that is
// not a per-launch tree directly in the packs dir, and any descriptor that is not that tree's
// held claim.
func TestTheReaperRemovesNothingItWasNotHanded(t *testing.T) {
	tree, claims, claim := reaperFixture(t)
	other, err := os.CreateTemp(t.TempDir(), "not-a-claim")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	// A fresh descriptor of the real claim, while the launch's own still holds it: a launch that
	// is running, and handed nothing.
	unhanded, err := os.OpenFile(filepath.Join(claims, filepath.Base(tree)+sessionRecordSuffix), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unhanded.Close()
	for _, tc := range []struct {
		name  string
		root  string
		claim *os.File
	}{
		{"a path outside the packs dir", filepath.Join(t.TempDir(), "yolo-ws.1"), claim},
		{"the packs dir itself", filepath.Dir(tree), claim},
		{"a path with dot-dot", filepath.Dir(tree) + "/../" + packsLeaf + "/" + filepath.Base(tree), claim},
		{"a descriptor that is not the claim", tree, other},
		{"no descriptor", tree, nil},
		{"the claim, not handed", tree, unhanded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var log bytes.Buffer
			rc := reapHandedPackTree(packTreeReaper{
				root: tc.root, anchor: time.Now(), claim: tc.claim,
				packsDir: filepath.Dir(tree), claimDir: claims,
				procs: func() ([]GuestProc, error) { return hostTable(), nil },
				run:   func(argv []string) int { t.Errorf("ran %q", argv); return 1 },
				out:   &log, now: time.Now, sleep: func(time.Duration) {},
			})
			if rc != 2 || !strings.Contains(log.String(), "nothing removed") {
				t.Errorf("reaper = %d, want 2 and a refusal:\n%s", rc, log.String())
			}
		})
	}
	if _, err := os.Stat(filepath.Join(tree, "bin", "x")); err != nil {
		t.Errorf("a refused reaper changed the tree: %v", err)
	}
}

func TestParsePackTreeReapArgsRoundTripsTheArgv(t *testing.T) {
	anchor := time.Unix(1760740184, 123456789)
	argv := PackTreeReaperArgv("/usr/local/bin/yolo", "/Users/Shared/yolo-staged/packs/a.b", anchor, ModeAccount)
	root, got, mode, err := parsePackTreeReapArgs(argv[3:])
	if err != nil || root != "/Users/Shared/yolo-staged/packs/a.b" || !got.Equal(anchor) || mode != ModeAccount {
		t.Errorf("parse(%q) = %q, %v, %v", argv, root, got, err)
	}
	for _, bad := range [][]string{nil, {"--root", "x"}, {"--anchor", "1"}, {"--root", "x", "--anchor", "soon"}, {"--bogus=1"}} {
		if _, _, _, err := parsePackTreeReapArgs(bad); err == nil {
			t.Errorf("parse(%q) accepted", bad)
		}
	}
}

// A process names itself, so a name that is markup or carries a control byte reaches no output
// as anything but text.
func TestDescribeHoldersNeutralizesAGuestChosenName(t *testing.T) {
	got := describeHolders([]GuestProc{{PID: 5, Name: "[red]x\x1b[2J"}})
	if strings.ContainsRune(got, 0x1b) {
		t.Errorf("describeHolders = %q", got)
	}
	var out bytes.Buffer
	p := printer{w: &out}
	p.printf("%s", got)
	if !strings.Contains(out.String(), "red]x") {
		t.Errorf("the name was rendered as markup: %q", out.String())
	}
}

// A pin that goes away while the guest runs (an upgrade swapped the bundle) leaves the launch with
// no yolo to run the reaper from, and it starts none: it says why, once, before its Kept line
// (design 3.4, row 2). The pin is "gone" from the moment the process table is first read, which
// is the end of the launch. Deleting the line in handOffPackTree fails this.
func TestAHandoffSkippedForWantOfAPinnedYoloSaysWhy(t *testing.T) {
	started, pinGone := false, false
	_, _, out, _ := runPackTreeLaunch(t, 0, func() ([]GuestProc, error) {
		pinGone = true
		return append(hostTable(), GuestProc{PID: 9004, PPID: 1, Sandbox: true, Start: time.Now()}), nil
	}, func(d *Deps, _ *Options) {
		d.StartPackTreeReaper = func([]string, *os.File) error { started = true; return nil }
		live := d.SelfExe
		d.SelfExe = func() string {
			if pinGone {
				return ""
			}
			return live()
		}
	})
	if started {
		t.Fatal("a reaper was started with no pinned yolo")
	}
	if n := strings.Count(out.String(), "no pinned yolo to run it from"); n != 1 {
		t.Errorf("the skipped hand-off was named %d times, want once:\n%s", n, out)
	}
	if !strings.Contains(out.String(), "Kept guest pack tree") {
		t.Errorf("the Kept line is gone:\n%s", out)
	}
}
