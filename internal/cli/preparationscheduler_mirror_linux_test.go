//go:build linux

package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// The configured pack and its contributed tree share one fetched mirror. A held foreground lock
// must defer the scheduler's advance, not invalidate its subscription; admission resolves before
// taking the state try-lock, and cancelling the scheduler while the mirror stays held must drain.
func TestPreparationSchedulerDefersLockedMirrorAndDrainsWithoutRevokingSelection(t *testing.T) {
	t.Setenv("YOLO_PACK_ROOT", "")
	pf := readyForABackgroundAdvance(t)
	packManifest := `{"name":"treepool","contributes":[` +
		`{"kind":"files","into":".tool/ext/ext-a","source":"git+file://` + pf.repo + `?ref=main"},` +
		`{"kind":"files","into":".tool/ext/ext-b","source":"git+file://` + pf.repo + `?ref=main"}]}`
	writeFile(t, filepath.Join(pf.repo, "pack.json"), packManifest)
	if out, err := exec.Command("git", "-C", pf.repo, "add", "pack.json").CombinedOutput(); err != nil {
		t.Fatalf("stage remote pack manifest: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", pf.repo, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "-m", "configured remote pack").CombinedOutput(); err != nil {
		t.Fatalf("commit remote pack manifest: %v\n%s", err, out)
	}
	packRef := "git+file://" + pf.repo + "?ref=main"
	configPath := filepath.Join(pf.home, ".config", "yolo-jail", "config.jsonc")
	writeFile(t, configPath, `{"packs":[{"source":"`+packRef+`","name":"treepool"}],"agent_updates":{"treepool":"next-launch"}}`)
	addr, err := packsrc.Parse(packRef)
	if err != nil {
		t.Fatal(err)
	}
	store := &packsrc.Store{Dir: paths.PacksDir()}
	if _, err := store.Sync(addr); err != nil {
		t.Fatalf("seed configured pack mirror: %v", err)
	}
	configured := chosenHostPacks(selectConfiguredHostPacks().packs)
	trees := packload.PatchedTrees(configured)
	if len(trees) != 2 || trees[0].Key() != poolKeyA || len(configured) != 1 {
		t.Fatalf("configured remote pack trees = %+v", trees)
	}
	packRoot := configured[0].Root
	stubPreparationWorkerStart(t)
	req := run.TreeBuildRequest{Trees: trees, Build: true, Runtime: "podman", Platform: patchedTestPlatform}
	if err := registerTreePreparation(trees, req, os.Stderr); err != nil {
		t.Fatalf("register production subscription: %v", err)
	}
	snapshot, err := readPreparationSnapshot(poolKeyA)
	if err != nil || snapshot.Generation == 0 {
		t.Fatalf("production registration did not persist a generation: %+v err=%v", snapshot, err)
	}

	locks, err := filepath.Glob(filepath.Join(paths.PacksDir(), "locks", "*.lock"))
	if err != nil {
		t.Fatal(err)
	}
	var mirrorLocks []string
	for _, path := range locks {
		base := filepath.Base(path)
		if !strings.HasPrefix(base, "check-") && !strings.HasPrefix(base, "lockfile-") {
			mirrorLocks = append(mirrorLocks, path)
		}
	}
	if len(mirrorLocks) != 1 {
		t.Fatalf("fixture expected its one source mirror lock, got %v", locks)
	}
	lockFile, err := os.OpenFile(mirrorLocks[0], os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lockFile.Close()
		t.Fatalf("hold configured remote-pack mirror lock: %v", err)
	}
	unlock := func() {
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		_ = lockFile.Close()
	}

	// Admission resolves the selected remote pack before the subscription-state try-lock. It must
	// not inherit a foreground resolver wait just because the selected mirror is owned elsewhere.
	admitted := false
	admissionDone := make(chan error, 1)
	go func() {
		admissionDone <- preparationAdmissionGuardUnder(context.Background(), poolKeyA, "podman", patchedTestPlatform, snapshot.Generation)(func() error {
			admitted = true
			return nil
		})
	}()
	select {
	case err := <-admissionDone:
		if err != nil || !admitted {
			unlock()
			t.Fatalf("admission under a held configured mirror: committed=%t err=%v", admitted, err)
		}
	case <-time.After(3 * time.Second):
		unlock()
		t.Fatal("admission blocked behind the foreground configured-pack mirror lock")
	}
	stateLock, err := pidlock.Acquire(preparationStateLockPath(), pidlock.NoWait, nil)
	if err != nil {
		unlock()
		t.Fatalf("hold the subscription-state record guard: %v", err)
	}
	guardedCommit := false
	guardedDone := make(chan error, 1)
	go func() {
		guardedDone <- preparationAdmissionGuardUnder(context.Background(), poolKeyA, "podman", patchedTestPlatform, snapshot.Generation)(func() error {
			guardedCommit = true
			return nil
		})
	}()
	select {
	case err := <-guardedDone:
		if !errors.Is(err, packsrc.ErrLockHeld) || guardedCommit {
			stateLock.Release()
			unlock()
			t.Fatalf("admission under the held state record guard: committed=%t err=%v; want immediate lock defer", guardedCommit, err)
		}
	case <-time.After(3 * time.Second):
		stateLock.Release()
		unlock()
		t.Fatal("admission did not resolve the selected declaration before its nonwaiting state guard")
	}
	stateLock.Release()

	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan time.Time)
	loopDone := make(chan error, 1)
	go func() { loopDone <- runPreparationScheduler(ctx, ticks, os.Stderr) }()
	select {
	case ticks <- pf.now:
	case <-time.After(3 * time.Second):
		cancel()
		unlock()
		<-loopDone
		t.Fatal("scheduler did not accept a tick with its configured pack mirror locked")
	}
	deferred := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state, stateErr := preparationSubscriptions()
		if stateErr != nil {
			break
		}
		for _, record := range state {
			if record.Key == poolKeyA && record.Active && record.Phase == "busy" {
				deferred = true
				break
			}
		}
		if deferred {
			break
		}
		select {
		case err := <-loopDone:
			unlock()
			t.Fatalf("scheduler returned while a valid subscription met a held mirror: %v", err)
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !deferred {
		cancel()
		unlock()
		select {
		case <-loopDone:
		case <-time.After(3 * time.Second):
			t.Fatal("scheduler did not drain after releasing a failed lock-contention attempt")
		}
		state, _ := preparationSubscriptions()
		t.Fatalf("held remote-pack mirror did not defer while preserving the subscription: %+v", state)
	}
	cancel()
	select {
	case err := <-loopDone:
		if !errors.Is(err, context.Canceled) {
			unlock()
			t.Fatalf("scheduler cancellation = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		unlock()
		t.Fatal("scheduler did not drain after cancellation while the mirror remained held")
	}
	state, err := preparationSubscriptions()
	if err != nil || len(state) == 0 || !state[0].Active {
		unlock()
		t.Fatalf("a transient mirror lock revoked its valid subscription: %+v err=%v", state, err)
	}

	// Remove only the test's configured-pack materialization. Resolve must then contend for its
	// mirror; the production scoped resolver's NoWait callsite returns the lock outcome promptly.
	storeRoot, err := filepath.Abs(filepath.Join(paths.PacksDir(), "trees"))
	if err != nil {
		unlock()
		t.Fatal(err)
	}
	resolvedPackRoot, err := filepath.Abs(packRoot)
	if err != nil {
		unlock()
		t.Fatal(err)
	}
	rel, err := filepath.Rel(storeRoot, resolvedPackRoot)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		unlock()
		t.Fatalf("refusing to remove a pack root outside this fixture's pack-store trees: %q", packRoot)
	}
	if err := os.RemoveAll(packRoot); err != nil {
		unlock()
		t.Fatalf("remove only the fixture's materialized configured pack: %v", err)
	}
	resolveDone := make(chan error, 1)
	go func() {
		_, release, resolveErr := scopedPreparationTrees(context.Background(), state)
		if release != nil {
			release()
		}
		resolveDone <- resolveErr
	}()
	select {
	case resolveErr := <-resolveDone:
		if !errors.Is(resolveErr, packsrc.ErrLockHeld) {
			unlock()
			t.Fatalf("missing configured-pack materialization under a held mirror resolved as %v; want ErrLockHeld", resolveErr)
		}
	case <-time.After(2 * time.Second):
		unlock()
		select {
		case <-resolveDone:
		case <-time.After(3 * time.Second):
			t.Fatal("resolver still blocked after releasing its owned mirror fixture")
		}
		t.Fatal("production scoped resolver waited for the foreground mirror instead of returning NoWait")
	}

	// A distinct ref is present in the local mirror but has not been resolved in this process. The
	// scheduler must cancel the actual detached git rev-parse when its context ends, even while the
	// same mirror lock remains owned by the foreground fixture.
	if out, err := exec.Command("git", "-C", pf.repo, "branch", "fixture-cancel", "main").CombinedOutput(); err != nil {
		unlock()
		t.Fatalf("create the unique cancellation ref: %v\n%s", err, out)
	}
	mirrors, err := filepath.Glob(filepath.Join(paths.PacksDir(), "mirrors", "*"))
	if err != nil || len(mirrors) != 1 {
		unlock()
		t.Fatalf("fixture expected one configured remote mirror, got %v err=%v", mirrors, err)
	}
	commit, err := exec.Command("git", "--git-dir="+mirrors[0], "rev-parse", "main").Output()
	if err != nil {
		unlock()
		t.Fatalf("read local mirror commit for unique ref: %v", err)
	}
	if out, err := exec.Command("git", "--git-dir="+mirrors[0], "update-ref", "refs/heads/fixture-cancel", strings.TrimSpace(string(commit))).CombinedOutput(); err != nil {
		unlock()
		t.Fatalf("seed the unique ref in the test mirror: %v\n%s", err, out)
	}
	writeFile(t, filepath.Join(pf.home, ".config", "yolo-jail", "config.jsonc"), `{"packs":[{"source":"git+file://`+pf.repo+`?ref=fixture-cancel","name":"treepool"}],"agent_updates":{"treepool":"next-launch"}}`)
	git, err := exec.LookPath("git")
	if err != nil {
		unlock()
		t.Fatal(err)
	}
	wrapperDir := t.TempDir()
	pidPath := filepath.Join(wrapperDir, "resolver.pid")
	writeFile(t, filepath.Join(wrapperDir, "git"), `#!/bin/sh
for a in "$@"; do
  if [ "$a" = rev-parse ]; then echo $$ > `+shquote.Quote(pidPath)+`; exec sleep 120; fi
done
exec `+shquote.Quote(git)+` "$@"
`)
	if err := os.Chmod(filepath.Join(wrapperDir, "git"), 0o755); err != nil {
		unlock()
		t.Fatal(err)
	}
	t.Setenv("PATH", wrapperDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	cancelCtx, cancelResolver := context.WithCancel(context.Background())
	resolverTicks := make(chan time.Time)
	resolverLoop := make(chan error, 1)
	go func() { resolverLoop <- runPreparationScheduler(cancelCtx, resolverTicks, os.Stderr) }()
	select {
	case resolverTicks <- pf.now:
	case <-time.After(3 * time.Second):
		cancelResolver()
		unlock()
		<-resolverLoop
		t.Fatal("scheduler did not accept a selection-cancellation tick")
	}
	var resolverPID int
	deadline = time.Now().Add(5 * time.Second)
	for resolverPID == 0 && time.Now().Before(deadline) {
		if b, readErr := os.ReadFile(pidPath); readErr == nil {
			resolverPID, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
		if resolverPID == 0 {
			select {
			case loopErr := <-resolverLoop:
				unlock()
				t.Fatalf("scheduler returned before reaching configured-pack git resolution: %v", loopErr)
			default:
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	if resolverPID == 0 {
		cancelResolver()
		unlock()
		select {
		case <-resolverLoop:
		case <-time.After(3 * time.Second):
		}
		t.Fatal("scheduler selection never started its unique-ref git resolver")
	}
	if pgid, err := syscall.Getpgid(resolverPID); err != nil || pgid != resolverPID {
		cancelResolver()
		unlock()
		<-resolverLoop
		t.Fatalf("resolver git process group=%d pid=%d err=%v; want detached group", pgid, resolverPID, err)
	}
	cancelResolver()
	select {
	case err := <-resolverLoop:
		if !errors.Is(err, context.Canceled) {
			unlock()
			t.Fatalf("scheduler did not report cancellation after draining resolver: %v", err)
		}
	case <-time.After(2 * time.Second):
		_ = syscall.Kill(-resolverPID, syscall.SIGKILL)
		unlock()
		<-resolverLoop
		t.Fatal("scheduler did not cancel and drain its detached configured-pack resolver")
	}
	if err := syscall.Kill(resolverPID, 0); !errors.Is(err, syscall.ESRCH) {
		unlock()
		t.Fatalf("cancelled resolver process %d remains alive: %v", resolverPID, err)
	}
	state, err = preparationSubscriptions()
	if err != nil || len(state) == 0 || !state[0].Active {
		unlock()
		t.Fatalf("configured-pack resolver cancellation revoked its subscription: %+v err=%v", state, err)
	}
	unlock()
}
