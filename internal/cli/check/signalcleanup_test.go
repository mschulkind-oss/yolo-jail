package check

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/nixchildren"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
)

const (
	signalCleanupChildEnv              = "YOLO_CHECK_SIGNAL_CLEANUP_CHILD"
	signalCleanupMissingMarkerChildEnv = "YOLO_CHECK_SIGNAL_MISSING_MARKER_CHILD"
)

// TestSignalExitRemovesSelectedPackTree runs a child through the real Check call and
// sends SIGTERM only after its BuildImage seam proves sectionPacks staged the selected
// pack. Unlike a fake exit callback, os.Exit in the signal arm skips every Go defer.
func TestSignalExitRemovesSelectedPackTree(t *testing.T) {
	if os.Getenv(signalCleanupChildEnv) == "1" {
		if os.Getenv(signalCleanupMissingMarkerChildEnv) == "1" {
			runSignalCleanupMissingMarkerChild(t)
			return
		}
		runSignalCleanupChild(t)
		return
	}

	root := t.TempDir()
	home := filepath.Join(root, "home")
	pack := filepath.Join(root, "fixture-pack")
	for _, dir := range []string{filepath.Join(home, ".config", "yolo-jail"), pack} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(pack, "pack.json"), []byte(`{"name":"signal-fixture","contributes":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf(`{"setup":"apple-container-self","packs":[%q]}`, "file://"+pack)
	if err := os.WriteFile(filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		sig  syscall.Signal
		code int
	}{{"SIGINT", syscall.SIGINT, 130}, {"SIGHUP", syscall.SIGHUP, 129}, {"SIGTERM", syscall.SIGTERM, 143}} {
		t.Run(tc.name, func(t *testing.T) {
			runSignalCleanupCase(t, root, home, caseTempDir(t, root, tc.name), tc.sig, tc.code)
		})
	}
	t.Run("normal-cleanup overlap", func(t *testing.T) {
		runSignalCleanupOverlapCase(t, root, home, caseTempDir(t, root, "overlap"))
	})
}

// caseTempDir gives each case its own TMPDIR, so a tree one case leaves behind (a
// failed case's deferred SIGKILL skips every cleanup by design) fails that case and
// not the next one.
func caseTempDir(t *testing.T, root, name string) string {
	t.Helper()
	tmp := filepath.Join(root, "tmp-"+name)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	return tmp
}

// assertNoStagingTrees fails the case whose child left a yolo-check-pack-* tree in
// its own TMPDIR after exiting.
func assertNoStagingTrees(t *testing.T, tmp string) {
	t.Helper()
	trees, err := filepath.Glob(filepath.Join(tmp, "yolo-check-pack-*"))
	if err != nil || len(trees) != 0 {
		t.Errorf("child exit left staging trees %q in its TMPDIR (glob error %v)", trees, err)
	}
}

// writeMarkerAtomically publishes a marker file the parent polls for. os.WriteFile
// truncates before it writes, so a polling reader can see an empty file.
func writeMarkerAtomically(path string, data []byte) error {
	partial := path + ".partial"
	if err := os.WriteFile(partial, data, 0o600); err != nil {
		return err
	}
	return os.Rename(partial, path)
}

// recordSignalCleanupPhase records only synthetic child phase names, never environment values.
func recordSignalCleanupPhase(t *testing.T, phase string) {
	t.Helper()
	path := os.Getenv("CHECK_SIGNAL_PHASES")
	if path == "" {
		return
	}
	previous, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read signal cleanup phase log: %v", err)
	}
	updated := append(previous, phase+"\n"...)
	if err := writeMarkerAtomically(path, updated); err != nil {
		t.Fatalf("write signal cleanup phase %q: %v", phase, err)
	}
	t.Logf("signal cleanup phase: %s", phase)
}

func readSignalCleanupPhases(path string) string {
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "<phase log read error: " + err.Error() + ">"
	}
	return strings.TrimSpace(string(b))
}

type signalCleanupCmdWait struct {
	done chan struct{}
	err  error
}

func startSignalCleanupCmdWait(cmd *exec.Cmd) *signalCleanupCmdWait {
	waited := &signalCleanupCmdWait{done: make(chan struct{})}
	go func() {
		waited.err = cmd.Wait()
		close(waited.done)
	}()
	return waited
}

func (w *signalCleanupCmdWait) wait(guard time.Duration) (error, bool) {
	timer := time.NewTimer(guard)
	defer timer.Stop()
	select {
	case <-w.done:
		return w.err, true
	case <-timer.C:
		return nil, false
	}
}

func (w *signalCleanupCmdWait) isDone() bool {
	select {
	case <-w.done:
		return true
	default:
		return false
	}
}

type signalCleanupTeardown struct {
	waitErr     error
	childJoined bool
	nixStarted  bool
	nixExited   bool
	groupGone   bool
	groupKill   error
}

func (r signalCleanupTeardown) safeToReadOutput() bool {
	return r.childJoined && r.groupGone
}

// stopSignalCleanupChild closes the fixture-owned lifetime pipe first, then bounds the
// stand-in exit event, Cmd.Wait, and final owned-group teardown before output may be read.
func stopSignalCleanupChild(cmd *exec.Cmd, waited *signalCleanupCmdWait, lifetime *os.File,
	nixStartedPath, nixExitedPath string) signalCleanupTeardown {
	var result signalCleanupTeardown
	if lifetime != nil {
		_ = lifetime.Close()
	}
	result.nixStarted = fileExists(nixStartedPath)
	if result.nixStarted {
		result.nixExited = waitForSignalCleanupMarker(nixExitedPath, 10*time.Second)
	}
	if cmd.Process != nil && !waited.isDone() {
		_ = cmd.Process.Kill()
	}
	result.waitErr, result.childJoined = waited.wait(10 * time.Second)
	if !result.childJoined {
		result.groupKill = killSignalCleanupOwnedGroup(cmd)
		result.groupGone = waitSignalCleanupOwnedGroupExit(cmd, 10*time.Second)
		result.waitErr, result.childJoined = waited.wait(10 * time.Second)
	}
	if result.childJoined {
		result.groupGone = waitSignalCleanupOwnedGroupExit(cmd, 10*time.Second)
	}
	if !result.groupGone {
		result.groupKill = killSignalCleanupOwnedGroup(cmd)
		result.groupGone = waitSignalCleanupOwnedGroupExit(cmd, 10*time.Second)
	}
	return result
}

type signalCleanupUnverifiedTeardown struct {
	nixStarted  bool
	nixExited   bool
	childJoined bool
	waitErr     error
}

// stopSignalCleanupUnverifiedChild uses only the parent-owned pipe and direct child handle;
// it never signals a process group whose ownership check failed.
func stopSignalCleanupUnverifiedChild(cmd *exec.Cmd, waited *signalCleanupCmdWait, lifetime *os.File,
	nixStartedPath, nixExitedPath string) signalCleanupUnverifiedTeardown {
	var result signalCleanupUnverifiedTeardown
	if lifetime != nil {
		_ = lifetime.Close()
	}
	result.nixStarted = fileExists(nixStartedPath)
	if result.nixStarted {
		result.nixExited = waitForSignalCleanupMarker(nixExitedPath, 10*time.Second)
	}
	if cmd.Process != nil && !waited.isDone() {
		_ = cmd.Process.Kill()
	}
	result.waitErr, result.childJoined = waited.wait(10 * time.Second)
	return result
}

func waitForSignalCleanupMarker(path string, guard time.Duration) bool {
	if fileExists(path) {
		return true
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(guard)
	defer timer.Stop()
	for {
		select {
		case <-ticker.C:
			if fileExists(path) {
				return true
			}
		case <-timer.C:
			return false
		}
	}
}

func runSignalCleanupOverlapCase(t *testing.T, root, home, tmp string) {
	t.Helper()
	started := filepath.Join(root, "normal-cleanup-started")
	release := filepath.Join(root, "release-normal-cleanup")
	terminal := filepath.Join(root, "overlap-terminal")
	cmd := exec.Command(os.Args[0], "-test.run=^TestSignalExitRemovesSelectedPackTree$", "-test.v")
	cmd.Env = testEnv(map[string]string{
		signalCleanupChildEnv:   "1",
		"CHECK_SIGNAL_OVERLAP":  "1",
		"HOME":                  home,
		"TMPDIR":                tmp,
		"CHECK_SIGNAL_MARKER":   filepath.Join(root, "overlap-staged"),
		"CHECK_SIGNAL_TERMINAL": terminal,
		"CHECK_SIGNAL_REMOVE":   started,
		"CHECK_SIGNAL_RELEASE":  release,
		"CHECK_SIGNAL_REPO":     root,
	})
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill() }()
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	waitForFile(t, started, waited, &output, "normal cleanup did not enter its paused removal")
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-waited:
		t.Fatalf("signal cleanup exited while normal removal was blocked (%v); output:\n%s", err, output.String())
	case <-time.After(250 * time.Millisecond):
	}
	if _, err := os.Stat(terminal); !os.IsNotExist(err) {
		t.Fatalf("signal cleanup returned before normal removal was released (terminal marker stat: %v)", err)
	}
	if err := os.WriteFile(release, []byte("release"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-waited:
		if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 143 {
			t.Fatalf("child exit = %v, want status 143; output:\n%s", err, output.String())
		}
	case <-time.After(15 * time.Second):
		_ = cmd.Process.Kill()
		<-waited
		t.Fatalf("child did not exit after cleanup release; output:\n%s", output.String())
	}
	staged, err := os.ReadFile(filepath.Join(root, "overlap-staged"))
	if err != nil {
		t.Fatalf("child did not record selected staging path: %v; output:\n%s", err, output.String())
	}
	if _, err := os.Lstat(strings.TrimSpace(string(staged))); !os.IsNotExist(err) {
		t.Fatalf("overlapping cleanup left selected tree %q (stat error %v)", staged, err)
	}
	removals, err := os.ReadFile(started)
	if err != nil || len(strings.Split(strings.TrimSpace(string(removals)), "\n")) != 1 {
		t.Fatalf("removal callback ran more than once or was not recorded: %q (%v)", removals, err)
	}
	if _, err := os.Stat(terminal); err != nil {
		t.Fatalf("signal callback did not finish after removal release: %v; output:\n%s", err, output.String())
	}
	assertNoStagingTrees(t, tmp)
}

func waitForFile(t *testing.T, path string, waited <-chan error, output *bytes.Buffer, failure string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case err := <-waited:
			t.Fatalf("child exited before expected cleanup marker (%v); %s; output:\n%s", err, failure, output.String())
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s; output:\n%s", failure, output.String())
}

func runSignalCleanupCase(t *testing.T, root, home, tmp string, sig syscall.Signal, code int) {
	t.Helper()
	name := sig.String()
	marker := filepath.Join(root, "staged-"+name)
	phaseLog := filepath.Join(root, "phases-"+name)
	terminalMarker := filepath.Join(root, "terminal-fence-"+name)
	nixStarted := filepath.Join(tmp, "check-signal-nix-started")
	nixStopped := filepath.Join(tmp, "check-signal-nix-stopped")
	nixExited := filepath.Join(tmp, "check-signal-nix-exited")
	lifetimeReader, lifetimeWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestSignalExitRemovesSelectedPackTree$", "-test.v")
	configureSignalCleanupOwnedGroup(cmd)
	cmd.ExtraFiles = []*os.File{lifetimeReader}
	cmd.Env = testEnv(map[string]string{
		signalCleanupChildEnv:      "1",
		"HOME":                     home,
		"TMPDIR":                   tmp,
		"CHECK_SIGNAL_MARKER":      marker,
		"CHECK_SIGNAL_PHASES":      phaseLog,
		"CHECK_SIGNAL_TERMINAL":    terminalMarker,
		"CHECK_SIGNAL_NIX_STARTED": nixStarted,
		"CHECK_SIGNAL_NIX_STOPPED": nixStopped,
		"CHECK_SIGNAL_NIX_EXITED":  nixExited,
		"CHECK_SIGNAL_REPO":        root,
	})
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		_ = lifetimeReader.Close()
		_ = lifetimeWriter.Close()
		t.Fatal(err)
	}
	_ = lifetimeReader.Close()
	waited := startSignalCleanupCmdWait(cmd)
	if err := verifySignalCleanupOwnedGroup(cmd); err != nil {
		result := stopSignalCleanupUnverifiedChild(cmd, waited, lifetimeWriter, nixStarted, nixExited)
		if !result.childJoined || (result.nixStarted && !result.nixExited) {
			t.Fatalf("fixture process group setup failed and unverified child teardown did not complete; captured output withheld: %v", err)
		}
		t.Fatalf("could not establish fixture-owned process group: %v; child output:\n%s", err, output.String())
	}
	teardownComplete := false
	defer func() {
		if teardownComplete {
			_ = lifetimeWriter.Close()
			return
		}
		result := stopSignalCleanupChild(cmd, waited, lifetimeWriter, nixStarted, nixExited)
		if !result.childJoined || !result.groupGone {
			t.Errorf("fixture teardown incomplete: standin-exited=%t child-joined=%t group-gone=%t group-kill=%v; captured output withheld", result.nixExited, result.childJoined, result.groupGone, result.groupKill)
		}
	}()

	var stagedPaths []string
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(marker); err == nil {
			stagedPaths = strings.Split(strings.TrimSpace(string(b)), "\n")
			break
		}
		select {
		case <-waited.done:
			result := stopSignalCleanupChild(cmd, waited, lifetimeWriter, nixStarted, nixExited)
			teardownComplete = result.childJoined && result.groupGone
			if !teardownComplete {
				t.Fatalf("Check child exited before its post-pack image-build block; phases=%q; teardown incomplete; captured output withheld", readSignalCleanupPhases(phaseLog))
			}
			t.Fatalf("Check child exited before its post-pack image-build block (%v); phases=%q; output:\n%s", waited.err, readSignalCleanupPhases(phaseLog), output.String())
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(stagedPaths) != 3 {
		result := stopSignalCleanupChild(cmd, waited, lifetimeWriter, nixStarted, nixExited)
		teardownComplete = result.childJoined && result.groupGone
		if !teardownComplete {
			trees, _ := filepath.Glob(filepath.Join(tmp, "yolo-check-pack-*"))
			t.Fatalf("child captured %d paths, want selected, fallback, and process-pack roots: %q; phases=%q; staged trees=%q; standin-exited=%t child-joined=%t group-gone=%t group-kill=%v; captured output withheld", len(stagedPaths), stagedPaths, readSignalCleanupPhases(phaseLog), trees, result.nixExited, result.childJoined, result.groupGone, result.groupKill)
		}
		trees, _ := filepath.Glob(filepath.Join(tmp, "yolo-check-pack-*"))
		t.Fatalf("child captured %d paths, want selected, fallback, and process-pack roots: %q; phases=%q; staged trees=%q; standin-exited=%t child-wait=%v; child output:\n%s", len(stagedPaths), stagedPaths, readSignalCleanupPhases(phaseLog), trees, result.nixExited, result.waitErr, output.String())
	}
	staged, fallbackRoot, processRoot := stagedPaths[0], stagedPaths[1], stagedPaths[2]
	if filepath.Dir(staged) != tmp || !strings.HasPrefix(filepath.Base(staged), "yolo-check-pack-") {
		t.Fatalf("captured staging path %q is not a yolo-check-pack-* tree in TMPDIR %q", staged, tmp)
	}
	for _, tree := range []string{fallbackRoot, processRoot} {
		if !strings.HasPrefix(filepath.Base(tree), packload.EmbeddedFallbackPrefix) {
			t.Fatalf("captured packload tree %q does not use the leased fallback naming", tree)
		}
	}
	if fallbackRoot == processRoot {
		t.Fatalf("embedded fallback and process pack roots unexpectedly alias: %q", fallbackRoot)
	}
	if manifests, err := filepath.Glob(filepath.Join(staged, "*", "pack.json")); err != nil || len(manifests) != 1 {
		t.Fatalf("captured selected tree does not contain one staged synthetic pack: manifests=%q error=%v", manifests, err)
	}
	for _, tree := range []string{staged, fallbackRoot, processRoot} {
		if _, err := os.Stat(tree); err != nil {
			t.Fatalf("process-owned tree %q absent before signal: %v", tree, err)
		}
	}
	t.Logf("signal child phase trace before %s: %s", sig, readSignalCleanupPhases(phaseLog))
	if err := cmd.Process.Signal(sig); err != nil {
		t.Fatal(err)
	}
	select {
	case <-waited.done:
		groupGone := waitSignalCleanupOwnedGroupExit(cmd, 10*time.Second)
		if !groupGone || !fileExists(nixExited) {
			result := stopSignalCleanupChild(cmd, waited, lifetimeWriter, nixStarted, nixExited)
			teardownComplete = result.childJoined && result.groupGone
			if !teardownComplete {
				t.Fatalf("signal child returned but owned standin teardown did not join: standin-exited=%t group-gone=%t; captured output withheld", result.nixExited, result.groupGone)
			}
			t.Fatalf("signal child returned without owned standin exit: standin-exited=%t group-gone=%t child-wait=%v; output:\n%s", result.nixExited, result.groupGone, waited.err, output.String())
		}
		teardownComplete = true
		if exitErr, ok := waited.err.(*exec.ExitError); !ok || exitErr.ExitCode() != code {
			t.Fatalf("child exit = %v, want status %d; output:\n%s", waited.err, code, output.String())
		}
	case <-time.After(15 * time.Second):
		result := stopSignalCleanupChild(cmd, waited, lifetimeWriter, nixStarted, nixExited)
		teardownComplete = result.childJoined && result.groupGone
		if !teardownComplete {
			t.Fatalf("child did not exit after %s; teardown incomplete; captured output withheld", sig)
		}
		t.Fatalf("child did not exit after %s; standin-exited=%t; output:\n%s", sig, result.nixExited, output.String())
	}
	if b, err := os.ReadFile(terminalMarker); err != nil {
		t.Fatalf("Check signal callback did not reach its terminal-fence probe: %v; output:\n%s", err, output.String())
	} else if got, want := strings.TrimSpace(string(b)), `embedded=0 process-error=true fallback="" process="" nix-stopped=true`; got != want {
		t.Fatalf("post-fence allocation/ordering probe = %q, want %q", got, want)
	}
	for _, tree := range []string{staged, fallbackRoot, processRoot} {
		if _, err := os.Lstat(tree); !os.IsNotExist(err) {
			t.Errorf("%s exit left process-owned tree %q (stat error %v)", sig, tree, err)
		}
	}
	assertNoStagingTrees(t, tmp)
}

func inheritedSignalCleanupLifetime(t *testing.T) *os.File {
	t.Helper()
	lifetime := os.NewFile(3, "signal-cleanup-lifetime")
	if lifetime == nil {
		t.Fatal("missing fixture-owned nix lifetime pipe")
	}
	return lifetime
}

func startSignalCleanupNix(t *testing.T, stopped, started, exited string, lifetime *os.File) {
	t.Helper()
	nixCmd := exec.Command("/bin/sh", "-c", `trap 'touch "$1"; exit 0' INT; trap 'touch "$3"' EXIT; touch "$2"; while IFS= read -r line; do :; done`, "signal-nix", stopped, started, exited)
	if lifetime != nil {
		nixCmd.Stdin = lifetime
	}
	releaseNix, err := nixchildren.Start(nixCmd)
	if err != nil {
		t.Fatalf("starting tracked stand-in nix: %v", err)
	}
	if lifetime != nil {
		if err := lifetime.Close(); err != nil {
			t.Fatalf("closing child copy of nix lifetime pipe: %v", err)
		}
	}
	go func() {
		_ = nixCmd.Wait()
		releaseNix()
	}()
}

func runSignalCleanupMissingMarkerChild(t *testing.T) {
	started := os.Getenv("CHECK_SIGNAL_NIX_STARTED")
	stopped := os.Getenv("CHECK_SIGNAL_NIX_STOPPED")
	exited := os.Getenv("CHECK_SIGNAL_NIX_EXITED")
	ready := os.Getenv("CHECK_SIGNAL_CHILD_READY")
	startSignalCleanupNix(t, stopped, started, exited, inheritedSignalCleanupLifetime(t))
	if !waitForSignalCleanupMarker(started, 5*time.Second) {
		t.Fatal("tracked stand-in nix did not start within its original guard")
	}
	if err := writeMarkerAtomically(ready, []byte("ready")); err != nil {
		t.Fatalf("writing missing-marker child readiness: %v", err)
	}
	select {}
}

func runSignalCleanupChild(t *testing.T) {
	cacheBlocker := filepath.Join(os.TempDir(), "embedded-cache-is-a-file")
	if err := os.WriteFile(cacheBlocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	packload.OverrideEmbeddedCacheDir(filepath.Join(cacheBlocker, "embedded-packs"))
	recordSignalCleanupPhase(t, "embedded-fallback.begin")
	if packs := packload.Embedded(); len(packs) == 0 {
		t.Fatalf("embedded pack fixture unavailable: %v", packload.EmbeddedProblems())
	}
	recordSignalCleanupPhase(t, "embedded-fallback.ready")
	fallbackRoot, fallback := packload.EmbeddedLocation()
	if !fallback || fallbackRoot == "" {
		t.Fatalf("embedded packs did not use fallback: root=%q fallback=%v", fallbackRoot, fallback)
	}
	recordSignalCleanupPhase(t, "process-pack.begin")
	if _, err := packload.ProcessPackDir("signal-check"); err != nil {
		t.Fatalf("creating process pack tree: %v", err)
	}
	recordSignalCleanupPhase(t, "process-pack.ready")
	processRoot := packload.ProcessTreeLocation()
	nixStopped := os.Getenv("CHECK_SIGNAL_NIX_STOPPED")
	nixStarted := os.Getenv("CHECK_SIGNAL_NIX_STARTED")
	nixExited := os.Getenv("CHECK_SIGNAL_NIX_EXITED")
	_ = os.Remove(nixStopped)
	_ = os.Remove(nixStarted)
	_ = os.Remove(nixExited)
	if os.Getenv("CHECK_SIGNAL_OVERLAP") != "1" {
		recordSignalCleanupPhase(t, "nix-child.begin")
		startSignalCleanupNix(t, nixStopped, nixStarted, nixExited, inheritedSignalCleanupLifetime(t))
		if !waitForSignalCleanupMarker(nixStarted, 5*time.Second) {
			t.Fatal("tracked stand-in nix did not start within its original guard")
		}
		recordSignalCleanupPhase(t, "nix-child.started")
	}
	var out bytes.Buffer
	opts := baseOptions(t, &out)
	opts.Build = true
	opts.IsMacOS = true // apple-container-self is a Mac's setup
	opts.Getenv = func(key string) string {
		if key == "YOLO_SETUP" {
			return "apple-container-self"
		}
		return ""
	}
	opts.LookPath = func(name string) (string, bool) {
		return "/fixture/" + name, name == "container" || name == "nix"
	}
	opts.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		switch strings.Join(argv, " ") {
		case "container --version":
			return ExecResult{Ran: true, Stdout: "container CLI version fixture", RC: 0}
		case "container system status":
			return ExecResult{Ran: true, Stdout: "running", RC: 0}
		case "nix --version":
			return ExecResult{Ran: true, Stdout: "nix (Nix) 2.24.0", RC: 0}
		case "nix store info":
			return ExecResult{Ran: true, Stdout: "Store: /nix/store", RC: 0}
		default:
			return ExecResult{Ran: false}
		}
	}
	opts.PathExists = func(path string) bool {
		return path == "/nix" || path == filepath.Join(os.Getenv("CHECK_SIGNAL_REPO"), "flake.nix")
	}
	opts.RepoRoot = func() (reporoot.Resolution, bool) {
		return reporoot.Resolution{Root: os.Getenv("CHECK_SIGNAL_REPO")}, true
	}
	opts.packTreeFactory = func() (string, error) {
		recordSignalCleanupPhase(t, "sectionPacks.tree-create.begin")
		path, err := os.MkdirTemp("", "yolo-check-pack-")
		if err == nil {
			recordSignalCleanupPhase(t, "sectionPacks.tree-created")
		}
		return path, err
	}
	if os.Getenv("CHECK_SIGNAL_OVERLAP") == "1" {
		removeStarted := os.Getenv("CHECK_SIGNAL_REMOVE")
		release := os.Getenv("CHECK_SIGNAL_RELEASE")
		stagedMarker := os.Getenv("CHECK_SIGNAL_MARKER")
		opts.signalCleanupProbe = func() {
			_ = os.WriteFile(os.Getenv("CHECK_SIGNAL_TERMINAL"), []byte("signal cleanup returned"), 0o600)
		}
		opts.packTreeRemoveAll = func(path string) error {
			file, err := os.OpenFile(removeStarted, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintln(file, path); err != nil {
				_ = file.Close()
				return err
			}
			if err := file.Close(); err != nil {
				return err
			}
			for !fileExists(release) {
				time.Sleep(5 * time.Millisecond)
			}
			return os.RemoveAll(path)
		}
		opts.BuildImage = func(string, []any) (string, []string) {
			recordSignalCleanupPhase(t, "build-image.begin")
			trees, err := filepath.Glob(filepath.Join(os.TempDir(), "yolo-check-pack-*"))
			if err != nil || len(trees) != 1 {
				t.Fatalf("post-pack image build saw staging trees %q, glob error %v", trees, err)
			}
			if err := writeMarkerAtomically(stagedMarker, []byte(trees[0])); err != nil {
				t.Fatal(err)
			}
			recordSignalCleanupPhase(t, "build-image.marker-written")
			return "", []string{"fixture build failure"}
		}
		recordSignalCleanupPhase(t, "check.begin")
		if rc := Check(opts); rc == 0 {
			t.Fatal("overlap Check unexpectedly passed")
		}
		recordSignalCleanupPhase(t, "check.returned")
		return
	}
	opts.signalCleanupProbe = func() {
		packs := packload.Embedded()
		_, processErr := packload.ProcessPackDir("after-signal")
		root, _ := packload.EmbeddedLocation()
		processRoot := packload.ProcessTreeLocation()
		result := fmt.Sprintf(`embedded=%d process-error=%t fallback=%q process=%q nix-stopped=%t`, len(packs), errors.Is(processErr, packload.ErrEmbeddedExiting), root, processRoot, fileExists(nixStopped))
		_ = os.WriteFile(os.Getenv("CHECK_SIGNAL_TERMINAL"), []byte(result), 0o600)
	}
	opts.BuildImage = func(string, []any) (string, []string) {
		recordSignalCleanupPhase(t, "build-image.begin")
		trees, err := filepath.Glob(filepath.Join(os.TempDir(), "yolo-check-pack-*"))
		if err != nil || len(trees) != 1 {
			t.Fatalf("post-pack image build saw staging trees %q, glob error %v", trees, err)
		}
		if err := writeMarkerAtomically(os.Getenv("CHECK_SIGNAL_MARKER"),
			[]byte(strings.Join([]string{trees[0], fallbackRoot, processRoot}, "\n"))); err != nil {
			t.Fatal(err)
		}
		recordSignalCleanupPhase(t, "build-image.marker-written")
		select {}
	}
	recordSignalCleanupPhase(t, "check.begin")
	rc := Check(opts)
	recordSignalCleanupPhase(t, "check.returned")
	t.Fatalf("Check returned instead of remaining in the injected image build (status %d):\n%s", rc, out.String())
}

func testEnv(overrides map[string]string) []string {
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := overrides[key]; !replaced {
			env = append(env, entry)
		}
	}
	for key, value := range overrides {
		env = append(env, key+"="+value)
	}
	return env
}
