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

const signalCleanupChildEnv = "YOLO_CHECK_SIGNAL_CLEANUP_CHILD"

// TestSignalExitRemovesSelectedPackTree runs a child through the real Check call and
// sends SIGTERM only after its BuildImage seam proves sectionPacks staged the selected
// pack. Unlike a fake exit callback, os.Exit in the signal arm skips every Go defer.
func TestSignalExitRemovesSelectedPackTree(t *testing.T) {
	if os.Getenv(signalCleanupChildEnv) == "1" {
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
	terminalMarker := filepath.Join(root, "terminal-fence-"+name)
	cmd := exec.Command(os.Args[0], "-test.run=^TestSignalExitRemovesSelectedPackTree$", "-test.v")
	cmd.Env = testEnv(map[string]string{
		signalCleanupChildEnv:   "1",
		"HOME":                  home,
		"TMPDIR":                tmp,
		"CHECK_SIGNAL_MARKER":   marker,
		"CHECK_SIGNAL_TERMINAL": terminalMarker,
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

	var stagedPaths []string
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(marker); err == nil {
			stagedPaths = strings.Split(strings.TrimSpace(string(b)), "\n")
			break
		}
		select {
		case err := <-waited:
			t.Fatalf("Check child exited before its post-pack image-build block (%v); output:\n%s", err, output.String())
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(stagedPaths) != 3 {
		t.Fatalf("child captured %d paths, want selected, fallback, and process-pack roots: %q", len(stagedPaths), stagedPaths)
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
	if err := cmd.Process.Signal(sig); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-waited:
		if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != code {
			t.Fatalf("child exit = %v, want status %d; output:\n%s", err, code, output.String())
		}
	case <-time.After(15 * time.Second):
		_ = cmd.Process.Kill()
		<-waited
		t.Fatalf("child did not exit after %s; output:\n%s", sig, output.String())
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

func runSignalCleanupChild(t *testing.T) {
	cacheBlocker := filepath.Join(os.TempDir(), "embedded-cache-is-a-file")
	if err := os.WriteFile(cacheBlocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	packload.OverrideEmbeddedCacheDir(filepath.Join(cacheBlocker, "embedded-packs"))
	if packs := packload.Embedded(); len(packs) == 0 {
		t.Fatalf("embedded pack fixture unavailable: %v", packload.EmbeddedProblems())
	}
	fallbackRoot, fallback := packload.EmbeddedLocation()
	if !fallback || fallbackRoot == "" {
		t.Fatalf("embedded packs did not use fallback: root=%q fallback=%v", fallbackRoot, fallback)
	}
	if _, err := packload.ProcessPackDir("signal-check"); err != nil {
		t.Fatalf("creating process pack tree: %v", err)
	}
	processRoot := packload.ProcessTreeLocation()
	nixStopped := filepath.Join(os.TempDir(), "check-signal-nix-stopped")
	nixStarted := filepath.Join(os.TempDir(), "check-signal-nix-started")
	_ = os.Remove(nixStopped)
	_ = os.Remove(nixStarted)
	if os.Getenv("CHECK_SIGNAL_OVERLAP") != "1" {
		nixCmd := exec.Command("/bin/sh", "-c", `trap 'touch "$1"; exit 0' INT; touch "$2"; while :; do sleep 1; done`, "signal-nix", nixStopped, nixStarted)
		releaseNix, err := nixchildren.Start(nixCmd)
		if err != nil {
			t.Fatalf("starting tracked stand-in nix: %v", err)
		}
		go func() {
			_ = nixCmd.Wait()
			releaseNix()
		}()
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
			if _, err := os.Stat(nixStarted); err == nil {
				break
			}
		}
		if _, err := os.Stat(nixStarted); err != nil {
			t.Fatalf("tracked stand-in nix did not start: %v", err)
		}
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
			trees, err := filepath.Glob(filepath.Join(os.TempDir(), "yolo-check-pack-*"))
			if err != nil || len(trees) != 1 {
				t.Fatalf("post-pack image build saw staging trees %q, glob error %v", trees, err)
			}
			if err := writeMarkerAtomically(stagedMarker, []byte(trees[0])); err != nil {
				t.Fatal(err)
			}
			return "", []string{"fixture build failure"}
		}
		if rc := Check(opts); rc == 0 {
			t.Fatal("overlap Check unexpectedly passed")
		}
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
		trees, err := filepath.Glob(filepath.Join(os.TempDir(), "yolo-check-pack-*"))
		if err != nil || len(trees) != 1 {
			t.Fatalf("post-pack image build saw staging trees %q, glob error %v", trees, err)
		}
		if err := writeMarkerAtomically(os.Getenv("CHECK_SIGNAL_MARKER"),
			[]byte(strings.Join([]string{trees[0], fallbackRoot, processRoot}, "\n"))); err != nil {
			t.Fatal(err)
		}
		select {}
	}
	rc := Check(opts)
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
