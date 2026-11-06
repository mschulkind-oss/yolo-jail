//go:build linux

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

type preparationLifecycleState struct {
	Subscriptions []struct {
		Key, Runtime, Platform, Phase, ServingEntry, Reason string
		Generation                                          uint64
		Active                                              bool
	} `json:"subscriptions"`
	Worker struct {
		Identity  string
		PID       int
		StoppedAt time.Time `json:"stopped_at"`
	} `json:"worker"`
}

// This is the checked-in form of the successful hidden-worker fixture: a local synthetic pack and
// git+file tree, no agent or API, a real one-minute timer, and the real public start/stop commands.
// It also holds a FIFO-paused build across stop, restart+SIGTERM, and a copied-binary replacement.
func TestPreparationSchedulerRealWorkerLifecycle(t *testing.T) {
	requireJail(t)
	if goruntime.GOOS != "linux" || detectRuntime() != "podman" {
		t.Skip("the real preparation lifecycle needs the sealed Linux Podman build backend; Apple Container and macos-user are unsupported")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for the synthetic local-source pack fixture")
	}

	upstream := patchFixtureUpstream{t: t, dir: resolvedTempDir(t)}
	upstream.git("init", "-q", "-b", "main")
	upstream.release("1.0.0", "ten", "")
	packDir := resolvedTempDir(t)
	writePreparationPack(t, packDir, upstream.dir, "printf 'PREPARED_TREE\\n' > prepared.txt")
	userConfig := `{"packs":[{"source":"file://` + packDir + `","name":"synthetic"}],"agent_updates":{"synthetic":"next-launch"}}`
	packHome(t, userConfig)
	home := withPrivateFixtureYoloStore(t)
	work := writeProject(t, `{}`)
	awaitDetachedWriters(t, work, home)
	statePath := filepath.Join(paths.BackgroundAdvanceDir(), "scheduler.json")
	platform := "linux/" + goruntime.GOARCH
	writePreparationFixtureState(t, statePath, platform)
	_ = os.Remove(filepath.Join(paths.BackgroundAdvanceDir(), "scheduler-wake"))

	owned := &preparationLifecycleOwnership{t: t}
	t.Cleanup(owned.cleanup)
	worker := exec.Command(yoloBin, "internal", "preparation-scheduler")
	worker.Dir = work
	worker.Env = preparationHostEnv(home)
	workerLog, err := os.OpenFile(filepath.Join(t.TempDir(), "minute-worker.log"), os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer workerLog.Close()
	worker.Stdout, worker.Stderr = workerLog, workerLog
	if err := worker.Start(); err != nil {
		workerLog.Close()
		t.Fatalf("start the real hidden preparation worker: %v", err)
	}
	owned.remember(worker.Process.Pid)
	waitPreparationState(t, statePath, 100*time.Second, "the real one-minute timer to admit the synthetic tree", func(s preparationLifecycleState) bool {
		return prepRecord(s, "synthetic/example").Phase == "admitted"
	})
	state := readPreparationLifecycleState(t, statePath)
	admitted := prepRecord(state, "synthetic/example")
	if !admitted.Active || admitted.ServingEntry == "" {
		t.Fatalf("timer status does not retain the admitted serving entry: %+v", admitted)
	}
	// A normal launch consumes the build notice. The read-only public status must still report the
	// actual admission rather than losing it with the notice.
	_ = runCommand(t, work, append(jailRunArgs(), "--", "bash", "-lc", "true"), withoutFixtureProgramInstall(), withHostSemantics())
	status := prepCLI(t, yoloBin, home, "pack", "prepare", "status")
	if !strings.Contains(status, "admitted entry: "+admitted.ServingEntry) {
		t.Fatalf("status after notice consumption lost the serving entry %q:\n%s", admitted.ServingEntry, status)
	}

	stopIdentity := state.Worker.Identity
	prepCLI(t, yoloBin, home, "pack", "prepare", "stop")
	waitPreparationState(t, statePath, 25*time.Second, "public stop to release the worker lease", func(s preparationLifecycleState) bool {
		return s.Worker.Identity == stopIdentity && preparationWorkerStopped(t, s)
	})
	if status := prepCLI(t, yoloBin, home, "pack", "prepare", "status"); !strings.Contains(status, "Tree preparation worker: not running;") {
		t.Fatalf("public status did not prove the stopped worker lease free:\n%s", status)
	}
	if !workerExited(t, owned.workers[len(owned.workers)-1]) {
		t.Fatal("public stop cleared worker state before the owned process exited")
	}
	if err := worker.Wait(); err != nil {
		t.Fatalf("reap the direct hidden-worker fixture: %v", err)
	}

	pausedBuild := `mkfifo /workspace/hold; printf 'PAUSED_SYNTHETIC_CHILD\\n'; read ignored < /workspace/hold; printf 'PREPARED_TREE\n' > prepared.txt`
	upstream.release("1.1.0", "eleven", "v1.1.0")
	writePreparationPackRef(t, packDir, upstream.dir, "v1.1.0", pausedBuild)
	t.Setenv("HTTP_PROXY", "synthetic-provider-secret")
	prepCLI(t, yoloBin, home, "pack", "prepare", "start")
	waitPreparationState(t, statePath, 10*time.Second, "public start to record its worker", func(s preparationLifecycleState) bool { return s.Worker.PID != 0 && s.Worker.StoppedAt.IsZero() })
	state = readPreparationLifecycleState(t, statePath)
	startWorker := owned.remember(state.Worker.PID)
	container := waitPreparationPausedChild(t, statePath, 45*time.Second)
	state = readPreparationLifecycleState(t, statePath)
	procEnv, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", state.Worker.PID))
	if err != nil {
		t.Fatalf("read the actual public-start worker environment: %v", err)
	}
	for _, entry := range strings.Split(string(procEnv), "\x00") {
		if strings.HasPrefix(entry, "HTTP_PROXY=") {
			t.Fatalf("selected provider credential name reached the actual spawned worker: %q", entry)
		}
	}
	owned.containers = append(owned.containers, container)
	prepCLI(t, yoloBin, home, "pack", "prepare", "stop")
	waitPreparationWorkerAndContainer(t, statePath, container, 30*time.Second, "public stop did not drain the paused FIFO build")
	if !workerExited(t, startWorker) {
		t.Fatal("public stop returned before its owned worker exited")
	}
	if got := prepRecord(readPreparationLifecycleState(t, statePath), "synthetic/example").Phase; got != "interrupted" {
		t.Fatalf("public stop phase=%q, want interrupted", got)
	}

	// Restart the same interrupted record, prove the second FIFO child is live, then exercise the
	// actual worker SIGTERM handler and container cleanup.
	prepCLI(t, yoloBin, home, "pack", "prepare", "start")
	waitPreparationState(t, statePath, 10*time.Second, "restart to record its worker", func(s preparationLifecycleState) bool { return s.Worker.PID != 0 && s.Worker.StoppedAt.IsZero() })
	state = readPreparationLifecycleState(t, statePath)
	workerProcess := owned.remember(state.Worker.PID)
	container = waitPreparationPausedChild(t, statePath, 45*time.Second)
	owned.containers = append(owned.containers, container)
	if !processHasArgs(workerProcess.pid, "internal", "preparation-scheduler") {
		t.Fatalf("recorded worker pid %d no longer names the preparation worker", workerProcess.pid)
	}
	if err := syscall.Kill(workerProcess.pid, syscall.SIGTERM); err != nil {
		t.Fatalf("SIGTERM the owned real worker: %v", err)
	}
	waitPreparationWorkerAndContainer(t, statePath, container, 30*time.Second, "SIGTERM did not drain the paused FIFO build")
	if got := prepRecord(readPreparationLifecycleState(t, statePath), "synthetic/example").Phase; got != "interrupted" {
		t.Fatalf("SIGTERM phase=%q, want interrupted", got)
	}

	// The mismatch reproduction replaces only a private copy of the test binary. Keep its
	// unchanged matched helper/flake bundle adjacent: a bare development binary correctly
	// refuses a build jail. The original bundle and host yolo binary are never modified.
	copyDir := t.TempDir()
	shareDir := filepath.Join(copyDir, "share")
	if err := os.MkdirAll(shareDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(bundleRoot, filepath.Join(shareDir, "yolo-jail")); err != nil {
		t.Fatal(err)
	}
	copyBinary := filepath.Join(copyDir, "yolo")
	binaryBytes, err := os.ReadFile(yoloBin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copyBinary, binaryBytes, 0o755); err != nil {
		t.Fatal(err)
	}
	prepCLI(t, copyBinary, home, "pack", "prepare", "start")
	waitPreparationState(t, statePath, 10*time.Second, "private binary-copy start to record its worker", func(s preparationLifecycleState) bool { return s.Worker.PID != 0 && s.Worker.StoppedAt.IsZero() })
	state = readPreparationLifecycleState(t, statePath)
	workerProcess = owned.remember(state.Worker.PID)
	container = waitPreparationPausedChild(t, statePath, 45*time.Second)
	owned.containers = append(owned.containers, container)
	replacement := copyBinary + ".replacement"
	if err := os.WriteFile(replacement, append(binaryBytes, []byte("\nSYNTHETIC_REPLACEMENT\n")...), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, copyBinary); err != nil {
		t.Fatalf("atomically replace the private worker binary copy: %v", err)
	}
	originalWorkspace := preparationChildWorkspace(workerProcess.pid)
	originalIdentity := state.Worker.Identity
	var originalChild xbProcess
	for _, pid := range xbProcessDescendants(workerProcess.pid) {
		if processHasArgs(pid, "internal", "fork-build-jail") {
			if originalChild.pid != 0 {
				t.Fatal("replacement fixture has more than one sealed build child")
			}
			originalChild = xbRemember(t, pid)
		}
	}
	if originalChild.pid == 0 {
		t.Fatal("replacement fixture lost the original sealed build child")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	replacementStart := exec.CommandContext(ctx, copyBinary, "pack", "prepare", "start")
	replacementStart.Dir, replacementStart.Env = bundleRoot, preparationCLIEnv(home)
	replacementOutput, replacementErr := replacementStart.CombinedOutput()
	cancel()
	if replacementErr == nil || !strings.Contains(string(replacementOutput), "teardown still owns its lease") {
		t.Fatalf("replacement start did not refuse early takeover: %v\n%s", replacementErr, replacementOutput)
	}
	current := readPreparationLifecycleState(t, statePath)
	if current.Worker.Identity != originalIdentity || !current.Worker.StoppedAt.IsZero() || !workerProcess.alive() || preparationChildWorkspace(workerProcess.pid) != originalWorkspace || !originalChild.alive() {
		t.Fatalf("replacement changed or cancelled the original draining work: %+v", current)
	}
	if status := prepCLI(t, copyBinary, home, "pack", "prepare", "status"); !strings.Contains(status, "Tree preparation worker: running") {
		t.Fatalf("draining incarnation lost its authenticated lease:\n%s", status)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	releaseErr := exec.CommandContext(ctx, "podman", "exec", container, "bash", "-c", "printf 'release\\n' > /workspace/hold").Run()
	cancel()
	if releaseErr != nil {
		t.Fatalf("release original fixture FIFO: %v", releaseErr)
	}
	waitPreparationWorkerAndContainer(t, statePath, container, 30*time.Second, "replacement did not drain the released original build")
	if !workerExited(t, workerProcess) || !workerExited(t, originalChild) {
		t.Fatal("replacement drain did not exit the original owned worker/build")
	}
	if state := readPreparationLifecycleState(t, statePath); state.Worker.Identity != originalIdentity {
		t.Fatal("replacement acquired authority before the original drain completed")
	}
	if status := prepCLI(t, copyBinary, home, "pack", "prepare", "status"); !strings.Contains(status, "Tree preparation worker: not running;") {
		t.Fatalf("replacement did not release its lease:\n%s", status)
	}
	if got := prepRecord(readPreparationLifecycleState(t, statePath), "synthetic/example").Phase; got != "admitted" {
		t.Fatalf("binary replacement phase=%q, want admitted", got)
	}
}

type preparationLifecycleOwnership struct {
	t          *testing.T
	workers    []xbProcess
	containers []string
}

func (o *preparationLifecycleOwnership) remember(pid int) xbProcess {
	o.t.Helper()
	p := xbRemember(o.t, pid)
	o.workers = append(o.workers, p)
	return p
}

func (o *preparationLifecycleOwnership) cleanup() {
	for _, p := range o.workers {
		if p.alive() && processHasArgs(p.pid, "internal", "preparation-scheduler") {
			if workspace := preparationChildWorkspace(p.pid); workspace != "" {
				name := jailName(workspace)
				if !containsPreparationContainer(o.containers, name) {
					o.containers = append(o.containers, name)
				}
			}
			_ = syscall.Kill(p.pid, syscall.SIGTERM)
		}
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		allDone := true
		for _, p := range o.workers {
			allDone = allDone && p.ended()
		}
		if allDone {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, p := range o.workers {
		if p.alive() && processHasArgs(p.pid, "internal", "preparation-scheduler") {
			_ = syscall.Kill(p.pid, syscall.SIGKILL)
		}
	}
	for _, name := range o.containers {
		exists, err := preparationContainerExists(name)
		if err != nil {
			o.t.Errorf("fixture cleanup could not inspect its owned build container %s: %v", name, err)
		} else if exists && !run.ForceRemoveContainer(name, "podman") {
			o.t.Errorf("fixture cleanup could not remove its owned build container %s", name)
		}
	}
}

func containsPreparationContainer(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

func preparationContainerExists(name string) (bool, error) {
	err := exec.Command("podman", "container", "exists", name).Run()
	if err == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

func writePreparationPack(t *testing.T, dir, upstream, build string) {
	t.Helper()
	writePreparationPackRef(t, dir, upstream, "main", build)
}

func writePreparationPackRef(t *testing.T, dir, upstream, ref, build string) {
	t.Helper()
	manifest := map[string]any{"name": "synthetic", "contributes": []any{
		map[string]any{"kind": "provider", "name": "synthetic-provider", "api_key_env_name": "HTTP_PROXY"},
		map[string]any{"kind": "files", "into": ".synthetic/ext/example", "source": "git+file://" + upstream + "?ref=" + ref,
			"build": build, "produces": []string{"prepared.txt"}},
	}}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writePreparationFixtureState(t *testing.T, path, platform string) {
	t.Helper()
	fixture := map[string]any{"schema": 1, "subscriptions": []any{map[string]any{
		"Key": "synthetic/example", "Runtime": "podman", "Platform": platform,
		"Generation": 1, "Active": true, "Phase": "pending",
	}}, "worker": map[string]any{}}
	data, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func preparationHostEnv(home string) []string {
	return preparationEnv(home, true)
}

func preparationCLIEnv(home string) []string {
	return preparationEnv(home, false)
}

func preparationEnv(home string, withoutProxy bool) []string {
	var env []string
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "YOLO_VERSION=") || strings.HasPrefix(entry, "HOME=") ||
			(withoutProxy && (strings.HasPrefix(entry, "HTTP_PROXY=") || strings.HasPrefix(entry, "HTTPS_PROXY=") ||
				strings.HasPrefix(entry, "ALL_PROXY=") || strings.HasPrefix(entry, "NO_PROXY="))) {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "HOME="+home)
}

func prepCLI(t *testing.T, binary, home string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = bundleRoot
	cmd.Env = preparationCLIEnv(home)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("%s %v exceeded 20s; stop and inspect the private scheduler state:\n%s", binary, args, out)
	}
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", binary, args, err, out)
	}
	return string(out)
}

func readPreparationLifecycleState(t *testing.T, path string) preparationLifecycleState {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read private scheduler state %s: %v", path, err)
	}
	var state preparationLifecycleState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("decode private scheduler state: %v\n%s", err, data)
	}
	return state
}

func prepRecord(state preparationLifecycleState, key string) struct {
	Key, Runtime, Platform, Phase, ServingEntry, Reason string
	Generation                                          uint64
	Active                                              bool
} {
	for _, record := range state.Subscriptions {
		if record.Key == key {
			return record
		}
	}
	return struct {
		Key, Runtime, Platform, Phase, ServingEntry, Reason string
		Generation                                          uint64
		Active                                              bool
	}{}
}

func waitPreparationState(t *testing.T, path string, bound time.Duration, what string, ready func(preparationLifecycleState) bool) {
	t.Helper()
	deadline := time.Now().Add(bound)
	var last preparationLifecycleState
	for time.Now().Before(deadline) {
		last = readPreparationLifecycleState(t, path)
		if ready(last) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s; state=%+v", bound, what, last)
}

func waitPreparationPausedChild(t *testing.T, statePath string, bound time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(bound)
	for time.Now().Before(deadline) {
		state := readPreparationLifecycleState(t, statePath)
		if state.Worker.PID != 0 {
			if workspace := preparationChildWorkspace(state.Worker.PID); workspace != "" {
				name := jailName(workspace)
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				err := exec.CommandContext(ctx, "podman", "exec", name, "test", "-p", "/workspace/hold").Run()
				cancel()
				if err == nil {
					return name
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	logPath := filepath.Join(paths.GlobalStorage(), "logs", "background-advance.log")
	logTail, _ := os.ReadFile(logPath)
	if len(logTail) > 8000 {
		logTail = logTail[len(logTail)-8000:]
	}
	t.Fatalf("no owned build worker reached the real /workspace/hold FIFO within %s; state=%+v; worker log tail (inspect %s):\n%s",
		bound, readPreparationLifecycleState(t, statePath), logPath, logTail)
	return ""
}

func preparationChildWorkspace(workerPID int) string {
	pending := []int{workerPID}
	seen := map[int]bool{}
	for len(pending) > 0 {
		pid := pending[0]
		pending = pending[1:]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		for _, child := range processChildren(pid) {
			args, err := processArgs(child)
			if err != nil {
				continue
			}
			if processHasArgs(child, "internal", "fork-build-jail") {
				for _, arg := range args {
					if workspace, ok := strings.CutPrefix(arg, "--workspace="); ok {
						return workspace
					}
				}
			}
			pending = append(pending, child)
		}
	}
	return ""
}

func waitPreparationWorkerAndContainer(t *testing.T, statePath, container string, bound time.Duration, what string) {
	t.Helper()
	deadline := time.Now().Add(bound)
	for time.Now().Before(deadline) {
		state := readPreparationLifecycleState(t, statePath)
		containerGone, inspectErr := preparationContainerExists(container)
		if inspectErr != nil {
			t.Fatalf("inspect owned build container %s while waiting for %s: %v", container, what, inspectErr)
		}
		if preparationWorkerStopped(t, state) && !containerGone {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s; final state=%+v container=%s", what, readPreparationLifecycleState(t, statePath), container)
}

// StoppedAt is published while the incarnation still owns its lease. Retained PID and
// identity are diagnostics, not a live-owner sentinel; independently prove lease release.
func preparationWorkerStopped(t *testing.T, state preparationLifecycleState) bool {
	t.Helper()
	if state.Worker.StoppedAt.IsZero() {
		return false
	}
	lock, err := os.OpenFile(filepath.Join(paths.BackgroundAdvanceDir(), "worker.lock"), os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("inspect scheduler lease after final publication: %v", err)
	}
	defer lock.Close()
	err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return false
	}
	if err != nil {
		t.Fatalf("probe scheduler lease: %v", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	return true
}

func workerExited(t *testing.T, p xbProcess) bool {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if p.ended() {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}
