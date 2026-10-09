//go:build linux

package cli

// backgroundadvance_cancel_linux_test.go pins a background advance's SIGTERM (backgroundadvance.go;
// docs/design/pi-extension-store-builds.md XB-D19) against a real build child: the signal ends the
// build's whole process group, the build jail is removed by name, and the key's outcome is
// "interrupted" with no failed build recorded.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

const backgroundCancelRootEnv = "YOLO_TEST_BACKGROUND_CANCEL_ROOT"

// TestASIGTERMEndsTheBackgroundAdvancesBuildsAndRemovesEachJail runs the background advance in a
// helper process, sends it a SIGTERM once its build's compiler runs, and reads what the helper found.
// Red if the verb stops catching the signal, or stops removing the build jail it stopped.
func TestASIGTERMEndsTheBackgroundAdvancesBuildsAndRemovesEachJail(t *testing.T) {
	root := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestBackgroundAdvanceSIGTERMHelper$", "-test.v")
	cmd.Env = append(os.Environ(), backgroundCancelRootEnv+"="+root)
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		if b, err := os.ReadFile(filepath.Join(root, "descendant.pid")); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	deadline := time.Now().Add(60 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(root, "descendant.pid")); err == nil {
			break
		}
		select {
		case err := <-waited:
			t.Fatalf("the helper ended before its build started (%v):\n%s", err, out.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("the helper's build never started:\n%s", out.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-waited:
		if err != nil {
			t.Fatalf("the helper failed (%v):\n%s", err, out.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("the background advance did not end on SIGTERM:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "--- PASS: TestBackgroundAdvanceSIGTERMHelper") {
		t.Errorf("the helper did not pass:\n%s", out.String())
	}
}

// TestBackgroundAdvanceSIGTERMHelper is the helper process: the pool fixture ready for a background
// advance, whose build child is a real child process running a harmless compiler that waits, and the
// real verb, which the parent signals.
func TestBackgroundAdvanceSIGTERMHelper(t *testing.T) {
	root := os.Getenv(backgroundCancelRootEnv)
	if root == "" {
		return
	}
	readyForABackgroundAdvance(t)
	compile := `#!/bin/sh
D=$1
exec 3>&- 4>&- 5>&-
trap 'exit 130' INT TERM
sh -c 'echo $$ > "$1/descendant.pid.tmp"; mv "$1/descendant.pid.tmp" "$1/descendant.pid"; exec sleep 120' sh "$D" &
wait $!
`
	script := filepath.Join(root, "compile.sh")
	if err := os.WriteFile(script, []byte(compile), 0o755); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var stagings []string
	prevCommand, prevChild, prevGrace := forkBuildChildCommand, forkBuildChild, forkBuildChildGrace
	forkBuildChild, forkBuildChildGrace = runForkBuildChild, 500*time.Millisecond
	forkBuildChildCommand = func(argv []string) (*exec.Cmd, error) {
		for _, a := range argv {
			if s, ok := strings.CutPrefix(a, "--workspace="); ok {
				mu.Lock()
				stagings = append(stagings, s)
				mu.Unlock()
			}
		}
		return exec.Command("sh", script, root), nil
	}
	var removed []string
	prevRemove := removeBuildJail
	removeBuildJail = func(cname, rt string) bool {
		mu.Lock()
		defer mu.Unlock()
		removed = append(removed, rt+" rm --force "+cname)
		return true
	}
	t.Cleanup(func() {
		forkBuildChildCommand, forkBuildChild, forkBuildChildGrace, removeBuildJail = prevCommand, prevChild, prevGrace, prevRemove
	})
	if rc := runBackgroundAdvance(backgroundAdvanceArgv([]string{poolKeyA}, "podman", patchedTestPlatform)[2:]); rc != 0 {
		t.Fatalf("the verb returned %d", rc)
	}
	b, err := os.ReadFile(filepath.Join(root, "descendant.pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Errorf("the build's descendant %d is still alive after the SIGTERM (%v)", pid, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(stagings) != 1 {
		t.Fatalf("the build children ran in %v, want one", stagings)
	}
	want := "podman rm --force " + runtime.FromWorkspace(stagings[0])
	if len(removed) != 1 || removed[0] != want {
		t.Errorf("the removals = %q, want %q", removed, want)
	}
	var o backgroundOutcome
	if data, err := os.ReadFile(backgroundOutcomePath(poolKeyA)); err != nil {
		t.Fatal(err)
	} else if err := json.Unmarshal(data, &o); err != nil {
		t.Fatal(err)
	}
	if o.State != bgInterrupted {
		t.Errorf("the outcome = %+v, want interrupted", o)
	}
	rec, err := patchedAdvanceStore(true).LoadCheckRecord(poolKeyA)
	if err != nil {
		t.Fatal(err)
	}
	for _, out := range rec.Outcomes {
		if out.Kind == packsrc.OutcomeBuildFailed {
			t.Errorf("a SIGTERM recorded a failed build: %+v", out)
		}
	}
	fmt.Println("helper done")
}

// Cancellation must reach git launched by production fetched-pack selection, not just a later
// tree advance. Detached is also pinned at the actual resolver call site by its process group.
func TestBackgroundSelectionCancelsItsDetachedGit(t *testing.T) {
	pf := newPoolFixture(t)
	t.Setenv("YOLO_PACK_ROOT", "")
	repo := gitPackRepoWith(t, map[string]string{"pack.json": mustRead(t, filepath.Join(pf.packs, "treepool", "pack.json"))})
	source := "git+file://" + repo + "?ref=main"
	writeFile(t, filepath.Join(pf.home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":[{"source":"`+source+`","name":"treepool"}]}`)
	store := &packsrc.Store{Dir: paths.PacksDir(), Detached: true}
	addr, err := packsrc.Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	commit, err := store.Sync(addr)
	if err != nil {
		t.Fatal(err)
	}
	res, err := store.Materialize(addr, commit)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(res.Root, ".yolo-pack-complete")); err != nil {
		t.Fatal(err)
	}
	// Use the same mirrored commit through a different ref so this process's Sync memo cannot
	// bypass selection's rev-parse and move the injected wait into the later tree advance.
	writeFile(t, filepath.Join(pf.home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":[{"source":"git+file://`+repo+`?ref=`+commit+`","name":"treepool"}]}`)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	pidFile := filepath.Join(bin, "resolver.pid")
	writeFile(t, filepath.Join(bin, "git"), "#!/bin/sh\nfor a in \"$@\"; do if [ \"$a\" = rev-parse ]; then echo $$ > "+
		shquote.Quote(pidFile)+"; exec sleep 120; fi; done\nexec "+shquote.Quote(realGit)+" \"$@\"\n")
	if err := os.Chmod(filepath.Join(bin, "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out syncBuffer
	done := make(chan struct{})
	go func() { runBackgroundAdvanceUnder(ctx, bgArgs(poolKeyA), &out); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	var pid int
	for pid == 0 && time.Now().Before(deadline) {
		if b, err := os.ReadFile(pidFile); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
		if pid == 0 {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if pid == 0 {
		cancel()
		<-done
		t.Fatalf("selection's git never started:\n%s", out.String())
	}
	pgid, err := syscall.Getpgid(pid)
	cancel()
	prompt := true
	select {
	case <-done:
	case <-time.After(time.Second):
		prompt = false
		_ = syscall.Kill(pid, syscall.SIGKILL) // drain a broken resolver before leaving the fixture
		<-done
	}
	if !prompt || err != nil || pgid != pid {
		t.Fatalf("resolver git cancellation=%v, pid=%d pgid=%d err=%v; want prompt cancellation in its own group\n%s",
			prompt, pid, pgid, err, out.String())
	}
	if got := outcomeOf(t, poolKeyA); got.State != bgInterrupted {
		t.Errorf("cancelled resolver outcome = %+v", got)
	}
	if _, err := os.Stat(filepath.Join(res.Root, ".yolo-pack-complete")); err == nil {
		t.Error("cancelled resolver completed checkout")
	}
}

const backgroundKillRootEnv = "YOLO_TEST_BACKGROUND_KILL_ROOT"
const backgroundKillKey = "sigkill-fixture/tree"

// The key lock belongs to the daemon, not to its surviving build. Exercise the actual
// withBackgroundKey and runForkBuildChild descriptor paths without a container or agent.
func TestBackgroundAdvanceSIGKILLReleasesItsKeyBeforeTheBuildEnds(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.Create(filepath.Join(root, "helper.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	cmd := exec.Command(exe, "-test.run=^TestBackgroundAdvanceSIGKILLHelper$", "-test.short="+strconv.FormatBool(testing.Short()))
	cmd.Env = append(os.Environ(), backgroundKillRootEnv+"="+root)
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	reaped, childPID := false, 0
	t.Cleanup(func() {
		if !reaped {
			_ = cmd.Process.Kill()
			<-done
		}
		if childPID == 0 {
			b, _ := os.ReadFile(filepath.Join(root, "build.pid"))
			childPID, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
		if childPID > 0 {
			_ = syscall.Kill(-childPID, syscall.SIGKILL)
		}
	})
	waitForFixtureFile(t, filepath.Join(root, "build.pid"), 10*time.Second)
	b, err := os.ReadFile(filepath.Join(root, "build.pid"))
	if err != nil {
		t.Fatal(err)
	}
	childPID, err = strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || childPID <= 0 {
		t.Fatalf("invalid fixture build pid %q: %v", b, err)
	}
	if group, err := syscall.Getpgid(childPID); err != nil || group != childPID {
		t.Fatalf("build group=%d pid=%d err=%v", group, childPID, err)
	}
	lock := backgroundKeyLock(backgroundKillKey)
	if !pidlock.Held(lock) || pidlock.Holder(lock) != cmd.Process.Pid {
		t.Fatal("the background helper does not own its key lock")
	}
	fds, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", childPID))
	if err != nil {
		t.Fatal(err)
	}
	for _, fd := range fds {
		if target, err := os.Readlink(fmt.Sprintf("/proc/%d/fd/%s", childPID, fd.Name())); err == nil && target == lock {
			t.Errorf("build child inherited the daemon's key lock on fd %s", fd.Name())
		}
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
		reaped = true
	case <-time.After(10 * time.Second):
		t.Fatal("SIGKILL did not end the background helper")
	}
	if err := syscall.Kill(childPID, 0); err != nil {
		t.Fatalf("the build ended before key ownership could be checked: %v", err)
	}
	if pidlock.Held(lock) {
		t.Fatal("the surviving build kept the dead daemon's key lock")
	}
	lk, err := pidlock.Acquire(lock, pidlock.NoWait, nil)
	if err != nil {
		t.Fatalf("retry cannot acquire the dead daemon's key: %v", err)
	}
	lk.Release()
}

func TestBackgroundAdvanceSIGKILLHelper(t *testing.T) {
	root := os.Getenv(backgroundKillRootEnv)
	if root == "" {
		return
	}
	previous := forkBuildChildCommand
	forkBuildChildCommand = func([]string) (*exec.Cmd, error) {
		return exec.Command("sh", "-c", `exec 3>&- 4>&- 5>&-; echo $$ > "$1/build.pid.tmp"; mv "$1/build.pid.tmp" "$1/build.pid"; exec sleep 120`, "sh", root), nil
	}
	defer func() { forkBuildChildCommand = previous }()
	withBackgroundKey(backgroundKillKey, "SIGKILL fixture", os.Stdout, func(*backgroundOutcome) {
		runForkBuildChild(context.Background(), time.Minute, root, forkBuild{}, captureStreams{out: io.Discard, errw: io.Discard}, false)
	})
	t.Fatal("the background helper returned before SIGKILL")
}
